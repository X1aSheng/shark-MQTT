package broker

import (
	"crypto/tls"
	"net"
	"time"

	"github.com/X1aSheng/shark-mqtt/pkg/logger"
	"github.com/X1aSheng/shark-mqtt/pkg/metrics"
)

// ServerOption configures the Server.
type ServerOption func(*serverOpts)

type serverOpts struct {
	tlsConfig        *tls.Config
	writeBufferSize  int
	readBufferSize   int
	maxPacketSize    uint32
	maxPacketSizeSet bool // true when WithMaxPacketSize was applied explicitly
	keepAlive        time.Duration
	connectTimeout   time.Duration
	listener         net.Listener  // custom listener (for testing)
	logr             logger.Logger // diagnostic logger
	metrics          metrics.Metrics
}

func defaultServerOptions() serverOpts {
	return serverOpts{
		writeBufferSize: 4096,
		readBufferSize:  4096,
		maxPacketSize:   268435455, // MQTT max packet size
		connectTimeout:  5 * time.Second,
		metrics:         metrics.Default(),
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
		o.maxPacketSizeSet = true
	}
}

// WithKeepAlive sets the keep-alive timeout.
func WithKeepAlive(d time.Duration) ServerOption {
	return func(o *serverOpts) {
		o.keepAlive = d
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

// WithServerLogger sets the logger for the MQTT server. Default is a no-op
// logger; set this in production entrypoints to enable server diagnostics.
func WithServerLogger(l logger.Logger) ServerOption {
	return func(o *serverOpts) {
		o.logr = l
	}
}

// WithServerMetrics sets the metrics collector used by the network server for
// the handshake-phase counters that moved out of the broker core with the
// protocol-adapter refactor (CONNECT read-deadline failures and CONNECT decode
// failures). Callers should pass the same collector given to the broker so the
// counters land on one sink. A nil value is normalized to a no-op (see
// NewMQTTServer) so it can never cause a nil-pointer panic.
func WithServerMetrics(m metrics.Metrics) ServerOption {
	return func(o *serverOpts) {
		o.metrics = m
	}
}
