// Package metrics provides MQTT-specific metrics collection interfaces.
package metrics

// Metrics defines the interface for MQTT metrics collection.
type Metrics interface {
	// Connection metrics
	IncConnections()
	DecConnections()
	IncRejections(reason string)
	IncAuthFailures()

	// Message metrics
	IncMessagesPublished(topic string, qos uint8)
	IncMessagesDelivered(clientID string, qos uint8)
	IncMessagesDropped(reason string)

	// QoS metrics
	IncInflight(clientID string)
	DecInflight(clientID string)
	DecInflightBatch(clientID string, count int)
	IncInflightDropped(clientID string)
	IncRetries(clientID string)

	// Session metrics
	SetOnlineSessions(count int)
	SetOfflineSessions(count int)
	SetRetainedMessages(count int)
	SetSubscriptions(count int)

	// System metrics
	IncErrors(component string)
}

// Default returns the default metrics implementation (no-op).
func Default() Metrics {
	return &noopMetrics{}
}
