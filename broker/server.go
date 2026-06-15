// Package broker provides the core MQTT broker and network server.
package broker

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/pkg/logger"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// MQTTServer handles incoming MQTT connections over TCP.
type MQTTServer struct {
	cfg        *config.Config
	listener   net.Listener
	handler    ConnectionHandler
	connCount  atomic.Int64
	earlyClose atomic.Int64
	tlsConfig  *tls.Config
	tlsErr     error // set when TLS config fails to load
	logr       logger.Logger
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	mu         sync.Mutex
	conns      map[net.Conn]struct{}
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
	return nil
}

// Stop gracefully shuts down the server.
func (s *MQTTServer) Stop() {
	s.cancel()
	if s.listener != nil {
		s.listener.Close()
	}
	s.wg.Wait()
	s.listener = nil

	// Close remaining connections
	s.mu.Lock()
	for conn := range s.conns {
		conn.Close()
	}
	s.conns = make(map[net.Conn]struct{})
	s.mu.Unlock()

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
