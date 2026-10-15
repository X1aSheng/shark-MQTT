package broker

import (
	"context"
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

// shutdownNotifyDelay bounds how long Stop waits for the per-connection
// writeLoop to flush the DISCONNECT(0x8B) "server shutting down" notice before
// the sockets are closed. The notice is best-effort: the delay is deliberately
// short so a shutdown is never held up by a client that stopped reading.

// shutdownNotifyDelay bounds how long Stop waits for the per-connection
// writeLoop to flush the DISCONNECT(0x8B) "server shutting down" notice before
// the sockets are closed. The notice is best-effort: the delay is deliberately
// short so a shutdown is never held up by a client that stopped reading.
const shutdownNotifyDelay = 50 * time.Millisecond

// finalWriteTimeout bounds how long a teardown path waits for the writer
// goroutine to put a final control packet on the wire, and is imposed as that
// packet's socket write deadline too. A teardown must not be held open by a peer
// that stopped reading, and the packet is a DISCONNECT: a couple of seconds is
// already generous.

// finalWriteTimeout bounds how long a teardown path waits for the writer
// goroutine to put a final control packet on the wire, and is imposed as that
// packet's socket write deadline too. A teardown must not be held open by a peer
// that stopped reading, and the packet is a DISCONNECT: a couple of seconds is
// already generous.
const finalWriteTimeout = 2 * time.Second

// syncWrite is one write request handed to a connection's writer goroutine,
// together with the channel its result is reported on. It is how a teardown
// path sends a packet that has to reach the peer before the connection closes.

// syncWrite is one write request handed to a connection's writer goroutine,
// together with the channel its result is reported on. It is how a teardown
// path sends a packet that has to reach the peer before the connection closes.
type syncWrite struct {
	pkt      protocol.Packet
	deadline time.Duration
	done     chan error
}

// clientState holds the connection and codec for a client.

// clientState holds the connection and codec for a client.
type clientState struct {
	conn  net.Conn
	codec protocol.PacketCodec
	wmu   sync.Mutex // serializes writes when no async write queue is configured

	// writeTimeout bounds each socket write; 0 disables the deadline (audit).
	writeTimeout time.Duration

	// out is the bounded per-connection outbound write queue. When non-nil,
	// writePacket/sendConnAck enqueue here and writeLoop drains it on a single
	// goroutine, so a slow consumer cannot block the producing client's read
	// loop (R1). It stays nil only for clientStates created outside
	// HandleConnection (e.g. unit tests), which fall back to synchronous
	// writes under wmu. stopWrites is closed to stop the writer goroutine.
	out        chan protocol.Packet
	stopWrites chan struct{}

	// syncWrites carries writeFinal requests. Every socket write belongs to the
	// writer goroutine, so a packet that must not race the teardown has to go
	// through it rather than be written from the caller; see writeFinal. It is
	// non-nil exactly when out is.
	syncWrites chan syncWrite

	// enhancedAuth is the active MQTT 5.0 enhanced authenticator for a
	// connection that connected via an AuthenticationMethod (§4.12). Non-nil
	// only for such connections; used to handle re-authentication AUTH packets
	// during the session.
	enhancedAuth EnhancedAuthenticator

	// metrics records outbound packets the codec refused to send because they
	// exceeded the client's Maximum Packet Size. The writer goroutine has no
	// Broker reference, so the counter is reached through here. Nil for
	// clientStates built without one (unit tests), hence the nil guard in
	// classifyWriteError.
	metrics metrics.Metrics
}

// Broker is the core MQTT message broker that orchestrates TopicTree, QoSEngine,
// WillHandler, and session management. It implements server.ConnectionHandler.

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
	// flapping throttles clients that reconnect in a tight loop. A nil
	// detector allows everything, so the feature costs nothing when disabled.
	flapping *FlappingDetector

	mu sync.RWMutex
	// connections maps clientID -> clientState
	connections map[string]*clientState

	// Inbound QoS 2 duplicate detection lives in the session (inboundQoS2),
	// not in broker-wide maps, so packet-id state travels with the session and
	// cannot collide with outbound tracking (audit H4).

	// offlineQueue tracks the number of queued offline messages per client so
	// queueOfflineMessage can enforce a per-client cap (audit H2). The count
	// is adjusted on every queue/delete/clear path below.
	offlineMu    sync.Mutex
	offlineCount map[string]int

	// msgSeq is a process-wide sequence for stored message IDs so queued
	// message IDs no longer embed the clientID (audit: ID composition plus
	// the store's ':' delimiters allowed cross-client prefix collisions).
	msgSeq atomic.Uint64

	retainedMu    sync.Mutex
	retainedCount atomic.Int64 // count of retained messages maintained with retainedMu
	// retainedExpirations tracks expiry times for retained messages when
	// retainedExpiry is configured (> 0). Keyed by topic name.
	retainedExpirations map[string]time.Time

	// connRate limits the rate of new TCP connections accepted.
	connRate *connRateLimiter

	started atomic.Bool // prevents double-Start
	// shuttingDown is set by Stop before the client sockets are closed. While
	// set, a connection teardown must NOT publish the client's will: a rolling
	// restart closes every socket at once, and publishing a will per client
	// would flood every will topic ("will storm") even though the clients did
	// not fail. Cleared again by Start so a Stop->Start cycle behaves normally.
	shuttingDown atomic.Bool
	// drained records that the in-flight drain already ran for this run, so an
	// embedder that drains before closing its sockets does not pay the wait
	// twice when Stop follows. Cleared by Start, alongside shuttingDown.
	drained atomic.Bool
	ctx     context.Context
	cancel  context.CancelFunc
	opts    brokerOptions

	// loops tracks the background goroutines started by Start (session/retained
	// cleanup and the $SYS status publisher). Stop waits on it before returning:
	// Start rewrites b.ctx and b.startedAt, and a loop that is still draining a
	// tick would otherwise read those fields concurrently with the next Start
	// (data race on the documented Stop -> Start cycle).
	loops sync.WaitGroup

	startedAt time.Time // broker start time, for $SYS/broker/uptime (R8)

	// latencyCounter drives metric sampling for publish latency observation.
	latencyCounter atomic.Uint64
}

// New creates a new Broker with the given options.

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
		flapping:            o.flapping,
		connections:         make(map[string]*clientState),
		offlineCount:        make(map[string]int),
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
			// Retry runs on the QoS engine's own schedule, long after the original
			// PUBLISH was handled, so it is broker-scoped by nature.
			return b.republish(b.ctx, clientID, packetID, topic, payload, qos, retain)
		},
	)

	// Setup Will callback
	b.will.SetPublishCallback(func(username string, topic string, payload []byte, qos uint8, retain bool, props *protocol.Properties) error {
		// A will fires from the will handler's own timer, long after the owning
		// connection is gone, so it is broker-scoped work by nature: b.ctx, not
		// any connection context.
		return b.publishWill(b.ctx, username, topic, payload, qos, retain, props)
	})

	return b
}

// connAckForConnectRejection maps a ValidateConnect failure to the CONNACK
// return/reason code. MQTT 5.0 uses 0x82 Protocol Error; MQTT 3.1.1 uses the
// 0x01 Unacceptable Protocol Version code (unchanged from before the refactor);
// MQTT 3.1 reports a ClientID rule violation with 0x02 Identifier Rejected.

// HandleConnection implements server.ConnectionHandler.
// It consumes a protocol-neutral Conn whose handshake has already been
// performed by the network server's protocol adapter; the broker core no
// longer decodes the MQTT CONNECT itself.
//
// # Context scopes
//
// Two contexts reach this package and they are NOT interchangeable:
//
//   - ctx (this parameter) is the CONNECTION scope. Every per-connection path
//     takes it as its first parameter -- readLoop, handlePublish, handleSubscribe,
//     deliverToClient, queueOfflineMessage, plugin dispatch -- so a store write
//     or authorization check started by this connection ends when the connection
//     ends. This is what a ctx-taking function should use unless it says otherwise.
//   - b.ctx is the BROKER scope, owned by Start/Stop. It belongs to the three
//     background loops (session expiry sweep, retained expiry sweep, $SYS
//     publisher) and to work that outlives its connection: the will publish
//     callback (fires from the will handler's own timer), the QoS retry republish,
//     and the connection teardown store writes (see teardownCtx).
//
// The distinction is not cosmetic: a per-connection store write issued with
// b.ctx survives its client, and a teardown write issued with the connection ctx
// is cancelled before it stores anything, losing session state that MQTT 5.0
// 3.1.2.11.2 requires the server to keep.
// FlappingEnabled reports whether connection-flap detection is active.
//
// Exposed so a status endpoint can show whether the protection is actually
// running, rather than leaving an operator to infer it from the config file
// they hoped was applied.
func (b *Broker) FlappingEnabled() bool { return b.flapping != nil }

// FlappingTracked reports how many client keys the detector is holding, so an
// operator can see whether the memory bound is being approached.

// FlappingTracked reports how many client keys the detector is holding, so an
// operator can see whether the memory bound is being approached.
func (b *Broker) FlappingTracked() int { return b.flapping.Tracked() }

// Metrics returns the broker's metrics collector.
func (b *Broker) Metrics() metrics.Metrics {
	return b.metrics
}

// IsStarted reports whether the broker's internal subsystems are running
// (Start has completed and Stop has not). Used by readiness probes.

// IsStarted reports whether the broker's internal subsystems are running
// (Start has completed and Stop has not). Used by readiness probes.
func (b *Broker) IsStarted() bool {
	return b.started.Load()
}

// sessionCleanupLoop periodically removes expired sessions from the store.
// It exits when the broker context is cancelled.
func (b *Broker) dispatch(ctx context.Context, hook plugin.Hook, data *plugin.Context) {
	if b.pluginMgr == nil {
		return
	}
	if err := b.pluginMgr.Dispatch(ctx, hook, data); err != nil {
		b.logger.Debug("plugin dispatch error", "hook", hook, "error", err)
	}
}

// publishEvent reports an occurrence on the plugin event bus.
//
// It is best-effort by design: a subscriber that fails or times out is recorded
// and logged, never propagated. An observer must not be able to fail the
// publish path it is observing -- that is the whole reason events live on a
// separate bus from the ordered hook dispatch.
//
// The optional hook argument lets an event also fire the legacy hook, so
// plugins written against the four-hook API keep working unchanged.

// publishEvent reports an occurrence on the plugin event bus.
//
// It is best-effort by design: a subscriber that fails or times out is recorded
// and logged, never propagated. An observer must not be able to fail the
// publish path it is observing -- that is the whole reason events live on a
// separate bus from the ordered hook dispatch.
//
// The optional hook argument lets an event also fire the legacy hook, so
// plugins written against the four-hook API keep working unchanged.
func (b *Broker) publishEvent(ctx context.Context, ev plugin.Event, data plugin.EventData, hook plugin.Hook, hookData *plugin.Context) {
	if b.pluginMgr == nil {
		return
	}
	if err := b.pluginMgr.Bus().Publish(ctx, ev, data); err != nil {
		b.logger.Warn("event bus subscriber failed", "event", ev, "error", err)
		b.metrics.IncErrors("plugin_event")
	}
	if hook != "" {
		b.dispatch(ctx, hook, hookData)
	}
}

// The bus stamps the event kind itself (Publish takes it as an argument), so a
// dispatch site cannot forget to set it and silently deliver to nobody.

// unsubscribeTopics removes a set of topic filters (regular or $share shared)
// from the topic tree for a client.
