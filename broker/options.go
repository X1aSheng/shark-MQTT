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
	sessionStore  store.SessionStore
	messageStore  store.MessageStore
	retainedStore store.RetainedStore
	authenticator Authenticator
	authorizer    Authorizer
	pluginManager *plugin.Manager
	logger        logger.Logger
	metrics       metrics.Metrics
	qosOpts       []QoSOption
	maxInflight   int
	retryInterval time.Duration
	maxRetries    int
}

func defaultBrokerOptions() brokerOptions {
	return brokerOptions{
		authenticator: DenyAllAuth{},
		authorizer:    AllowAllAuth{},
		pluginManager: plugin.NewManager(),
		logger:        logger.Noop(),
		metrics:       metrics.Default(),
		qosOpts:       []QoSOption{},
		maxInflight:   100,
		retryInterval: 10 * time.Second,
		maxRetries:    3,
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
