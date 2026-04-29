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
}

// Subscription represents a topic subscription.
type Subscription struct {
	Topic string
	QoS   uint8
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
}

// RetainedMessage represents a retained message.
type RetainedMessage struct {
	Topic     string
	QoS       uint8
	Payload   []byte
	Timestamp time.Time
}
