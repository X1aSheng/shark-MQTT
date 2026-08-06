package broker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-mqtt/pkg/logger"
	"github.com/X1aSheng/shark-mqtt/pkg/metrics"
	"github.com/X1aSheng/shark-mqtt/plugin"
	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store"
)

// Compile-time interface compliance checks.
var _ ConnectionHandler = (*Broker)(nil)

// clientState holds the connection and codec for a client.
type clientState struct {
	conn  net.Conn
	codec *protocol.Codec
	wmu   sync.Mutex // serializes writes when no async write queue is configured

	// out is the bounded per-connection outbound write queue. When non-nil,
	// writePacket/sendConnAck enqueue here and writeLoop drains it on a single
	// goroutine, so a slow consumer cannot block the producing client's read
	// loop (R1). It stays nil only for clientStates created outside
	// HandleConnection (e.g. unit tests), which fall back to synchronous
	// writes under wmu. stopWrites is closed to stop the writer goroutine.
	out        chan protocol.Packet
	stopWrites chan struct{}

	// enhancedAuth is the active MQTT 5.0 enhanced authenticator for a
	// connection that connected via an AuthenticationMethod (§4.12). Non-nil
	// only for such connections; used to handle re-authentication AUTH packets
	// during the session.
	enhancedAuth EnhancedAuthenticator
}

// Broker is the core MQTT message broker that orchestrates TopicTree, QoSEngine,
// WillHandler, and session management. It implements server.ConnectionHandler.
type Broker struct {
	topics   *TopicTree
	qos      *QoSEngine
	will     *WillHandler
	sessions *Manager

	sessionStore  store.SessionStore
	messageStore  store.MessageStore
	retainedStore store.RetainedStore

	logger    logger.Logger
	metrics   metrics.Metrics
	pluginMgr *plugin.Manager

	mu sync.RWMutex
	// connections maps clientID -> clientState
	connections map[string]*clientState

	// QoS 2 duplicate detection: tracks incoming PUBLISH packet IDs per client
	// to detect and suppress duplicates when DUP flag is set (MQTT §4.3.3).
	receivedQoS2   map[string]map[uint16]struct{}
	receivedQoS2Mu sync.Mutex

	retainedMu    sync.Mutex
	retainedCount atomic.Int64 // count of retained messages maintained with retainedMu
	// retainedExpirations tracks expiry times for retained messages when
	// retainedExpiry is configured (> 0). Keyed by topic name.
	retainedExpirations map[string]time.Time

	// connRate limits the rate of new TCP connections accepted.
	connRate *connRateLimiter

	started atomic.Bool // prevents double-Start
	ctx     context.Context
	cancel  context.CancelFunc
	opts    brokerOptions

	startedAt time.Time // broker start time, for $SYS/broker/uptime (R8)
}

// New creates a new Broker with the given options.
func New(opts ...Option) *Broker {
	o := defaultBrokerOptions()
	for _, opt := range opts {
		opt(&o)
	}

	ctx, cancel := context.WithCancel(context.Background())

	retainedExpirations := make(map[string]time.Time)

	b := &Broker{
		topics:              NewTopicTree(),
		qos:                 NewQoSEngine(o.qosOpts...),
		will:                NewWillHandler(),
		sessions:            NewManager(o.sessionStore),
		sessionStore:        o.sessionStore,
		messageStore:        o.messageStore,
		retainedStore:       o.retainedStore,
		logger:              o.logger,
		metrics:             o.metrics,
		pluginMgr:           o.pluginManager,
		connections:         make(map[string]*clientState),
		receivedQoS2:        make(map[string]map[uint16]struct{}),
		connRate:            newConnRateLimiter(o.connectionRateWindow),
		retainedExpirations: retainedExpirations,
		ctx:                 ctx,
		cancel:              cancel,
		opts:                o,
	}

	// Setup QoS callbacks
	b.qos.SetCallbacks(
		func(clientID string, packetID uint16) error {
			return b.sendPubAck(clientID, packetID)
		},
		func(clientID string, packetID uint16) error {
			return b.sendPubRel(clientID, packetID)
		},
		func(clientID string, packetID uint16) error {
			return b.sendPubComp(clientID, packetID)
		},
		func(clientID string, packetID uint16, topic string, payload []byte, qos uint8, retain bool) error {
			return b.republish(clientID, packetID, topic, payload, qos, retain)
		},
	)

	// Setup Will callback
	b.will.SetPublishCallback(func(username string, topic string, payload []byte, qos uint8, retain bool) error {
		return b.publishWill(username, topic, payload, qos, retain)
	})

	return b
}

// HandleConnection implements server.ConnectionHandler.
// This is called by the network server when a new TCP connection is accepted.
func (b *Broker) HandleConnection(ctx context.Context, conn net.Conn, codec *protocol.Codec) error {
	c := codec
	if c == nil {
		c = protocol.NewCodec(b.opts.maxPacketSize)
	}

	// Check connection limit
	if b.opts.maxConnections > 0 {
		b.mu.RLock()
		count := len(b.connections)
		b.mu.RUnlock()
		if count >= b.opts.maxConnections {
			b.metrics.IncRejections("max_connections")
			_ = conn.Close()
			return fmt.Errorf("broker: max connections (%d) reached", b.opts.maxConnections)
		}
	}

	// Check connection rate limit
	if b.opts.maxConnRate > 0 {
		b.connRate.SetRate(b.opts.maxConnRate)
		if !b.connRate.Allow() {
			b.metrics.IncRejections("rate_limited")
			b.metrics.IncErrors("rate_limit")
			b.logger.Debug("connection rate limit exceeded",
				"remote", conn.RemoteAddr().String())
			b.sendConnAckRaw(conn, c, protocol.ReasonCodeConnectionRateExceeded, false)
			_ = conn.Close()
			return fmt.Errorf("broker: connection rate limit exceeded")
		}
	}

	// Plugin hook: OnAccept
	b.dispatch(plugin.OnAccept, &plugin.Context{
		RemoteAddr: conn.RemoteAddr().String(),
	})

	// Set read deadline for CONNECT
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		b.metrics.IncErrors("deadline")
		return fmt.Errorf("broker: set CONNECT deadline failed: %w", err)
	}

	// Wait for CONNECT packet
	pkt, err := c.Decode(conn)
	if err != nil {
		b.metrics.IncRejections("decode_error")
		b.metrics.IncErrors("decode")
		return fmt.Errorf("broker: decode CONNECT failed: %w", err)
	}

	connectPkt, ok := pkt.(*protocol.ConnectPacket)
	if !ok {
		b.metrics.IncRejections("invalid_packet")
		b.metrics.IncErrors("protocol")
		return fmt.Errorf("broker: expected CONNECT, got %T", pkt)
	}

	// Validate CONNECT per MQTT spec
	if err := protocol.ValidateConnect(connectPkt); err != nil {
		b.metrics.IncRejections("invalid_connect")
		b.metrics.IncErrors("protocol")
		var reasonCode byte = protocol.ConnAckUnacceptableProtocol
		if connectPkt.ProtocolVersion == protocol.Version50 {
			reasonCode = protocol.ConnAckProtocolError
		}
		b.sendConnAckRaw(conn, c, byte(reasonCode), false)
		return fmt.Errorf("broker: CONNECT validation failed: %w", err)
	}

	// Check client ID length limit to prevent resource exhaustion
	if b.opts.maxClientIDLength > 0 && len(connectPkt.ClientID) > b.opts.maxClientIDLength {
		b.metrics.IncRejections("client_id_too_long")
		b.metrics.IncErrors("protocol")
		var reasonCode byte
		if connectPkt.ProtocolVersion == protocol.Version50 {
			reasonCode = protocol.ReasonCodeProtocolError
		} else {
			reasonCode = protocol.ConnAckIdentifierRejected
		}
		b.sendConnAckRaw(conn, c, reasonCode, false)
		return fmt.Errorf("broker: client ID %d bytes exceeds max %d bytes",
			len(connectPkt.ClientID), b.opts.maxClientIDLength)
	}

	// Clear read deadline
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		b.logger.Debug("failed to clear read deadline", "error", err)
	}

	// MQTT 5.0 enhanced authentication (§4.12): a CONNECT carrying an
	// AuthenticationMethod runs the enhanced auth exchange (AUTH packets)
	// instead of the traditional username/password path. The handshake sends
	// AUTH packets only; the final CONNACK 0x00 is sent later, once the session
	// is set up.
	var enhancedAuth EnhancedAuthenticator
	if connectPkt.Properties != nil && connectPkt.Properties.AuthenticationMethod != "" {
		enhancedAuth = b.findEnhancedAuth(connectPkt.Properties.AuthenticationMethod)
		if enhancedAuth == nil {
			b.metrics.IncAuthFailures()
			b.sendConnAckRaw(conn, c, protocol.ReasonCodeBadAuthMethod, false)
			return fmt.Errorf("broker: unsupported authentication method %q", connectPkt.Properties.AuthenticationMethod)
		}
		if err := b.enhancedAuthHandshake(conn, c, connectPkt, enhancedAuth); err != nil {
			b.metrics.IncAuthFailures()
			b.logger.Debug("enhanced auth failed", "clientID", connectPkt.ClientID, "error", err)
			return err
		}
	} else if b.opts.authenticator != nil {
		authErr := b.opts.authenticator.Authenticate(ctx, connectPkt.ClientID, connectPkt.Username, string(connectPkt.Password))
		if authErr != nil {
			b.metrics.IncAuthFailures()
			b.sendConnAckRaw(conn, c, protocol.ConnAckBadUsernameOrPassword, false)
			return fmt.Errorf("broker: auth failed: %w", authErr)
		}
	}

	if connectPkt.Flags.WillFlag && !protocol.ValidatePublishTopic(connectPkt.WillTopic) {
		b.sendConnAckRaw(conn, c, protocol.ConnAckUnspecifiedError, false)
		return fmt.Errorf("broker: will topic %q contains wildcards", connectPkt.WillTopic)
	}

	// Allocate assigned client ID if empty (MQTT 5.0 §3.1.3.6).
	// Must happen before session creation so the session uses the assigned ID.
	var assignedClientID string
	if len(connectPkt.ClientID) == 0 {
		assignedClientID = fmt.Sprintf("shark-%x", time.Now().UnixNano())
		connectPkt.ClientID = assignedClientID
	}

	// Create or resume session. Check in-memory first, then persistent store.
	// A clean session must never report SessionPresent=1 (MQTT 5.0 §3.2.2.2
	// / 3.1.1 §3.2.2.1).
	isResuming := !connectPkt.Flags.CleanSession && b.sessions.SessionExists(connectPkt.ClientID)
	if !isResuming && !connectPkt.Flags.CleanSession && b.sessionStore != nil {
		if exists, storeErr := b.sessionStore.IsSessionExists(ctx, connectPkt.ClientID); exists {
			restored, err := b.sessions.Restore(ctx, connectPkt.ClientID)
			if err == nil && restored != nil {
				isResuming = true
				for topic, qos := range restored.Subscriptions {
					b.topics.Subscribe(topic, connectPkt.ClientID, qos)
				}
				for _, msg := range restored.Inflight {
					// Restored inflight entries are outbound deliveries that were
					// never acknowledged; retry them as outbound.
					if msg.QoS == 2 {
						_ = b.qos.TrackOutboundQoS2(connectPkt.ClientID, msg.PacketID, msg.Topic, msg.Payload, msg.Retain)
					} else {
						_ = b.qos.TrackOutboundQoS1(connectPkt.ClientID, msg.PacketID, msg.Topic, msg.Payload, msg.Retain)
					}
				}
			}
		} else if storeErr != nil {
			b.logger.Debug("session store check failed, treating as new", "clientID", connectPkt.ClientID, "error", storeErr)
		}
	}
	sess := b.sessions.CreateSession(connectPkt.ClientID, connectPkt, isResuming)
	clientID := connectPkt.ClientID

	// The client may request Response Information in the CONNACK (MQTT 5.0
	// §3.2.2.3.8).
	if connectPkt.Properties != nil && connectPkt.Properties.RequestResponseInfo != nil && *connectPkt.Properties.RequestResponseInfo == 1 {
		sess.RequestResponseInfo = true
	}

	// Reconnecting to an existing session cancels any pending delayed will
	// (MQTT 5.0 §3.1.2.5): the session is no longer being abandoned.
	if isResuming {
		b.will.CancelWill(clientID)
	}

	// A clean-session connect discards any previously stored session and its
	// queued offline messages (MQTT session state is not carried over).
	if connectPkt.Flags.CleanSession {
		if b.sessionStore != nil {
			_ = b.sessionStore.DeleteSession(b.ctx, clientID)
		}
		if b.messageStore != nil {
			_ = b.messageStore.ClearMessages(b.ctx, clientID)
		}
	}

	if assignedClientID != "" {
		sess.AssignedClientID = assignedClientID
	}

	// Apply server-enforced publish rate limit if configured
	if b.opts.maxPublishRate > 0 {
		sess.publishRate.SetMaxRate(b.opts.maxPublishRate)
	}

	// Set session expiry interval (MQTT 5.0 §3.1.2.11.2).
	// Use the smaller of client-requested and server-configured values.
	if connectPkt.Flags.CleanSession {
		sess.ExpiryInterval = 0
	} else {
		serverMax := uint32(b.opts.sessionExpiry.Seconds())
		clientVal := uint32(0)
		if connectPkt.Properties != nil && connectPkt.Properties.SessionExpiryInterval != nil {
			clientVal = *connectPkt.Properties.SessionExpiryInterval
		}
		if clientVal > 0 && clientVal < serverMax {
			sess.ExpiryInterval = clientVal
		} else {
			sess.ExpiryInterval = serverMax
		}
	}

	// Negotiate Receive Maximum (MQTT 5.0 §3.1.2.11.6).
	// Use the smaller of client-requested and server-configured values.
	serverReceiveMax := uint16(b.opts.maxInflight)
	if serverReceiveMax == 0 {
		serverReceiveMax = 65535
	}
	clientReceiveMax := uint16(65535)
	if connectPkt.Properties != nil && connectPkt.Properties.ReceiveMaximum != nil {
		clientReceiveMax = *connectPkt.Properties.ReceiveMaximum
	}
	if clientReceiveMax < serverReceiveMax {
		sess.ReceiveMax = clientReceiveMax
	} else {
		sess.ReceiveMax = serverReceiveMax
	}

	// Negotiate Topic Alias Maximum (MQTT 5.0 §3.1.2.11.7).
	// Server supports up to 64 topic aliases per connection.
	const serverTopicAliasMax uint16 = 64
	clientAliasMax := uint16(0)
	if connectPkt.Properties != nil && connectPkt.Properties.TopicAliasMaximum != nil {
		clientAliasMax = *connectPkt.Properties.TopicAliasMaximum
	}
	if clientAliasMax > 0 {
		sess.TopicAliasMax = min(clientAliasMax, serverTopicAliasMax)
	} else {
		sess.TopicAliasMax = 0
	}

	// Negotiate Server Keep Alive (MQTT 5.0 §3.1.2.11.4).
	// If server-configured keepalive is shorter, override client's.
	if b.opts.keepAlive > 0 && b.opts.keepAlive < connectPkt.KeepAlive {
		ka := b.opts.keepAlive
		sess.ServerKeepAlive = &ka
		sess.KeepAlive = ka
	}

	// Register client connection — kick previous connection if one exists.
	// The old readLoop will call disconnect() asynchronously; it must not
	// remove the new connection from the map, so disconnect() checks conn
	// identity before deleting.
	//
	// Takeover: the previous connection ends abnormally, so its will must be
	// triggered before the new connection registers its own will under the
	// same clientID (P2-10). TriggerWill is idempotent per client, so the old
	// readLoop firing later is a no-op.
	b.mu.RLock()
	_, hadOld := b.connections[clientID]
	b.mu.RUnlock()
	if hadOld {
		b.will.TriggerWill(clientID)
	}

	b.mu.Lock()
	if old, exists := b.connections[clientID]; exists {
		if err := old.conn.Close(); err != nil {
			b.logger.Debug("failed to close previous connection", "clientID", clientID, "error", err)
		}
		// Stop the previous connection's writer goroutine; its readLoop will
		// later call disconnect() and bail out on the conn identity check, so
		// it must be stopped here (R1).
		if old.out != nil {
			close(old.stopWrites)
		}
		b.logger.Info("session takeover", "clientID", clientID)
	}
	qcap := b.opts.writeQueueSize
	if qcap < 1 {
		qcap = 1
	}
	cs := &clientState{
		conn:         conn,
		codec:        c,
		out:          make(chan protocol.Packet, qcap),
		stopWrites:   make(chan struct{}),
		enhancedAuth: enhancedAuth,
	}
	b.connections[clientID] = cs
	online := len(b.connections)
	b.mu.Unlock()
	go cs.writeLoop()
	b.metrics.SetOnlineSessions(online)

	// Register will message
	if connectPkt.Flags.WillFlag {
		var willDelay time.Duration
		if connectPkt.WillProperties != nil && connectPkt.WillProperties.WillDelayInterval != nil {
			willDelay = time.Duration(*connectPkt.WillProperties.WillDelayInterval) * time.Second
		}
		// Cap will delay to the configured maximum to prevent abuse. A
		// maxWillDelay of 0 disables will delay entirely (P2-5).
		if willDelay > b.opts.maxWillDelay {
			b.logger.Debug("capping will delay", "clientID", clientID,
				"requested", willDelay, "max", b.opts.maxWillDelay)
			willDelay = b.opts.maxWillDelay
		}
		if err := b.will.RegisterWill(clientID, connectPkt.Username, connectPkt.WillTopic, connectPkt.WillMessage, connectPkt.Flags.WillQoS, connectPkt.Flags.WillRetain, willDelay); err != nil {
			b.metrics.IncErrors("will")
			return fmt.Errorf("broker: register will failed: %w", err)
		}
	}

	// Plugin hook: OnConnected
	b.dispatch(plugin.OnConnected, &plugin.Context{
		ClientID: clientID,
		Username: connectPkt.Username,
	})

	// Metrics
	b.metrics.IncConnections()

	// Send CONNACK
	b.sendConnAck(clientID, protocol.ConnAckAccepted, isResuming, sess)

	// Deliver messages queued while this persistent session was offline (P1-5).
	b.deliverQueuedMessages(clientID, sess)

	// Set initial keep-alive deadline so idle clients are detected
	if sess.KeepAlive > 0 {
		timeout := time.Duration(sess.KeepAlive) * time.Second * 3 / 2
		if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			b.logger.Debug("failed to set keep-alive deadline", "clientID", clientID, "error", err)
		}
	}

	// Run read loop (handles its own cleanup via abnormalDisconnect/gracefulDisconnect)
	b.readLoop(clientID, sess, c, conn)

	return nil
}

// findEnhancedAuth returns the registered enhanced authenticator for a method
// name, or nil if none is registered (§4.12).
func (b *Broker) findEnhancedAuth(method string) EnhancedAuthenticator {
	for _, a := range b.opts.enhancedAuth {
		if a.Method() == method {
			return a
		}
	}
	return nil
}

// enhancedAuthHandshake runs the MQTT 5.0 enhanced authentication exchange
// (§4.12). The server answers the CONNECT's AuthenticationData with AUTH
// packets: reason 0x18 (Continue authentication) advances the exchange, and a
// final AUTH 0x00 (Success) completes it. On single-step success (the
// authenticator accepts the CONNECT data immediately) no AUTH packet is sent —
// HandleConnection sends the CONNACK 0x00 afterwards.
func (b *Broker) enhancedAuthHandshake(conn net.Conn, codec *protocol.Codec, connectPkt *protocol.ConnectPacket, auth EnhancedAuthenticator) error {
	var data []byte
	if connectPkt.Properties != nil {
		data = connectPkt.Properties.AuthenticationData
	}
	reason, respData, err := auth.Initial(data)
	if err != nil {
		return fmt.Errorf("broker: enhanced auth initial: %w", err)
	}

	// Bound the exchange so a stalled client cannot hold the connection open.
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		b.logger.Debug("failed to set enhanced-auth read deadline", "error", err)
	}
	defer conn.SetReadDeadline(time.Time{})

	continued := false
	for reason == protocol.AuthContinueAuth {
		continued = true
		if err := b.sendAuthPacket(conn, codec, reason, auth.Method(), respData); err != nil {
			return err
		}
		pkt, err := codec.Decode(conn)
		if err != nil {
			return fmt.Errorf("broker: enhanced auth: read AUTH: %w", err)
		}
		authPkt, ok := pkt.(*protocol.AuthPacket)
		if !ok {
			return fmt.Errorf("broker: enhanced auth: expected AUTH, got %T", pkt)
		}
		var in []byte
		if authPkt.Properties != nil {
			in = authPkt.Properties.AuthenticationData
		}
		reason, respData, err = auth.Continue(in)
		if err != nil {
			return fmt.Errorf("broker: enhanced auth continue: %w", err)
		}
	}
	if reason != protocol.AuthSuccess {
		// AUTH packets only carry 0x00/0x18/0x19; a failed exchange is signalled
		// with a DISCONNECT carrying the rejection reason (§4.12).
		_ = codec.Encode(conn, &protocol.DisconnectPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeDisconnect},
			ReasonCode:  protocol.ReasonCodeNotAuthorized,
		})
		return fmt.Errorf("broker: enhanced auth rejected (reason 0x%02X)", reason)
	}
	// A multi-step exchange ends with a final AUTH 0x00 (Success).
	if continued {
		if err := b.sendAuthPacket(conn, codec, reason, auth.Method(), respData); err != nil {
			return err
		}
	}
	return nil
}

// sendAuthPacket encodes and writes an AUTH packet directly to the connection.
func (b *Broker) sendAuthPacket(conn net.Conn, codec *protocol.Codec, reason byte, method string, data []byte) error {
	pkt := &protocol.AuthPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeAuth},
		ReasonCode:  reason,
		Properties: &protocol.Properties{
			AuthenticationMethod: method,
			AuthenticationData:   data,
		},
	}
	if err := codec.Encode(conn, pkt); err != nil {
		return fmt.Errorf("broker: send AUTH: %w", err)
	}
	return nil
}

// sendAuthPacketToClient queues an AUTH packet on the client's write queue
// (used for re-authentication during an established session).
func (b *Broker) sendAuthPacketToClient(clientID string, reason byte, method string, data []byte) {
	b.writePacket(clientID, &protocol.AuthPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeAuth},
		ReasonCode:  reason,
		Properties: &protocol.Properties{
			AuthenticationMethod: method,
			AuthenticationData:   data,
		},
	})
}

// Start starts the broker's internal subsystems.
func (b *Broker) Start() error {
	if b.started.Swap(true) {
		return fmt.Errorf("broker already started")
	}
	// Rebuild the context so cleanup loops actually run after a Stop->Start
	// cycle (Stop cancels the previous context).
	b.ctx, b.cancel = context.WithCancel(context.Background())
	b.startedAt = time.Now()
	b.qos.Start()
	go b.sessionCleanupLoop()
	if b.opts.retainedExpiry > 0 {
		// Rebuild the in-memory expiry map from persisted retained messages so
		// TTL cleanup survives a restart (P3-5).
		b.rebuildRetainedExpirations()
		go b.retainedCleanupLoop()
	}
	if b.opts.sysInterval > 0 {
		go b.sysStatusLoop()
	}
	return nil
}

// rebuildRetainedExpirations reconstructs the in-memory retained-expiry map and
// count from the store after a restart (P3-5).
func (b *Broker) rebuildRetainedExpirations() {
	if b.retainedStore == nil || b.opts.retainedExpiry <= 0 {
		return
	}
	msgs, err := b.retainedStore.MatchRetained(b.ctx, "#")
	if err != nil {
		b.logger.Debug("failed to list retained messages on start", "error", err)
		return
	}
	b.retainedMu.Lock()
	b.retainedExpirations = make(map[string]time.Time)
	b.retainedCount.Store(0)
	for _, m := range msgs {
		base := m.Timestamp
		if base.IsZero() {
			base = time.Now()
		}
		b.retainedExpirations[m.Topic] = base.Add(b.opts.retainedExpiry)
		b.retainedCount.Add(1)
	}
	b.retainedMu.Unlock()
	b.metrics.SetRetainedMessages(int(b.retainedCount.Load()))
}

// Metrics returns the broker's metrics collector.
func (b *Broker) Metrics() metrics.Metrics {
	return b.metrics
}

// sessionCleanupLoop periodically removes expired sessions from the store.
// It exits when the broker context is cancelled.
func (b *Broker) sessionCleanupLoop() {
	ticker := time.NewTicker(b.opts.sessionCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			b.cleanupExpiredSessions()
		}
	}
}

func (b *Broker) cleanupExpiredSessions() {
	if b.sessionStore == nil {
		return
	}

	clientIDs, err := b.sessionStore.ListSessions(b.ctx)
	if err != nil {
		b.logger.Debug("failed to list sessions for cleanup", "error", err)
		return
	}

	now := time.Now()
	for _, clientID := range clientIDs {
		// Skip connected clients
		b.mu.RLock()
		_, connected := b.connections[clientID]
		b.mu.RUnlock()
		if connected {
			continue
		}

		data, err := b.sessionStore.GetSession(b.ctx, clientID)
		if err != nil {
			continue
		}

		if data.ExpiryInterval > 0 && !data.ExpiryTime.IsZero() && now.After(data.ExpiryTime) {
			if err := b.sessionStore.DeleteSession(b.ctx, clientID); err != nil {
				b.logger.Debug("failed to delete expired session", "clientID", clientID, "error", err)
			} else {
				// Release the expired session's topic-tree subscriptions so
				// they do not leak after the session is gone (P2-13).
				topics := make([]string, 0, len(data.Subscriptions))
				for _, sub := range data.Subscriptions {
					topics = append(topics, sub.Topic)
				}
				b.unsubscribeTopics(clientID, topics)
				b.logger.Debug("expired session cleaned up", "clientID", clientID)
			}
		}
	}
}

// retainedCleanupLoop periodically removes expired retained messages.
func (b *Broker) retainedCleanupLoop() {
	ticker := time.NewTicker(b.opts.retainedCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			b.cleanupExpiredRetained()
		}
	}
}

func (b *Broker) cleanupExpiredRetained() {
	if b.retainedStore == nil || b.opts.retainedExpiry <= 0 {
		return
	}

	b.retainedMu.Lock()
	defer b.retainedMu.Unlock()

	now := time.Now()
	for topic, expiry := range b.retainedExpirations {
		if now.After(expiry) {
			if err := b.retainedStore.DeleteRetained(b.ctx, topic); err != nil {
				b.logger.Debug("failed to delete expired retained message", "topic", topic, "error", err)
				b.metrics.IncErrors("retained_store")
				continue
			}
			b.retainedCount.Add(-1)
			delete(b.retainedExpirations, topic)
			b.logger.Debug("expired retained message cleaned up", "topic", topic)
		}
	}
	b.metrics.SetRetainedMessages(int(b.retainedCount.Load()))
}

// sysStatusLoop periodically publishes $SYS broker status topics (R8).
func (b *Broker) sysStatusLoop() {
	b.publishSystemStatus()
	ticker := time.NewTicker(b.opts.sysInterval)
	defer ticker.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			b.publishSystemStatus()
		}
	}
}

// publishSystemStatus publishes current broker state to $SYS topics (R8).
// $SYS topic protection is enforced by the topic trie, so only clients with an
// explicit $SYS subscription (e.g. $SYS/#) receive these messages.
func (b *Broker) publishSystemStatus() {
	b.mu.RLock()
	conns := len(b.connections)
	b.mu.RUnlock()

	b.publishSystem("$SYS/broker/version", []byte(b.opts.version))
	b.publishSystem("$SYS/broker/uptime", []byte(time.Since(b.startedAt).Round(time.Second).String()))
	b.publishSystem("$SYS/broker/connections", []byte(strconv.Itoa(conns)))
	b.publishSystem("$SYS/broker/retained", []byte(strconv.FormatInt(b.retainedCount.Load(), 10)))
	b.publishSystem("$SYS/broker/subscriptions", []byte(strconv.FormatInt(b.topics.SubscriberCount(), 10)))
}

// publishSystem routes a $SYS status message to matching subscribers using the
// normal delivery path (flow control, per-connection write queue).
func (b *Broker) publishSystem(topic string, payload []byte) {
	pkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
		Topic:       topic,
		Payload:     payload,
	}
	for _, sub := range b.topics.Match(topic) {
		b.deliverToClient(sub.ClientID, "$SYS", pkt)
	}
}

// Stop stops the broker's internal subsystems and closes all sessions.
// Connections are closed first to stop readLoops and prevent new inflight
// messages from arriving during shutdown. Then we drain remaining inflight
// and stop QoS/will subsystems.
func (b *Broker) Stop() {
	b.cancel()

	// Close all client connections first — stops readLoops so no
	// new messages can arrive during the drain phase.
	b.mu.Lock()
	for id, cs := range b.connections {
		if err := cs.conn.Close(); err != nil {
			b.logger.Debug("failed to close client connection", "clientID", id, "error", err)
		}
		if cs.out != nil {
			close(cs.stopWrites)
		}
		delete(b.connections, id)
	}
	b.mu.Unlock()

	// Drain remaining in-flight messages (they are already queued,
	// no new ones can arrive since connections are closed).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		total := 0
		for _, clientID := range b.sessions.ListSessions() {
			total += b.qos.InflightCount(clientID)
		}
		if total == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	b.qos.Stop()
	b.will.Stop()
	b.started.Store(false) // allow re-Start after Stop
}

func (b *Broker) dispatch(hook plugin.Hook, data *plugin.Context) {
	if b.pluginMgr == nil {
		return
	}
	if err := b.pluginMgr.Dispatch(b.ctx, hook, data); err != nil {
		b.logger.Debug("plugin dispatch error", "hook", hook, "error", err)
	}
}

// unsubscribeTopics removes a set of topic filters (regular or $share shared)
// from the topic tree for a client.
func (b *Broker) unsubscribeTopics(clientID string, topics []string) {
	for _, topic := range topics {
		if IsSharedSubscription(topic) {
			shareName, realFilter, ok := ParseSharedFilter(topic)
			if ok {
				b.topics.UnsubscribeShared(shareName, realFilter, clientID)
			}
			continue
		}
		b.topics.Unsubscribe(topic, clientID)
	}
}

// unsubscribeSessionTopics releases all of a session's topic-tree entries.
func (b *Broker) unsubscribeSessionTopics(sess *Session) {
	sess.mu.RLock()
	topics := make([]string, 0, len(sess.Subscriptions))
	for topic := range sess.Subscriptions {
		topics = append(topics, topic)
	}
	sess.mu.RUnlock()
	b.unsubscribeTopics(sess.ClientID, topics)
}

func (b *Broker) disconnect(clientID string, conn net.Conn) {
	// The will is NOT touched here. gracefulDisconnect removes it and
	// abnormalDisconnect triggers it; calling RemoveWill here cancelled a
	// just-armed delayed will (P2-5b) and, on takeover, deleted the NEW
	// connection's will (P2-10).

	// Persist session BEFORE taking the lock, since Save() is a heavy
	// operation that should not block connection registration.
	if sess, ok := b.sessions.GetSession(clientID); ok && !sess.IsClean && b.sessionStore != nil {
		if err := sess.Save(b.ctx, b.sessionStore); err != nil {
			b.logger.Debug("failed to save session", "clientID", clientID, "error", err)
			b.metrics.IncErrors("session_save")
		}
	}

	// Atomic identity check + cleanup under a single Lock to prevent
	// session takeover race. Hold b.mu.Lock() throughout cleanup so
	// HandleConnection cannot register a new connection mid-cleanup.
	b.mu.Lock()
	cs, exists := b.connections[clientID]
	if !exists || cs.conn != conn {
		b.mu.Unlock()
		return
	}

	// Reset flow control counter before removing session. A clean session
	// also releases its topic-tree subscriptions so stale entries do not
	// accumulate across disconnect/reconnect cycles (P2-13, NEW-3).
	// Persistent sessions keep theirs for offline queueing and are
	// re-subscribed on reconnect; any flow-control-buffered deliveries are
	// persisted so they are not lost (P1-5 + P2-14).
	if sess, ok := b.sessions.GetSession(clientID); ok {
		sess.ResetOutboundUnacked()
		if sess.IsClean {
			b.unsubscribeSessionTopics(sess)
		} else if b.messageStore != nil {
			b.persistBufferedOutbound(clientID, sess)
		}
	}

	b.sessions.RemoveSession(clientID)
	b.qos.RemoveClient(clientID)
	delete(b.connections, clientID)
	// Stop this connection's writer goroutine. Producers that already passed
	// the map lookup select on stopWrites and back off instead of enqueuing to
	// a torn-down connection (R1).
	if cs.out != nil {
		close(cs.stopWrites)
	}
	online := len(b.connections)
	b.mu.Unlock()

	b.receivedQoS2Mu.Lock()
	delete(b.receivedQoS2, clientID)
	b.receivedQoS2Mu.Unlock()

	b.metrics.SetOnlineSessions(online)
	// Plugin hook
	b.dispatch(plugin.OnClose, &plugin.Context{ClientID: clientID})

	// Metrics
	b.metrics.OnDisconnect()

	b.logger.Info("client disconnected", "clientID", clientID)
}

func (b *Broker) readLoop(clientID string, sess *Session, codec *protocol.Codec, conn net.Conn) {
	for {
		pkt, err := codec.Decode(conn)
		if err != nil {
			b.logger.Debug("read error", "clientID", clientID, "error", err)
			b.abnormalDisconnect(clientID, conn)
			return
		}

		// Plugin hook: OnMessage
		b.dispatch(plugin.OnMessage, &plugin.Context{ClientID: clientID})

		// Update activity
		sess.UpdateActivity()

		// Set keep-alive deadline
		if sess.KeepAlive > 0 {
			timeout := time.Duration(sess.KeepAlive) * time.Second * 3 / 2
			if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
				b.logger.Debug("failed to refresh keep-alive deadline", "clientID", clientID, "error", err)
			}
		}

		switch p := pkt.(type) {
		case *protocol.PublishPacket:
			sess.TrackReceived(len(p.Payload) + len(p.Topic))
			b.handlePublish(clientID, sess, p)
		case *protocol.SubscribePacket:
			b.handleSubscribe(clientID, sess, p)
		case *protocol.UnsubscribePacket:
			b.handleUnsubscribe(clientID, sess, p)
		case *protocol.PingReqPacket:
			b.writePacket(clientID, &protocol.PingRespPacket{
				FixedHeader: protocol.FixedHeader{
					PacketType: protocol.PacketTypePingResp,
				},
			})
		case *protocol.DisconnectPacket:
			b.logger.Info("graceful disconnect", "clientID", clientID)
			b.gracefulDisconnect(clientID, conn)
			return
		case *protocol.PubAckPacket:
			b.handlePubAck(clientID, p.PacketID)
		case *protocol.PubRecPacket:
			b.handlePubRec(clientID, p.PacketID)
		case *protocol.PubRelPacket:
			b.handlePubRel(clientID, p.PacketID)
		case *protocol.PubCompPacket:
			b.handlePubComp(clientID, p.PacketID)
		case *protocol.AuthPacket:
			// MQTT 5.0 re-authentication (§4.12): a connection established via
			// enhanced auth may exchange more AUTH packets during the session.
			if cs := b.connection(clientID); cs != nil && cs.enhancedAuth != nil {
				var in []byte
				if p.Properties != nil {
					in = p.Properties.AuthenticationData
				}
				reason, respData, err := cs.enhancedAuth.Continue(in)
				if err != nil {
					b.logger.Debug("re-auth failed", "clientID", clientID, "error", err)
					b.writePacket(clientID, &protocol.DisconnectPacket{
						FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeDisconnect},
						ReasonCode:  protocol.ReasonCodeBadAuthMethod,
					})
					b.gracefulDisconnect(clientID, conn)
					return
				}
				b.sendAuthPacketToClient(clientID, reason, cs.enhancedAuth.Method(), respData)
				if reason != protocol.AuthContinueAuth {
					b.gracefulDisconnect(clientID, conn)
					return
				}
				continue
			}
			// No enhanced auth: reject AUTH per §4.12.
			b.logger.Debug("AUTH packet received but no enhanced auth in use",
				"clientID", clientID, "reasonCode", p.ReasonCode)
			b.writePacket(clientID, &protocol.DisconnectPacket{
				FixedHeader: protocol.FixedHeader{
					PacketType: protocol.PacketTypeDisconnect,
				},
				ReasonCode: protocol.ReasonCodeBadAuthMethod,
			})
			b.gracefulDisconnect(clientID, conn)
			return
		default:
			b.logger.Debug("unhandled packet type", "clientID", clientID, "type", fmt.Sprintf("%T", pkt))
		}
	}
}

func (b *Broker) handlePublish(clientID string, sess *Session, pkt *protocol.PublishPacket) {
	start := time.Now()
	defer func() {
		b.metrics.ObserveMessageLatency(time.Since(start).Seconds(), pkt.FixedHeader.QoS)
	}()

	// Check client publish rate limit
	if sess != nil && sess.publishRate != nil && !sess.publishRate.Allow() {
		b.metrics.IncMessagesDropped("rate_limited")
		b.metrics.IncErrors("rate_limit")
		b.logger.Debug("publish rate limit exceeded", "clientID", clientID)
		return
	}

	// Resolve Topic Alias (MQTT 5.0 §3.3.2.3.4).
	// If TopicAlias is set and TopicName is empty, resolve from alias map.
	// If both are set, register/replace the alias mapping.
	if pkt.Properties != nil && pkt.Properties.TopicAlias != nil {
		alias := *pkt.Properties.TopicAlias
		if alias == 0 {
			// Alias 0 is reserved (MQTT 5.0 §3.3.2.3.4).
			b.writePacket(clientID, &protocol.PubAckPacket{
				FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubAck},
				PacketID:    pkt.PacketID,
				ReasonCode:  protocol.ReasonCodeTopicAliasInvalid,
			})
			b.metrics.IncMessagesDropped("invalid_topic_alias")
			return
		}
		if pkt.Topic == "" {
			// Resolve alias to topic.
			resolved, ok := sess.ResolveTopicAlias(alias)
			if !ok {
				b.metrics.IncMessagesDropped("unknown_topic_alias")
				return
			}
			pkt.Topic = resolved
		} else {
			// Register alias mapping.
			if err := sess.RegisterTopicAlias(alias, pkt.Topic); err != nil {
				b.logger.Debug("failed to register topic alias", "clientID", clientID, "alias", alias, "error", err)
			}
		}
	}

	// Check Message Expiry Interval (MQTT 5.0 §3.3.2.3.2).
	// Drop messages that have already expired.
	if pkt.Properties != nil && pkt.Properties.MessageExpiryInterval != nil {
		if *pkt.Properties.MessageExpiryInterval == 0 {
			b.metrics.IncMessagesDropped("message_expired")
			return
		}
	}

	// Reject wildcard topics per MQTT spec §3.3.2
	if !protocol.ValidatePublishTopic(pkt.Topic) {
		if pkt.QoS > 0 {
			b.writePacket(clientID, &protocol.PubAckPacket{
				FixedHeader: protocol.FixedHeader{
					PacketType: protocol.PacketTypePubAck,
				},
				PacketID:   pkt.PacketID,
				ReasonCode: protocol.ReasonCodeTopicNameInvalid,
			})
		}
		b.metrics.IncMessagesDropped("invalid_topic")
		return
	}

	b.metrics.IncMessagesPublished(pkt.QoS)

	// Check authorization
	username := ""
	if sess != nil {
		username = sess.Username
	}
	if b.opts.authorizer != nil && !b.opts.authorizer.CanPublish(b.ctx, username, pkt.Topic) {
		if pkt.QoS > 0 {
			b.writePacket(clientID, &protocol.PubAckPacket{
				FixedHeader: protocol.FixedHeader{
					PacketType: protocol.PacketTypePubAck,
				},
				PacketID:   pkt.PacketID,
				ReasonCode: protocol.ReasonCodeNotAuthorized,
			})
		}
		b.metrics.IncAuthFailures()
		return
	}

	// Handle retained message
	if pkt.Retain {
		b.handleRetainedMessage(pkt)
	}

	// QoS 2: detect duplicate PUBLISH (DUP flag). Per MQTT §4.3.3, when a
	// publisher resends a QoS 2 PUBLISH because the PUBREC was lost, the broker
	// must re-send PUBREC without re-processing the message.
	if pkt.QoS == 2 && pkt.Dup {
		b.receivedQoS2Mu.Lock()
		clientDups := b.receivedQoS2[clientID]
		if clientDups != nil {
			if _, dup := clientDups[pkt.PacketID]; dup {
				b.receivedQoS2Mu.Unlock()
				b.writePacket(clientID, &protocol.PubRecPacket{
					FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubRec},
					PacketID:    pkt.PacketID,
					ReasonCode:  protocol.ReasonCodeSuccess,
				})
				return
			}
		}
		b.receivedQoS2Mu.Unlock()
	}

	// QoS 2: defer subscriber delivery until PUBCOMP completes the handshake.
	// The duplicate-tracking entry is only added for accepted messages, so the
	// map stays bounded by the QoS engine's maxInflight (P2-15).
	if pkt.QoS == 2 {
		var reasonCode byte = protocol.ReasonCodeSuccess
		if err := b.qos.TrackQoS2(clientID, pkt.PacketID, pkt.Topic, pkt.Payload, pkt.Retain); err != nil {
			reasonCode = protocol.ReasonCodeReceiveMaxExceeded
		} else {
			b.receivedQoS2Mu.Lock()
			if b.receivedQoS2[clientID] == nil {
				b.receivedQoS2[clientID] = make(map[uint16]struct{})
			}
			b.receivedQoS2[clientID][pkt.PacketID] = struct{}{}
			b.receivedQoS2Mu.Unlock()
		}
		b.writePacket(clientID, &protocol.PubRecPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePubRec,
			},
			PacketID:   pkt.PacketID,
			ReasonCode: reasonCode,
		})
		return
	}

	// Route to subscribers for QoS 0 and QoS 1. Live forwards to established
	// subscriptions always carry Retain=0 regardless of the source flag.
	forwardPkt := *pkt
	forwardPkt.FixedHeader.Retain = false
	subscribers := b.topics.Match(forwardPkt.Topic)
	for _, sub := range subscribers {
		b.deliverToClient(sub.ClientID, clientID, &forwardPkt)
	}
	// Route to shared subscribers (round-robin, one per share group)
	b.routeSharedPublish(clientID, &forwardPkt)

	// Send PUBACK for QoS 1. Incoming QoS 1 has no client acknowledgment and
	// must NOT be tracked in the QoS engine: tracking it here caused the
	// retry loop to re-route the message to subscribers (duplicate delivery)
	// because the inflight entry could never be acked.
	if pkt.QoS == 1 {
		b.writePacket(clientID, &protocol.PubAckPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePubAck,
			},
			PacketID:   pkt.PacketID,
			ReasonCode: protocol.ReasonCodeSuccess,
		})
	}
}

func (b *Broker) handleRetainedMessage(pkt *protocol.PublishPacket) {
	if b.retainedStore == nil {
		return
	}

	b.retainedMu.Lock()
	defer b.retainedMu.Unlock()

	existed := true
	if _, err := b.retainedStore.GetRetained(b.ctx, pkt.Topic); err != nil {
		if errors.Is(err, store.ErrRetainedNotFound) {
			existed = false
		} else {
			b.logger.Debug("failed to check retained message", "topic", pkt.Topic, "error", err)
			b.metrics.IncErrors("retained_store")
			return
		}
	}

	// Enforce the retained-message count limit for NEW topics. The check and
	// the store write share one lock, so concurrent publishers cannot both
	// pass the limit and exceed maxRetainedTopics (NEW-5).
	if len(pkt.Payload) > 0 && !existed && b.opts.maxRetainedTopics > 0 &&
		int(b.retainedCount.Load()) >= b.opts.maxRetainedTopics {
		b.logger.Debug("retained message limit reached, dropping",
			"topic", pkt.Topic, "max", b.opts.maxRetainedTopics)
		b.metrics.IncMessagesDropped("retained_limit")
		return
	}

	if len(pkt.Payload) == 0 {
		if err := b.retainedStore.DeleteRetained(b.ctx, pkt.Topic); err != nil {
			b.logger.Debug("failed to delete retained message", "topic", pkt.Topic, "error", err)
			b.metrics.IncErrors("retained_store")
			return
		}
		if existed {
			b.retainedCount.Add(-1)
		}
		delete(b.retainedExpirations, pkt.Topic)
		b.metrics.SetRetainedMessages(int(b.retainedCount.Load()))
		return
	}

	if err := b.retainedStore.SaveRetained(b.ctx, pkt.Topic, pkt.FixedHeader.QoS, pkt.Payload); err != nil {
		b.logger.Debug("failed to save retained message", "topic", pkt.Topic, "error", err)
		b.metrics.IncErrors("retained_store")
		return
	}
	if !existed {
		b.retainedCount.Add(1)
	}
	// Record expiry for retained TTL cleanup if configured
	if b.opts.retainedExpiry > 0 {
		b.retainedExpirations[pkt.Topic] = time.Now().Add(b.opts.retainedExpiry)
	}
	b.metrics.SetRetainedMessages(int(b.retainedCount.Load()))
}

func (b *Broker) handleSubscribe(clientID string, sess *Session, pkt *protocol.SubscribePacket) {
	// Enforce topic filter count limit to prevent resource exhaustion
	if b.opts.maxTopicFiltersPerSub > 0 && len(pkt.Topics) > b.opts.maxTopicFiltersPerSub {
		b.logger.Debug("too many topic filters in SUBSCRIBE",
			"clientID", clientID, "count", len(pkt.Topics),
			"max", b.opts.maxTopicFiltersPerSub)
		// Report failure for every filter instead of granting QoS 0, so the
		// client does not wrongly believe the subscriptions succeeded.
		codes := make([]byte, len(pkt.Topics))
		for i := range codes {
			codes[i] = protocol.SubAckFailure
		}
		b.writePacket(clientID, &protocol.SubAckPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypeSubAck,
			},
			PacketID:    pkt.PacketID,
			ReasonCodes: codes,
		})
		b.metrics.IncErrors("protocol")
		return
	}

	reasonCodes := make([]byte, len(pkt.Topics))
	deliverRetained := make([]bool, len(pkt.Topics))
	for i, topic := range pkt.Topics {
		// Check authorization
		username := ""
		if sess != nil {
			username = sess.Username
		}
		if b.opts.authorizer != nil && !b.opts.authorizer.CanSubscribe(b.ctx, username, topic.Topic) {
			reasonCodes[i] = protocol.SubAckFailure
			continue
		}

		// Handle shared subscriptions ($share/{ShareName}/{filter})
		if IsSharedSubscription(topic.Topic) {
			shareName, realFilter, ok := ParseSharedFilter(topic.Topic)
			if !ok || !protocol.ValidateTopicFilter(realFilter) {
				reasonCodes[i] = protocol.SubAckFailure
				continue
			}
			b.topics.SubscribeShared(shareName, realFilter, clientID, topic.QoS)
			sess.AddSubscriptionFilter(topic)
			reasonCodes[i] = topic.QoS
			deliverRetained[i] = shouldDeliverRetained(topic.RetainHandling, false)
			continue
		}

		existed := sess.HasSubscription(topic.Topic)
		if !b.topics.Subscribe(topic.Topic, clientID, topic.QoS) {
			reasonCodes[i] = protocol.SubAckFailure
			continue
		}
		sess.AddSubscriptionFilter(topic)
		reasonCodes[i] = topic.QoS
		deliverRetained[i] = shouldDeliverRetained(topic.RetainHandling, existed)
	}

	b.writePacket(clientID, &protocol.SubAckPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeSubAck,
		},
		PacketID:    pkt.PacketID,
		ReasonCodes: reasonCodes,
	})

	b.metrics.SetSubscriptions(int(b.topics.SubscriberCount()))

	// Deliver retained messages matching the new subscriptions
	for i, topic := range pkt.Topics {
		if !deliverRetained[i] {
			continue
		}
		// For shared subscriptions, use the real topic filter for retained delivery
		if IsSharedSubscription(topic.Topic) {
			_, realFilter, ok := ParseSharedFilter(topic.Topic)
			if ok && protocol.ValidateTopicFilter(realFilter) {
				b.deliverRetainedMessages(clientID, sess, realFilter)
			}
			continue
		}
		b.deliverRetainedMessages(clientID, sess, topic.Topic)
	}
}

func shouldDeliverRetained(retainHandling uint8, existed bool) bool {
	switch retainHandling {
	case 1:
		return !existed
	case 2:
		return false
	default:
		return true
	}
}

func (b *Broker) handleUnsubscribe(clientID string, sess *Session, pkt *protocol.UnsubscribePacket) {
	var reasonCodes []byte
	if sess != nil && sess.ProtocolVer == protocol.Version50 {
		reasonCodes = make([]byte, 0, len(pkt.Topics))
	}

	for _, topic := range pkt.Topics {
		// Handle shared subscription unsubscribe
		if IsSharedSubscription(topic) {
			shareName, realFilter, ok := ParseSharedFilter(topic)
			if !ok || !protocol.ValidateTopicFilter(realFilter) {
				if reasonCodes != nil {
					reasonCodes = append(reasonCodes, protocol.ReasonCodeTopicFilterInvalid)
				}
				continue
			}
			b.topics.UnsubscribeShared(shareName, realFilter, clientID)
			sess.RemoveSubscription(topic)
			if reasonCodes != nil {
				reasonCodes = append(reasonCodes, protocol.ReasonCodeSuccess)
			}
			continue
		}

		if !protocol.ValidateTopicFilter(topic) {
			if reasonCodes != nil {
				reasonCodes = append(reasonCodes, protocol.ReasonCodeTopicFilterInvalid)
			}
			continue
		}
		b.topics.Unsubscribe(topic, clientID)
		sess.RemoveSubscription(topic)
		if reasonCodes != nil {
			reasonCodes = append(reasonCodes, protocol.ReasonCodeSuccess)
		}
	}

	b.writePacket(clientID, &protocol.UnsubAckPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeUnsubAck,
		},
		PacketID:    pkt.PacketID,
		ReasonCodes: reasonCodes,
	})

	b.metrics.SetSubscriptions(int(b.topics.SubscriberCount()))
}

// messageExpiresAt returns the absolute Message Expiry Interval deadline for a
// PUBLISH, or the zero time if it has none (MQTT 5.0 §3.3.2.3.2).
func messageExpiresAt(pkt *protocol.PublishPacket) time.Time {
	if pkt.Properties != nil && pkt.Properties.MessageExpiryInterval != nil && *pkt.Properties.MessageExpiryInterval > 0 {
		return time.Now().Add(time.Duration(*pkt.Properties.MessageExpiryInterval) * time.Second)
	}
	return time.Time{}
}

// deliverToClient sends a PUBLISH packet to a specific client.
// It checks subscription matches and applies QoS downgrade. An offline
// persistent-session subscriber has its message queued for later delivery
// instead of silently dropped (P1-5).
func (b *Broker) deliverToClient(clientID, sourceClientID string, pkt *protocol.PublishPacket) {
	expiresAt := messageExpiresAt(pkt)
	sess, ok := b.sessions.GetSession(clientID)
	if !ok {
		b.queueOfflineMessage(clientID, pkt, expiresAt)
		return
	}
	if sourceClientID != "" && clientID == sourceClientID && !sess.AllowsLocalPublish(pkt.Topic) {
		return
	}

	// Look up subscription options for the matching subscription to get
	// the SubscriptionIdentifier (MQTT 5.0) for the delivered PUBLISH.
	matches, subQoS, subOpts := sess.MatchesSubscription(pkt.Topic)
	if !matches {
		return
	}
	deliverQoS := pkt.FixedHeader.QoS
	if subQoS < deliverQoS {
		deliverQoS = subQoS
	}
	b.doDeliver(clientID, pkt, deliverQoS, expiresAt, subOpts)
}

// deliverToSharedClient delivers a PUBLISH to a shared subscription member.
// MatchesSubscription is skipped — the match was already done by MatchShared.
func (b *Broker) deliverToSharedClient(clientID, sourceClientID string, pkt *protocol.PublishPacket, subQoS uint8) {
	expiresAt := messageExpiresAt(pkt)
	sess, ok := b.sessions.GetSession(clientID)
	if !ok {
		return
	}
	if sourceClientID != "" && clientID == sourceClientID && !sess.AllowsLocalPublish(pkt.Topic) {
		return
	}
	deliverQoS := pkt.FixedHeader.QoS
	if subQoS < deliverQoS {
		deliverQoS = subQoS
	}
	b.doDeliver(clientID, pkt, deliverQoS, expiresAt, SubscriptionOptions{QoS: subQoS})
}

// queueOfflineMessage stores a QoS 1/2 message for an offline persistent
// session so it can be delivered when the client reconnects (P1-5). QoS 0 is
// fire-and-forget and is never queued.
func (b *Broker) queueOfflineMessage(clientID string, pkt *protocol.PublishPacket, expiresAt time.Time) {
	if b.messageStore == nil || b.sessionStore == nil || pkt.FixedHeader.QoS == 0 {
		return
	}
	exists, err := b.sessionStore.IsSessionExists(b.ctx, clientID)
	if err != nil || !exists {
		return
	}
	data, err := b.sessionStore.GetSession(b.ctx, clientID)
	if err != nil {
		return
	}
	if data.ExpiryInterval > 0 && !data.ExpiryTime.IsZero() && time.Now().After(data.ExpiryTime) {
		return // session already expired; nothing to deliver to
	}
	msg := &store.StoredMessage{
		ID:        fmt.Sprintf("%s-%d", clientID, time.Now().UnixNano()),
		Topic:     pkt.Topic,
		QoS:       pkt.FixedHeader.QoS,
		Payload:   pkt.Payload,
		Retain:    pkt.Retain,
		Timestamp: time.Now(),
		ExpiresAt: expiresAt,
	}
	if err := b.messageStore.SaveMessage(b.ctx, clientID, msg); err != nil {
		b.metrics.IncErrors("message_store")
		b.logger.Debug("failed to queue offline message", "clientID", clientID, "error", err)
	}
}

// deliverQueuedMessages delivers messages queued while a persistent session
// was offline, respecting the subscription QoS downgrade and the client's
// ReceiveMaximum. Delivered messages are removed from the store; messages that
// would exceed the flow-control window stay queued for a later reconnect.
func (b *Broker) deliverQueuedMessages(clientID string, sess *Session) {
	if b.messageStore == nil {
		return
	}
	msgs, err := b.messageStore.ListMessages(b.ctx, clientID)
	if err != nil {
		b.logger.Debug("failed to list queued messages", "clientID", clientID, "error", err)
		return
	}
	for _, msg := range msgs {
		// Drop a queued message whose Message Expiry Interval has passed while
		// the client was offline (§3.3.2.3.2).
		if !msg.ExpiresAt.IsZero() && time.Now().After(msg.ExpiresAt) {
			b.metrics.IncMessagesDropped("message_expired")
			if err := b.messageStore.DeleteMessage(b.ctx, clientID, msg.ID); err != nil {
				b.logger.Debug("failed to delete expired queued message", "clientID", clientID, "error", err)
			}
			continue
		}
		matches, subQoS, subOpts := sess.MatchesSubscription(msg.Topic)
		if !matches {
			continue
		}
		deliverQoS := msg.QoS
		if subQoS < deliverQoS {
			deliverQoS = subQoS
		}
		if deliverQoS > 0 && !sess.CanSendOutbound() {
			continue // keep queued; the client must catch up on flow control first
		}
		pubPkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePublish,
				QoS:        deliverQoS,
				Retain:     msg.Retain,
			},
			Topic:   msg.Topic,
			Payload: msg.Payload,
		}
		if !msg.ExpiresAt.IsZero() {
			remaining := uint32(time.Until(msg.ExpiresAt).Seconds())
			if remaining < 1 {
				remaining = 1 // never advertise 0 (0 means "already expired")
			}
			pubPkt.Properties = &protocol.Properties{MessageExpiryInterval: &remaining}
		}
		b.doDeliver(clientID, pubPkt, deliverQoS, msg.ExpiresAt, subOpts)
		if err := b.messageStore.DeleteMessage(b.ctx, clientID, msg.ID); err != nil {
			b.logger.Debug("failed to delete queued message", "clientID", clientID, "error", err)
		}
	}
}

// doDeliver performs the actual PUBLISH write after QoS negotiation.
// If subscription options include a SubscriptionIdentifier, it is added to
// the outgoing PUBLISH properties (MQTT 5.0 3.3.2.3.7).
func (b *Broker) doDeliver(clientID string, pkt *protocol.PublishPacket, deliverQoS uint8, expiresAt time.Time, subOpts ...SubscriptionOptions) {
	sess, ok := b.sessions.GetSession(clientID)
	if !ok {
		return
	}

	// MQTT 5.0 §3.3.2.3.2: never onward-deliver a message whose Message Expiry
	// Interval has passed.
	if !expiresAt.IsZero() && time.Now().After(expiresAt) {
		b.metrics.IncMessagesDropped("message_expired")
		return
	}

	// MQTT 5.0 flow control (ReceiveMaximum). When the client's receive window
	// is full, buffer the QoS 1/2 message instead of silently dropping it
	// (P2-14); it is flushed once the client acknowledges earlier deliveries.
	// The buffer is bounded (R6): if a client never acknowledges and the buffer
	// is full, the new message is dropped rather than exhausting memory.
	if deliverQoS > 0 && !sess.CanSendOutbound() {
		var opts SubscriptionOptions
		if len(subOpts) > 0 {
			opts = subOpts[0]
		}
		if !sess.BufferOutbound(pkt, deliverQoS, opts, expiresAt) {
			b.metrics.IncMessagesDropped("receive_max_buffered_overflow")
			b.metrics.IncErrors("flow_control")
			b.logger.Debug("outbound flow-control buffer full, dropping message",
				"clientID", clientID, "max", maxBufferedOutbound)
		}
		return
	}

	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        deliverQoS,
			Retain:     pkt.Retain,
		},
		Topic:   pkt.Topic,
		Payload: pkt.Payload,
	}

	// Include SubscriptionIdentifier from the matching subscription (MQTT 5.0),
	// and the remaining Message Expiry Interval (§3.3.2.3.2).
	if len(subOpts) > 0 && subOpts[0].SubscriptionIdentifier != nil || !expiresAt.IsZero() {
		if pubPkt.Properties == nil {
			pubPkt.Properties = &protocol.Properties{}
		}
		if len(subOpts) > 0 && subOpts[0].SubscriptionIdentifier != nil {
			pubPkt.Properties.SubscriptionIdentifier = subOpts[0].SubscriptionIdentifier
		}
		if !expiresAt.IsZero() {
			remaining := uint32(time.Until(expiresAt).Seconds())
			if remaining < 1 {
				remaining = 1 // never advertise 0 (0 means "already expired")
			}
			pubPkt.Properties.MessageExpiryInterval = &remaining
		}
	}

	if deliverQoS > 0 {
		pubPkt.PacketID = sess.NextPacketID()
		// Track the outbound delivery so it can be retried until the client
		// acknowledges it (NEW-1) and persisted across a disconnect (P2-3).
		sess.AddInflight(&InflightMsg{
			PacketID:  pubPkt.PacketID,
			QoS:       deliverQoS,
			Topic:     pkt.Topic,
			Payload:   pkt.Payload,
			Retain:    pkt.Retain,
			SentAt:    time.Now(),
			ExpiresAt: expiresAt,
		})
		if deliverQoS == 1 {
			_ = b.qos.TrackOutboundQoS1(clientID, pubPkt.PacketID, pkt.Topic, pkt.Payload, pkt.Retain)
		} else {
			_ = b.qos.TrackOutboundQoS2(clientID, pubPkt.PacketID, pkt.Topic, pkt.Payload, pkt.Retain)
		}
	}

	sess.TrackSent(len(pkt.Topic) + len(pkt.Payload))

	if deliverQoS > 0 {
		sess.IncOutboundUnacked()
	}

	b.writePacket(clientID, pubPkt)
	b.metrics.IncMessagesDelivered(deliverQoS)
}

// flushBufferedOutbound delivers messages buffered while the client's receive
// window was full, as soon as the window has room again (P2-14). Called after
// an acknowledgment frees a slot.
func (b *Broker) flushBufferedOutbound(clientID string, sess *Session) {
	for sess.OutboundQueueLen() > 0 {
		if !sess.CanSendOutbound() {
			return
		}
		msg, _ := sess.PopOutbound()
		b.doDeliver(clientID, msg.pkt, msg.deliverQoS, msg.expiresAt, msg.subOpts)
	}
}

// persistBufferedOutbound moves a persistent session's buffered deliveries into
// the message store when it disconnects, so they are not lost and are delivered
// on reconnect (P1-5 + P2-14).
func (b *Broker) persistBufferedOutbound(clientID string, sess *Session) {
	if b.messageStore == nil {
		return
	}
	for {
		msg, ok := sess.PopOutbound()
		if !ok {
			return
		}
		sm := &store.StoredMessage{
			ID:        fmt.Sprintf("%s-%d", clientID, time.Now().UnixNano()),
			Topic:     msg.pkt.Topic,
			QoS:       msg.deliverQoS,
			Payload:   msg.pkt.Payload,
			Retain:    msg.pkt.Retain,
			Timestamp: time.Now(),
			ExpiresAt: msg.expiresAt,
		}
		if err := b.messageStore.SaveMessage(b.ctx, clientID, sm); err != nil {
			b.metrics.IncErrors("message_store")
		}
	}
}

// isClientOnline reports whether a client currently has a connected session.
func (b *Broker) isClientOnline(clientID string) bool {
	_, ok := b.sessions.GetSession(clientID)
	return ok
}

// connection returns the clientState for a client, or nil if not connected.
func (b *Broker) connection(clientID string) *clientState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.connections[clientID]
}

// routeSharedPublish delivers a PUBLISH to shared subscribers via MatchShared,
// which selects one subscriber per share group using round-robin. Only online
// members are considered so the message is never handed to an offline member
// (P2-9).
func (b *Broker) routeSharedPublish(sourceClientID string, pkt *protocol.PublishPacket) {
	shared := b.topics.MatchSharedOnline(pkt.Topic, b.isClientOnline)
	for _, sub := range shared {
		b.deliverToSharedClient(sub.ClientID, sourceClientID, pkt, sub.QoS)
	}
}

func (b *Broker) deliverRetainedMessages(clientID string, sess *Session, topicFilter string) {
	if b.retainedStore == nil {
		return
	}

	retained, err := b.retainedStore.MatchRetained(b.ctx, topicFilter)
	if err != nil {
		b.logger.Debug("failed to match retained messages", "filter", topicFilter, "error", err)
		return
	}
	if len(retained) == 0 {
		return
	}

	for _, msg := range retained {
		// Use the sys-topic-protected matcher so a $SYS retained message is
		// never delivered to a bare '#' or '+' subscription (P3-4).
		matches, subQoS, subOpts := sess.MatchesRetainedSubscription(msg.Topic)
		if !matches {
			continue
		}
		deliverQoS := msg.QoS
		if subQoS < deliverQoS {
			deliverQoS = subQoS
		}
		pubPkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePublish,
				QoS:        deliverQoS,
				Retain:     true,
			},
			Topic:   msg.Topic,
			Payload: msg.Payload,
		}
		// doDeliver applies flow control (buffering when the window is full),
		// assigns the packet ID, and tracks the delivery (NEW-4). Retained
		// messages carry no per-message expiry here; their lifecycle is handled
		// by the retained-message TTL (retained_expiry).
		b.doDeliver(clientID, pubPkt, deliverQoS, time.Time{}, subOpts)
	}
}

// writePacket queues a packet to a client via its per-connection write queue
// (R1). QoS 0 publishes are at-most-once: when the queue is full they are
// dropped instead of blocking, so a slow subscriber cannot stall the producing
// client's read loop. Control packets and QoS 1/2 deliveries apply backpressure
// because the protocol requires them to reach the client.
func (b *Broker) writePacket(clientID string, pkt protocol.Packet) {
	b.mu.RLock()
	cs, ok := b.connections[clientID]
	b.mu.RUnlock()
	if !ok {
		return
	}

	if pub, ok := pkt.(*protocol.PublishPacket); ok && pub.FixedHeader.QoS == 0 && cs.out != nil {
		select {
		case cs.out <- pkt:
			return
		case <-cs.stopWrites:
			return
		default:
			b.metrics.IncMessagesDropped("write_queue_full")
			return
		}
	}

	if err := cs.writeOrEnqueue(pkt); err != nil {
		b.logger.Warn("write error", "clientID", clientID, "error", err)
	}
}

// writeOrEnqueue writes pkt to a client connection. With an async write queue
// configured it enqueues and returns; the writer goroutine serializes the
// actual socket writes (R1). Without one (clientStates created outside
// HandleConnection, e.g. unit tests) it writes synchronously under wmu.
func (cs *clientState) writeOrEnqueue(pkt protocol.Packet) error {
	if cs.out == nil {
		cs.wmu.Lock()
		err := cs.codec.Encode(cs.conn, pkt)
		cs.wmu.Unlock()
		return err
	}
	select {
	case cs.out <- pkt:
	case <-cs.stopWrites:
	}
	return nil
}

// writeLoop drains a connection's outbound queue, serializing all socket
// writes to that connection on a single goroutine (R1). It exits when the
// connection is torn down (stopWrites closed) or a write fails. QoS 1/2
// deliveries left behind are retried from the session's inflight tracking on
// reconnect, so dropping them here does not break delivery semantics.
func (cs *clientState) writeLoop() {
	for {
		select {
		case pkt := <-cs.out:
			if err := cs.codec.Encode(cs.conn, pkt); err != nil {
				return
			}
			// Transports that frame packets (WebSocket) flush the buffered
			// packet as one message after each Encode (R5).
			if f, ok := cs.conn.(packetFlusher); ok {
				if err := f.FlushPacket(); err != nil {
					return
				}
			}
		case <-cs.stopWrites:
			return
		}
	}
}

func (b *Broker) sendConnAck(clientID string, reasonCode byte, sessionPresent bool, sess *Session) {
	b.mu.RLock()
	cs, ok := b.connections[clientID]
	b.mu.RUnlock()
	if !ok {
		return
	}
	pkt := &protocol.ConnAckPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnAck,
		},
		ReasonCode:     reasonCode,
		SessionPresent: sessionPresent,
	}

	// MQTT 5.0: advertise server capabilities
	if sess != nil && sess.ProtocolVer == protocol.Version50 {
		pkt.Properties = b.buildConnAckProperties(sess)
	}

	if err := cs.writeOrEnqueue(pkt); err != nil {
		b.logger.Warn("write error", "clientID", clientID, "error", err)
	}
}

// buildConnAckProperties builds MQTT 5.0 CONNACK capability properties.
func (b *Broker) buildConnAckProperties(sess *Session) *protocol.Properties {
	xretainAvailable := byte(0)
	if b.retainedStore != nil {
		xretainAvailable = 1
	}
	wildcardAvailable := byte(1)
	subIDAvailable := byte(0)
	sharedSubAvailable := byte(1)

	receiveMax := sess.ReceiveMax
	if receiveMax == 0 {
		receiveMax = 65535
	}
	maxPktSize := uint32(b.opts.maxPacketSize)
	// MQTT 5.0 §3.2.2.3.8: only advertise Response Information when the client
	// requested it, and return the client ID as the basis for building a
	// response topic (§3.2.2.3.9).
	requestResponseInfo := byte(0)
	if sess.RequestResponseInfo {
		requestResponseInfo = 1
	}

	props := &protocol.Properties{
		SessionExpiryInterval: &sess.ExpiryInterval,
		ReceiveMaximum:        &receiveMax,
		RetainAvailable:       &xretainAvailable,
		MaximumPacketSize:     &maxPktSize,
		WildcardSubAvailable:  &wildcardAvailable,
		SubIDAvailable:        &subIDAvailable,
		SharedSubAvailable:    &sharedSubAvailable,
		RequestResponseInfo:   &requestResponseInfo,
	}
	if sess.RequestResponseInfo {
		props.ResponseInfo = sess.ClientID
	}

	// Advertise Topic Alias Maximum if the session negotiated a non-zero value.
	if sess.TopicAliasMax > 0 {
		tam := sess.TopicAliasMax
		props.TopicAliasMaximum = &tam
	}

	// Server Keep Alive: if server enforces a shorter keep-alive than client requests.
	if sess.ServerKeepAlive != nil {
		props.ServerKeepAlive = sess.ServerKeepAlive
	}

	// Assigned Client ID: if server generated the client ID.
	if sess.AssignedClientID != "" {
		props.AssignedClientID = sess.AssignedClientID
	}

	return props
}

func (b *Broker) sendConnAckRaw(conn net.Conn, codec *protocol.Codec, reasonCode byte, sessionPresent bool) {
	pkt := &protocol.ConnAckPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnAck,
		},
		ReasonCode:     reasonCode,
		SessionPresent: sessionPresent,
	}
	if err := codec.Encode(conn, pkt); err != nil {
		b.logger.Debug("failed to send CONNACK", "error", err)
	}
}

func (b *Broker) handlePubAck(clientID string, packetID uint16) {
	b.qos.AckQoS1(clientID, packetID)
	if sess, ok := b.sessions.GetSession(clientID); ok {
		// Only count a real outbound message as acknowledged; a spurious
		// PUBACK must not drive the flow-control counter below zero (P2-16).
		if sess.RemoveInflight(packetID) {
			sess.DecOutboundUnacked()
		}
		b.flushBufferedOutbound(clientID, sess)
	}
}

func (b *Broker) handlePubRec(clientID string, packetID uint16) {
	b.qos.AckPubRec(clientID, packetID)
}

func (b *Broker) handlePubRel(clientID string, packetID uint16) {
	// Retrieve the inflight message to route to subscribers after QoS 2 handshake
	msg, ok := b.qos.GetInflight(clientID, packetID)
	if ok && msg.QoS == 2 {
		pubPkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePublish,
				QoS:        msg.QoS,
				// Live forward to established subscriptions: Retain=0.
				Retain: false,
			},
			Topic:    msg.Topic,
			Payload:  msg.Payload,
			PacketID: msg.PacketID,
		}
		subscribers := b.topics.Match(msg.Topic)
		for _, sub := range subscribers {
			b.deliverToClient(sub.ClientID, clientID, pubPkt)
		}
		b.routeSharedPublish(clientID, pubPkt)
	}
	b.qos.AckPubRel(clientID, packetID)
}

func (b *Broker) handlePubComp(clientID string, packetID uint16) {
	b.qos.AckPubComp(clientID, packetID)
	if sess, ok := b.sessions.GetSession(clientID); ok {
		// Only count a real outbound message as acknowledged; an inbound QoS 2
		// PUBCOMP has no outbound inflight entry (P2-16).
		if sess.RemoveInflight(packetID) {
			sess.DecOutboundUnacked()
		}
		b.flushBufferedOutbound(clientID, sess)
	}
	b.receivedQoS2Mu.Lock()
	if clientDups := b.receivedQoS2[clientID]; clientDups != nil {
		delete(clientDups, packetID)
	}
	b.receivedQoS2Mu.Unlock()
}

func (b *Broker) sendPubAck(clientID string, packetID uint16) error {
	pkt := &protocol.PubAckPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePubAck,
		},
		PacketID: packetID,
	}
	b.writePacket(clientID, pkt)
	return nil
}

func (b *Broker) sendPubRel(clientID string, packetID uint16) error {
	pkt := &protocol.PubRelPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePubRel,
			QoS:        1,
		},
		PacketID: packetID,
	}
	b.writePacket(clientID, pkt)
	return nil
}

func (b *Broker) sendPubComp(clientID string, packetID uint16) error {
	pkt := &protocol.PubCompPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePubComp,
		},
		PacketID: packetID,
	}
	b.writePacket(clientID, pkt)
	return nil
}

func (b *Broker) republish(clientID string, packetID uint16, topic string, payload []byte, qos uint8, retain bool) error {
	// The QoS engine's retry callback fires for both directions. An outbound
	// delivery (broker->subscriber, tracked in the session's Inflight) is
	// re-sent as a PUBLISH with the DUP flag. An inbound QoS 2 publish has the
	// client waiting on our PUBREC, so re-send the PUBREC.
	if sess, ok := b.sessions.GetSession(clientID); ok {
		if msg, found := sess.GetInflight(packetID); found && msg.QoS == qos {
			// MQTT 5.0 §3.3.2.3.2: stop retrying once the Message Expiry
			// Interval has passed.
			if !msg.ExpiresAt.IsZero() && time.Now().After(msg.ExpiresAt) {
				sess.RemoveInflight(packetID)
				b.metrics.IncMessagesDropped("message_expired")
				return nil
			}
			pub := &protocol.PublishPacket{
				FixedHeader: protocol.FixedHeader{
					PacketType: protocol.PacketTypePublish,
					QoS:        qos,
					Dup:        true,
					Retain:     retain,
				},
				PacketID: packetID,
				Topic:    topic,
				Payload:  payload,
			}
			b.writePacket(clientID, pub)
			return nil
		}
	}
	pubRec := &protocol.PubRecPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePubRec,
		},
		PacketID:   packetID,
		ReasonCode: protocol.ReasonCodeSuccess,
	}
	b.writePacket(clientID, pubRec)
	return nil
}

func (b *Broker) publishWill(username string, topic string, payload []byte, qos uint8, retain bool) error {
	// A will message must respect publish authorization just like a normal
	// PUBLISH, otherwise any client could set a will on a topic it has no
	// permission to publish.
	if b.opts.authorizer != nil && !b.opts.authorizer.CanPublish(b.ctx, username, topic) {
		b.logger.Debug("will publish denied by authorizer", "topic", topic, "username", username)
		b.metrics.IncAuthFailures()
		return nil
	}

	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        qos,
		},
		Topic:   topic,
		Payload: payload,
	}

	if retain {
		retainedPkt := *pubPkt
		retainedPkt.FixedHeader.Retain = true
		b.handleRetainedMessage(&retainedPkt)
	}

	// Live forward to established subscriptions: Retain=0.
	pubPkt.FixedHeader.Retain = false
	subscribers := b.topics.Match(topic)
	for _, sub := range subscribers {
		b.deliverToClient(sub.ClientID, "", pubPkt)
	}
	b.routeSharedPublish("", pubPkt)
	return nil
}

func (b *Broker) abnormalDisconnect(clientID string, conn net.Conn) {
	if err := b.will.TriggerWill(clientID); err != nil {
		b.logger.Debug("failed to trigger will", "clientID", clientID, "error", err)
	}
	b.disconnect(clientID, conn)
}

func (b *Broker) gracefulDisconnect(clientID string, conn net.Conn) {
	b.will.RemoveWill(clientID)
	b.disconnect(clientID, conn)
}
