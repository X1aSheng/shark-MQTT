package broker

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The Prometheus counters below take their label value from a string argument.
// A label value that varies with client input (a client ID, a topic, a
// username) mints a new time series per distinct value and can take the
// scrape endpoint down. Production code therefore may only use the fixed
// tokens listed here; this test makes adding one a deliberate edit instead of
// an accident.
var allowedMetricLabels = map[string]map[string]bool{
	"IncRejections": {
		"client_id_too_long":    true,
		"decode_error":          true,
		"invalid_connect":       true,
		"invalid_packet":        true,
		"keepalive_timeout":     true,
		"max_connections":       true,
		"rate_limited":          true,
		"session_user_mismatch": true,
	},
	"IncMessagesDropped": {
		"invalid_topic":                 true,
		"invalid_topic_alias":           true,
		"message_expired":               true,
		"offline_queue_full":            true,
		"offline_stale":                 true,
		"packet_too_large":              true,
		"pubrec_rejected":               true,
		"qos2_pid_reuse":                true,
		"rate_limited":                  true,
		"receive_max_buffered_overflow": true,
		"retained_limit":                true,
		"unknown_topic_alias":           true,
		"write_queue_full":              true,
	},
	"IncErrors": {
		"deadline":       true,
		"decode":         true,
		"flow_control":   true,
		"message_store":  true,
		"protocol":       true,
		"rate_limit":     true,
		"retained_store": true,
		"session_save":   true,
		"will":           true,
	},
}

// metricLabelCall matches a literal label argument passed to one of the
// string-labelled counters, e.g. `b.metrics.IncRejections("rate_limited")`.
var metricLabelCall = regexp.MustCompile(`metrics\.(IncRejections|IncMessagesDropped|IncErrors)\("([^"]*)"\)`)

// stableLabelToken rejects anything that could be user-derived: label values
// must be a short lowercase token, never a topic path, client ID or username.
var stableLabelToken = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// TestMetricLabelValuesAreBounded scans the package's production sources and
// checks that every string label handed to the metrics layer is a fixed,
// allow-listed token.
func TestMetricLabelValuesAreBounded(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]map[string]bool{}
	seenIn := map[string]string{}

	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range metricLabelCall.FindAllStringSubmatch(string(raw), -1) {
			fn, val := m[1], m[2]
			if seen[fn] == nil {
				seen[fn] = map[string]bool{}
			}
			seen[fn][val] = true
			seenIn[fn+":"+val] = f
		}
	}

	if len(seen) == 0 {
		t.Fatal("found no metrics label call sites; the scan pattern is stale")
	}

	for fn, vals := range seen {
		allowed := allowedMetricLabels[fn]
		if allowed == nil {
			t.Errorf("no allowlist for metrics.%s; add one before using it", fn)
			continue
		}
		for val := range vals {
			if !stableLabelToken.MatchString(val) {
				t.Errorf("metrics.%s(%q) in %s: label value is not a stable token "+
					"(must match %s); user-derived values explode cardinality",
					fn, val, seenIn[fn+":"+val], stableLabelToken)
			}
			if !allowed[val] {
				t.Errorf("metrics.%s(%q) in %s: label value is not allow-listed; "+
					"add it to allowedMetricLabels only after confirming it is a "+
					"fixed, bounded set", fn, val, seenIn[fn+":"+val])
			}
		}
	}
}
