package broker

import (
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
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
// interval to advertise on the wire. It never returns 0: a zero interval means
// "already expired", so a message that survives only because the clock has not
// passed the deadline yet would be discarded by the receiver.
func remainingExpiry(expiresAt time.Time) *uint32 {
	d := time.Until(expiresAt)
	// Clamp before the unsigned conversion: time.Until on a past deadline is a
	// large negative Duration, and uint32() of it wraps around to a huge
	// interval that would keep the message alive far beyond its deadline.
	if d < time.Second {
		d = time.Second
	}
	remaining := uint32(d / time.Second)
	return &remaining
}
