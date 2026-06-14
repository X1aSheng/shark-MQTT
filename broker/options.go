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

	// Resource limits
	maxClientIDLength       int // max bytes for MQTT client ID (0 = unlimited)
	maxTopicFiltersPerSub   int // max topic filters per SUBSCRIBE packet
	maxRetainedTopics       int // max retained messages (0 = unlimited)
	maxWillDelay            time.Duration
	retainedExpiry          time.Duration
	retainedCleanupInterval time.Duration
	connectionRateWindow    time.Duration
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
		maxWillDelay:            24 * time.Hour,
		retainedExpiry:          0,
		retainedCleanupInterval: 10 * time.Minute,
		connectionRateWindow:    time.Second,
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
