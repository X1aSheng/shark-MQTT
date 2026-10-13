package broker

import (
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store"
)

// forwardedPublishProperties builds the property set of one outbound PUBLISH.
//
// MQTT 5.0 §3.3.2.3 makes the Server a transparent relay for the publisher's
// per-message properties: the Payload Format Indicator, Content Type, Response
// Topic and Correlation Data must be sent unaltered, and every User Property
// must be sent unaltered with its relative order preserved (§3.3.2.3.7). Losing
// them silently broke request/response (§4.10) -- the responder never saw the
// Response Topic -- and stripped the application-level tags.
//
// Topic Alias is deliberately NOT copied: it names a mapping in the
// *publisher's* inbound alias space and is meaningless to a subscriber
// (§3.3.2.3.4). Likewise absent are Authentication Method/Data and the Will
// properties, which are CONNECT/AUTH scope.
//
// Two properties belong to the Server rather than the publisher:
//   - the Subscription Identifier of the matching subscription, and
//   - the remaining Message Expiry Interval, which §3.3.2.3.3 requires to be
//     sent decremented by the time the message was held in the Server.
//
// The UserProperties slice is shared with the incoming packet instead of being
// copied: it is read-only on every delivery path, and copying it would allocate
// once per subscriber per message. The returned set is nil when the publisher
// sent nothing forwardable and the Server has nothing of its own to add, which
// keeps the encoded packet identical to the pre-existing behaviour.
func forwardedPublishProperties(in *protocol.Properties, subID *uint32, expiresAt time.Time) *protocol.Properties {
	var out *protocol.Properties
	if in != nil && (in.PayloadFormat != nil || in.ContentType != "" ||
		in.ResponseTopic != "" || in.CorrelationData != nil || len(in.UserProperties) > 0) {
		out = &protocol.Properties{
			PayloadFormat:   in.PayloadFormat,
			ContentType:     in.ContentType,
			ResponseTopic:   in.ResponseTopic,
			CorrelationData: in.CorrelationData,
			UserProperties:  in.UserProperties,
		}
	}
	if subID == nil && expiresAt.IsZero() {
		return out
	}
	if out == nil {
		out = &protocol.Properties{}
	}
	out.SubscriptionIdentifier = subID
	if !expiresAt.IsZero() {
		out.MessageExpiryInterval = remainingExpiry(expiresAt)
	}
	return out
}

// remainingExpiry converts an absolute Message Expiry deadline into the
// interval to advertise on the wire.
//
// [MQTT-3.3.2-6] asks for the received value minus the time the message waited
// in the Server, so the two cases below are told apart rather than clamped
// together:
//
//   - The deadline has passed: the subtraction is not positive, and 0 is the
//     faithful answer. It is also the useful one -- an interval of 0 tells the
//     receiving client not to hand a stale payload to its application. This
//     does not cut the exchange short: [MQTT-4.3.3-7] and §4.4 keep the
//     obligation to finish the handshake independent of the interval, which
//     only governs application delivery.
//   - The deadline is still ahead but less than a second away: never advertise
//     0, because 0 means "already expired" on the wire and would tell the
//     receiver to discard a message we are still delivering.
func remainingExpiry(expiresAt time.Time) *uint32 {
	d := time.Until(expiresAt)
	// Both branches clamp before the unsigned conversion: time.Until on a past
	// deadline is a large negative Duration, and uint32() of it wraps around to
	// a huge interval that would keep the message alive far beyond its deadline.
	if d <= 0 {
		expired := uint32(0)
		return &expired
	}
	if d < time.Second {
		d = time.Second
	}
	remaining := uint32(d / time.Second)
	return &remaining
}

// storeProperties converts a publisher's property set into the store-neutral
// form kept alongside an unacknowledged message.
//
// Only the relay data is carried over: it is what §3.3.2.3 requires a
// retransmission to send, and what a QoS 2 staging record has to hold until
// PUBREL arrives. The Message Expiry Interval is deliberately left out -- the
// store keeps the absolute deadline (store.InflightMessage.ExpiresAt) instead,
// and the interval is re-derived from it so a retransmission advertises the time
// actually left rather than the value captured when the packet was first sent.
func storeProperties(p *protocol.Properties) store.MessageProperties {
	if p == nil {
		return store.MessageProperties{}
	}
	sp := store.MessageProperties{
		PayloadFormat:          p.PayloadFormat,
		ContentType:            p.ContentType,
		ResponseTopic:          p.ResponseTopic,
		CorrelationData:        p.CorrelationData,
		SubscriptionIdentifier: p.SubscriptionIdentifier,
	}
	if len(p.UserProperties) > 0 {
		// Rebuilt rather than aliased: the protocol slice belongs to the
		// decoded packet, while the store record outlives it.
		sp.UserProperties = make([]store.UserProperty, len(p.UserProperties))
		for i, up := range p.UserProperties {
			sp.UserProperties[i] = store.UserProperty{Key: up.Key, Value: up.Value}
		}
	}
	return sp
}

// protocolProperties is the inverse of storeProperties: it rebuilds the property
// set a retransmission puts back on the wire. It returns nil when the record
// holds nothing to relay, so a session restored from an older snapshot produces
// exactly the packet it produced before -- the pre-existing, property-free
// behaviour, rather than an empty property section.
func protocolProperties(sp store.MessageProperties) *protocol.Properties {
	if sp.PayloadFormat == nil && sp.ContentType == "" && sp.ResponseTopic == "" &&
		sp.CorrelationData == nil && len(sp.UserProperties) == 0 &&
		sp.SubscriptionIdentifier == nil {
		return nil
	}
	p := &protocol.Properties{
		PayloadFormat:          sp.PayloadFormat,
		ContentType:            sp.ContentType,
		ResponseTopic:          sp.ResponseTopic,
		CorrelationData:        sp.CorrelationData,
		SubscriptionIdentifier: sp.SubscriptionIdentifier,
	}
	if len(sp.UserProperties) > 0 {
		p.UserProperties = make([]protocol.UserProperty, len(sp.UserProperties))
		for i, up := range sp.UserProperties {
			p.UserProperties[i] = protocol.UserProperty{Key: up.Key, Value: up.Value}
		}
	}
	return p
}
