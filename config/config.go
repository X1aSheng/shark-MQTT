// Package config provides configuration management for the shark-mqtt broker.
package config

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	DefaultListenAddr    = ":18983"
	DefaultTLSListenAddr = ":18993"
	DefaultMetricsAddr   = ":18999"
)

// Config holds all configuration for the MQTT broker.
type Config struct {
	// Server settings
	// ListenAddr is the primary MQTT-over-TCP address. Kept as a scalar for
	// compatibility; ListenAddrs, when non-empty, takes precedence and may hold
	// several addresses so one process can serve several ports (see
	// effectiveListenAddrs).
	ListenAddr string `yaml:"listen_addr" toml:"listen_addr" env:"MQTT_LISTEN_ADDR"`
	// ListenAddrs serves additional MQTT-over-TCP addresses in the same process.
	// Empty means "use ListenAddr only", so existing configs are unaffected.
	ListenAddrs []string `yaml:"listen_addrs" toml:"listen_addrs" env:"MQTT_LISTEN_ADDRS"`
	// WSListenAddrs / WSSListenAddrs are the array forms of the two WebSocket
	// settings below, same precedence rule.
	WSListenAddrs  []string `yaml:"ws_listen_addrs" toml:"ws_listen_addrs" env:"MQTT_WS_LISTEN_ADDRS"`    // MQTT-over-WebSocket, empty disables (R5)
	WSSListenAddrs []string `yaml:"wss_listen_addrs" toml:"wss_listen_addrs" env:"MQTT_WSS_LISTEN_ADDRS"` // TLS WebSocket, requires TLS (R5)

	WSListenAddr  string `yaml:"ws_listen_addr" toml:"ws_listen_addr" env:"MQTT_WS_LISTEN_ADDR"`    // MQTT-over-WebSocket, empty disables (R5); see WSListenAddrs
	WSSListenAddr string `yaml:"wss_listen_addr" toml:"wss_listen_addr" env:"MQTT_WSS_LISTEN_ADDR"` // TLS WebSocket, requires TLS (R5); see WSSListenAddrs
	TLSEnabled    bool   `yaml:"tls_enabled" toml:"tls_enabled" env:"MQTT_TLS_ENABLED"`
	TLSCertFile   string `yaml:"tls_cert_file" toml:"tls_cert_file" env:"MQTT_TLS_CERT_FILE"`
	TLSKeyFile    string `yaml:"tls_key_file" toml:"tls_key_file" env:"MQTT_TLS_KEY_FILE"`
	TLSMinVersion uint16 `yaml:"tls_min_version" toml:"tls_min_version" env:"MQTT_TLS_MIN_VERSION"`
	TLSMaxVersion uint16 `yaml:"tls_max_version" toml:"tls_max_version" env:"MQTT_TLS_MAX_VERSION"`
	// mTLS settings
	TLSMutual      bool          `yaml:"tls_mutual" toml:"tls_mutual" env:"MQTT_TLS_MUTUAL"`
	TLSCACertFile  string        `yaml:"tls_ca_cert_file" toml:"tls_ca_cert_file" env:"MQTT_TLS_CA_CERT_FILE"`
	ConnectTimeout time.Duration `yaml:"connect_timeout" toml:"connect_timeout" env:"MQTT_CONNECT_TIMEOUT"`
	// DrainTimeout bounds how long shutdown waits for in-flight QoS 1/2
	// messages to be acknowledged before closing the sockets anyway.
	// 0 closes immediately; the wait requires live sockets, so it happens
	// before they close.
	DrainTimeout   time.Duration `yaml:"drain_timeout" toml:"drain_timeout" env:"MQTT_DRAIN_TIMEOUT"`
	KeepAlive      uint16        `yaml:"keep_alive" toml:"keep_alive" env:"MQTT_KEEP_ALIVE"`
	MaxPacketSize  int           `yaml:"max_packet_size" toml:"max_packet_size" env:"MQTT_MAX_PACKET_SIZE"`
	MaxConnections int           `yaml:"max_connections" toml:"max_connections" env:"MQTT_MAX_CONNECTIONS"`
	WriteQueueSize int           `yaml:"write_queue_size" toml:"write_queue_size" env:"MQTT_WRITE_QUEUE_SIZE"`
	// TCPKeepAlivePeriod is the OS-level TCP keep-alive probe interval applied
	// to accepted connections (0 = keep the Go runtime default). It bounds how
	// long a dead peer that disables the MQTT keep-alive (KeepAlive=0) can
	// stay registered as an online connection before being reaped.
	TCPKeepAlivePeriod time.Duration `yaml:"tcp_keepalive_period" toml:"tcp_keepalive_period" env:"MQTT_TCP_KEEPALIVE_PERIOD"`

	// QoS settings
	QoSRetryInterval time.Duration `yaml:"qos_retry_interval" toml:"qos_retry_interval" env:"MQTT_QOS_RETRY_INTERVAL"`
	QoSMaxRetries    int           `yaml:"qos_max_retries" toml:"qos_max_retries" env:"MQTT_QOS_MAX_RETRIES"`
	QoSMaxInflight   int           `yaml:"qos_max_inflight" toml:"qos_max_inflight" env:"MQTT_QOS_MAX_INFLIGHT"`

	// Session settings
	SessionExpiryInterval time.Duration `yaml:"session_expiry_interval" toml:"session_expiry_interval" env:"MQTT_SESSION_EXPIRY"`

	// $SYS status topic publish interval (0 disables the status loop).
	SysInterval time.Duration `yaml:"sys_interval" toml:"sys_interval" env:"MQTT_SYS_INTERVAL"`

	// Storage backend: "memory", "redis", "badger"
	StorageBackend string `yaml:"storage_backend" toml:"storage_backend" env:"MQTT_STORAGE_BACKEND"`

	// Redis settings
	RedisAddr     string `yaml:"redis_addr" toml:"redis_addr" env:"MQTT_REDIS_ADDR"`
	RedisPassword string `yaml:"redis_password" toml:"redis_password" env:"MQTT_REDIS_PASSWORD"`
	RedisDB       int    `yaml:"redis_db" toml:"redis_db" env:"MQTT_REDIS_DB"`

	// BadgerDB settings
	BadgerPath string `yaml:"badger_path" toml:"badger_path" env:"MQTT_BADGER_PATH"`

	// Logging
	LogLevel  string `yaml:"log_level" toml:"log_level" env:"MQTT_LOG_LEVEL"`
	LogFormat string `yaml:"log_format" toml:"log_format" env:"MQTT_LOG_FORMAT"`

	// AuthFile points to a YAML/JSON user credential file (see
	// broker.NewFileAuth). When set, the CLI authenticates clients against
	// it instead of denying everything (the CLI's default). An explicit
	// --allow-all flag overrides this.
	AuthFile string `yaml:"auth_file" toml:"auth_file" env:"MQTT_AUTH_FILE"`

	// Metrics
	// MetricsEnabled controls whether the /metrics endpoint is served on the
	// metrics listener. /healthz and /readyz are always served so probes and
	// deployments relying on them keep working. Default true to match the
	// documented out-of-the-box Prometheus behavior; set false to reduce the
	// network exposure.
	MetricsEnabled bool   `yaml:"metrics_enabled" toml:"metrics_enabled" env:"MQTT_METRICS_ENABLED"`
	MetricsAddr    string `yaml:"metrics_addr" toml:"metrics_addr" env:"MQTT_METRICS_ADDR"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		ListenAddr:            DefaultListenAddr,
		TLSEnabled:            false,
		ConnectTimeout:        10 * time.Second,
		DrainTimeout:          5 * time.Second,
		KeepAlive:             60,
		MaxPacketSize:         256 * 1024,
		MaxConnections:        10000,
		WriteQueueSize:        256,
		TCPKeepAlivePeriod:    60 * time.Second,
		QoSRetryInterval:      10 * time.Second,
		QoSMaxRetries:         3,
		QoSMaxInflight:        100,
		SessionExpiryInterval: 24 * time.Hour,
		SysInterval:           30 * time.Second,
		StorageBackend:        "memory",
		LogLevel:              "info",
		LogFormat:             "text",
		MetricsEnabled:        true,
		MetricsAddr:           DefaultMetricsAddr,
	}
}

// Validate checks that configuration values are valid.
// EffectiveListenAddrs returns the MQTT-over-TCP addresses to bind.
//
// Precedence: ListenAddrs wins when non-empty, otherwise the scalar
// ListenAddr is used alone. The scalar is never merged into the array -- doing
// so would silently bind :18983 alongside an operator's explicit list, which is
// a surprising port to open. Duplicates within the array are dropped while
// preserving order, because binding the same address twice fails anyway and the
// resulting error message would not say which entry was at fault.
func (c *Config) EffectiveListenAddrs() []string {
	addrs := c.ListenAddrs
	if len(addrs) == 0 {
		if c.ListenAddr == "" {
			return nil
		}
		return []string{c.ListenAddr}
	}
	return dedupAddrs(addrs)
}

// EffectiveWSListenAddrs and EffectiveWSSListenAddrs apply the same precedence
// to the two WebSocket settings.
func (c *Config) EffectiveWSListenAddrs() []string {
	if len(c.WSListenAddrs) > 0 {
		return dedupAddrs(c.WSListenAddrs)
	}
	if c.WSListenAddr != "" {
		return []string{c.WSListenAddr}
	}
	return nil
}

func (c *Config) EffectiveWSSListenAddrs() []string {
	if len(c.WSSListenAddrs) > 0 {
		return dedupAddrs(c.WSSListenAddrs)
	}
	if c.WSSListenAddr != "" {
		return []string{c.WSSListenAddr}
	}
	return nil
}

// dedupAddrs removes empty and repeated entries while preserving order.
func dedupAddrs(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, a := range in {
		if a == "" {
			continue
		}
		if _, dup := seen[a]; dup {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (c *Config) Validate() error {
	// Every problem is collected, not just the first one. The old version
	// returned on the first failure, so an operator with three bad fields had to
	// fix, redeploy, and read the error log three times. errors.Join (Go 1.20+)
	// keeps every cause intact and still satisfies errors.Is/errors.As.
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	// Check the addresses that will actually be bound, not the scalar alone: a
	// config using only listen_addrs leaves listen_addr empty by design.
	if len(c.EffectiveListenAddrs()) == 0 {
		add("listen_addr or listen_addrs must not be empty")
	}
	if len(c.EffectiveWSSListenAddrs()) > 0 && !c.TLSEnabled {
		add("wss_listen_addr(s) requires TLS to be enabled (tls_enabled)")
	}
	if c.MaxPacketSize <= 0 {
		add("max_packet_size must be > 0, got %d", c.MaxPacketSize)
	}
	if c.MaxConnections < 0 {
		add("max_connections must be >= 0, got %d", c.MaxConnections)
	}
	if c.WriteQueueSize < 0 {
		add("write_queue_size must be >= 0, got %d", c.WriteQueueSize)
	}
	if c.TCPKeepAlivePeriod < 0 {
		add("tcp_keepalive_period must be >= 0, got %v", c.TCPKeepAlivePeriod)
	}
	if c.SysInterval < 0 {
		add("sys_interval must be >= 0, got %v", c.SysInterval)
	}
	if c.QoSMaxRetries < 0 {
		add("qos_max_retries must be >= 0, got %d", c.QoSMaxRetries)
	}
	if c.QoSMaxInflight <= 0 {
		add("qos_max_inflight must be > 0, got %d", c.QoSMaxInflight)
	}
	if c.QoSRetryInterval <= 0 {
		add("qos_retry_interval must be > 0, got %v", c.QoSRetryInterval)
	}
	if c.TLSEnabled {
		if c.TLSCertFile == "" {
			add("tls_cert_file is required when TLS is enabled")
		}
		if c.TLSKeyFile == "" {
			add("tls_key_file is required when TLS is enabled")
		}
		if c.TLSMutual && c.TLSCACertFile == "" {
			add("tls_ca_cert_file is required when tls_mutual is enabled")
		}
	}
	if c.StorageBackend == "badger" && c.BadgerPath == "" {
		add("badger_path is required when storage_backend is badger")
	}
	if c.TLSMinVersion > 0 && c.TLSMaxVersion > 0 && c.TLSMinVersion > c.TLSMaxVersion {
		add("tls_min_version (%d) must be <= tls_max_version (%d)", c.TLSMinVersion, c.TLSMaxVersion)
	}
	switch c.StorageBackend {
	case "memory", "redis", "badger", "":
	default:
		add("unknown storage_backend: %q (must be memory, redis, or badger)", c.StorageBackend)
	}
	if c.StorageBackend == "redis" && c.RedisAddr == "" {
		add("redis_addr is required when storage_backend is redis")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error", "":
	default:
		add("unknown log_level: %q (must be debug, info, warn, or error)", c.LogLevel)
	}
	return errors.Join(errs...)
}

// secureCipherSuites lists recommended TLS 1.2+ cipher suites that provide
// forward secrecy and authenticated encryption. Used when no explicit
// cipher suite list is configured.
var secureCipherSuites = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
	tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
}

// TLSConfig builds a *tls.Config from the configuration.
func (c *Config) TLSConfig() (*tls.Config, error) {
	if !c.TLSEnabled {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(c.TLSCertFile, c.TLSKeyFile)
	if err != nil {
		return nil, err
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		CipherSuites: secureCipherSuites,
	}

	if c.TLSMinVersion > 0 {
		tlsCfg.MinVersion = c.TLSMinVersion
	}
	if c.TLSMaxVersion > 0 {
		tlsCfg.MaxVersion = c.TLSMaxVersion
	}

	// Configure mTLS (mutual TLS) if a CA certificate file is provided
	if c.TLSMutual && c.TLSCACertFile != "" {
		caCert, err := os.ReadFile(c.TLSCACertFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA cert file: %w", err)
		}
		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate from %s", c.TLSCACertFile)
		}
		tlsCfg.ClientCAs = caPool
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return tlsCfg, nil
}
