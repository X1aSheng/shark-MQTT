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
	cfg        *config.Config
	listener   net.Listener
	handler    ConnectionHandler
	connCount  atomic.Int64
	earlyClose atomic.Int64
	tlsConfig  *tls.Config
	tlsErr     error       // set when TLS config fails to load
	started    atomic.Bool // prevents double Start
	logr       logger.Logger
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	mu         sync.Mutex
	conns      map[net.Conn]struct{}
	wsListener net.Listener // WebSocket listener (nil when WS disabled, R5)
	wsServer   *http.Server // WebSocket HTTP server (R5)
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
		if err := s.startWS(s.cfg.WSListenAddr); err != nil {
			s.listener.Close()
			return fmt.Errorf("server: failed to listen on ws %s: %w", s.cfg.WSListenAddr, err)
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
	// Stop the WebSocket listener/server; its accepted connections are closed
	// below via s.conns (R5).
	if s.wsListener != nil {
		s.wsListener.Close()
	}
	if s.wsServer != nil {
		s.wsServer.Close()
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

// WSAddr returns the WebSocket listener address, or nil when WS is disabled (R5).
func (s *MQTTServer) WSAddr() net.Addr {
	if s.wsListener != nil {
		return s.wsListener.Addr()
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

// wsUpgrader negotiates MQTT-over-WebSocket connections (R5). The "mqtt"
// subprotocol is offered per the MQTT WebSocket binding; any Origin is
// accepted since MQTT clients are not browsers.
var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	Subprotocols:    []string{"mqtt"},
	CheckOrigin:     func(*http.Request) bool { return true },
}

// startWS starts an HTTP/WebSocket listener for MQTT-over-WebSocket on addr (R5).
func (s *MQTTServer) startWS(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.wsListener = ln
	mux := http.NewServeMux()
	mux.HandleFunc("/mqtt", s.wsHandler)
	mux.HandleFunc("/", s.wsHandler)
	s.wsServer = &http.Server{Handler: mux}
	s.logr.Info("ws listening", "addr", addr)
	go func() {
		if err := s.wsServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
