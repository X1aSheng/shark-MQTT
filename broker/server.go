// Package broker provides the core MQTT broker and network server.
package broker

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/pkg/logger"
	"github.com/X1aSheng/shark-mqtt/pkg/metrics"
	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/gorilla/websocket"
)

// MQTTServer handles incoming MQTT connections over TCP and, when configured,
// MQTT-over-WebSocket (R5).
type MQTTServer struct {
	cfg *config.Config
	// listener is the primary MQTT-over-TCP listener (the first entry of
	// cfg.EffectiveListenAddrs, or one injected via options). It stays the
	// handle Addr() and WithListener address, so existing callers are
	// unaffected by multi-port support.
	listener net.Listener
	// listeners holds every bound TCP listener, primary first. A failure to
	// bind any one of them aborts Start, so the set is only ever read after a
	// successful Start and is rebuilt from scratch on each Start.
	listeners   []net.Listener
	handler     ConnectionHandler
	connCount   atomic.Int64
	earlyClose  atomic.Int64
	tlsConfig   *tls.Config
	tlsErr      error       // set when TLS config fails to load
	started     atomic.Bool // prevents double Start
	logr        logger.Logger
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	mu          sync.Mutex
	conns       map[net.Conn]struct{}
	wsEndpoints []*wsEndpoint // WebSocket listeners (plain WS and/or WSS, R5)

	// registry selects the protocol adapter for an accepted connection, and
	// connectTimeout bounds identification + handshake (migrated here from
	// Broker.HandleConnection).
	registry       *protocol.Registry
	connectTimeout time.Duration

	// metrics records handshake-phase counters that moved out of the broker
	// core with the protocol-adapter refactor (CONNECT read-deadline failures
	// and CONNECT decode failures). Never nil after NewMQTTServer.
	metrics metrics.Metrics
}

// wsEndpoint is one WebSocket listener (plain WS or TLS WSS).
type wsEndpoint struct {
	ln  net.Listener
	srv *http.Server
	tls bool
}

// NewMQTTServer creates a new MQTT network server.
func NewMQTTServer(cfg *config.Config, opts ...ServerOption) *MQTTServer {
	ctx, cancel := context.WithCancel(context.Background())

	sopts := defaultServerOptions()
	for _, opt := range opts {
		opt(&sopts)
	}

	s := &MQTTServer{
		cfg:    cfg,
		logr:   logger.Noop(),
		ctx:    ctx,
		cancel: cancel,
		conns:  make(map[net.Conn]struct{}),
	}

	// Apply server options
	if sopts.tlsConfig != nil {
		s.tlsConfig = sopts.tlsConfig
	} else if cfg.TLSEnabled {
		tlsCfg, err := cfg.TLSConfig()
		if err != nil {
			s.tlsErr = fmt.Errorf("TLS config failed: %w", err)
			s.logr.Error("TLS config failed", "error", err)
		}
		s.tlsConfig = tlsCfg
	}

	// Use logger if provided
	if sopts.logr != nil {
		s.logr = sopts.logr
	}

	// Use custom listener if provided
	if sopts.listener != nil {
		s.listener = sopts.listener
	}

	// Build the protocol adapter registry. This iteration registers a single
	// MQTT adapter (3.1 / 3.1.1 / 5.0); the registry is the seam that lets
	// additional adapters (QUIC / MQTT-SN / CoAP) be added later without
	// touching the server or the broker core.
	ct := sopts.connectTimeout
	if cfg != nil && cfg.ConnectTimeout > 0 {
		ct = cfg.ConnectTimeout
	}
	s.connectTimeout = ct
	mps := int(sopts.maxPacketSize)
	if !sopts.maxPacketSizeSet && cfg != nil && cfg.MaxPacketSize > 0 {
		mps = cfg.MaxPacketSize
	}
	s.registry = protocol.NewRegistry(protocol.NewMQTTAdapterWithTimeout(mps, ct))

	// Normalize metrics so handleConn never dereferences a nil collector (a
	// caller passing WithServerMetrics(nil) must not panic).
	s.metrics = sopts.metrics
	if s.metrics == nil {
		s.metrics = metrics.Default()
	}

	return s
}

// SetHandler sets the connection handler (typically the broker).
func (s *MQTTServer) SetHandler(h ConnectionHandler) {
	s.handler = h
}

// Start begins accepting TCP connections.
func (s *MQTTServer) Start() error {
	if s.started.Swap(true) {
		return fmt.Errorf("server already started")
	}
	if s.tlsErr != nil {
		return s.tlsErr
	}

	select {
	case <-s.ctx.Done():
		s.ctx, s.cancel = context.WithCancel(context.Background())
	default:
	}

	// Use pre-set listener (from options) or create one per configured address.
	// An injected listener wins outright: the caller already owns that socket,
	// so binding cfg.ListenAddr as well would open a port the caller never asked
	// for (and would conflict in the common ":0" test setup).
	addrs := s.cfg.EffectiveListenAddrs()
	if s.listener != nil {
		addrs = nil
	} else if len(addrs) == 0 {
		return fmt.Errorf("server: no listen address configured")
	}

	bound := make([]net.Listener, 0, len(addrs)+1)
	// rollback closes whatever was bound before an error, so a failed Start
	// leaves no port dangling for the next Start to trip over.
	rollback := func() {
		for _, ln := range bound {
			ln.Close()
		}
		bound = nil
	}

	for _, addr := range addrs {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			rollback()
			if s.listener != nil {
				s.listener.Close()
			}
			return fmt.Errorf("server: failed to listen on %s: %w", addr, err)
		}
		if s.tlsConfig != nil {
			ln = tls.NewListener(ln, s.tlsConfig)
		}
		bound = append(bound, ln)
		s.logr.Info("server listening", "addr", ln.Addr(), "tls", s.cfg.TLSEnabled)
	}
	if s.listener != nil {
		// Injected via options: the caller owns that socket, so it is the
		// primary and nothing was bound from cfg above.
		s.listeners = []net.Listener{s.listener}
	} else {
		// bound is already in configured order, and its first entry is the
		// primary. Do NOT prepend s.listener again -- it is bound[0].
		s.listener = bound[0]
		s.listeners = bound
	}

	for _, ln := range s.listeners {
		ln := ln
		s.wg.Add(1)
		go s.acceptLoop(ln)
	}

	for _, addr := range s.cfg.EffectiveWSListenAddrs() {
		if err := s.startWS(addr, false); err != nil {
			rollback()
			return fmt.Errorf("server: failed to listen on ws %s: %w", addr, err)
		}
	}
	for _, addr := range s.cfg.EffectiveWSSListenAddrs() {
		if s.tlsConfig == nil {
			rollback()
			return fmt.Errorf("server: wss_listen_addr(s) requires TLS to be configured (tls_enabled)")
		}
		if err := s.startWS(addr, true); err != nil {
			rollback()
			return fmt.Errorf("server: failed to listen on wss %s: %w", addr, err)
		}
	}
	return nil
}

// Stop gracefully shuts down the server.
func (s *MQTTServer) Stop() {
	s.cancel()
	// Close every bound listener, not just the primary: a multi-port Start may
	// have left several sockets open, and only the first one is s.listener.
	for _, ln := range s.listeners {
		if ln != nil {
			ln.Close()
		}
	}
	if s.listener != nil {
		s.listener.Close()
	}
	// Stop the WebSocket listener(s); their accepted connections are closed
	// below via s.conns (R5).
	for _, ep := range s.wsEndpoints {
		ep.ln.Close()
		ep.srv.Close()
	}
	// Close connections BEFORE waiting on the WaitGroup: connection
	// goroutines block in the read loop (bounded by the keep-alive read
	// deadline), so closing them here lets wg.Wait() return promptly
	// instead of stalling shutdown for up to 1.5x keep-alive per conn.
	s.mu.Lock()
	for conn := range s.conns {
		conn.Close()
	}
	s.conns = make(map[net.Conn]struct{})
	s.mu.Unlock()

	s.wg.Wait()
	s.listener = nil
	s.listeners = nil
	s.started.Store(false) // allow re-Start after Stop

	if n := s.earlyClose.Load(); n > 0 {
		s.logr.Info("connections closed before CONNECT", "count", n)
	}
}

// Addr returns the server's listening address.
// Addrs returns every bound MQTT-over-TCP address, primary first. It is empty
// before Start and after Stop, matching Addr's contract of never advertising an
// address nobody listens on.
//
// Use this (not Addr) when reporting which ports a multi-port broker serves --
// Addr returns only the first.
func (s *MQTTServer) Addrs() []net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]net.Addr, 0, len(s.listeners))
	for _, ln := range s.listeners {
		if ln != nil {
			out = append(out, ln.Addr())
		}
	}
	return out
}

func (s *MQTTServer) Addr() net.Addr {
	if s.listener != nil {
		return s.listener.Addr()
	}
	return nil
}

// WSAddr returns the plain WebSocket listener address, or nil when plain WS is
// disabled (R5).
func (s *MQTTServer) WSAddr() net.Addr {
	for _, ep := range s.wsEndpoints {
		if !ep.tls {
			return ep.ln.Addr()
		}
	}
	return nil
}

// WSSAddr returns the TLS WebSocket (WSS) listener address, or nil when WSS is
// disabled (R5).
func (s *MQTTServer) WSSAddr() net.Addr {
	for _, ep := range s.wsEndpoints {
		if ep.tls {
			return ep.ln.Addr()
		}
	}
	return nil
}

// ConnCount returns the current number of active connections.
func (s *MQTTServer) ConnCount() int64 {
	return s.connCount.Load()
}

func (s *MQTTServer) acceptLoop(ln net.Listener) {
	defer s.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			s.logr.Debug("accept error", "error", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// Check max connections
		if s.cfg.MaxConnections > 0 && int(s.connCount.Load()) >= s.cfg.MaxConnections {
			s.logr.Warn("max connections reached, rejecting", "limit", s.cfg.MaxConnections)
			conn.Close()
			continue
		}

		s.connCount.Add(1)
		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()

		// Configure OS-level TCP keep-alive so a dead peer is detected even
		// for clients that disable the MQTT keep-alive (KeepAlive=0).
		configureTCPKeepAlive(conn, s.cfg.TCPKeepAlivePeriod)

		s.wg.Add(1)
		go func(c net.Conn) {
			defer s.wg.Done()
			defer func() {
				s.connCount.Add(-1)
				s.mu.Lock()
				delete(s.conns, c)
				s.mu.Unlock()
				c.Close()
			}()

			if s.handler != nil {
				if err := s.handleConn(c); err != nil {
					if isEarlyClose(err) {
						s.earlyClose.Add(1)
					} else {
						s.logr.Debug("connection handler error", "error", err)
					}
				}
			}
		}(conn)
	}
}

// handleConn performs protocol identification and handshake on an accepted
// connection, assembles a protocol-neutral Conn, and hands it to the configured
// handler. The CONNECT handshake deadline lives here (migrated out of
// Broker.HandleConnection).
func (s *MQTTServer) handleConn(conn net.Conn) error {
	if s.handler == nil {
		return nil
	}
	pc := protocol.NewPeekConn(conn)

	// Bound protocol identification (Peek) with the handshake deadline so a
	// client that connects but never sends a byte is reaped promptly. Failing
	// to arm the deadline was counted as a "deadline" error before the
	// protocol-adapter refactor; preserve that counter (R2).
	timeout := s.connectTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if err := pc.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		s.metrics.IncErrors("deadline")
		return err
	}

	adapter, err := s.registry.Select(pc, "")
	if err != nil {
		// Identification failed: the opening bytes could not be read (e.g. a
		// client that connected but sent nothing and was reaped by the
		// deadline) or no registered adapter claimed them. Before the refactor
		// both surfaced as a CONNECT decode failure, so they keep the same
		// decode_error/decode counters.
		s.metrics.IncRejections("decode_error")
		s.metrics.IncErrors("decode")
		return err
	}
	info, codec, err := adapter.Handshake(s.ctx, pc)
	if err != nil {
		// The CONNECT frame failed to decode/read. The broker core owned this
		// counter before the refactor; the handshake now lives in the adapter,
		// so count it here. This is strictly the wire-decode path: semantic
		// validation (protocol name/version/flags/ClientID) still runs in the
		// broker and is counted there (invalid_packet / invalid_connect), so
		// the two never double-count.
		s.metrics.IncRejections("decode_error")
		s.metrics.IncErrors("decode")
		s.rejectConnect(pc, codec, err)
		return err
	}
	// The handshake deadline only covers identification + CONNECT; clear it so
	// the per-connection keep-alive deadline (set by the broker) governs the
	// read loop.
	_ = pc.SetReadDeadline(time.Time{})

	spec, _ := adapter.Spec(info.Version)
	return s.handler.HandleConnection(s.ctx, &Conn{
		Raw:     pc,
		Adapter: adapter,
		Info:    info,
		Codec:   codec,
		Spec:    spec,
	})
}

// rejectConnect answers a CONNECT that failed to decode with a CONNACK before
// the connection is closed. MQTT 5.0 §4.13.1 permits it -- "In the case of an
// error in a CONNECT packet it MAY send a CONNACK packet containing the Reason
// Code" -- and it is the only chance to tell the peer what was wrong: after the
// CONNECT the server either has a session to report on or nothing to report on
// at all.
//
// It is deliberately narrow on two axes.
//
// Version: nothing is sent unless the codec reports a version whose CONNACK
// carries a reason code. Handshake returns the codec even on failure and
// decodeConnect adopts the declared version as soon as it reads that byte, so
// this fires exactly when the peer identified itself as 5.0 before the frame
// went wrong. A 3.1.1 or 3.1 peer, and any failure before the version byte (a
// bad Remaining Length, a truncated body, an oversized packet), keeps the bare
// close it has always got: a 5.0-shaped CONNACK is unparseable to them and would
// be worse than silence. Gating on the capability rather than on the version
// value also guarantees the codec writes a reason code at all -- encodeConnAck
// puts that byte on the wire unconditionally, so encoding 0x82 through a 3.1.1
// codec would emit a return code the version does not define.
//
// Cause: only the codec's own sentinels map to a reason code; a transport
// failure (EOF, read deadline) stays silent. See decodeErrorReasonCode.
func (s *MQTTServer) rejectConnect(conn net.Conn, codec protocol.PacketCodec, err error) {
	if codec == nil || !codec.Spec().ConnAckHasReasonCode {
		return
	}
	reason, reportable := decodeErrorReasonCode(err)
	if !reportable {
		return
	}
	s.logr.Warn("CONNECT rejected before the session was established",
		"reasonCode", reason, "error", err)
	if werr := writeConnAck(conn, codec, reason, false); werr != nil {
		s.logr.Debug("failed to send rejection CONNACK", "error", werr)
	}
}

// isEarlyClose returns true if the error indicates the client disconnected
// before completing the MQTT handshake (e.g., closed without sending CONNECT).
func isEarlyClose(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	// "use of closed network connection" — client closed before/during CONNECT decode
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Op == "read" {
		return true
	}
	return false
}

// configureTCPKeepAlive enables OS-level TCP keep-alive on an accepted
// connection. Plain TCP is configured directly; TLS connections are unwrapped
// through tls.Conn.NetConn() — previously the *tls.Conn returned by the
// tls-wrapped listener failed the plain *net.TCPConn type assertion, so
// tcp_keepalive_period silently did nothing for TLS/WSS endpoints (audit).
func configureTCPKeepAlive(conn net.Conn, period time.Duration) {
	if period <= 0 {
		return
	}
	var tc *net.TCPConn
	switch c := conn.(type) {
	case *net.TCPConn:
		tc = c
	case *tls.Conn:
		if nc, ok := c.NetConn().(*net.TCPConn); ok {
			tc = nc
		}
	}
	if tc == nil {
		return
	}
	_ = tc.SetKeepAlive(true)
	_ = tc.SetKeepAlivePeriod(period)
}

// wsUpgrader negotiates MQTT-over-WebSocket connections (R5). The "mqtt"
// subprotocol is offered per the MQTT WebSocket binding; any Origin is
// accepted since MQTT clients are not browsers.
var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	Subprotocols:    []string{"mqtt"},
	CheckOrigin:     func(*http.Request) bool { return true },
}

// startWS starts an HTTP/WebSocket listener for MQTT-over-WebSocket on addr
// (R5). When useTLS is true, the listener serves WSS (TLS-wrapped WebSocket)
// using the server's TLS config.
func (s *MQTTServer) startWS(addr string, useTLS bool) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if useTLS {
		if s.tlsConfig == nil {
			ln.Close()
			return fmt.Errorf("server: WSS requires TLS configuration")
		}
		ln = tls.NewListener(ln, s.tlsConfig)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/mqtt", s.wsHandler)
	mux.HandleFunc("/", s.wsHandler)
	ep := &wsEndpoint{
		ln:  ln,
		srv: &http.Server{Handler: mux},
		tls: useTLS,
	}
	s.wsEndpoints = append(s.wsEndpoints, ep)
	scheme := "ws"
	if useTLS {
		scheme = "wss"
	}
	s.logr.Info("ws listening", "addr", addr, "scheme", scheme)
	go func() {
		if err := ep.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logr.Debug("ws server error", "error", err)
		}
	}()
	return nil
}

// wsHandler upgrades an HTTP request to WebSocket and hands the connection to
// the broker's HandleConnection, which runs the normal MQTT read loop.
func (s *MQTTServer) wsHandler(w http.ResponseWriter, r *http.Request) {
	ws, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logr.Debug("ws upgrade failed", "error", err)
		return
	}
	conn := newWSConn(ws)
	// WebSocket connections bypass the accept-loop path above, so apply the
	// OS TCP keep-alive configuration to the upgraded socket here (audit).
	configureTCPKeepAlive(ws.UnderlyingConn(), s.cfg.TCPKeepAlivePeriod)
	s.connCount.Add(1)
	s.mu.Lock()
	s.conns[conn] = struct{}{}
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			conn.Close()
			s.connCount.Add(-1)
			s.mu.Lock()
			delete(s.conns, conn)
			s.mu.Unlock()
		}()
		if s.handler != nil {
			if err := s.handleConn(conn); err != nil {
				s.logr.Debug("ws connection handler error", "error", err)
			}
		}
	}()
}
