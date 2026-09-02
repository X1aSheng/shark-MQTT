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
	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/gorilla/websocket"
)

// MQTTServer handles incoming MQTT connections over TCP and, when configured,
// MQTT-over-WebSocket (R5).
type MQTTServer struct {
	cfg         *config.Config
	listener    net.Listener
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
}

// wsEndpoint is one WebSocket listener (plain WS or TLS WSS).
type wsEndpoint struct {
	ln  net.Listener
	srv *http.Server
	tls bool
}

// ConnectionHandler is called when a new connection is accepted.
// It receives the raw connection and should handle the MQTT protocol handshake.
type ConnectionHandler interface {
	HandleConnection(ctx context.Context, conn net.Conn, codec *protocol.Codec) error
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

	// Use pre-set listener (from options) or create one from config
	if s.listener == nil {
		addr := s.cfg.ListenAddr
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("server: failed to listen on %s: %w", addr, err)
		}
		s.listener = ln
	}

	if s.tlsConfig != nil {
		s.listener = tls.NewListener(s.listener, s.tlsConfig)
	}

	s.logr.Info("server listening", "addr", s.listener.Addr(), "tls", s.cfg.TLSEnabled)

	ln := s.listener
	s.wg.Add(1)
	go s.acceptLoop(ln)

	if s.cfg.WSListenAddr != "" {
		if err := s.startWS(s.cfg.WSListenAddr, false); err != nil {
			s.listener.Close()
			return fmt.Errorf("server: failed to listen on ws %s: %w", s.cfg.WSListenAddr, err)
		}
	}
	if s.cfg.WSSListenAddr != "" {
		if s.tlsConfig == nil {
			s.listener.Close()
			return fmt.Errorf("server: wss_listen_addr requires TLS to be configured (tls_enabled)")
		}
		if err := s.startWS(s.cfg.WSSListenAddr, true); err != nil {
			s.listener.Close()
			return fmt.Errorf("server: failed to listen on wss %s: %w", s.cfg.WSSListenAddr, err)
		}
	}
	return nil
}

// Stop gracefully shuts down the server.
func (s *MQTTServer) Stop() {
	s.cancel()
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
	s.started.Store(false) // allow re-Start after Stop

	if n := s.earlyClose.Load(); n > 0 {
		s.logr.Info("connections closed before CONNECT", "count", n)
	}
}

// Addr returns the server's listening address.
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
				if err := s.handler.HandleConnection(s.ctx, c, nil); err != nil {
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
			if err := s.handler.HandleConnection(s.ctx, conn, nil); err != nil {
				s.logr.Debug("ws connection handler error", "error", err)
			}
		}
	}()
}
