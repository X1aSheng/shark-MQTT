// Package broker provides the core MQTT broker and network server.
package broker

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// MQTTServer handles incoming MQTT connections over TCP.
type MQTTServer struct {
	cfg        *config.Config
	listener   net.Listener
	handler    ConnectionHandler
	codec      *protocol.Codec
	connCount  atomic.Int64
	earlyClose atomic.Int64
	tlsConfig  *tls.Config
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
		codec:  protocol.NewCodec(cfg.MaxPacketSize),
		ctx:    ctx,
		cancel: cancel,
		conns:  make(map[net.Conn]struct{}),
	}

	// TLS: explicit option takes priority over config
	if sopts.tlsConfig != nil {
		s.tlsConfig = sopts.tlsConfig
	} else if cfg.TLSEnabled {
		tlsCfg, err := cfg.TLSConfig()
		if err != nil {
			log.Printf("[server] warning: TLS enabled but config failed: %v", err)
		}
		s.tlsConfig = tlsCfg
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

	log.Printf("[server] listening on %s (TLS: %v)", s.listener.Addr(), s.cfg.TLSEnabled)

	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

// Stop gracefully shuts down the server.
func (s *MQTTServer) Stop() {
	s.cancel()
	if s.listener != nil {
		s.listener.Close()
	}
	s.wg.Wait()

	// Close remaining connections
	s.mu.Lock()
	for conn := range s.conns {
		conn.Close()
	}
	s.conns = make(map[net.Conn]struct{})
	s.mu.Unlock()

	if n := s.earlyClose.Load(); n > 0 {
		log.Printf("[server] %d connections closed before CONNECT", n)
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

func (s *MQTTServer) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
				log.Printf("[server] accept error: %v", err)
				continue
			}
		}

		// Check max connections
		if s.cfg.MaxConnections > 0 && int(s.connCount.Load()) >= s.cfg.MaxConnections {
			log.Printf("[server] max connections reached (%d), rejecting", s.cfg.MaxConnections)
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
				if err := s.handler.HandleConnection(s.ctx, c, s.codec); err != nil {
					if isEarlyClose(err) {
						s.earlyClose.Add(1)
					} else {
						log.Printf("[server] connection handler error: %v", err)
					}
				}
			}
		}(conn)
	}
}

// SafeConn wraps net.Conn with safe Read/Write that respect deadlines.
type SafeConn struct {
	net.Conn
	readDeadline  time.Time
	writeDeadline time.Time
}

// ReadPacket reads a complete MQTT packet from the connection.
func ReadPacket(conn net.Conn, codec *protocol.Codec) (protocol.Packet, error) {
	return codec.Decode(conn)
}

// WritePacket writes a complete MQTT packet to the connection.
func WritePacket(conn net.Conn, codec *protocol.Codec, pkt protocol.Packet) error {
	return codec.Encode(conn, pkt)
}

// DrainAndClose gracefully closes a connection by reading any remaining data.
func DrainAndClose(conn net.Conn, timeout time.Duration) {
	conn.SetReadDeadline(time.Now().Add(timeout))
	io.Copy(io.Discard, conn)
	conn.Close()
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
