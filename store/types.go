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
//
// It carries the MQTT 5 relay data as well as the message itself: a session
// restored from a store must be able to retransmit the same PUBLISH it would
// have sent before the restart, and §3.3.2.3 requires the forwarded Response
// Topic, Correlation Data, User Properties, Content Type and Payload Format
// Indicator to survive that. ExpiresAt keeps §3.3.2.3.3 enforceable across the
// restart: without it a restored message would be retried even after its
// Message Expiry Interval had passed.
type InflightMessage struct {
	PacketID uint16
	QoS      uint8
	Topic    string
	Payload  []byte
	Retain   bool

	// Properties is the MQTT 5 per-message relay data. Zero value for a
	// 3.1/3.1.1 session.
	Properties MessageProperties
	// ExpiresAt is the absolute Message Expiry deadline; zero means none.
	ExpiresAt time.Time

	// PendingPubRel records how far an outbound QoS 2 delivery got before the
	// session was torn down: true means the peer answered the PUBLISH with a
	// PUBREC and we are still waiting for its PUBCOMP.
	//
	// It decides what a resume has to resend, which MQTT 5.0 §4.4 / 3.1.1
	// §4.4 make a MUST: "both the Client and Server MUST resend any
	// unacknowledged PUBLISH packets (where QoS > 0) and PUBREL packets using
	// their original Packet Identifiers". A delivery past PUBREC is in the
	// second group -- its PUBLISH may no longer be sent ([MQTT-4.3.3-6]) and
	// its PUBREL stays unacknowledged until PUBCOMP ([MQTT-4.3.3-5]) -- so
	// without this bit a resumed session would restart the handshake from the
	// PUBLISH instead of finishing it.
	//
	// Always false for a QoS 1 entry, and unused for the inbound QoS 2 staging
	// entries in SessionData.InboundQoS2, which await their PUBREL by
	// definition.
	PendingPubRel bool
}

// MessageProperties is the store-neutral form of the MQTT 5 per-message
// properties that must survive a restart.
//
// The store layer deliberately does not persist protocol.Properties: the
// protocol struct holds some thirty fields, most of which are per-packet or
// server-scoped and have no meaning for a stored message, and persisting the
// struct would make every future protocol field part of the store schema by
// accident. Subscription.SubscriptionIdentifier set the same precedent.
type MessageProperties struct {
	PayloadFormat   *byte
	ContentType     string
	ResponseTopic   string
	CorrelationData []byte
	UserProperties  []UserProperty

	// SubscriptionIdentifier carries the identifiers of every subscription a
	// stored message matched, so a retransmission after a restart still reports
	// them all ([MQTT-3.3.4-4]). It is a slice because a single published
	// message can match several of the subscriber's filters, and a single
	// delivery has to name them all; an empty slice means none applied.
	//
	// Format note: this replaced a single *uint32, so a snapshot written by an
	// earlier build holds a JSON number where this build reads an array. Such a
	// record fails to unmarshal and is rejected as a corrupt entry rather than
	// silently misread, which is the intended outcome of the deliberate
	// no-compatibility change.
	SubscriptionIdentifier []uint32
}

// UserProperty is a store-neutral MQTT 5 user-defined property pair. The order
// of the pairs is significant and must be preserved (§3.3.2.3.7).
type UserProperty struct {
	Key   string
	Value string
}

// StoredMessage represents a message stored for QoS delivery.
type StoredMessage struct {
	ID        string
	Topic     string
	QoS       uint8
	Payload   []byte
	Retain    bool
	Timestamp time.Time
	ExpiresAt time.Time // absolute Message Expiry Interval deadline; zero = none (MQTT 5.0 §3.3.2.3.3)
}

// RetainedMessage represents a retained message.
//
// It carries the MQTT 5 relay data alongside the payload because a retained
// message is stored now and delivered later to a subscriber that was not
// connected at publish time -- that subscriber is still "receiving the
// Application Message", so §3.3.2.3 requires the Payload Format Indicator,
// Content Type, Response Topic, Correlation Data and User Properties to be
// replayed unaltered, and §3.3.2.3.3 requires the Message Expiry Interval to be
// decremented by the waiting time.
type RetainedMessage struct {
	Topic     string
	QoS       uint8
	Payload   []byte
	Timestamp time.Time
	// Properties is the MQTT 5 per-message relay data to replay with the
	// retained copy. Zero value for a 3.1/3.1.1 publisher.
	Properties MessageProperties
	// ExpiresAt is the absolute Message Expiry deadline derived from the
	// publisher's Message Expiry Interval; zero means the retained message does
	// not expire on its own (§3.3.1.3: an expired retained message is discarded).
	ExpiresAt time.Time
}
