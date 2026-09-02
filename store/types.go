// Package store provides storage interfaces and implementations for shark-mqtt.
package store

import (
	"time"
)

// SessionData represents stored session data.
type SessionData struct {
	ClientID       string
	Username       string
	IsClean        bool
	KeepAlive      uint16
	ProtocolVer    uint8
	ExpiryInterval uint32
	ExpiryTime     time.Time
	Subscriptions  []Subscription
	Inflight       map[uint16]*InflightMessage
	// InboundQoS2 holds QoS 2 publishes accepted from the client (PUBREC
	// sent) whose PUBREL is still pending. Persisted so a reconnect after a
	// crash can still route the message and complete the handshake (audit H4).
	InboundQoS2 map[uint16]*InflightMessage
}

// Subscription represents a topic subscription.
type Subscription struct {
	Topic             string
	QoS               uint8
	NoLocal           bool
	RetainAsPublished bool
	RetainHandling    uint8
	// SubscriptionIdentifier persists the MQTT 5 subscription identifier so
	// restored sessions keep echoing it in delivered PUBLISH packets (audit:
	// it was lost on reconnect, breaking request/response correlation).
	SubscriptionIdentifier *uint32
}

// InflightMessage represents a message awaiting acknowledgment.
type InflightMessage struct {
	PacketID uint16
	QoS      uint8
	Topic    string
	Payload  []byte
	Retain   bool
}

// StoredMessage represents a message stored for QoS delivery.
type StoredMessage struct {
	ID        string
	Topic     string
	QoS       uint8
	Payload   []byte
	Retain    bool
	Timestamp time.Time
	ExpiresAt time.Time // absolute Message Expiry Interval deadline; zero = none (MQTT 5.0 §3.3.2.3.2)
}

// RetainedMessage represents a retained message.
type RetainedMessage struct {
	Topic     string
	QoS       uint8
	Payload   []byte
	Timestamp time.Time
}
