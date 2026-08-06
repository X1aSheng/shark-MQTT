// Package client provides an MQTT 3.1.1/5.0 client implementation.
package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"os"
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
	wmu            sync.Mutex // serializes writes to the connection
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
	connecting     bool         // guards against concurrent Connect calls
	lastRead       atomic.Int64 // unix nanos of last received packet
	onMessage      func(topic string, qos byte, payload []byte)
	msgMu          sync.RWMutex
	onError        func(format string, args ...interface{})
	receivedQoS2   map[uint16]struct{} // tracks received QoS 2 PacketIDs for dedup
}

type inflightEntry struct {
	pkt *protocol.PublishPacket
}

// New creates a new MQTTClient with the given options.
func New(opts ...Option) *MQTTClient {
	o := defaultOptions()
	for _, fn := range opts {
		fn(o)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &MQTTClient{
		opts:         o,
		codec:        protocol.NewCodec(o.MaxPacketSize),
		inflight:     make(map[uint16]*inflightEntry),
		pending:      make(map[uint16]chan protocol.Packet),
		receivedQoS2: make(map[uint16]struct{}),
		ctx:          ctx,
		cancel:       cancel,
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
	if c.connecting {
		c.mu.Unlock()
		return fmt.Errorf("connect already in progress")
	}
	c.connecting = true
	c.mu.Unlock()
	// Always release the in-progress marker, including on error paths.
	defer func() {
		c.mu.Lock()
		c.connecting = false
		c.mu.Unlock()
	}()

	// Rebuild one-shot state so Disconnect followed by Connect works: the
	// previous context was cancelled and the maps were drained on close.
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.pendingMu.Lock()
	c.pending = make(map[uint16]chan protocol.Packet)
	c.pendingMu.Unlock()
	c.inflightMu.Lock()
	c.inflight = make(map[uint16]*inflightEntry)
	c.inflightMu.Unlock()
	c.mu.Lock()
	c.receivedQoS2 = make(map[uint16]struct{})
	c.lastRead.Store(time.Now().UnixNano())
	c.mu.Unlock()
	c.nextPID.Store(1)

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
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnect,
		},
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
	c.wmu.Lock()
	err = c.codec.Encode(conn, pkt)
	c.wmu.Unlock()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("send CONNECT: %w", err)
	}

	// Read CONNACK
	resp, err := c.codec.Decode(conn)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("read CONNACK: %w", err)
	}

	connack, ok := resp.(*protocol.ConnAckPacket)
	if !ok {
		_ = conn.Close()
		return fmt.Errorf("expected CONNACK, got %T", resp)
	}

	if connack.ReasonCode != protocol.ConnAckAccepted {
		_ = conn.Close()
		return fmt.Errorf("connection rejected: reason code 0x%02x", connack.ReasonCode)
	}

	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.sessionPresent = connack.SessionPresent
	c.lastRead.Store(time.Now().UnixNano())
	c.mu.Unlock()

	// Start reader goroutine and, if keep-alive is configured, a PINGREQ
	// keepalive loop so an idle connection is not dropped by the broker.
	c.wg.Add(1)
	go c.readLoop()
	if c.opts.KeepAlive > 0 {
		c.wg.Add(1)
		go c.keepAliveLoop(conn)
	}

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
	pkt.PacketType = protocol.PacketTypePublish
	pkt.Retain = retained

	var respCh chan protocol.Packet

	if qos > 0 {
		pkt.QoS = qos
		pid := c.nextPacketID()
		c.inflightMu.Lock()
		pkt.PacketID = pid
		c.inflight[pid] = &inflightEntry{pkt: pkt}
		c.inflightMu.Unlock()

		bufSize := 1
		if qos == 2 {
			bufSize = 2 // need space for both PUBREC and PUBCOMP
		}
		respCh = make(chan protocol.Packet, bufSize)
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

	c.wmu.Lock()
	if err := c.codec.Encode(conn, pkt); err != nil {
		c.wmu.Unlock()
		return fmt.Errorf("send PUBLISH: %w", err)
	}
	c.wmu.Unlock()

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
			c.wmu.Lock()
			if err := c.codec.Encode(conn, pubrel); err != nil {
				c.wmu.Unlock()
				return fmt.Errorf("send PUBREL: %w", err)
			}
			c.wmu.Unlock()
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

	pid := c.nextPacketID()

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

	c.wmu.Lock()
	if err := c.codec.Encode(conn, pkt); err != nil {
		c.wmu.Unlock()
		return nil, fmt.Errorf("send SUBSCRIBE: %w", err)
	}
	c.wmu.Unlock()

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

	pid := c.nextPacketID()

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

	c.wmu.Lock()
	if err := c.codec.Encode(conn, pkt); err != nil {
		c.wmu.Unlock()
		return fmt.Errorf("send UNSUBSCRIBE: %w", err)
	}
	c.wmu.Unlock()

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

	c.wmu.Lock()
	if err := c.codec.Encode(conn, pkt); err != nil {
		c.logError("failed to send DISCONNECT: %v", err)
	}
	c.wmu.Unlock()

	c.cancel()
	closeErr := conn.Close()

	// Clear client-side state so a subsequent Connect starts clean.
	c.pendingMu.Lock()
	c.pending = make(map[uint16]chan protocol.Packet)
	c.pendingMu.Unlock()
	c.inflightMu.Lock()
	c.inflight = make(map[uint16]*inflightEntry)
	c.inflightMu.Unlock()
	c.mu.Lock()
	c.receivedQoS2 = make(map[uint16]struct{})
	c.mu.Unlock()

	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()

	if ctx == nil {
		<-done
		return closeErr
	}

	select {
	case <-done:
		return closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SetOnMessage sets the callback for incoming PUBLISH packets.
func (c *MQTTClient) SetOnMessage(fn func(topic string, qos byte, payload []byte)) {
	c.msgMu.Lock()
	defer c.msgMu.Unlock()
	c.onMessage = fn
}

// SetOnError sets the callback for non-fatal errors (e.g. encode failures).
func (c *MQTTClient) SetOnError(fn func(format string, args ...interface{})) {
	c.msgMu.Lock()
	defer c.msgMu.Unlock()
	c.onError = fn
}

func (c *MQTTClient) logError(format string, args ...interface{}) {
	c.msgMu.RLock()
	fn := c.onError
	c.msgMu.RUnlock()
	if fn != nil {
		fn(format, args...)
		return
	}
	log.New(os.Stderr, "[mqtt-client] ", log.LstdFlags).Printf(format, args...)
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
			c.receivedQoS2 = make(map[uint16]struct{})
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
			c.inflightMu.Lock()
			c.inflight = make(map[uint16]*inflightEntry)
			c.inflightMu.Unlock()
			return
		}
		c.lastRead.Store(time.Now().UnixNano())

		switch p := pkt.(type) {
		case *protocol.PublishPacket:
			c.handlePublish(p)
		case *protocol.PubAckPacket:
			c.deliverResponse(p.PacketID, p)
		case *protocol.PubRecPacket:
			c.deliverResponse(p.PacketID, p)
		case *protocol.PubCompPacket:
			c.deliverResponse(p.PacketID, p)
		case *protocol.PubRelPacket:
			// Broker is completing an inbound QoS 2 exchange: send PUBCOMP
			// and clear the duplicate-tracking entry.
			c.handlePubRel(p.PacketID)
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
	// Detect duplicate QoS 2 PUBLISH (MQTT 3.1.1 §4.3.3, MQTT 5.0 §4.3.3)
	if pkt.FixedHeader.QoS == 2 {
		c.mu.Lock()
		if _, dup := c.receivedQoS2[pkt.PacketID]; dup {
			c.mu.Unlock()
			// Duplicate detected; still send PUBREC but skip onMessage delivery
			if conn := c.conn; conn != nil {
				pubrec := &protocol.PubRecPacket{PacketID: pkt.PacketID}
				pubrec.FixedHeader.PacketType = protocol.PacketTypePubRec
				pubrec.FixedHeader.QoS = 1
				c.wmu.Lock()
				if err := c.codec.Encode(conn, pubrec); err != nil {
					c.logError("failed to send PUBREC for packet %d: %v", pkt.PacketID, err)
				}
				c.wmu.Unlock()
			}
			return
		}
		c.receivedQoS2[pkt.PacketID] = struct{}{}
		c.mu.Unlock()
	}

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
			c.wmu.Lock()
			if err := c.codec.Encode(conn, puback); err != nil {
				c.logError("failed to send PUBACK for packet %d: %v", pkt.PacketID, err)
			}
			c.wmu.Unlock()
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
			c.wmu.Lock()
			if err := c.codec.Encode(conn, pubrec); err != nil {
				c.logError("failed to send PUBREC for packet %d: %v", pkt.PacketID, err)
			}
			c.wmu.Unlock()
		}
	}
}

// handlePubRel completes an inbound QoS 2 exchange: the broker sent PUBREL
// after our PUBREC, so we send PUBCOMP and drop the duplicate-tracking entry.
func (c *MQTTClient) handlePubRel(packetID uint16) {
	c.mu.Lock()
	conn := c.conn
	delete(c.receivedQoS2, packetID)
	c.mu.Unlock()
	if conn == nil {
		return
	}
	pubcomp := &protocol.PubCompPacket{
		PacketID: packetID,
	}
	pubcomp.FixedHeader.PacketType = protocol.PacketTypePubComp
	c.wmu.Lock()
	if err := c.codec.Encode(conn, pubcomp); err != nil {
		c.logError("failed to send PUBCOMP for packet %d: %v", packetID, err)
	}
	c.wmu.Unlock()
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

// keepAliveLoop sends PINGREQ packets on a keep-alive schedule and detects a
// dead connection when no packet has been received within 1.5x KeepAlive.
// It runs only when the client is configured with a non-zero KeepAlive.
func (c *MQTTClient) keepAliveLoop(conn net.Conn) {
	defer c.wg.Done()
	interval := time.Duration(c.opts.KeepAlive) * time.Second / 2
	if interval <= 0 {
		interval = time.Second
	}
	deadline := time.Duration(c.opts.KeepAlive) * time.Second * 3 / 2
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			current := c.conn
			c.mu.Unlock()
			if current != conn {
				return // connection was replaced or closed
			}
			if time.Since(time.Unix(0, c.lastRead.Load())) > deadline {
				// No traffic within the keep-alive window: connection is dead.
				_ = conn.Close()
				return
			}
			ping := &protocol.PingReqPacket{
				FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePingReq},
			}
			c.wmu.Lock()
			if err := c.codec.Encode(conn, ping); err != nil {
				_ = conn.Close()
				c.wmu.Unlock()
				return
			}
			c.wmu.Unlock()
		}
	}
}

// nextPacketID returns the next packet identifier, cycling through 1-65535 and
// skipping identifiers already in flight or awaiting a response so that a
// wrapped ID never collides with an outstanding operation.
func (c *MQTTClient) nextPacketID() uint16 {
	for attempts := 0; attempts < 65535; attempts++ {
		old := c.nextPID.Load()
		next := old + 1
		if next > 65535 {
			next = 1
		}
		if c.nextPID.CompareAndSwap(old, next) {
			id := uint16(old)
			c.inflightMu.RLock()
			_, inFlight := c.inflight[id]
			c.inflightMu.RUnlock()
			if inFlight {
				continue // skip an ID that is still in flight
			}
			c.pendingMu.RLock()
			_, inPending := c.pending[id]
			c.pendingMu.RUnlock()
			if !inPending {
				return id
			}
		}
	}
	// Fallback: all attempts exhausted (extreme contention), use atomic add.
	pid := uint16(c.nextPID.Add(1) - 1)
	if pid == 0 {
		pid = 1
	}
	return pid
}
