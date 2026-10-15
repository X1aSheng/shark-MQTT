package config

import (
	"encoding/json"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestDuration_ParseAcceptsBothForms pins the whole reason this type exists: a
// bare number means seconds, a string means a normal Go duration. Before it,
// the env-var path accepted both while the YAML path accepted only strings, so
// the same key was valid in one configuration source and not the other.
func TestDuration_ParseAcceptsBothForms(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"30s", 30 * time.Second},
		{"24h", 24 * time.Hour},
		{"1h30m", 90 * time.Minute},
		{"500ms", 500 * time.Millisecond},
		{"86400", 24 * time.Hour}, // bare number = seconds
		{"0", 0},
		{"-5", -5 * time.Second},
		{"", 0},
		{"  10s  ", 10 * time.Second}, // surrounding space tolerated
	}
	for _, tc := range cases {
		got, err := ParseDuration(tc.in)
		if err != nil {
			t.Errorf("ParseDuration(%q) = error %v", tc.in, err)
			continue
		}
		if got.Duration() != tc.want {
			t.Errorf("ParseDuration(%q) = %v, want %v", tc.in, got.Duration(), tc.want)
		}
	}
}

func TestDuration_ParseRejectsGarbage(t *testing.T) {
	for _, in := range []string{"soon", "24 hours", "1h30", "10 s", "--5"} {
		if _, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) = nil error, want a failure", in)
		}
	}
}

// TestDuration_YAMLAcceptsBareNumber is the regression that motivated the type:
// yaml.Unmarshal could not decode an int into time.Duration, and a Helm
// ConfigMap renders durations as bare numbers.
func TestDuration_YAMLAcceptsBareNumber(t *testing.T) {
	var cfg Config
	err := yaml.Unmarshal([]byte("session_expiry_interval: 86400\n"), &cfg)
	if err != nil {
		t.Fatalf("unmarshal bare number: %v", err)
	}
	if got := cfg.SessionExpiryInterval.Duration(); got != 24*time.Hour {
		t.Errorf("SessionExpiryInterval = %v, want 24h", got)
	}
}

func TestDuration_YAMLAcceptsString(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal([]byte("session_expiry_interval: 30m\n"), &cfg); err != nil {
		t.Fatalf("unmarshal string: %v", err)
	}
	if got := cfg.SessionExpiryInterval.Duration(); got != 30*time.Minute {
		t.Errorf("SessionExpiryInterval = %v, want 30m", got)
	}
}

func TestDuration_YAMLNullIsZero(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal([]byte("session_expiry_interval: null\n"), &cfg); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if cfg.SessionExpiryInterval != 0 {
		t.Errorf("SessionExpiryInterval = %v, want 0", cfg.SessionExpiryInterval)
	}
}

// TestDuration_MarshalStaysAString guards the round trip: a value written back
// out must stay readable as a duration, not degrade into a bare number that
// another tool would misread as nanoseconds.
func TestDuration_MarshalStaysAString(t *testing.T) {
	d, err := ParseDuration("86400")
	if err != nil {
		t.Fatal(err)
	}
	out, err := yaml.Marshal(struct {
		V Duration `yaml:"v"`
	}{V: d})
	if err != nil {
		t.Fatal(err)
	}
	if want := "v: 24h0m0s\n"; string(out) != want {
		t.Errorf("marshal = %q, want %q", string(out), want)
	}
}

func TestDuration_JSONMirrorsYAML(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"SessionExpiryInterval": 90}`), &cfg); err != nil {
		t.Fatalf("json bare number: %v", err)
	}
	if got := cfg.SessionExpiryInterval.Duration(); got != 90*time.Second {
		t.Errorf("from JSON number = %v, want 90s", got)
	}

	var cfg2 Config
	if err := json.Unmarshal([]byte(`{"SessionExpiryInterval": "2h"}`), &cfg2); err != nil {
		t.Fatalf("json string: %v", err)
	}
	if got := cfg2.SessionExpiryInterval.Duration(); got != 2*time.Hour {
		t.Errorf("from JSON string = %v, want 2h", got)
	}
}
