package server

import (
	"crypto/tls"
	"net"
	"time"
)

// Option configures the Server.
type Option func(*serverOptions)

type serverOptions struct {
	tlsConfig       *tls.Config
	writeBufferSize int
	readBufferSize  int
	maxPacketSize   uint32
	keepAlive       time.Duration
	writeQueueSize  int
	connectTimeout  time.Duration
	listener        net.Listener // custom listener (for testing)
}

func defaultServerOptions() serverOptions {
	return serverOptions{
		writeBufferSize: 4096,
		readBufferSize:  4096,
		maxPacketSize:   268435455, // MQTT max packet size
		writeQueueSize:  100,
		connectTimeout:  5 * time.Second,
	}
}

// WithTLSConfig sets the TLS configuration.
func WithTLSConfig(cfg *tls.Config) Option {
	return func(o *serverOptions) {
		o.tlsConfig = cfg
	}
}

// WithWriteBufferSize sets the write buffer size.
func WithWriteBufferSize(n int) Option {
	return func(o *serverOptions) {
		o.writeBufferSize = n
	}
}

// WithReadBufferSize sets the read buffer size.
func WithReadBufferSize(n int) Option {
	return func(o *serverOptions) {
		o.readBufferSize = n
	}
}

// WithMaxPacketSize sets the maximum packet size.
func WithMaxPacketSize(n uint32) Option {
	return func(o *serverOptions) {
		o.maxPacketSize = n
	}
}

// WithKeepAlive sets the keep-alive timeout.
func WithKeepAlive(d time.Duration) Option {
	return func(o *serverOptions) {
		o.keepAlive = d
	}
}

// WithWriteQueueSize sets the outbound message queue size.
func WithWriteQueueSize(n int) Option {
	return func(o *serverOptions) {
		o.writeQueueSize = n
	}
}

// WithConnectTimeout sets the connect timeout.
func WithConnectTimeout(d time.Duration) Option {
	return func(o *serverOptions) {
		o.connectTimeout = d
	}
}

// WithListener sets a custom listener (useful for testing).
func WithListener(ln net.Listener) Option {
	return func(o *serverOptions) {
		o.listener = ln
	}
}
