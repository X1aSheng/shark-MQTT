//go:build !nometrics

package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/pkg/metrics"
)

// TestBrokerMetricsEndpoint verifies the /metrics endpoint is served when a
// Prometheus metrics implementation is explicitly wired.
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

// TestBrokerMetricsEndpointByDefault verifies the default build serves
// Prometheus metrics out of the box (NEW-20). Excluded from the `nometrics`
// build, where the default metrics is a no-op (R4).
func TestBrokerMetricsEndpointByDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"

	// Default metrics is Prometheus — /metrics is served out of the box (NEW-20).
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
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for default Prometheus metrics, got %d", resp.StatusCode)
	}
}

// TestBrokerMetricsDisabledByConfig verifies metrics_enabled actually gates
// the /metrics endpoint (audit: the switch was a no-op and /metrics was
// always exposed), while /healthz and /readyz stay available for probes.
func TestBrokerMetricsDisabledByConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	cfg.MetricsEnabled = false

	b := NewBroker(
		WithConfig(cfg),
		WithAuth(broker.AllowAllAuth{}),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer b.Stop()

	time.Sleep(50 * time.Millisecond)

	resp, err := http.Get("http://" + b.MetricsAddr() + "/healthz")
	if err != nil {
		t.Fatalf("healthz request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz: expected 200, got %d", resp.StatusCode)
	}

	resp2, err := http.Get("http://" + b.MetricsAddr() + "/metrics")
	if err != nil {
		t.Fatalf("metrics request failed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("metrics with MetricsEnabled=false: expected 404, got %d", resp2.StatusCode)
	}
}
