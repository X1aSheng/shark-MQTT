package broker

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/X1aSheng/shark-mqtt/pkg/logger"
	"github.com/X1aSheng/shark-mqtt/pkg/metrics"
	"github.com/X1aSheng/shark-mqtt/plugin"
	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store"
)

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
	codec     *protocol.Codec

	mu sync.RWMutex
	// connections maps clientID -> clientState
	connections map[string]*clientState

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

	b := &Broker{
		topics:        NewTopicTree(),
		qos:           NewQoSEngine(o.qosOpts...),
		will:          NewWillHandler(),
		sessions:      NewManager(o.sessionStore),
		sessionStore:  o.sessionStore,
		messageStore:  o.messageStore,
		retainedStore: o.retainedStore,
		logger:        o.logger,
		metrics:       o.metrics,
		pluginMgr:     o.pluginManager,
		codec:         protocol.NewCodec(256 * 1024),
		connections:   make(map[string]*clientState),
		ctx:           ctx,
		cancel:        cancel,
		opts:          o,
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
		c = protocol.NewCodec(256 * 1024)
	}

	// Check connection limit
	if b.opts.maxConnections > 0 {
		b.mu.RLock()
		count := len(b.connections)
		b.mu.RUnlock()
		if count >= b.opts.maxConnections {
			b.metrics.IncRejections("max_connections")
			conn.Close()
			return fmt.Errorf("broker: max connections (%d) reached", b.opts.maxConnections)
		}
	}

	// Plugin hook: OnAccept
	b.dispatch(plugin.OnAccept, &plugin.Context{
		RemoteAddr: conn.RemoteAddr().String(),
	})

	// Set read deadline for CONNECT
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	// Wait for CONNECT packet
	pkt, err := c.Decode(conn)
	if err != nil {
		b.metrics.IncRejections("decode_error")
		return fmt.Errorf("broker: decode CONNECT failed: %w", err)
	}

	connectPkt, ok := pkt.(*protocol.ConnectPacket)
	if !ok {
		b.metrics.IncRejections("invalid_packet")
		return fmt.Errorf("broker: expected CONNECT, got %T", pkt)
	}

	// Clear read deadline
	conn.SetReadDeadline(time.Time{})

	// Authenticate
	if b.opts.authenticator != nil {
		authErr := b.opts.authenticator.Authenticate(ctx, connectPkt.ClientID, connectPkt.Username, string(connectPkt.Password))
		if authErr != nil {
			b.metrics.IncAuthFailures()
			b.sendConnAckRaw(conn, protocol.ConnAckBadUsernameOrPassword, false)
			return fmt.Errorf("broker: auth failed: %w", authErr)
		}
	}

	// Create or resume session
	isResuming := b.sessions.SessionExists(connectPkt.ClientID)
	sess := b.sessions.CreateSession(connectPkt.ClientID, connectPkt, isResuming)
	clientID := connectPkt.ClientID

	// Register client connection
	b.mu.Lock()
	b.connections[clientID] = &clientState{conn: conn, codec: c}
	b.mu.Unlock()

	// Register will message
	if connectPkt.Flags.WillFlag {
		var willDelay time.Duration
		if connectPkt.WillProperties != nil && connectPkt.WillProperties.WillDelayInterval != nil {
			willDelay = time.Duration(*connectPkt.WillProperties.WillDelayInterval) * time.Second
		}
		b.will.RegisterWill(clientID, connectPkt.WillTopic, connectPkt.WillMessage, connectPkt.Flags.WillQoS, connectPkt.Flags.WillRetain, willDelay)
	}

	// Plugin hook: OnConnected
	b.dispatch(plugin.OnConnected, &plugin.Context{
		ClientID: clientID,
		Username: connectPkt.Username,
	})

	// Metrics
	b.metrics.IncConnections()

	// Send CONNACK
	b.sendConnAck(clientID, protocol.ConnAckAccepted, isResuming)

	// Run read loop
	b.readLoop(clientID, sess, c, conn)

	// Cleanup on disconnect
	b.disconnect(clientID)

	return nil
}

// Start starts the broker's internal subsystems.
func (b *Broker) Start() error {
	b.qos.Start()
	return nil
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
		for _, id := range b.connections {
			total += b.qos.InflightCount(id.conn.RemoteAddr().String())
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
		cs.conn.Close()
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
	b.pluginMgr.Dispatch(b.ctx, hook, data)
}

func (b *Broker) disconnect(clientID string) {
	b.will.RemoveWill(clientID)
	b.sessions.RemoveSession(clientID)
	b.qos.RemoveClient(clientID)

	b.mu.Lock()
	delete(b.connections, clientID)
	b.mu.Unlock()

	// Plugin hook
	b.dispatch(plugin.OnClose, &plugin.Context{ClientID: clientID})

	// Metrics
	b.metrics.DecConnections()

	b.logger.Info("client disconnected", "clientID", clientID)
}

func (b *Broker) readLoop(clientID string, sess *Session, codec *protocol.Codec, conn net.Conn) {
	for {
		pkt, err := codec.Decode(conn)
		if err != nil {
			b.logger.Debug("read error", "clientID", clientID, "error", err)
			b.abnormalDisconnect(clientID)
			return
		}

		// Plugin hook: OnMessage
		b.dispatch(plugin.OnMessage, &plugin.Context{ClientID: clientID})

		// Update activity
		sess.UpdateActivity()

		// Set keep-alive deadline
		if sess.KeepAlive > 0 {
			timeout := time.Duration(sess.KeepAlive) * time.Second * 3 / 2
			conn.SetReadDeadline(time.Now().Add(timeout))
		}

		switch p := pkt.(type) {
		case *protocol.PublishPacket:
			sess.TrackReceived(len(p.Payload) + len(p.Topic))
			b.handlePublish(clientID, sess, p, conn, codec)
		case *protocol.SubscribePacket:
			b.handleSubscribe(clientID, sess, p, conn, codec)
		case *protocol.UnsubscribePacket:
			b.handleUnsubscribe(clientID, sess, p, conn, codec)
		case *protocol.PingReqPacket:
			b.writePacket(clientID, conn, codec, &protocol.PingRespPacket{
				FixedHeader: protocol.FixedHeader{
					PacketType: protocol.PacketTypePingResp,
				},
			})
		case *protocol.DisconnectPacket:
			b.logger.Info("graceful disconnect", "clientID", clientID)
			b.gracefulDisconnect(clientID)
			return
		case *protocol.PubAckPacket:
			b.handlePubAck(clientID, p.PacketID)
		case *protocol.PubRecPacket:
			b.handlePubRec(clientID, p.PacketID)
		case *protocol.PubRelPacket:
			b.handlePubRel(clientID, p.PacketID)
		case *protocol.PubCompPacket:
			b.handlePubComp(clientID, p.PacketID)
		default:
			b.logger.Debug("unhandled packet type", "clientID", clientID, "type", fmt.Sprintf("%T", pkt))
		}
	}
}

func (b *Broker) handlePublish(clientID string, sess *Session, pkt *protocol.PublishPacket, conn net.Conn, codec *protocol.Codec) {
	b.metrics.IncMessagesPublished(pkt.Topic, pkt.FixedHeader.QoS)

	// Check authorization
	if b.opts.authorizer != nil && !b.opts.authorizer.CanPublish(b.ctx, clientID, pkt.Topic) {
		if pkt.FixedHeader.QoS > 0 {
			b.writePacket(clientID, conn, codec, &protocol.PubAckPacket{
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
	if pkt.FixedHeader.Retain {
		if len(pkt.Payload) == 0 {
			if b.retainedStore != nil {
				b.retainedStore.DeleteRetained(context.Background(), pkt.Topic)
			}
		} else {
			if b.retainedStore != nil {
				b.retainedStore.SaveRetained(context.Background(), pkt.Topic, pkt.FixedHeader.QoS, pkt.Payload)
			}
		}
	}

	// Route to subscribers using TopicTree
	subscribers := b.topics.Match(pkt.Topic)
	for _, sub := range subscribers {
		// Don't deliver to the publishing client
		if sub.ClientID == clientID {
			continue
		}
		b.deliverToClient(sub.ClientID, pkt)
	}

	// Send PUBACK for QoS 1
	if pkt.FixedHeader.QoS == 1 {
		b.writePacket(clientID, conn, codec, &protocol.PubAckPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePubAck,
			},
			PacketID: pkt.PacketID,
		})
		b.qos.TrackQoS1(clientID, pkt.PacketID, pkt.Topic, pkt.Payload, pkt.FixedHeader.Retain)
	}

	// Send PUBREC for QoS 2
	if pkt.FixedHeader.QoS == 2 {
		b.writePacket(clientID, conn, codec, &protocol.PubRecPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePubRec,
			},
			PacketID: pkt.PacketID,
		})
		b.qos.TrackQoS2(clientID, pkt.PacketID, pkt.Topic, pkt.Payload, pkt.FixedHeader.Retain)
	}
}

func (b *Broker) handleSubscribe(clientID string, sess *Session, pkt *protocol.SubscribePacket, conn net.Conn, codec *protocol.Codec) {
	reasonCodes := make([]byte, len(pkt.Topics))
	for i, topic := range pkt.Topics {
		// Check authorization
		if b.opts.authorizer != nil && !b.opts.authorizer.CanSubscribe(b.ctx, clientID, topic.Topic) {
			reasonCodes[i] = protocol.SubAckFailure
			continue
		}

		if !b.topics.Subscribe(topic.Topic, clientID, topic.QoS) {
			reasonCodes[i] = protocol.SubAckFailure
			continue
		}
		sess.AddSubscription(topic.Topic, topic.QoS)
		reasonCodes[i] = topic.QoS
	}

	b.writePacket(clientID, conn, codec, &protocol.SubAckPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeSubAck,
		},
		PacketID:    pkt.PacketID,
		ReasonCodes: reasonCodes,
	})

	// Deliver retained messages matching the new subscriptions
	for _, topic := range pkt.Topics {
		b.deliverRetainedMessages(clientID, sess, topic.Topic)
	}
}

func (b *Broker) handleUnsubscribe(clientID string, sess *Session, pkt *protocol.UnsubscribePacket, conn net.Conn, codec *protocol.Codec) {
	for _, topic := range pkt.Topics {
		b.topics.Unsubscribe(topic, clientID)
		sess.RemoveSubscription(topic)
	}

	b.writePacket(clientID, conn, codec, &protocol.UnsubAckPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeUnsubAck,
		},
		PacketID: pkt.PacketID,
	})
}

func (b *Broker) deliverToClient(clientID string, pkt *protocol.PublishPacket) {
	sess, ok := b.sessions.GetSession(clientID)
	if !ok {
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

	b.writePacketTo(clientID, pubPkt)
	b.metrics.IncMessagesDelivered(clientID, deliverQoS)
}

func (b *Broker) deliverRetainedMessages(clientID string, sess *Session, topicFilter string) {
	if b.retainedStore == nil {
		return
	}

	retained, err := b.retainedStore.MatchRetained(context.Background(), topicFilter)
	if err != nil || len(retained) == 0 {
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

		b.writePacketTo(clientID, pubPkt)
		b.metrics.IncMessagesDelivered(clientID, deliverQoS)
	}
}

// writePacket writes a packet directly to the connection (used in read loop).
func (b *Broker) writePacket(clientID string, conn net.Conn, codec *protocol.Codec, pkt protocol.Packet) {
	if err := codec.Encode(conn, pkt); err != nil {
		b.logger.Debug("write error", "clientID", clientID, "error", err)
	}
}

// writePacketTo writes a packet to a client via the stored connection.
func (b *Broker) writePacketTo(clientID string, pkt protocol.Packet) {
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

func (b *Broker) sendConnAck(clientID string, reasonCode byte, sessionPresent bool) {
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
	cs.wmu.Lock()
	cs.codec.Encode(cs.conn, pkt)
	cs.wmu.Unlock()
}

func (b *Broker) sendConnAckRaw(conn net.Conn, reasonCode byte, sessionPresent bool) {
	pkt := &protocol.ConnAckPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnAck,
		},
		ReasonCode:     reasonCode,
		SessionPresent: sessionPresent,
	}
	codec := protocol.NewCodec(256 * 1024)
	codec.Encode(conn, pkt)
}

func (b *Broker) handlePubAck(clientID string, packetID uint16) {
	b.qos.AckQoS1(clientID, packetID)
}

func (b *Broker) handlePubRec(clientID string, packetID uint16) {
	b.qos.AckPubRec(clientID, packetID)
}

func (b *Broker) handlePubRel(clientID string, packetID uint16) {
	b.qos.AckPubRel(clientID, packetID)
}

func (b *Broker) handlePubComp(clientID string, packetID uint16) {
	b.qos.AckPubComp(clientID, packetID)
}

func (b *Broker) sendPubAck(clientID string, packetID uint16) error {
	pkt := &protocol.PubAckPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePubAck,
		},
		PacketID: packetID,
	}
	b.writePacketTo(clientID, pkt)
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
	b.writePacketTo(clientID, pkt)
	return nil
}

func (b *Broker) sendPubComp(clientID string, packetID uint16) error {
	pkt := &protocol.PubCompPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePubComp,
		},
		PacketID: packetID,
	}
	b.writePacketTo(clientID, pkt)
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
	b.writePacketTo(clientID, pubPkt)
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

	subscribers := b.topics.Match(topic)
	for _, sub := range subscribers {
		b.deliverToClient(sub.ClientID, pubPkt)
	}
	return nil
}

func (b *Broker) abnormalDisconnect(clientID string) {
	b.will.TriggerWill(clientID)
	b.disconnect(clientID)
}

func (b *Broker) gracefulDisconnect(clientID string) {
	b.will.RemoveWill(clientID)
	b.disconnect(clientID)
}
