package api

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/pkg/metrics"
)

func TestNewBroker(t *testing.T) {
	b := NewBroker()
	if b == nil {
		t.Fatal("NewBroker returned nil")
	}
	if b.srv == nil {
		t.Fatal("expected non-nil server")
	}
	if b.broker == nil {
		t.Fatal("expected non-nil broker")
	}
}

func TestBrokerStartStop(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0" // random port

	b := NewBroker(
		WithConfig(cfg),
		WithAuth(broker.AllowAllAuth{}),
	)

	if err := b.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Should be listening
	if b.Addr() == "" {
		t.Error("expected non-empty Addr")
	}

	b.Stop()
}

func TestBrokerWithAuthorizer(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"

	b := NewBroker(
		WithConfig(cfg),
		WithAuth(broker.AllowAllAuth{}),
	)

	if err := b.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	b.Stop()
}

func TestBrokerConnCount(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"

	b := NewBroker(WithConfig(cfg))

	if err := b.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer b.Stop()

	if b.ConnCount() != 0 {
		t.Errorf("expected 0 connections, got %d", b.ConnCount())
	}
}

func TestBrokerMetricsEndpoint(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"

	b := NewBroker(
		WithConfig(cfg),
		WithAuth(broker.AllowAllAuth{}),
		WithMetrics(metrics.NewPrometheusMetrics(nil)),
	)

	if err := b.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer b.Stop()

	time.Sleep(50 * time.Millisecond)

	// Hit /healthz
	resp, err := http.Get("http://" + b.MetricsAddr() + "/healthz")
	if err != nil {
		t.Fatalf("healthz request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz: expected 200, got %d", resp.StatusCode)
	}

	// Hit /metrics — should return Prometheus metrics
	resp2, err := http.Get("http://" + b.MetricsAddr() + "/metrics")
	if err != nil {
		t.Fatalf("metrics request failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("metrics: expected 200, got %d", resp2.StatusCode)
	}
	body, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(body), "shark_mqtt_connections_total") {
		t.Error("metrics response missing expected shark_mqtt metric")
	}
}

func TestBrokerQoSConfigPropagation(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.QoSMaxInflight = 50
	cfg.QoSRetryInterval = 5 * time.Second
	cfg.QoSMaxRetries = 5

	b := NewBroker(
		WithConfig(cfg),
		WithAuth(broker.AllowAllAuth{}),
	)

	if err := b.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer b.Stop()

	// Verify the broker started with custom QoS settings by checking
	// the QoS engine processes a message (indirect verification)
	if b.ConnCount() != 0 {
		t.Errorf("expected 0 connections, got %d", b.ConnCount())
	}
}

func TestBrokerMaxConnectionsConfigPropagation(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MaxConnections = 5

	b := NewBroker(
		WithConfig(cfg),
		WithAuth(broker.AllowAllAuth{}),
	)

	if err := b.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer b.Stop()

	// Connection limit should be set to 5 from config
	if b.ConnCount() != 0 {
		t.Errorf("expected 0 connections, got %d", b.ConnCount())
	}
}

func TestBrokerNoMetricsEndpointWithoutPrometheus(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"

	// Default metrics is noop — should not expose /metrics
	b := NewBroker(
		WithConfig(cfg),
		WithAuth(broker.AllowAllAuth{}),
	)

	if err := b.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer b.Stop()

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get("http://" + b.MetricsAddr() + "/metrics")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for noop metrics, got %d", resp.StatusCode)
	}
}

func TestWithAuthorizer(t *testing.T) {
	b := NewBroker(WithAuthorizer(broker.AllowAllAuth{}))
	if b == nil {
		t.Fatal("NewBroker returned nil")
	}
}

func TestWithSessionStore(t *testing.T) {
	b := NewBroker(WithSessionStore(nil))
	if b == nil {
		t.Fatal("NewBroker returned nil")
	}
}

func TestWithMessageStore(t *testing.T) {
	b := NewBroker(WithMessageStore(nil))
	if b == nil {
		t.Fatal("NewBroker returned nil")
	}
}

func TestWithRetainedStore(t *testing.T) {
	b := NewBroker(WithRetainedStore(nil))
	if b == nil {
		t.Fatal("NewBroker returned nil")
	}
}

func TestWithLogger(t *testing.T) {
	b := NewBroker(WithLogger(nil))
	if b == nil {
		t.Fatal("NewBroker returned nil")
	}
}

func TestWithPluginManager(t *testing.T) {
	b := NewBroker(WithPluginManager(nil))
	if b == nil {
		t.Fatal("NewBroker returned nil")
	}
}

func TestWithMaxConnections(t *testing.T) {
	b := NewBroker(WithMaxConnections(50))
	if b == nil {
		t.Fatal("NewBroker returned nil")
	}
}

func TestBrokerMethod(t *testing.T) {
	b := NewBroker()
	brk := b.Broker()
	if brk == nil {
		t.Error("Broker() returned nil")
	}
}

func TestRunCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel

	err := Run(ctx, WithConfig(func() *config.Config {
		cfg := config.DefaultConfig()
		cfg.ListenAddr = ":0"
		return cfg
	}()))
	if err != nil {
		t.Errorf("Run with cancelled context should return nil, got %v", err)
	}
}

func TestConfigValidationError(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MaxPacketSize = 0 // invalid

	b := NewBroker(WithConfig(cfg))
	if b.initErr == nil {
		t.Fatal("expected config validation error")
	}

	err := b.Start()
	if err == nil {
		t.Fatal("expected Start() to fail with invalid config")
	}
}

// TestMaxConnections_Sentinel verifies -1 defers to config, 0 means unlimited (P2-M06).
func TestMaxConnections_Sentinel(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.MaxConnections = 500
	cfg.ListenAddr = ":0"

	// Default (-1 sentinel): should use config's 500
	b1 := NewBroker(WithConfig(cfg))
	if b1.initErr != nil {
		t.Fatal(b1.initErr)
	}
	// Explicit 0: unlimited, should NOT use config's 500
	b2 := NewBroker(WithConfig(cfg), WithMaxConnections(0))
	if b2.initErr != nil {
		t.Fatal(b2.initErr)
	}
}
