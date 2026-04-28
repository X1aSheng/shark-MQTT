package metrics

import (
	"testing"
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
}
