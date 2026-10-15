package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that accepts BOTH a duration string ("30s", "24h")
// and a bare number of seconds.
//
// Why this exists: the environment-variable path has always accepted both
// (loader.go feeds the value through time.ParseDuration after treating a bare
// integer as seconds), while the YAML path went straight to yaml.Unmarshal and
// therefore REJECTED a bare integer:
//
//	yaml: unmarshal errors: line 11: cannot unmarshal !!int `86400` into time.Duration
//
// The same key was valid in one source and invalid in the other. That is a
// configuration trap: an operator moving a value between config file and
// deployment manifest hits an error that names neither the key nor the fix.
// Kubernetes ConfigMaps in particular render durations as bare numbers by
// habit, so the YAML path is the one more likely to receive them.
//
// A bare number is interpreted as seconds, matching the env-var behaviour.
type Duration time.Duration

// ParseDuration converts s into a Duration. A bare integer means seconds.
func ParseDuration(s string) (Duration, error) {
	s = trimSpace(s)
	if s == "" {
		return 0, nil
	}
	if isBareNumber(s) {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", s, err)
		}
		return Duration(time.Duration(n) * time.Second), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q (use e.g. 30s, 24h, or a bare number of seconds): %w", s, err)
	}
	return Duration(d), nil
}

func (d Duration) String() string { return time.Duration(d).String() }

// Duration returns the standard library value.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// UnmarshalYAML accepts a duration string, a bare number (seconds), or null.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Tag == "!!null" {
		*d = 0
		return nil
	}
	parsed, err := ParseDuration(value.Value)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// MarshalYAML always writes a string, so a round trip stays human-readable and
// never degrades into a bare number that another tool would misread.
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// UnmarshalJSON mirrors UnmarshalYAML so a JSON config behaves the same way.
func (d *Duration) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*d = 0
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		parsed, err := ParseDuration(s)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err == nil {
		*d = Duration(time.Duration(n) * time.Second)
		return nil
	}
	return fmt.Errorf("invalid duration: %s", string(b))
}

// MarshalJSON writes a string.
func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

func isBareNumber(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '-' || s[0] == '+' {
		i = 1
	}
	if i >= len(s) {
		return false
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
