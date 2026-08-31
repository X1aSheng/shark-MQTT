// Package metrics provides MQTT-specific metrics collection interfaces.
package metrics

// Metrics defines the interface for MQTT metrics collection.
type Metrics interface {
	// Connection metrics
	IncConnections()
	OnDisconnect()
	IncRejections(reason string)
	IncAuthFailures()

	// Message metrics — only bounded label dimensions (qos: 0-2).
	IncMessagesPublished(qos uint8)
	IncMessagesDelivered(qos uint8)
	IncMessagesDropped(reason string)

	// Session metrics
	SetOnlineSessions(count int)
	SetRetainedMessages(count int)
	SetSubscriptions(count int)

	// System metrics
	IncErrors(component string)

	// Latency metrics
	ObserveMessageLatency(seconds float64, qos uint8)
}

// Default returns the default metrics implementation (no-op).
func Default() Metrics {
	return &noopMetrics{}
}
