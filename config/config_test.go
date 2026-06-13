package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ListenAddr != ":18983" {
		t.Errorf("expected :18983, got %s", cfg.ListenAddr)
	}
	if cfg.MetricsAddr != ":18999" {
		t.Errorf("expected :18999, got %s", cfg.MetricsAddr)
	}
	if cfg.KeepAlive != 60 {
		t.Errorf("expected 60, got %d", cfg.KeepAlive)
	}
	if cfg.StorageBackend != "memory" {
		t.Errorf("expected memory, got %s", cfg.StorageBackend)
	}
}

func TestTLSConfig(t *testing.T) {
	cfg := DefaultConfig()
	tc, err := cfg.TLSConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tc != nil {
		t.Fatal("expected nil when TLS disabled")
	}

	cfg.TLSEnabled = true
	cfg.TLSCertFile = "nonexistent.crt"
	cfg.TLSKeyFile = "nonexistent.key"
	_, err = cfg.TLSConfig()
	if err == nil {
		t.Fatal("expected error for nonexistent cert files")
	}
}

func TestLoadEnv(t *testing.T) {
	_ = os.Setenv("MQTT_LISTEN_ADDR", ":9999")
	_ = os.Setenv("MQTT_KEEP_ALIVE", "120")
	_ = os.Setenv("MQTT_LOG_LEVEL", "debug")
	defer func() {
		_ = os.Unsetenv("MQTT_LISTEN_ADDR")
		_ = os.Unsetenv("MQTT_KEEP_ALIVE")
		_ = os.Unsetenv("MQTT_LOG_LEVEL")
	}()

	loader := NewLoader("")
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ListenAddr != ":9999" {
		t.Errorf("expected :9999, got %s", cfg.ListenAddr)
	}
	if cfg.KeepAlive != 120 {
		t.Errorf("expected 120, got %d", cfg.KeepAlive)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected debug, got %s", cfg.LogLevel)
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `
listen_addr: ":18993"
keep_alive: 30
log_level: warn
qos_max_retries: 5
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	loader := NewLoader(cfgPath)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ListenAddr != ":18993" {
		t.Errorf("expected :18993, got %s", cfg.ListenAddr)
	}
	if cfg.KeepAlive != 30 {
		t.Errorf("expected 30, got %d", cfg.KeepAlive)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("expected warn, got %s", cfg.LogLevel)
	}
	if cfg.QoSMaxRetries != 5 {
		t.Errorf("expected 5, got %d", cfg.QoSMaxRetries)
	}
}

func TestFileOverridesEnv(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	content := `listen_addr: ":7777"`
	_ = os.WriteFile(cfgPath, []byte(content), 0644)

	_ = os.Setenv("MQTT_LISTEN_ADDR", ":1234")
	defer os.Unsetenv("MQTT_LISTEN_ADDR")

	loader := NewLoader(cfgPath)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// File should override env
	if cfg.ListenAddr != ":7777" {
		t.Errorf("expected :7777 (file), got %s", cfg.ListenAddr)
	}
}

func TestDurationEnv(t *testing.T) {
	_ = os.Setenv("MQTT_CONNECT_TIMEOUT", "30s")
	_ = os.Setenv("MQTT_QOS_RETRY_INTERVAL", "5m")
	defer func() {
		_ = os.Unsetenv("MQTT_CONNECT_TIMEOUT")
		_ = os.Unsetenv("MQTT_QOS_RETRY_INTERVAL")
	}()

	loader := NewLoader("")
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ConnectTimeout != 30*time.Second {
		t.Errorf("expected 30s, got %v", cfg.ConnectTimeout)
	}
	if cfg.QoSRetryInterval != 5*time.Minute {
		t.Errorf("expected 5m, got %v", cfg.QoSRetryInterval)
	}
}

func TestInvalidDuration(t *testing.T) {
	_ = os.Setenv("MQTT_CONNECT_TIMEOUT", "notaduration")
	defer os.Unsetenv("MQTT_CONNECT_TIMEOUT")

	loader := NewLoader("")
	_, err := loader.Load()
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestValidate_Valid(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error for default config, got %v", err)
	}
}

func TestValidate_InvalidMaxPacketSize(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxPacketSize = 0
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for MaxPacketSize=0")
	}
	cfg.MaxPacketSize = -1
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for MaxPacketSize=-1")
	}
}

func TestValidate_InvalidMaxConnections(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxConnections = -1
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for MaxConnections=-1")
	}
}

func TestValidate_InvalidQoSMaxRetries(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QoSMaxRetries = -1
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for QoSMaxRetries=-1")
	}
}

func TestValidate_InvalidQoSMaxInflight(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QoSMaxInflight = 0
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for QoSMaxInflight=0")
	}
	cfg.QoSMaxInflight = -1
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for QoSMaxInflight=-1")
	}
}

func TestValidate_InvalidQoSRetryInterval(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QoSRetryInterval = 0
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for QoSRetryInterval=0")
	}
	cfg.QoSRetryInterval = -1
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for QoSRetryInterval=-1")
	}
}

func TestValidate_TLSMissingCert(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TLSEnabled = true
	cfg.TLSCertFile = ""
	cfg.TLSKeyFile = "key.pem"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing TLS cert")
	}
}

func TestValidate_TLSMissingKey(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TLSEnabled = true
	cfg.TLSCertFile = "cert.pem"
	cfg.TLSKeyFile = ""
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for missing TLS key")
	}
}

func TestValidate_UnknownStorageBackend(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StorageBackend = "mongodb"
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for unknown storage backend")
	}
}

func TestValidate_RedisWithoutAddr(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StorageBackend = "redis"
	cfg.RedisAddr = ""
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for redis backend without redis_addr")
	}
}

func TestValidate_EmptyStorageBackend(t *testing.T) {
	cfg := DefaultConfig()
	cfg.StorageBackend = ""
	if err := cfg.Validate(); err != nil {
		t.Errorf("empty storage_backend should be accepted (defaults to memory), got %v", err)
	}
}
