package client

import (
	"crypto/tls"
	"net"
	"strconv"
	"time"
)

// Options holds the configuration for an MQTT client.
type Options struct {
	Host            string
	Port            int
	ClientID        string
	Username        string
	Password        string
	KeepAlive       uint16
	CleanSession    bool
	TLSConfig       *tls.Config
	MaxPacketSize   int
	ConnectTimeout  time.Duration
	WriteBufferSize int
}

// Option is a functional option for configuring an MQTT client.
type Option func(*Options)

// WithAddr sets the broker address as "host:port".
// Supports IPv4 (e.g., "localhost:1883") and IPv6 (e.g., "[::1]:1883").
func WithAddr(addr string) Option {
	return func(o *Options) {
		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			// Fall back to treating the whole string as host on default port
			o.Host = addr
			return
		}
		o.Host = host
		port, _ := strconv.Atoi(portStr)
		if port > 0 {
			o.Port = port
		}
	}
}

// WithHostPort sets the broker host and port.
func WithHostPort(host string, port int) Option {
	return func(o *Options) {
		o.Host = host
		o.Port = port
	}
}

// WithClientID sets the client identifier.
func WithClientID(id string) Option {
	return func(o *Options) {
		o.ClientID = id
	}
}

// WithCredentials sets the username and password.
func WithCredentials(username, password string) Option {
	return func(o *Options) {
		o.Username = username
		o.Password = password
	}
}

// WithKeepAlive sets the keep-alive interval in seconds.
func WithKeepAlive(seconds uint16) Option {
	return func(o *Options) {
		o.KeepAlive = seconds
	}
}

// WithCleanSession sets the clean session flag.
func WithCleanSession(clean bool) Option {
	return func(o *Options) {
		o.CleanSession = clean
	}
}

// WithTLS sets the TLS configuration.
func WithTLS(cfg *tls.Config) Option {
	return func(o *Options) {
		o.TLSConfig = cfg
	}
}

// WithMaxPacketSize sets the maximum packet size.
func WithMaxPacketSize(size int) Option {
	return func(o *Options) {
		o.MaxPacketSize = size
	}
}

// WithConnectTimeout sets the connection timeout.
func WithConnectTimeout(d time.Duration) Option {
	return func(o *Options) {
		o.ConnectTimeout = d
	}
}

// defaultOptions returns Options with sensible defaults.
func defaultOptions() *Options {
	return &Options{
		Host:            "localhost",
		Port:            1883,
		ClientID:        "",
		Username:        "",
		Password:        "",
		KeepAlive:       60,
		CleanSession:    true,
		MaxPacketSize:   256 * 1024,
		ConnectTimeout:  30 * time.Second,
		WriteBufferSize: 4096,
	}
}
