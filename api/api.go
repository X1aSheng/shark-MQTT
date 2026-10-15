// Package api provides the unified entry point for Shark-MQTT.
package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/pkg/logger"
	"github.com/X1aSheng/shark-mqtt/pkg/metrics"
	"github.com/X1aSheng/shark-mqtt/plugin"
	"github.com/X1aSheng/shark-mqtt/store"
	"github.com/X1aSheng/shark-mqtt/store/memory"
)

// Broker is the main MQTT broker that combines network server with broker core.
type Broker struct {
	srv    *broker.MQTTServer
	broker *broker.Broker
	cfg    *config.Config
	// healthMu guards the three health/metrics fields below. They are written
	// by Start -- which may legitimately run again after a Stop -- and read by
	// the serving goroutine, by Stop and by MetricsAddr. As plain fields they
	// raced: Start -> Stop -> Start assigned healthSrv while the goroutine left
	// over from the first Start was still reading it.
	healthMu   sync.Mutex
	healthSrv  *http.Server
	healthLn   net.Listener  // closed by Stop so a pre-Serve stop cannot leak the port
	healthAddr string        // actual listener address for health/metrics
	logr       logger.Logger // diagnostic logger for lifecycle events
	initErr    error         // set if config validation fails during construction
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
	version        string
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

// WithMaxConnections sets the maximum concurrent connections.
// Use 0 for unlimited, -1 to defer to config (default).
func WithMaxConnections(n int) Option {
	return func(o *brokerOpts) {
		o.maxConnections = n
	}
}

// WithVersion sets the broker version published to $SYS/broker/version (R8).
func WithVersion(v string) Option {
	return func(o *brokerOpts) {
		o.version = v
	}
}

// NewBroker creates a new MQTT broker with the given options.
func NewBroker(opts ...Option) *Broker {
	o := &brokerOpts{
		cfg:            config.DefaultConfig(),
		maxConnections: -1, // sentinel: not set by API user
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
		// initErr returned from Start(); no separate log needed
		initErr = err
	}

	// Default to Prometheus metrics so the /metrics endpoint is served out of
	// the box (NEW-20); an explicit WithMetrics overrides this. With the
	// `nometrics` build tag, DefaultMetrics is a no-op so a minimal binary does
	// not link prometheus/client_golang (R4).
	if o.metrics == nil {
		o.metrics = metrics.DefaultMetrics()
	}

	// Build broker options. Preserve broker package defaults unless the API
	// caller explicitly provides an override.
	bopts := []broker.Option{}

	if o.auth != nil {
		bopts = append(bopts, broker.WithAuth(o.auth))
	}
	if o.authorizer != nil {
		bopts = append(bopts, broker.WithAuthorizer(o.authorizer))
	}
	if o.pluginManager != nil {
		bopts = append(bopts, broker.WithPluginManager(o.pluginManager))
	}

	if o.maxConnections >= 0 {
		bopts = append(bopts, broker.WithMaxConnections(o.maxConnections))
	}
	if o.cfg.MaxPacketSize > 0 {
		bopts = append(bopts, broker.WithBrokerMaxPacketSize(o.cfg.MaxPacketSize))
	}
	if o.cfg.WriteQueueSize > 0 {
		bopts = append(bopts, broker.WithWriteQueueSize(o.cfg.WriteQueueSize))
	}
	if o.cfg.SysInterval > 0 {
		bopts = append(bopts, broker.WithSysInterval(o.cfg.SysInterval))
	}
	if o.version != "" {
		bopts = append(bopts, broker.WithVersion(o.version))
	}
	if o.cfg.SessionExpiryInterval > 0 {
		bopts = append(bopts, broker.WithSessionExpiry(o.cfg.SessionExpiryInterval))
	}
	// Wire the config values that used to be dead: server-enforced keep-alive
	// (config.keep_alive) and the CONNECT handshake timeout
	// (config.connect_timeout) (audit).
	if o.cfg.KeepAlive > 0 {
		bopts = append(bopts, broker.WithBrokerKeepAlive(o.cfg.KeepAlive))
	}
	if o.cfg.ConnectTimeout > 0 {
		bopts = append(bopts, broker.WithBrokerConnectTimeout(o.cfg.ConnectTimeout))
	}
	// Passed through as-is, including 0: "close without waiting" is a valid
	// choice for a caller who set it. A zero-value Config built by hand (not via
	// DefaultConfig) is indistinguishable from that intent, so only a
	// non-zero value overrides the broker's own default.
	if o.cfg.DrainTimeout != 0 {
		bopts = append(bopts, broker.WithDrainTimeout(o.cfg.DrainTimeout))
	}
	if o.cfg.QoSMaxInflight > 0 || o.cfg.QoSRetryInterval > 0 || o.cfg.QoSMaxRetries > 0 {
		var qosOpts []broker.QoSOption
		if o.cfg.QoSMaxInflight > 0 {
			qosOpts = append(qosOpts, broker.WithMaxInflight(o.cfg.QoSMaxInflight))
		}
		if o.cfg.QoSRetryInterval > 0 {
			qosOpts = append(qosOpts, broker.WithRetryInterval(o.cfg.QoSRetryInterval))
		}
		if o.cfg.QoSMaxRetries >= 0 {
			qosOpts = append(qosOpts, broker.WithMaxRetries(o.cfg.QoSMaxRetries))
		}
		bopts = append(bopts, broker.WithQoSOptions(qosOpts...))
	}
	// Propagate max connections from config when not explicitly set. A
	// configured 0 means "unlimited" and must reach the broker instead of
	// silently falling back to its 10000 default (audit).
	if o.maxConnections < 0 {
		bopts = append(bopts, broker.WithMaxConnections(o.cfg.MaxConnections))
	}

	// Default to in-memory storage when no stores are provided and the
	// configured backend is memory (the default). This makes persistent
	// sessions and the offline persistent-session message queue work out of
	// the box instead of silently ignoring StorageBackend (P2-3, P1-5).
	// Redis/Badger backends are wired explicitly via WithSessionStore etc.
	if o.sessionStore == nil && o.messageStore == nil && o.retainedStore == nil &&
		(o.cfg.StorageBackend == "" || o.cfg.StorageBackend == "memory") {
		o.sessionStore = memory.NewSessionStore()
		o.messageStore = memory.NewMessageStore()
		o.retainedStore = memory.NewRetainedStore()
	}

	// A non-memory backend with no explicitly wired stores must fail loudly
	// instead of silently running without persistence: the broker would
	// accept connections but never save sessions/messages/retained state,
	// which is a configuration mistake, not a supported mode.
	if o.cfg.StorageBackend != "" && o.cfg.StorageBackend != "memory" &&
		o.sessionStore == nil && o.messageStore == nil && o.retainedStore == nil {
		initErr = fmt.Errorf(
			"storage_backend %q requires explicit stores (WithSessionStore/WithMessageStore/WithRetainedStore)",
			o.cfg.StorageBackend)
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
	srv := broker.NewMQTTServer(o.cfg, broker.WithServerMetrics(o.metrics))

	// Connect server to broker (broker.Broker implements broker.ConnectionHandler)
	srv.SetHandler(brk)

	return &Broker{
		logr:    logger.Noop(),
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

	b.logr.Info("Shark-MQTT started", "addrs", b.Addrs(), "ws", b.cfg.EffectiveWSListenAddrs())
	return nil
}

// Stop gracefully shuts down both the server and broker.
//
// Order matters. The broker is asked to drain BEFORE the network server closes
// the client sockets, because the acknowledgments that clear the in-flight
// count travel over those sockets: srv.Stop() first would abort every
// connection and leave broker.Stop() with nothing it could ever wait for.
//
// BeginShutdown still runs first of all: it suppresses wills for the whole
// window and sends DISCONNECT 0x8B while the sockets are still open, so a
// rolling restart neither floods will topics nor cuts clients off silently.
func (b *Broker) Stop() {
	b.broker.BeginShutdown()

	b.stopHealthServer()

	// Drain in-flight QoS 1/2 deliveries while clients can still acknowledge.
	b.broker.DrainInflight()

	// Now cut the sockets for good, then release the broker subsystems.
	b.srv.Stop()
	b.broker.Stop()
	b.logr.Info("Shark-MQTT stopped")
}

// stopHealthServer tears down the health/metrics listener. Stop can run more
// than once (a caller's deferred Stop plus a test's t.Cleanup), so the handles
// are claimed under the mutex and the whole body is a no-op on the second call.
func (b *Broker) stopHealthServer() {
	b.healthMu.Lock()
	srv, ln := b.healthSrv, b.healthLn
	b.healthSrv, b.healthLn, b.healthAddr = nil, nil, ""
	b.healthMu.Unlock()

	// Close the listener explicitly rather than relying on Shutdown alone.
	// http.Server.Shutdown only closes the listeners that Serve has already
	// registered, so a Stop landing before the serving goroutine reached Serve
	// would leave the port bound -- and the next Start would then fail to
	// rebind it, silently losing the health and metrics endpoints.
	if ln != nil {
		_ = ln.Close()
	}
	if srv == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		b.logr.Warn("health shutdown error", "error", err)
	}
}

// Addr returns the listening address.
func (b *Broker) Addr() string {
	addr := b.srv.Addr()
	if addr != nil {
		return addr.String()
	}
	return ""
}

// Addrs returns every bound MQTT-over-TCP address, primary first, or nil before
// Start and after Stop. Addr remains the single-address convenience accessor.
func (b *Broker) Addrs() []string {
	listeners := b.srv.Addrs()
	if len(listeners) == 0 {
		return nil
	}
	out := make([]string, 0, len(listeners))
	for _, a := range listeners {
		out = append(out, a.String())
	}
	return out
}

// WSAddr returns the MQTT-over-WebSocket address, or "" when disabled (R5).
func (b *Broker) WSAddr() string {
	addr := b.srv.WSAddr()
	if addr != nil {
		return addr.String()
	}
	return ""
}

// WSSAddr returns the MQTT-over-TLS-WebSocket (WSS) address, or "" when disabled.
func (b *Broker) WSSAddr() string {
	addr := b.srv.WSSAddr()
	if addr != nil {
		return addr.String()
	}
	return ""
}

// ConnCount returns active connection count.
func (b *Broker) ConnCount() int64 {
	return b.srv.ConnCount()
}

// Core is the read-only view of the broker core that this package exposes.
// It is deliberately narrower than *broker.Broker: handing out the concrete
// type would let a caller bypass the lifecycle this wrapper owns (Stop order,
// drain, health server) and mutate core state behind its back. Add a method
// here when a new capability genuinely needs exposing.
type Core interface {
	// IsStarted reports whether the broker is running.
	IsStarted() bool
	// Metrics returns the broker's metrics recorder, for a host that renders
	// its own exposition format.
	Metrics() metrics.Metrics
	// BeginShutdown marks a broker-wide shutdown in progress and notifies
	// connected MQTT 5.0 clients. Safe to call more than once.
	BeginShutdown()
	// DrainInflight waits for in-flight QoS 1/2 messages to be acknowledged,
	// bounded by the configured drain timeout. Sockets must still be open.
	DrainInflight()
	// DisconnectClient closes one client by ClientID as a server-initiated
	// disconnect (will suppressed, session kept per negotiated expiry).
	// Reports false when that client is not connected.
	DisconnectClient(clientID string) bool
	// DisconnectAllClients does the same for every connected client and
	// returns how many were closed.
	DisconnectAllClients() int
}

// Core returns a narrow, read-only handle on the broker core for advanced use
// (metrics scraping, embedding in a larger process). Prefer the methods on
// Broker; reach for this only when the wrapper does not cover the need.
func (b *Broker) Core() Core {
	return b.broker
}

// MetricsAddr returns the health/metrics server's listening address, or "" when
// that server is not running (never started, or already stopped).
func (b *Broker) MetricsAddr() string {
	b.healthMu.Lock()
	defer b.healthMu.Unlock()
	return b.healthAddr
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
		// Ready only when the listener is up AND the broker's subsystems
		// (QoS retry loop, session cleanup, etc.) are running.
		if b.srv.Addr() != nil && b.broker.IsStarted() {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "ok")
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "not ready")
		}
	})

	// Register /metrics only when metrics are enabled (audit:
	// metrics_enabled used to be a dead switch and /metrics was always
	// exposed on the metrics listener). /healthz and /readyz stay available
	// for probes regardless.
	if b.cfg.MetricsEnabled {
		if m, ok := interface{}(b.broker.Metrics()).(interface{ Handler() http.Handler }); ok {
			mux.Handle("/metrics", m.Handler())
		}
	}

	ln, err := net.Listen("tcp", b.cfg.MetricsAddr)
	if err != nil {
		b.logr.Error("health/metrics server failed to start — endpoints unavailable", "addr", b.cfg.MetricsAddr, "error", err)
		return
	}
	srv := &http.Server{
		Handler:           mux,
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	addr := ln.Addr().String()

	b.healthMu.Lock()
	b.healthSrv, b.healthLn, b.healthAddr = srv, ln, addr
	b.healthMu.Unlock()

	// Serve the captured srv/ln, never the b.healthSrv field: a re-Start
	// replaces that field, and the goroutine left over from the previous Start
	// would otherwise end up serving its own stale listener under the new
	// server object (and be missed by that server's Shutdown).
	go func() {
		err := srv.Serve(ln)
		// net.ErrClosed is expected too: stopHealthServer closes the listener
		// directly, so Serve returns the raw accept error rather than
		// http.ErrServerClosed.
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			b.logr.Warn("health server error", "error", err)
		}
	}()
	b.logr.Info("health endpoint", "addr", addr)
}
