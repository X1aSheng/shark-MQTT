package api

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
)

// freeTCPAddr asks the OS for a port and immediately releases it, so a test can
// hand a concrete address to the broker instead of ":0". The interesting
// failures below are "the port was never released", which ":0" would hide.
func freeTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("probe close: %v", err)
	}
	return addr
}

// getOK performs a GET and returns the status code, closing the body.
func getOK(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// TestHealthServerRestartReleasesAddr is the regression guard for the
// Stop-before-Serve leak. http.Server.Shutdown only closes the listeners that
// Serve has already registered, so a Stop that lands before the serving
// goroutine reached Serve used to leave the metrics port bound forever: the
// next Start could not rebind it, and the health/metrics endpoints were
// silently lost after a restart.
//
// Stop is called immediately after Start on purpose -- that is exactly the
// window in question -- and the cycle is repeated so that the Stop-first
// ordering is certainly exercised. Re-listening on the same address is a
// deterministic check: if the old listener leaked, this cannot succeed.
func TestHealthServerRestartReleasesAddr(t *testing.T) {
	addr := freeTCPAddr(t)

	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = addr

	b := NewBroker(
		WithConfig(cfg),
		WithAuth(broker.AllowAllAuth{}),
	)

	for cycle := 0; cycle < 10; cycle++ {
		if err := b.Start(); err != nil {
			t.Fatalf("cycle %d: Start() error = %v", cycle, err)
		}
		if got := b.MetricsAddr(); got != addr {
			t.Fatalf("cycle %d: MetricsAddr() = %q, want %q", cycle, got, addr)
		}
		if code := getOK(t, fmt.Sprintf("http://%s/healthz", addr)); code != http.StatusOK {
			t.Fatalf("cycle %d: /healthz = %d, want %d", cycle, code, http.StatusOK)
		}

		b.Stop()

		if got := b.MetricsAddr(); got != "" {
			t.Fatalf("cycle %d: MetricsAddr() after Stop = %q, want empty (the listener is gone)", cycle, got)
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatalf("cycle %d: metrics port %s still bound after Stop: %v", cycle, addr, err)
		}
		_ = ln.Close()
	}
}

// TestHealthServerStopIsIdempotent: a caller's deferred Stop can follow an
// explicit one, and the teardown must not trip over the already-claimed
// handles or try to shut the health server down twice.
func TestHealthServerStopIsIdempotent(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"

	b := NewBroker(
		WithConfig(cfg),
		WithAuth(broker.AllowAllAuth{}),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if b.MetricsAddr() == "" {
		t.Fatal("expected a metrics address while running")
	}

	b.Stop()
	b.Stop()

	if got := b.MetricsAddr(); got != "" {
		t.Errorf("MetricsAddr() after double Stop = %q, want empty", got)
	}
}

// TestHealthServerNotStartedHasNoAddr: MetricsAddr must not advertise an
// address before Start, otherwise a probe would dial a port nobody listens on.
func TestHealthServerNotStartedHasNoAddr(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"

	b := NewBroker(WithConfig(cfg), WithAuth(broker.AllowAllAuth{}))
	if got := b.MetricsAddr(); got != "" {
		t.Errorf("MetricsAddr() before Start = %q, want empty", got)
	}
}
