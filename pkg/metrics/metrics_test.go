package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestDefaultMetrics(t *testing.T) {
	m := Default()
	if m == nil {
		t.Fatal("Default() returned nil")
	}

	// Verify no panics on any interface method
	m.IncConnections()
	m.OnDisconnect()
	m.IncRejections("test")
	m.IncAuthFailures()
	m.IncMessagesPublished(1)
	m.IncMessagesDelivered(0)
	m.IncMessagesDropped("timeout")
	m.IncInflight("client1")
	m.DecInflight("client1")
	m.DecInflightBatch("client1", 5)
	m.IncInflightDropped("client1")
	m.IncRetries("client1")
	m.SetOnlineSessions(10)
	m.SetOfflineSessions(5)
	m.SetRetainedMessages(3)
	m.SetSubscriptions(2)
	m.IncErrors("broker")
	m.ObserveMessageLatency(0.001, 1)
}

func TestNoopMetricsImplementsInterface(t *testing.T) {
	var _ Metrics = &noopMetrics{}
}

func TestNoopMetricsDoesNotPanic(t *testing.T) {
	m := &noopMetrics{}

	m.IncConnections()
	m.OnDisconnect()
	m.IncRejections("test")
	m.IncAuthFailures()
	m.IncMessagesPublished(0)
	m.IncMessagesDelivered(1)
	m.IncMessagesDropped("test")
	m.IncInflight("c1")
	m.DecInflight("c1")
	m.DecInflightBatch("c1", 10)
	m.IncInflightDropped("c1")
	m.IncRetries("c1")
	m.SetOnlineSessions(0)
	m.SetOfflineSessions(0)
	m.SetRetainedMessages(0)
	m.SetSubscriptions(0)
	m.IncErrors("test")
	m.ObserveMessageLatency(0.5, 2)
}

func TestPrometheusMetricsImplementsInterface(t *testing.T) {
	var _ Metrics = &prometheusMetrics{}
	var _ HTTPHandler = &prometheusMetrics{}
}

func TestPrometheusMetrics_AllMethods(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewPrometheusMetrics(reg)
	if m == nil {
		t.Fatal("NewPrometheusMetrics returned nil")
	}

	// Verify no panics and counters/gauge increment correctly
	t.Run("connections", func(t *testing.T) {
		m.IncConnections()
		m.OnDisconnect()
		m.IncConnections()
		m.IncConnections()
		if got := testutil.ToFloat64(m.(*prometheusMetrics).connections); got != 3 {
			t.Errorf("connections_total = %f, want 3", got)
		}
		if got := testutil.ToFloat64(m.(*prometheusMetrics).disconnections); got != 1 {
			t.Errorf("disconnections_total = %f, want 1", got)
		}
	})

	t.Run("rejections", func(t *testing.T) {
		m.IncRejections("full")
		m.IncRejections("full")
		m.IncRejections("auth")
		if got := testutil.ToFloat64(m.(*prometheusMetrics).rejections.WithLabelValues("full")); got != 2 {
			t.Errorf("rejections[full] = %f, want 2", got)
		}
	})

	t.Run("auth failures", func(t *testing.T) {
		m.IncAuthFailures()
		m.IncAuthFailures()
		m.IncAuthFailures()
		if got := testutil.ToFloat64(m.(*prometheusMetrics).authFailures); got != 3 {
			t.Errorf("auth_failures_total = %f, want 3", got)
		}
	})

	t.Run("messages published", func(t *testing.T) {
		m.IncMessagesPublished(0)
		m.IncMessagesPublished(1)
		m.IncMessagesPublished(1)
		if got := testutil.ToFloat64(m.(*prometheusMetrics).messagesPublished.WithLabelValues("0")); got != 1 {
			t.Errorf("messages_published[qos=0] = %f, want 1", got)
		}
		if got := testutil.ToFloat64(m.(*prometheusMetrics).messagesPublished.WithLabelValues("1")); got != 2 {
			t.Errorf("messages_published[qos=1] = %f, want 2", got)
		}
	})

	t.Run("messages delivered", func(t *testing.T) {
		m.IncMessagesDelivered(0)
		if got := testutil.ToFloat64(m.(*prometheusMetrics).messagesDelivered.WithLabelValues("0")); got != 1 {
			t.Errorf("messages_delivered[qos=0] = %f, want 1", got)
		}
	})

	t.Run("messages dropped", func(t *testing.T) {
		m.IncMessagesDropped("expired")
		m.IncMessagesDropped("expired")
		m.IncMessagesDropped("queue_full")
		if got := testutil.ToFloat64(m.(*prometheusMetrics).messagesDropped.WithLabelValues("expired")); got != 2 {
			t.Errorf("messages_dropped[expired] = %f, want 2", got)
		}
	})

	t.Run("inflight", func(t *testing.T) {
		m.IncInflight("c1")
		m.IncInflight("c2")
		m.IncInflight("c3")
		m.DecInflight("c1")
		m.DecInflightBatch("c2", 1)
		if got := testutil.ToFloat64(m.(*prometheusMetrics).inflight); got != 1 {
			t.Errorf("inflight = %f, want 1 (inc 3, dec 1, decbatch 1)", got)
		}
	})

	t.Run("inflight dropped", func(t *testing.T) {
		m.IncInflightDropped("c1")
		m.IncInflightDropped("c1")
		if got := testutil.ToFloat64(m.(*prometheusMetrics).inflightDropped); got != 2 {
			t.Errorf("inflight_dropped = %f, want 2", got)
		}
	})

	t.Run("retries", func(t *testing.T) {
		m.IncRetries("c1")
		if got := testutil.ToFloat64(m.(*prometheusMetrics).retries); got != 1 {
			t.Errorf("retries = %f, want 1", got)
		}
	})

	t.Run("sessions", func(t *testing.T) {
		m.SetOnlineSessions(42)
		m.SetOfflineSessions(7)
		m.SetRetainedMessages(100)
		m.SetSubscriptions(500)
		if got := testutil.ToFloat64(m.(*prometheusMetrics).onlineSessions); got != 42 {
			t.Errorf("online_sessions = %f, want 42", got)
		}
		if got := testutil.ToFloat64(m.(*prometheusMetrics).offlineSessions); got != 7 {
			t.Errorf("offline_sessions = %f, want 7", got)
		}
		if got := testutil.ToFloat64(m.(*prometheusMetrics).retainedMsgs); got != 100 {
			t.Errorf("retained_messages = %f, want 100", got)
		}
		if got := testutil.ToFloat64(m.(*prometheusMetrics).subscriptions); got != 500 {
			t.Errorf("subscriptions = %f, want 500", got)
		}
	})

	t.Run("errors", func(t *testing.T) {
		m.IncErrors("broker")
		m.IncErrors("broker")
		m.IncErrors("store")
		if got := testutil.ToFloat64(m.(*prometheusMetrics).errors.WithLabelValues("broker")); got != 2 {
			t.Errorf("errors[broker] = %f, want 2", got)
		}
	})

	t.Run("latency", func(t *testing.T) {
		m.ObserveMessageLatency(0.001, 0)
		m.ObserveMessageLatency(0.002, 1)
		m.ObserveMessageLatency(0.003, 2)
		// QoS beyond 2 maps to "unknown"
		m.ObserveMessageLatency(0.004, 99)
	})
}

func TestPrometheusMetrics_Handler(t *testing.T) {
	pm := &prometheusMetrics{handler: nil}
	// Handler should return nil when not initialized via NewPrometheusMetrics
	_ = pm.Handler()
}

func TestNewPrometheusMetrics_RegisterReuse(t *testing.T) {
	// Calling NewPrometheusMetrics twice with same registry should not panic
	reg := prometheus.NewRegistry()
	m1 := NewPrometheusMetrics(reg)
	if m1 == nil {
		t.Fatal("first NewPrometheusMetrics returned nil")
	}

	m2 := NewPrometheusMetrics(reg)
	if m2 == nil {
		t.Fatal("second NewPrometheusMetrics returned nil")
	}

	// Both should work
	m1.IncConnections()
	m2.IncConnections()
	if got := testutil.ToFloat64(m2.(*prometheusMetrics).connections); got != 2 {
		t.Errorf("connections after two InCs = %f, want 2", got)
	}
}

func TestPrometheusMetrics_NilRegistry(t *testing.T) {
	// nil registry should fall back to DefaultRegisterer (which might panic in parallel tests
	// due to AlreadyRegisteredError on duplicate metric registration, but our registerOrReuse
	// handles that). Just verify no panic on creation.
	m := NewPrometheusMetrics(nil)
	if m == nil {
		t.Fatal("NewPrometheusMetrics(nil) returned nil")
	}
}

func TestQoSLabel(t *testing.T) {
	tests := []struct {
		qos  uint8
		want string
	}{
		{0, "0"},
		{1, "1"},
		{2, "2"},
		{3, "unknown"},
		{255, "unknown"},
	}
	for _, tt := range tests {
		if got := qosLabel(tt.qos); got != tt.want {
			t.Errorf("qosLabel(%d) = %q, want %q", tt.qos, got, tt.want)
		}
	}
}

func TestPrometheusMetrics_SubPackageMethod(t *testing.T) {
	// Validate that IncMessagesPublished/IncMessagesDelivered with a
	// QoS value of 3+ doesn't create unbounded label dimensions.
	reg := prometheus.NewRegistry()
	m := NewPrometheusMetrics(reg)

	m.IncMessagesPublished(3)
	m.IncMessagesDelivered(200)

	// Should all map to "unknown" label, not create new label values per QoS.
	if got := testutil.ToFloat64(m.(*prometheusMetrics).messagesPublished.WithLabelValues("unknown")); got != 1 {
		t.Errorf("messages_published[unknown] = %f, want 1", got)
	}
	if got := testutil.ToFloat64(m.(*prometheusMetrics).messagesDelivered.WithLabelValues("unknown")); got != 1 {
		t.Errorf("messages_delivered[unknown] = %f, want 1", got)
	}
}

func BenchmarkPrometheusMetrics(b *testing.B) {
	m := NewPrometheusMetrics(prometheus.NewRegistry())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.IncConnections()
		m.IncMessagesPublished(1)
		m.IncMessagesDropped("timeout")
		m.IncInflight("c1")
		m.DecInflight("c1")
		m.SetOnlineSessions(i)
		m.IncErrors("broker")
		m.ObserveMessageLatency(0.001, 0)
	}
}
