package broker

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	wmu   sync.Mutex // serializes writes to this connection
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

	ctx    context.Context
	cancel context.CancelFunc
	opts   brokerOptions
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
	b.will.SetPublishCallback(func(topic string, payload []byte, qos uint8, retain bool) error {
		return b.publishWill(topic, payload, qos, retain)
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
		var reasonCode byte = protocol.ReasonCodeTopicNameInvalid
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

	// Authenticate
	if b.opts.authenticator != nil {
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
	isResuming := b.sessions.SessionExists(connectPkt.ClientID)
	if !isResuming && !connectPkt.Flags.CleanSession && b.sessionStore != nil {
		if exists, storeErr := b.sessionStore.IsSessionExists(ctx, connectPkt.ClientID); exists {
			restored, err := b.sessions.Restore(ctx, connectPkt.ClientID)
			if err == nil && restored != nil {
				isResuming = true
				for topic, qos := range restored.Subscriptions {
					b.topics.Subscribe(topic, connectPkt.ClientID, qos)
				}
				for _, msg := range restored.Inflight {
					if msg.QoS == 2 {
						_ = b.qos.TrackQoS2(connectPkt.ClientID, msg.PacketID, msg.Topic, msg.Payload, msg.Retain)
					} else {
						_ = b.qos.TrackQoS1(connectPkt.ClientID, msg.PacketID, msg.Topic, msg.Payload, msg.Retain)
					}
				}
			}
		} else if storeErr != nil {
			b.logger.Debug("session store check failed, treating as new", "clientID", connectPkt.ClientID, "error", storeErr)
		}
	}
	sess := b.sessions.CreateSession(connectPkt.ClientID, connectPkt, isResuming)
	clientID := connectPkt.ClientID

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
	b.mu.Lock()
	if old, exists := b.connections[clientID]; exists {
		if err := old.conn.Close(); err != nil {
			b.logger.Debug("failed to close previous connection", "clientID", clientID, "error", err)
		}
		b.logger.Info("session takeover", "clientID", clientID)
	}
	b.connections[clientID] = &clientState{conn: conn, codec: c}
	online := len(b.connections)
	b.mu.Unlock()
	b.metrics.SetOnlineSessions(online)

	// Register will message
	if connectPkt.Flags.WillFlag {
		var willDelay time.Duration
		if connectPkt.WillProperties != nil && connectPkt.WillProperties.WillDelayInterval != nil {
			willDelay = time.Duration(*connectPkt.WillProperties.WillDelayInterval) * time.Second
		}
		// Cap will delay to the configured maximum to prevent abuse
		if b.opts.maxWillDelay > 0 && willDelay > b.opts.maxWillDelay {
			b.logger.Debug("capping will delay", "clientID", clientID,
				"requested", willDelay, "max", b.opts.maxWillDelay)
			willDelay = b.opts.maxWillDelay
		}
		if err := b.will.RegisterWill(clientID, connectPkt.WillTopic, connectPkt.WillMessage, connectPkt.Flags.WillQoS, connectPkt.Flags.WillRetain, willDelay); err != nil {
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

// Start starts the broker's internal subsystems.
func (b *Broker) Start() error {
	b.qos.Start()
	go b.sessionCleanupLoop()
	if b.opts.retainedExpiry > 0 {
		go b.retainedCleanupLoop()
	}
	return nil
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

// Stop stops the broker's internal subsystems and closes all sessions.
// It waits up to drainTimeout for in-flight QoS messages to complete.
func (b *Broker) Stop() {
	b.cancel()

	// Drain in-flight messages with a timeout
	drainTimeout := 5 * time.Second
	deadline := time.Now().Add(drainTimeout)
	for time.Now().Before(deadline) {
		total := 0
		b.mu.RLock()
		for clientID := range b.connections {
			total += b.qos.InflightCount(clientID)
		}
		b.mu.RUnlock()
		if total == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Close all client connections
	b.mu.Lock()
	for id, cs := range b.connections {
		if err := cs.conn.Close(); err != nil {
			b.logger.Debug("failed to close client connection", "clientID", id, "error", err)
		}
		delete(b.connections, id)
	}
	b.mu.Unlock()

	b.qos.Stop()
	b.will.Stop()
}

func (b *Broker) dispatch(hook plugin.Hook, data *plugin.Context) {
	if b.pluginMgr == nil {
		return
	}
	if err := b.pluginMgr.Dispatch(b.ctx, hook, data); err != nil {
		b.logger.Debug("plugin dispatch error", "hook", hook, "error", err)
	}
}

func (b *Broker) disconnect(clientID string, conn net.Conn) {
	b.will.RemoveWill(clientID)

	// Verify this connection is still the current one before cleaning up.
	// In session takeover, the old readLoop calls disconnect after the new
	// connection is already registered; the identity check prevents the old
	// cleanup from removing the new connection's state.
	b.mu.RLock()
	cs, exists := b.connections[clientID]
	b.mu.RUnlock()
	if !exists || cs.conn != conn {
		return
	}

	// Persist non-clean session before removal so state survives reconnect
	if sess, ok := b.sessions.GetSession(clientID); ok && !sess.IsClean && b.sessionStore != nil {
		if err := sess.Save(b.ctx, b.sessionStore); err != nil {
			b.logger.Debug("failed to save session", "clientID", clientID, "error", err)
			b.metrics.IncErrors("session_save")
		}
	}

	// Reset flow control counter before removing session
	if sess, ok := b.sessions.GetSession(clientID); ok {
		sess.ResetOutboundUnacked()
	}

	b.sessions.RemoveSession(clientID)
	b.qos.RemoveClient(clientID)

	b.receivedQoS2Mu.Lock()
	delete(b.receivedQoS2, clientID)
	b.receivedQoS2Mu.Unlock()

	b.mu.Lock()
	if cs, exists := b.connections[clientID]; exists && cs.conn == conn {
		delete(b.connections, clientID)
	}
	online := len(b.connections)
	b.mu.Unlock()
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
			// MQTT 5.0 enhanced authentication is not supported.
			// Per spec §4.12, send DISCONNECT with ReasonCode 0x8C
			// (Bad Authentication Method) when AUTH is received.
			b.logger.Debug("AUTH packet received but enhanced auth not supported",
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

	// QoS 2: defer subscriber delivery until PUBCOMP completes the handshake
	if pkt.QoS == 2 {
		b.receivedQoS2Mu.Lock()
		if b.receivedQoS2[clientID] == nil {
			b.receivedQoS2[clientID] = make(map[uint16]struct{})
		}
		b.receivedQoS2[clientID][pkt.PacketID] = struct{}{}
		b.receivedQoS2Mu.Unlock()

		var reasonCode byte = protocol.ReasonCodeSuccess
		if err := b.qos.TrackQoS2(clientID, pkt.PacketID, pkt.Topic, pkt.Payload, pkt.Retain); err != nil {
			reasonCode = protocol.ReasonCodeReceiveMaxExceeded
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

	// Route to subscribers for QoS 0 and QoS 1
	subscribers := b.topics.Match(pkt.Topic)
	for _, sub := range subscribers {
		b.deliverToClient(sub.ClientID, clientID, pkt)
	}

	// Send PUBACK for QoS 1
	if pkt.QoS == 1 {
		var reasonCode byte = protocol.ReasonCodeSuccess
		if err := b.qos.TrackQoS1(clientID, pkt.PacketID, pkt.Topic, pkt.Payload, pkt.Retain); err != nil {
			reasonCode = protocol.ReasonCodeReceiveMaxExceeded
		}
		b.writePacket(clientID, &protocol.PubAckPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePubAck,
			},
			PacketID:   pkt.PacketID,
			ReasonCode: reasonCode,
		})
	}
}

func (b *Broker) handleRetainedMessage(pkt *protocol.PublishPacket) {
	if b.retainedStore == nil {
		return
	}

	// Enforce retained message count limit before attempting to store
	if len(pkt.Payload) > 0 && b.opts.maxRetainedTopics > 0 &&
		int(b.retainedCount.Load()) >= b.opts.maxRetainedTopics {

		// Check if this is an update to an existing retained message
		b.retainedMu.Lock()
		if _, err := b.retainedStore.GetRetained(b.ctx, pkt.Topic); err != nil {
			// New retained topic would exceed the limit
			b.retainedMu.Unlock()
			b.logger.Debug("retained message limit reached, dropping",
				"topic", pkt.Topic, "max", b.opts.maxRetainedTopics)
			b.metrics.IncMessagesDropped("retained_limit")
			return
		}
		b.retainedMu.Unlock()
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
		b.writePacket(clientID, &protocol.SubAckPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypeSubAck,
			},
			PacketID:    pkt.PacketID,
			ReasonCodes: make([]byte, len(pkt.Topics)),
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

func (b *Broker) deliverToClient(clientID, sourceClientID string, pkt *protocol.PublishPacket) {
	sess, ok := b.sessions.GetSession(clientID)
	if !ok {
		return
	}
	if sourceClientID != "" && clientID == sourceClientID && !sess.AllowsLocalPublish(pkt.Topic) {
		return
	}

	// Use the lower of published and subscribed QoS
	matches, subQoS := sess.MatchesSubscription(pkt.Topic)
	if !matches {
		return
	}
	deliverQoS := pkt.FixedHeader.QoS
	if subQoS < deliverQoS {
		deliverQoS = subQoS
	}

	// MQTT 5.0 flow control (ReceiveMaximum): before sending a QoS 1/2 message
	// to this client, check that the number of outstanding unacknowledged messages
	// has not reached the negotiated ReceiveMaximum (§4.9.0).
	if deliverQoS > 0 && !sess.CanSendOutbound() {
		b.metrics.IncMessagesDropped("receive_max_exceeded")
		b.metrics.IncErrors("flow_control")
		b.logger.Debug("receive maximum exceeded, dropping message",
			"clientID", clientID, "receiveMax", sess.ReceiveMax)
		return
	}

	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        deliverQoS,
		},
		Topic:   pkt.Topic,
		Payload: pkt.Payload,
	}

	// Assign packet ID for QoS 1 and 2
	if deliverQoS > 0 {
		pubPkt.PacketID = sess.NextPacketID()
	}

	// Track sent messages for statistics
	sess.TrackSent(len(pkt.Topic) + len(pkt.Payload))

	// Track outbound unacknowledged count for ReceiveMaximum flow control
	if deliverQoS > 0 {
		sess.IncOutboundUnacked()
	}

	b.writePacket(clientID, pubPkt)
	b.metrics.IncMessagesDelivered(deliverQoS)
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
		deliverQoS := msg.QoS
		// Downgrade QoS to the lower of stored and subscribed QoS
		_, subQoS := sess.MatchesSubscription(msg.Topic)
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

		if deliverQoS > 0 {
			pubPkt.PacketID = sess.NextPacketID()
		}

		// Track sent messages for statistics
		sess.TrackSent(len(msg.Topic) + len(msg.Payload))

		b.writePacket(clientID, pubPkt)
		b.metrics.IncMessagesDelivered(deliverQoS)
	}
}

// writePacket writes a packet to a client via the stored connection (used in read loop).
func (b *Broker) writePacket(clientID string, pkt protocol.Packet) {
	b.mu.RLock()
	cs, ok := b.connections[clientID]
	b.mu.RUnlock()
	if !ok {
		return
	}
	cs.wmu.Lock()
	err := cs.codec.Encode(cs.conn, pkt)
	cs.wmu.Unlock()
	if err != nil {
		b.logger.Debug("write error", "clientID", clientID, "error", err)
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

	cs.wmu.Lock()
	if err := cs.codec.Encode(cs.conn, pkt); err != nil {
		b.logger.Debug("write error", "clientID", clientID, "error", err)
	}
	cs.wmu.Unlock()
}

// buildConnAckProperties builds MQTT 5.0 CONNACK capability properties.
func (b *Broker) buildConnAckProperties(sess *Session) *protocol.Properties {
	xretainAvailable := byte(0)
	if b.retainedStore != nil {
		xretainAvailable = 1
	}
	wildcardAvailable := byte(1)
	subIDAvailable := byte(0)
	sharedSubAvailable := byte(0)

	receiveMax := sess.ReceiveMax
	if receiveMax == 0 {
		receiveMax = 65535
	}
	maxPktSize := uint32(b.opts.maxPacketSize)
	requestResponseInfo := byte(0) // don't send ResponseInfo by default

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
		sess.DecOutboundUnacked()
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
				Retain:     msg.Retain,
			},
			Topic:    msg.Topic,
			Payload:  msg.Payload,
			PacketID: msg.PacketID,
		}
		subscribers := b.topics.Match(msg.Topic)
		for _, sub := range subscribers {
			b.deliverToClient(sub.ClientID, clientID, pubPkt)
		}
	}
	b.qos.AckPubRel(clientID, packetID)
}

func (b *Broker) handlePubComp(clientID string, packetID uint16) {
	b.qos.AckPubComp(clientID, packetID)
	if sess, ok := b.sessions.GetSession(clientID); ok {
		sess.DecOutboundUnacked()
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
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        qos,
			Retain:     retain,
		},
		Topic:    topic,
		Payload:  payload,
		PacketID: packetID,
	}

	// Route to subscribers via TopicTree
	subscribers := b.topics.Match(topic)
	for _, sub := range subscribers {
		b.deliverToClient(sub.ClientID, clientID, pubPkt)
	}
	return nil
}

func (b *Broker) publishWill(topic string, payload []byte, qos uint8, retain bool) error {
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        qos,
			Retain:     retain,
		},
		Topic:   topic,
		Payload: payload,
	}

	if retain {
		b.handleRetainedMessage(pubPkt)
	}

	subscribers := b.topics.Match(topic)
	for _, sub := range subscribers {
		b.deliverToClient(sub.ClientID, "", pubPkt)
	}
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
