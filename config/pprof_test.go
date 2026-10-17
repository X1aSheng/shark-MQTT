package config

import (
	"strings"
	"testing"
)

// TestValidateGatesPprofOnLoopback pins the safety rule for the profiling
// endpoints.
//
// A heap profile is a dump of everything the process holds: message payloads,
// usernames, whatever a client happened to send. Exposing it on a routable
// interface is a data leak with a debugger's name, so the configuration is
// rejected rather than warned about.
func TestValidateGatesPprofOnLoopback(t *testing.T) {
	c := DefaultConfig()
	c.PprofEnabled = true

	c.MetricsAddr = "0.0.0.0:18999"
	err := c.Validate()
	if err == nil {
		t.Fatal("pprof_enabled on 0.0.0.0 was accepted, want rejection")
	}
	if !strings.Contains(err.Error(), "pprof_enabled") {
		t.Fatalf("error does not name the setting: %v", err)
	}

	for _, addr := range []string{"127.0.0.1:18999", "localhost:18999", "[::1]:18999"} {
		c.MetricsAddr = addr
		if err := c.Validate(); err != nil {
			t.Errorf("pprof_enabled on %s was rejected: %v", addr, err)
		}
	}

	// Off is the default and must stay usable on any address.
	c.PprofEnabled = false
	c.MetricsAddr = "0.0.0.0:18999"
	if err := c.Validate(); err != nil {
		t.Errorf("pprof disabled on 0.0.0.0 was rejected: %v", err)
	}
}
