// Package api provides the unified entry point for Shark-MQTT.
package api

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/pkg/logger"
	"github.com/X1aSheng/shark-mqtt/pkg/metrics"
	"github.com/X1aSheng/shark-mqtt/plugin"
	"github.com/X1aSheng/shark-mqtt/store"
)

// Broker is the main MQTT broker that combines network server with broker core.
type Broker struct {
	srv       *broker.MQTTServer
	broker    *broker.Broker
	cfg       *config.Config
	healthSrv *http.Server
	initErr   error // set if config validation fails during construction
}

// Option configures the broker.
type Option func(*brokerOpts)

type brokerOpts struct {
	cfg            *config.Config
	auth           broker.Authenticator
	authorizer     broker.Authorizer
	sessionStore   store.SessionStore
	messageStore   store.MessageStore
	retainedStore  store.RetainedStore
	logger         logger.Logger
	metrics        metrics.Metrics
	pluginManager  *plugin.Manager
	maxConnections int
}

// WithAuth sets the authenticator.
func WithAuth(a broker.Authenticator) Option {
	return func(o *brokerOpts) {
		o.auth = a
	}
}

// WithAuthorizer sets the authorizer.
func WithAuthorizer(a broker.Authorizer) Option {
	return func(o *brokerOpts) {
		o.authorizer = a
	}
}

// WithConfig applies a custom config.
func WithConfig(cfg *config.Config) Option {
	return func(o *brokerOpts) {
		o.cfg = cfg
	}
}

// WithSessionStore sets the session store.
func WithSessionStore(s store.SessionStore) Option {
	return func(o *brokerOpts) {
		o.sessionStore = s
	}
}

// WithMessageStore sets the message store.
func WithMessageStore(s store.MessageStore) Option {
	return func(o *brokerOpts) {
		o.messageStore = s
	}
}

// WithRetainedStore sets the retained message store.
func WithRetainedStore(s store.RetainedStore) Option {
	return func(o *brokerOpts) {
		o.retainedStore = s
	}
}

// WithLogger sets the logger.
func WithLogger(l logger.Logger) Option {
	return func(o *brokerOpts) {
		o.logger = l
	}
}

// WithMetrics sets the metrics collector.
func WithMetrics(m metrics.Metrics) Option {
	return func(o *brokerOpts) {
		o.metrics = m
	}
}

// WithPluginManager sets the plugin manager.
func WithPluginManager(m *plugin.Manager) Option {
	return func(o *brokerOpts) {
		o.pluginManager = m
	}
}

// WithMaxConnections sets the maximum concurrent connections (0 = unlimited).
func WithMaxConnections(n int) Option {
	return func(o *brokerOpts) {
		o.maxConnections = n
	}
}

// NewBroker creates a new MQTT broker with the given options.
func NewBroker(opts ...Option) *Broker {
	o := &brokerOpts{
		cfg: config.DefaultConfig(),
	}

	// Apply options to collect config and auth components
	for _, opt := range opts {
		opt(o)
	}

	// Ensure config is not nil and validate it
	if o.cfg == nil {
		o.cfg = config.DefaultConfig()
	}
	var initErr error
	if err := o.cfg.Validate(); err != nil {
		initErr = err
		log.Printf("[api] config validation error: %v", err)
	}

	// Build broker options
	bopts := []broker.Option{
		broker.WithAuth(o.auth),
		broker.WithAuthorizer(o.authorizer),
		broker.WithPluginManager(o.pluginManager),
	}

	if o.maxConnections > 0 {
		bopts = append(bopts, broker.WithMaxConnections(o.maxConnections))
	}
	if o.cfg.MaxPacketSize > 0 {
		bopts = append(bopts, broker.WithBrokerMaxPacketSize(o.cfg.MaxPacketSize))
	}
	if o.cfg.SessionExpiryInterval > 0 {
		bopts = append(bopts, broker.WithSessionExpiry(o.cfg.SessionExpiryInterval))
	}

	if o.sessionStore != nil {
		bopts = append(bopts, broker.WithSessionStore(o.sessionStore))
	}
	if o.messageStore != nil {
		bopts = append(bopts, broker.WithMessageStore(o.messageStore))
	}
	if o.retainedStore != nil {
		bopts = append(bopts, broker.WithRetainedStore(o.retainedStore))
	}
	if o.logger != nil {
		bopts = append(bopts, broker.WithLogger(o.logger))
	}
	if o.metrics != nil {
		bopts = append(bopts, broker.WithMetrics(o.metrics))
	}

	// Create the broker core
	brk := broker.New(bopts...)

	// Create the network server
	srv := broker.NewMQTTServer(o.cfg)

	// Connect server to broker (broker.Broker implements broker.ConnectionHandler)
	srv.SetHandler(brk)

	return &Broker{
		srv:     srv,
		broker:  brk,
		cfg:     o.cfg,
		initErr: initErr,
	}
}

// Start starts both the network server and the broker core.
func (b *Broker) Start() error {
	if b.initErr != nil {
		return fmt.Errorf("api: invalid configuration: %w", b.initErr)
	}
	// Broker core manages client connections
	if err := b.broker.Start(); err != nil {
		return fmt.Errorf("api: broker start failed: %w", err)
	}

	// Network server accepts connections
	if err := b.srv.Start(); err != nil {
		b.broker.Stop()
		return fmt.Errorf("api: server start failed: %w", err)
	}

	// Health check endpoint
	b.startHealthServer()

	log.Printf("[api] Shark-MQTT started on %s", b.cfg.ListenAddr)
	return nil
}

// Stop gracefully shuts down both the server and broker.
func (b *Broker) Stop() {
	if b.healthSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		b.healthSrv.Shutdown(ctx)
	}
	b.srv.Stop()
	b.broker.Stop()
	log.Println("[api] Shark-MQTT stopped")
}

// Addr returns the listening address.
func (b *Broker) Addr() string {
	addr := b.srv.Addr()
	if addr != nil {
		return addr.String()
	}
	return ""
}

// ConnCount returns active connection count.
func (b *Broker) ConnCount() int64 {
	return b.srv.ConnCount()
}

// Broker returns the underlying broker core for advanced usage.
func (b *Broker) Broker() *broker.Broker {
	return b.broker
}

// Run creates and starts a broker, blocking until ctx is cancelled.
func Run(ctx context.Context, opts ...Option) error {
	b := NewBroker(opts...)

	if err := b.Start(); err != nil {
		return err
	}

	<-ctx.Done()
	b.Stop()
	return nil
}

func (b *Broker) startHealthServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if b.srv.Addr() != nil {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "ok")
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "not ready")
		}
	})

	ln, err := net.Listen("tcp", b.cfg.MetricsAddr)
	if err != nil {
		log.Printf("[api] health server: %v (skipped)", err)
		return
	}
	b.healthSrv = &http.Server{
		Handler:           mux,
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go b.healthSrv.Serve(ln)
	log.Printf("[api] health endpoint on %s", b.cfg.MetricsAddr)
}
