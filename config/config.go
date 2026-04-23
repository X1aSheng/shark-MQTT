// Package config provides configuration management for the shark-mqtt broker.
package config

import (
	"crypto/tls"
	"time"
)

// Config holds all configuration for the MQTT broker.
type Config struct {
	// Server settings
	ListenAddr     string        `yaml:"listen_addr" toml:"listen_addr" env:"MQTT_LISTEN_ADDR"`
	TLSEnabled     bool          `yaml:"tls_enabled" toml:"tls_enabled" env:"MQTT_TLS_ENABLED"`
	TLSCertFile    string        `yaml:"tls_cert_file" toml:"tls_cert_file" env:"MQTT_TLS_CERT_FILE"`
	TLSKeyFile     string        `yaml:"tls_key_file" toml:"tls_key_file" env:"MQTT_TLS_KEY_FILE"`
	ConnectTimeout time.Duration `yaml:"connect_timeout" toml:"connect_timeout" env:"MQTT_CONNECT_TIMEOUT"`
	KeepAlive      uint16        `yaml:"keep_alive" toml:"keep_alive" env:"MQTT_KEEP_ALIVE"`
	MaxPacketSize  int           `yaml:"max_packet_size" toml:"max_packet_size" env:"MQTT_MAX_PACKET_SIZE"`
	MaxConnections int           `yaml:"max_connections" toml:"max_connections" env:"MQTT_MAX_CONNECTIONS"`
	WriteQueueSize int           `yaml:"write_queue_size" toml:"write_queue_size" env:"MQTT_WRITE_QUEUE_SIZE"`

	// QoS settings
	QoSRetryInterval time.Duration `yaml:"qos_retry_interval" toml:"qos_retry_interval" env:"MQTT_QOS_RETRY_INTERVAL"`
	QoSMaxRetries    int           `yaml:"qos_max_retries" toml:"qos_max_retries" env:"MQTT_QOS_MAX_RETRIES"`
	QoSMaxInflight   int           `yaml:"qos_max_inflight" toml:"qos_max_inflight" env:"MQTT_QOS_MAX_INFLIGHT"`

	// Session settings
	SessionExpiryInterval time.Duration `yaml:"session_expiry_interval" toml:"session_expiry_interval" env:"MQTT_SESSION_EXPIRY"`

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

	// Metrics
	MetricsEnabled  bool   `yaml:"metrics_enabled" toml:"metrics_enabled" env:"MQTT_METRICS_ENABLED"`
	MetricsAddr     string `yaml:"metrics_addr" toml:"metrics_addr" env:"MQTT_METRICS_ADDR"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		ListenAddr:            ":1883",
		TLSEnabled:            false,
		ConnectTimeout:        10 * time.Second,
		KeepAlive:             60,
		MaxPacketSize:         256 * 1024,
		MaxConnections:        10000,
		WriteQueueSize:        256,
		QoSRetryInterval:      10 * time.Second,
		QoSMaxRetries:         3,
		QoSMaxInflight:        100,
		SessionExpiryInterval: 24 * time.Hour,
		StorageBackend:        "memory",
		LogLevel:              "info",
		LogFormat:             "text",
		MetricsEnabled:        false,
		MetricsAddr:           ":9090",
	}
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
	return &tls.Config{Certificates: []tls.Certificate{cert}}, nil
}
