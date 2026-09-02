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
	// connCtx/connCancel/connDone describe the CURRENT connection generation
	// (protected by mu). Each Connect creates a fresh generation; the old
	// generation's goroutines hold their own copies as parameters, so a
	// closing old connection can never cancel or tear down a newer one
	// (L-005 Connect TOCTOU). connDone is closed when the generation's
	// readLoop exits.
	connCtx      context.Context
	connCancel   context.CancelFunc
	connDone     chan struct{}
	connected    bool
	connecting   bool         // guards against concurrent Connect calls
	lastRead     atomic.Int64 // unix nanos of last received packet
	onMessage    func(topic string, qos byte, payload []byte)
	msgMu        sync.RWMutex
	onError      func(format string, args ...interface{})
	receivedQoS2 map[uint16]struct{} // tracks received QoS 2 PacketIDs for dedup
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
	c := &MQTTClient{
		opts:         o,
		codec:        protocol.NewCodec(o.MaxPacketSize),
		inflight:     make(map[uint16]*inflightEntry),
		pending:      make(map[uint16]chan protocol.Packet),
		receivedQoS2: make(map[uint16]struct{}),
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
	// previous connection generation was cancelled and the maps were drained
	// on close.
	c.pendingMu.Lock()
	c.pending = make(map[uint16]chan protocol.Packet)
	c.pendingMu.Unlock()
	c.inflightMu.Lock()
	c.inflight = make(map[uint16]*inflightEntry)
	c.inflightMu.Unlock()
	c.mu.Lock()
	// QoS 2 duplicate tracking is session state: it survives reconnects of a
	// persistent session (the broker may re-send an unacknowledged QoS 2
	// PUBLISH with the same packet id) and is only reset for clean sessions
	// or when the broker reports a fresh session (audit: resetting it on
	// every Connect re-delivered duplicates after a reconnect).
	if c.opts.CleanSession {
		c.receivedQoS2 = make(map[uint16]struct{})
	}
	c.lastRead.Store(time.Now().UnixNano())
	// Create this connection's generation context. It is registered before
	// the dial so that a concurrent Disconnect can cancel the in-progress
	// connection attempt; the old generation's context is left untouched, so
	// a stale readLoop can never cancel this new connection (L-005).
	connCtx, connCancel := context.WithCancel(context.Background())
	connDone := make(chan struct{})
	c.connCtx = connCtx
	c.connCancel = connCancel
	c.connDone = connDone
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

	// Read CONNACK. Bound the wait with a read deadline (audit): ctx only
	// governed the dial, so a peer that accepted the TCP connection but never
	// answered left Connect blocked forever with a leaked goroutine/socket.
	readDeadline := time.Now().Add(c.opts.ConnectTimeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(readDeadline) {
		readDeadline = dl
	}
	if err := conn.SetReadDeadline(readDeadline); err != nil {
		_ = conn.Close()
		return fmt.Errorf("set CONNACK deadline: %w", err)
	}
	resp, err := c.codec.Decode(conn)
	_ = conn.SetReadDeadline(time.Time{})
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
	// A fresh broker session has no memory of our packet ids: drop stale
	// QoS 2 duplicate markers so a reused id is not wrongly suppressed
	// (audit). Persistent resumed sessions keep them.
	if !connack.SessionPresent {
		c.receivedQoS2 = make(map[uint16]struct{})
	}
	c.lastRead.Store(time.Now().UnixNano())
	c.mu.Unlock()

	// Start reader goroutine and, if keep-alive is configured, a PINGREQ
	// keepalive loop so an idle connection is not dropped by the broker.
	// Both receive this generation's context/cancel/done so their shutdown
	// can never touch a newer connection (L-005).
	go c.readLoop(conn, connCtx, connCancel, connDone)
	if c.opts.KeepAlive > 0 {
		go c.keepAliveLoop(conn, connCtx)
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
	connCtx := c.connCtx // this connection generation; cancelled on disconnect
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
	case <-connCtx.Done():
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
	connCtx := c.connCtx // this connection generation; cancelled on disconnect
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
	case <-connCtx.Done():
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
	connCtx := c.connCtx // this connection generation; cancelled on disconnect
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
	case <-connCtx.Done():
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
	// Snapshot this generation's cancellation and completion channels: a
	// concurrent Connect may already have replaced c.connCtx/connDone with a
	// newer generation, which this disconnect must not touch (L-005).
	connCancel := c.connCancel
	connDone := c.connDone
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

	// Cancel only this generation: wakes waiters of this connection
	// (Publish/Subscribe/Unsubscribe blocked on connCtx) and stops this
	// generation's keep-alive loop, without affecting a newer connection.
	connCancel()
	closeErr := conn.Close()

	// Clear client-side state so a subsequent Connect starts clean.
	c.pendingMu.Lock()
	c.pending = make(map[uint16]chan protocol.Packet)
	c.pendingMu.Unlock()
	c.inflightMu.Lock()
	c.inflight = make(map[uint16]*inflightEntry)
	c.inflightMu.Unlock()
	// NB: the QoS 2 duplicate set is deliberately NOT cleared here — it is
	// session state that must survive reconnects of a persistent session
	// (audit); Connect resets it for clean/new sessions.

	// Wait for this generation's readLoop to exit (it closes connDone).
	if ctx == nil {
		<-connDone
		return closeErr
	}

	select {
	case <-connDone:
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

// readLoop reads packets from the broker connection. It operates on the
// connection generation it was started for: the generation's connCtx is used
// for shutdown checks, connCancel is called on read errors to wake this
// generation's waiters (never a newer connection), and connDone is closed on
// exit so Disconnect can wait for exactly this generation (L-005).
func (c *MQTTClient) readLoop(conn net.Conn, connCtx context.Context, connCancel context.CancelFunc, connDone chan struct{}) {
	defer close(connDone)
	for {
		select {
		case <-connCtx.Done():
			return
		default:
		}

		pkt, err := c.codec.Decode(conn)
		if err != nil {
			c.mu.Lock()
			if c.conn == conn {
				c.connected = false
			}
			c.mu.Unlock()
			// Cancel this generation's context to wake up pending QoS
			// publish/response waiters blocked on connCtx.
			connCancel()
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
			c.handlePublish(conn, p)
		case *protocol.PubAckPacket:
			c.deliverResponse(p.PacketID, p)
		case *protocol.PubRecPacket:
			c.deliverResponse(p.PacketID, p)
		case *protocol.PubCompPacket:
			c.deliverResponse(p.PacketID, p)
		case *protocol.PubRelPacket:
			// Broker is completing an inbound QoS 2 exchange: send PUBCOMP
			// and clear the duplicate-tracking entry.
			c.handlePubRel(conn, p.PacketID)
		case *protocol.SubAckPacket:
			c.deliverResponse(p.PacketID, p)
		case *protocol.UnsubAckPacket:
			c.deliverResponse(p.PacketID, p)
		case *protocol.PingRespPacket:
			// PINGRESP received, no action needed
		}
	}
}

// ackOnGen acknowledges a packet on the connection generation that received
// it. If that generation has already been superseded by a newer connection,
// the ack is dropped: writing it through c.conn could inject the bytes into
// the new connection's stream, e.g. between CONNECT and CONNACK (audit).
func (c *MQTTClient) ackOnGen(conn net.Conn, pkt protocol.Packet) {
	c.mu.Lock()
	current := c.conn == conn
	c.mu.Unlock()
	if !current {
		return
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.codec.Encode(conn, pkt); err != nil {
		c.logError("failed to send %T for packet: %v", pkt, err)
	}
}

// handlePublish processes an incoming PUBLISH packet.
func (c *MQTTClient) handlePublish(conn net.Conn, pkt *protocol.PublishPacket) {
	// Detect duplicate QoS 2 PUBLISH (MQTT 3.1.1 §4.3.3, MQTT 5.0 §4.3.3)
	if pkt.FixedHeader.QoS == 2 {
		c.mu.Lock()
		if _, dup := c.receivedQoS2[pkt.PacketID]; dup {
			c.mu.Unlock()
			// Duplicate detected; still send PUBREC but skip onMessage delivery
			c.ackOnGen(conn, &protocol.PubRecPacket{
				FixedHeader: protocol.FixedHeader{
					PacketType: protocol.PacketTypePubRec,
					QoS:        1,
				},
				PacketID: pkt.PacketID,
			})
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
		c.ackOnGen(conn, &protocol.PubAckPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubAck},
			PacketID:    pkt.PacketID,
		})
	}

	// Send PUBREC for QoS 2
	if pkt.FixedHeader.QoS == 2 {
		c.ackOnGen(conn, &protocol.PubRecPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePubRec,
				QoS:        1,
			},
			PacketID: pkt.PacketID,
		})
	}
}

// handlePubRel completes an inbound QoS 2 exchange: the broker sent PUBREL
// after our PUBREC, so we send PUBCOMP and drop the duplicate-tracking entry.
func (c *MQTTClient) handlePubRel(conn net.Conn, packetID uint16) {
	c.mu.Lock()
	delete(c.receivedQoS2, packetID)
	c.mu.Unlock()
	c.ackOnGen(conn, &protocol.PubCompPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubComp},
		PacketID:    packetID,
	})
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
// It runs only when the client is configured with a non-zero KeepAlive and
// shuts down with its connection generation's context (L-005).
func (c *MQTTClient) keepAliveLoop(conn net.Conn, connCtx context.Context) {
	interval := time.Duration(c.opts.KeepAlive) * time.Second / 2
	if interval <= 0 {
		interval = time.Second
	}
	deadline := time.Duration(c.opts.KeepAlive) * time.Second * 3 / 2
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-connCtx.Done():
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
