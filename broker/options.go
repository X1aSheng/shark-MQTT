package broker

import (
	"time"

	"github.com/X1aSheng/shark-mqtt/pkg/logger"
	"github.com/X1aSheng/shark-mqtt/pkg/metrics"
	"github.com/X1aSheng/shark-mqtt/plugin"
	"github.com/X1aSheng/shark-mqtt/store"
)

// Option configures a Broker.
type Option func(*brokerOptions)

type brokerOptions struct {
	sessionStore           store.SessionStore
	messageStore           store.MessageStore
	retainedStore          store.RetainedStore
	authenticator          Authenticator
	enhancedAuth           []EnhancedAuthenticator // MQTT 5.0 §4.12 (by AuthenticationMethod)
	authorizer             Authorizer
	pluginManager          *plugin.Manager
	logger                 logger.Logger
	metrics                metrics.Metrics
	qosOpts                []QoSOption
	maxInflight            int
	retryInterval          time.Duration
	maxRetries             int
	maxConnections         int
	maxConnRate            float64 // connections per second (0 = unlimited)
	maxPublishRate         int     // publishes per second per client (0 = unlimited)
	maxPacketSize          int
	sessionExpiry          time.Duration
	sessionCleanupInterval time.Duration
	keepAlive              uint16
	connectTimeout         time.Duration // read deadline for the CONNECT handshake

	// Resource limits
	maxClientIDLength       int           // max bytes for MQTT client ID (0 = unlimited)
	maxTopicFiltersPerSub   int           // max topic filters per SUBSCRIBE packet
	maxRetainedTopics       int           // max retained messages (0 = unlimited)
	maxOfflineQueue         int           // max QoS 1/2 messages queued per offline session (0 = unlimited)
	writeQueueSize          int           // per-connection outbound write queue capacity (R1)
	writeTimeout            time.Duration // max duration of a single socket write before the connection is reaped
	maxWillDelay            time.Duration
	retainedExpiry          time.Duration
	retainedCleanupInterval time.Duration
	connectionRateWindow    time.Duration
	sysInterval             time.Duration // $SYS status topic publish interval (0 = off, R8)
	version                 string        // broker version published to $SYS/broker/version
	latencySampling         int           // observe publish latency every N messages (0 = off, 1 = every message)
}

func defaultBrokerOptions() brokerOptions {
	return brokerOptions{
		authenticator:           DenyAllAuth{},
		authorizer:              AllowAllAuth{},
		pluginManager:           plugin.NewManager(),
		logger:                  logger.Noop(),
		metrics:                 metrics.Default(),
		qosOpts:                 []QoSOption{},
		maxInflight:             100,
		retryInterval:           10 * time.Second,
		maxRetries:              3,
		maxConnections:          10000,
		maxConnRate:             0,
		maxPublishRate:          0,
		maxPacketSize:           256 * 1024,
		sessionExpiry:           24 * time.Hour,
		sessionCleanupInterval:  60 * time.Second,
		maxClientIDLength:       128,
		maxTopicFiltersPerSub:   100,
		maxRetainedTopics:       10000,
		maxOfflineQueue:         1000,
		writeQueueSize:          256,
		writeTimeout:            30 * time.Second,
		connectTimeout:          10 * time.Second,
		maxWillDelay:            24 * time.Hour,
		retainedExpiry:          0,
		retainedCleanupInterval: 10 * time.Minute,
		connectionRateWindow:    time.Second,
		sysInterval:             30 * time.Second,
		version:                 "dev",
		latencySampling:         1,
	}
}

// WithSessionStore sets the session store.
func WithSessionStore(s store.SessionStore) Option {
	return func(o *brokerOptions) {
		o.sessionStore = s
	}
}

// WithMessageStore sets the message store.
func WithMessageStore(s store.MessageStore) Option {
	return func(o *brokerOptions) {
		o.messageStore = s
	}
}

// WithRetainedStore sets the retained message store.
func WithRetainedStore(s store.RetainedStore) Option {
	return func(o *brokerOptions) {
		o.retainedStore = s
	}
}

// WithAuth sets the authenticator.
func WithAuth(a Authenticator) Option {
	return func(o *brokerOptions) {
		o.authenticator = a
	}
}

// WithEnhancedAuth registers an MQTT 5.0 enhanced authenticator (by its
// AuthenticationMethod). A CONNECT carrying that method runs the enhanced
// authentication exchange instead of the traditional username/password path.
// Multiple authenticators with distinct methods may be registered.
func WithEnhancedAuth(a EnhancedAuthenticator) Option {
	return func(o *brokerOptions) {
		o.enhancedAuth = append(o.enhancedAuth, a)
	}
}

// WithAuthorizer sets the authorizer.
func WithAuthorizer(a Authorizer) Option {
	return func(o *brokerOptions) {
		o.authorizer = a
	}
}

// WithPluginManager sets the plugin manager.
func WithPluginManager(m *plugin.Manager) Option {
	return func(o *brokerOptions) {
		o.pluginManager = m
	}
}

// WithLogger sets the logger.
func WithLogger(l logger.Logger) Option {
	return func(o *brokerOptions) {
		o.logger = l
	}
}

// WithMetrics sets the metrics collector.
func WithMetrics(m metrics.Metrics) Option {
	return func(o *brokerOptions) {
		o.metrics = m
	}
}

// WithQoSOptions sets QoS engine options.
func WithQoSOptions(opts ...QoSOption) Option {
	return func(o *brokerOptions) {
		o.qosOpts = opts
	}
}

// WithMaxConnections sets the maximum number of concurrent connections.
// Set to 0 to disable the limit.
func WithMaxConnections(n int) Option {
	return func(o *brokerOptions) {
		o.maxConnections = n
	}
}

// WithBrokerMaxPacketSize sets the maximum MQTT packet size for the broker codec.
func WithBrokerMaxPacketSize(n int) Option {
	return func(o *brokerOptions) {
		o.maxPacketSize = n
	}
}

// WithSessionExpiry sets the maximum session expiry interval.
func WithSessionExpiry(d time.Duration) Option {
	return func(o *brokerOptions) {
		o.sessionExpiry = d
	}
}

// WithSessionCleanupInterval sets the interval for the session expiry cleanup loop.
func WithSessionCleanupInterval(d time.Duration) Option {
	return func(o *brokerOptions) {
		o.sessionCleanupInterval = d
	}
}

// WithBrokerKeepAlive sets the server-enforced keep-alive interval.
// When set and shorter than the client's requested value, the server
// will override the client's keep-alive via MQTT 5.0 ServerKeepAlive property.
func WithBrokerKeepAlive(seconds uint16) Option {
	return func(o *brokerOptions) {
		o.keepAlive = seconds
	}
}

// WithBrokerConnectTimeout sets how long a connection may wait for its
// CONNECT handshake before being closed (audit: the deadline was hard-coded
// at 10s and the config value was never consumed). Default is 10s; 0
// disables the handshake deadline.
func WithBrokerConnectTimeout(d time.Duration) Option {
	return func(o *brokerOptions) {
		o.connectTimeout = d
	}
}

// WithMaxConnectRate sets the maximum rate of new connections per second
// (0 = unlimited). When exceeded, new connections are rejected with a
// server-busy response.
func WithMaxConnectRate(rate float64) Option {
	return func(o *brokerOptions) {
		o.maxConnRate = rate
	}
}

// WithMaxPublishRate sets the maximum number of PUBLISH packets per second
// per client (0 = unlimited). When exceeded, messages are silently dropped.
func WithMaxPublishRate(rate int) Option {
	return func(o *brokerOptions) {
		o.maxPublishRate = rate
	}
}

// WithMaxClientIDLength sets the maximum allowed length for a client ID
// in bytes. The MQTT spec allows up to 65535, but shorter limits help
// prevent resource exhaustion. Default is 128.
func WithMaxClientIDLength(n int) Option {
	return func(o *brokerOptions) {
		o.maxClientIDLength = n
	}
}

// WithMaxTopicFiltersPerSubscribe sets the maximum number of topic filters
// allowed in a single SUBSCRIBE packet. Default is 100.
func WithMaxTopicFiltersPerSubscribe(n int) Option {
	return func(o *brokerOptions) {
		o.maxTopicFiltersPerSub = n
	}
}

// WithMaxRetainedTopics sets the maximum number of retained messages the
// broker will store. Default is 10000, 0 means unlimited.
func WithMaxRetainedTopics(n int) Option {
	return func(o *brokerOptions) {
		o.maxRetainedTopics = n
	}
}

// WithMaxOfflineQueue sets the maximum number of QoS 1/2 messages queued per
// offline persistent session (audit H2). Default is 1000; 0 means unlimited.
// When the limit is reached, further messages for that session are dropped
// and counted as "offline_queue_full".
func WithMaxOfflineQueue(n int) Option {
	return func(o *brokerOptions) {
		o.maxOfflineQueue = n
	}
}

// WithMaxWillDelay sets the maximum Will Delay Interval the server will
// accept from clients (MQTT 5.0 §3.1.2.11.8). Default is 24 hours, and 0
// disables will delay entirely. When a client requests a delay longer than
// the maximum, the server caps the value at the maximum.
func WithMaxWillDelay(d time.Duration) Option {
	return func(o *brokerOptions) {
		o.maxWillDelay = d
	}
}

// WithRetainedExpiry sets the TTL for retained messages. When set to a
// positive duration, retained messages that have been stored longer than
// this duration are automatically removed by a periodic cleanup loop.
// Default is 0 (no expiry).
func WithRetainedExpiry(d time.Duration) Option {
	return func(o *brokerOptions) {
		o.retainedExpiry = d
	}
}

// WithRetainedCleanupInterval sets how often the retained message cleanup
// loop checks for expired messages. Default is 10 minutes.
func WithRetainedCleanupInterval(d time.Duration) Option {
	return func(o *brokerOptions) {
		o.retainedCleanupInterval = d
	}
}

// WithWriteQueueSize sets the per-connection outbound write queue capacity.
// A bounded queue drained by a per-connection writer goroutine decouples slow
// consumers from producers, so a slow subscriber cannot block the publishing
// client's read loop (R1). Values <= 0 fall back to a 1-slot queue.
func WithWriteQueueSize(n int) Option {
	return func(o *brokerOptions) {
		o.writeQueueSize = n
	}
}

// WithWriteTimeout sets how long a single socket write to a client may take
// before the connection is considered dead and closed (audit: writes had no
// deadline, so a peer that stopped reading could wedge the writer goroutine
// forever and, with a full queue, stall every publisher delivering QoS 1/2
// messages to it). Default is 30s; 0 disables the deadline.
func WithWriteTimeout(d time.Duration) Option {
	return func(o *brokerOptions) {
		o.writeTimeout = d
	}
}

// WithSysInterval sets how often the broker publishes $SYS status topics
// (R8). Zero disables the status loop.
func WithSysInterval(d time.Duration) Option {
	return func(o *brokerOptions) {
		o.sysInterval = d
	}
}

// WithVersion sets the broker version published to $SYS/broker/version (R8).
func WithVersion(v string) Option {
	return func(o *brokerOptions) {
		o.version = v
	}
}

// WithLatencySampling sets the rate at which publish latency is observed for
// metrics: 1 (default) observes every message, N observes 1 in N, and 0
// disables latency observation entirely. Prometheus histogram observations are
// comparatively expensive, so sampling reduces per-message overhead when
// metrics are enabled.
func WithLatencySampling(n int) Option {
	return func(o *brokerOptions) {
		o.latencySampling = n
	}
}
