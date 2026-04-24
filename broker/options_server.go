package broker

import (
	"crypto/tls"
	"net"
	"time"
)

// ServerOption configures the Server.
type ServerOption func(*serverOpts)

type serverOpts struct {
	tlsConfig       *tls.Config
	writeBufferSize int
	readBufferSize  int
	maxPacketSize   uint32
	keepAlive       time.Duration
	writeQueueSize  int
	connectTimeout  time.Duration
	listener        net.Listener // custom listener (for testing)
}

func defaultServerOptions() serverOpts {
	return serverOpts{
		writeBufferSize: 4096,
		readBufferSize:  4096,
		maxPacketSize:   268435455, // MQTT max packet size
		writeQueueSize:  100,
		connectTimeout:  5 * time.Second,
	}
}

// WithTLSConfig sets the TLS configuration.
func WithTLSConfig(cfg *tls.Config) ServerOption {
	return func(o *serverOpts) {
		o.tlsConfig = cfg
	}
}

// WithWriteBufferSize sets the write buffer size.
func WithWriteBufferSize(n int) ServerOption {
	return func(o *serverOpts) {
		o.writeBufferSize = n
	}
}

// WithReadBufferSize sets the read buffer size.
func WithReadBufferSize(n int) ServerOption {
	return func(o *serverOpts) {
		o.readBufferSize = n
	}
}

// WithMaxPacketSize sets the maximum packet size.
func WithMaxPacketSize(n uint32) ServerOption {
	return func(o *serverOpts) {
		o.maxPacketSize = n
	}
}

// WithKeepAlive sets the keep-alive timeout.
func WithKeepAlive(d time.Duration) ServerOption {
	return func(o *serverOpts) {
		o.keepAlive = d
	}
}

// WithWriteQueueSize sets the outbound message queue size.
func WithWriteQueueSize(n int) ServerOption {
	return func(o *serverOpts) {
		o.writeQueueSize = n
	}
}

// WithConnectTimeout sets the connect timeout.
func WithConnectTimeout(d time.Duration) ServerOption {
	return func(o *serverOpts) {
		o.connectTimeout = d
	}
}

// WithListener sets a custom listener (useful for testing).
func WithListener(ln net.Listener) ServerOption {
	return func(o *serverOpts) {
		o.listener = ln
	}
}
