// Package client provides an MQTT 3.1.1/5.0 client implementation.
package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TopicSubscription represents a topic with its requested QoS level.
type TopicSubscription struct {
	Topic string
	QoS   byte
}

// MQTTClient is an MQTT 3.1.1/5.0 client.
type MQTTClient struct {
	mu             sync.Mutex
	conn           net.Conn
	codec          *protocol.Codec
	opts           *Options
	clientID       string
	sessionPresent bool
	inflight       map[uint16]*inflightEntry
	inflightMu     sync.RWMutex
	nextPID        atomic.Uint32
	pending        map[uint16]chan protocol.Packet
	pendingMu      sync.RWMutex
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	connected      bool
	onMessage      func(topic string, qos byte, payload []byte)
	msgMu          sync.RWMutex
}

type inflightEntry struct {
	pkt   *protocol.PublishPacket
	timer *time.Timer
}

// New creates a new MQTTClient with the given options.
func New(opts ...Option) *MQTTClient {
	o := defaultOptions()
	for _, fn := range opts {
		fn(o)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &MQTTClient{
		opts:     o,
		codec:    protocol.NewCodec(o.MaxPacketSize),
		inflight: make(map[uint16]*inflightEntry),
		pending:  make(map[uint16]chan protocol.Packet),
		ctx:      ctx,
		cancel:   cancel,
	}
	c.nextPID.Store(1)
	return c
}

// Connect establishes connection to broker and performs MQTT handshake.
func (c *MQTTClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.connected {
		c.mu.Unlock()
		return fmt.Errorf("already connected")
	}
	c.mu.Unlock()

	addr := fmt.Sprintf("%s:%d", c.opts.Host, c.opts.Port)

	var d net.Dialer
	dialCtx, dialCancel := context.WithTimeout(ctx, c.opts.ConnectTimeout)
	defer dialCancel()

	var conn net.Conn
	var err error
	if c.opts.TLSConfig != nil {
		tlsDialer := tls.Dialer{NetDialer: &d, Config: c.opts.TLSConfig}
		conn, err = tlsDialer.DialContext(dialCtx, "tcp", addr)
	} else {
		conn, err = d.DialContext(dialCtx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}

	// Build CONNECT packet
	pkt := &protocol.ConnectPacket{
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version311,
		Flags: protocol.ConnectFlags{
			CleanSession: c.opts.CleanSession,
		},
		KeepAlive: c.opts.KeepAlive,
		ClientID:  c.opts.ClientID,
	}

	if c.opts.Username != "" {
		pkt.Flags.UsernameFlag = true
		pkt.Username = c.opts.Username
	}
	if c.opts.Password != "" {
		pkt.Flags.PasswordFlag = true
		pkt.Password = []byte(c.opts.Password)
	}

	// Set client ID if not provided
	if pkt.ClientID == "" {
		pkt.ClientID = fmt.Sprintf("shark-%d", time.Now().UnixNano())
	}

	c.clientID = pkt.ClientID

	// Send CONNECT
	if err := c.codec.Encode(conn, pkt); err != nil {
		conn.Close()
		return fmt.Errorf("send CONNECT: %w", err)
	}

	// Read CONNACK
	resp, err := c.codec.Decode(conn)
	if err != nil {
		conn.Close()
		return fmt.Errorf("read CONNACK: %w", err)
	}

	connack, ok := resp.(*protocol.ConnAckPacket)
	if !ok {
		conn.Close()
		return fmt.Errorf("expected CONNACK, got %T", resp)
	}

	if connack.ReasonCode != protocol.ConnAckAccepted {
		conn.Close()
		return fmt.Errorf("connection rejected: reason code 0x%02x", connack.ReasonCode)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.sessionPresent = connack.SessionPresent
	c.mu.Unlock()

	// Start reader goroutine
	c.wg.Add(1)
	go c.readLoop()

	return nil
}

// Publish sends a PUBLISH packet.
func (c *MQTTClient) Publish(ctx context.Context, topic string, qos byte, retained bool, payload []byte) error {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	conn := c.conn
	c.mu.Unlock()

	pkt := &protocol.PublishPacket{
		Topic:   topic,
		Payload: payload,
	}
	pkt.FixedHeader.PacketType = protocol.PacketTypePublish
	pkt.FixedHeader.Retain = retained

	var respCh chan protocol.Packet

	if qos > 0 {
		pkt.FixedHeader.QoS = qos
		c.inflightMu.Lock()
		pid := c.nextPacketID()
		pkt.PacketID = pid
		c.inflight[pid] = &inflightEntry{pkt: pkt}
		c.inflightMu.Unlock()

		respCh = make(chan protocol.Packet, 1)
		c.pendingMu.Lock()
		c.pending[pid] = respCh
		c.pendingMu.Unlock()
		defer func() {
			c.pendingMu.Lock()
			delete(c.pending, pid)
			c.pendingMu.Unlock()
			c.inflightMu.Lock()
			delete(c.inflight, pid)
			c.inflightMu.Unlock()
		}()
	}

	if err := c.codec.Encode(conn, pkt); err != nil {
		return fmt.Errorf("send PUBLISH: %w", err)
	}

	// For QoS 0, return immediately
	if qos == 0 {
		return nil
	}

	// Wait for acknowledgment
	select {
	case <-ctx.Done():
		return ctx.Err()
	case resp := <-respCh:
		switch r := resp.(type) {
		case *protocol.PubAckPacket:
			if r.ReasonCode != protocol.ReasonCodeSuccess {
				return fmt.Errorf("PUBACK rejected: reason code 0x%02x", r.ReasonCode)
			}
			return nil
		case *protocol.PubRecPacket:
			// QoS 2: send PUBREL
			pubrel := &protocol.PubRelPacket{
				PacketID: pkt.PacketID,
			}
			pubrel.FixedHeader.PacketType = protocol.PacketTypePubRel
			pubrel.FixedHeader.QoS = 1
			if err := c.codec.Encode(conn, pubrel); err != nil {
				return fmt.Errorf("send PUBREL: %w", err)
			}
			// Wait for PUBCOMP
			select {
			case <-ctx.Done():
				return ctx.Err()
			case compResp := <-respCh:
				if _, ok := compResp.(*protocol.PubCompPacket); !ok {
					return fmt.Errorf("expected PUBCOMP, got %T", compResp)
				}
				return nil
			}
		default:
			return fmt.Errorf("unexpected response: %T", resp)
		}
	case <-c.ctx.Done():
		return fmt.Errorf("client disconnected")
	}
}

// Subscribe subscribes to topics.
func (c *MQTTClient) Subscribe(ctx context.Context, topics []TopicSubscription) ([]byte, error) {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return nil, fmt.Errorf("not connected")
	}
	conn := c.conn
	c.mu.Unlock()

	c.inflightMu.Lock()
	pid := c.nextPacketID()
	c.inflightMu.Unlock()

	filters := make([]protocol.TopicFilter, len(topics))
	for i, t := range topics {
		filters[i] = protocol.TopicFilter{
			Topic: t.Topic,
			QoS:   t.QoS,
		}
	}

	pkt := &protocol.SubscribePacket{
		PacketID: pid,
		Topics:   filters,
	}
	pkt.FixedHeader.PacketType = protocol.PacketTypeSubscribe
	pkt.FixedHeader.QoS = 1

	respCh := make(chan protocol.Packet, 1)
	c.pendingMu.Lock()
	c.pending[pid] = respCh
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, pid)
		c.pendingMu.Unlock()
	}()

	if err := c.codec.Encode(conn, pkt); err != nil {
		return nil, fmt.Errorf("send SUBSCRIBE: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-respCh:
		suback, ok := resp.(*protocol.SubAckPacket)
		if !ok {
			return nil, fmt.Errorf("expected SUBACK, got %T", resp)
		}
		return suback.ReasonCodes, nil
	case <-c.ctx.Done():
		return nil, fmt.Errorf("client disconnected")
	}
}

// Unsubscribe unsubscribes from topics.
func (c *MQTTClient) Unsubscribe(ctx context.Context, topics []string) error {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return fmt.Errorf("not connected")
	}
	conn := c.conn
	c.mu.Unlock()

	c.inflightMu.Lock()
	pid := c.nextPacketID()
	c.inflightMu.Unlock()

	pkt := &protocol.UnsubscribePacket{
		PacketID: pid,
		Topics:   topics,
	}
	pkt.FixedHeader.PacketType = protocol.PacketTypeUnsubscribe
	pkt.FixedHeader.QoS = 1

	respCh := make(chan protocol.Packet, 1)
	c.pendingMu.Lock()
	c.pending[pid] = respCh
	c.pendingMu.Unlock()
	defer func() {
		c.pendingMu.Lock()
		delete(c.pending, pid)
		c.pendingMu.Unlock()
	}()

	if err := c.codec.Encode(conn, pkt); err != nil {
		return fmt.Errorf("send UNSUBSCRIBE: %w", err)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case resp := <-respCh:
		if _, ok := resp.(*protocol.UnsubAckPacket); !ok {
			return fmt.Errorf("expected UNSUBACK, got %T", resp)
		}
		return nil
	case <-c.ctx.Done():
		return fmt.Errorf("client disconnected")
	}
}

// Disconnect sends DISCONNECT and closes connection.
func (c *MQTTClient) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return nil
	}
	conn := c.conn
	c.connected = false
	c.mu.Unlock()

	pkt := &protocol.DisconnectPacket{
		ReasonCode: 0,
	}
	pkt.FixedHeader.PacketType = protocol.PacketTypeDisconnect

	_ = c.codec.Encode(conn, pkt)

	c.cancel()
	c.wg.Wait()

	return conn.Close()
}

// SetOnMessage sets the callback for incoming PUBLISH packets.
func (c *MQTTClient) SetOnMessage(fn func(topic string, qos byte, payload []byte)) {
	c.msgMu.Lock()
	defer c.msgMu.Unlock()
	c.onMessage = fn
}

// IsConnected returns whether the client is connected.
func (c *MQTTClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// readLoop reads packets from the broker connection.
func (c *MQTTClient) readLoop() {
	defer c.wg.Done()
	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()

		if conn == nil {
			return
		}

		pkt, err := c.codec.Decode(conn)
		if err != nil {
			c.mu.Lock()
			c.connected = false
			c.mu.Unlock()
			// Cancel context to wake up pending QoS publish/response waiters
			c.cancel()
			// Drain pending response channels to prevent goroutine leaks
			c.pendingMu.Lock()
			for pid, ch := range c.pending {
				close(ch)
				delete(c.pending, pid)
			}
			c.pendingMu.Unlock()
			return
		}

		switch p := pkt.(type) {
		case *protocol.PublishPacket:
			c.handlePublish(p)
		case *protocol.PubAckPacket:
			c.deliverResponse(p.PacketID, p)
		case *protocol.PubRecPacket:
			c.deliverResponse(p.PacketID, p)
		case *protocol.PubCompPacket:
			c.deliverResponse(p.PacketID, p)
		case *protocol.SubAckPacket:
			c.deliverResponse(p.PacketID, p)
		case *protocol.UnsubAckPacket:
			c.deliverResponse(p.PacketID, p)
		case *protocol.PingRespPacket:
			// PINGRESP received, no action needed
		}
	}
}

// handlePublish processes an incoming PUBLISH packet.
func (c *MQTTClient) handlePublish(pkt *protocol.PublishPacket) {
	c.msgMu.RLock()
	fn := c.onMessage
	c.msgMu.RUnlock()

	if fn != nil {
		fn(pkt.Topic, pkt.FixedHeader.QoS, pkt.Payload)
	}

	// Send PUBACK for QoS 1
	if pkt.FixedHeader.QoS == 1 {
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()
		if conn != nil {
			puback := &protocol.PubAckPacket{
				PacketID: pkt.PacketID,
			}
			puback.FixedHeader.PacketType = protocol.PacketTypePubAck
			_ = c.codec.Encode(conn, puback)
		}
	}

	// Send PUBREC for QoS 2
	if pkt.FixedHeader.QoS == 2 {
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()
		if conn != nil {
			pubrec := &protocol.PubRecPacket{
				PacketID: pkt.PacketID,
			}
			pubrec.FixedHeader.PacketType = protocol.PacketTypePubRec
			pubrec.FixedHeader.QoS = 1
			_ = c.codec.Encode(conn, pubrec)
		}
	}
}

// deliverResponse delivers a packet to a waiting response channel.
func (c *MQTTClient) deliverResponse(packetID uint16, pkt protocol.Packet) {
	c.pendingMu.RLock()
	ch, ok := c.pending[packetID]
	c.pendingMu.RUnlock()
	if ok {
		select {
		case ch <- pkt:
		default:
		}
	}
}

// nextPacketID returns the next packet identifier, cycling through 1-65535.
func (c *MQTTClient) nextPacketID() uint16 {
	for {
		old := c.nextPID.Load()
		next := old + 1
		if next > 65535 {
			next = 1
		}
		if c.nextPID.CompareAndSwap(old, next) {
			return uint16(old)
		}
	}
}
