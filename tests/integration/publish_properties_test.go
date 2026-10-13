package integration

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestPublishPropertiesRelayedToV50Subscriber covers MQTT 5.0 §3.3.2.3
// end-to-end: a v5.0 subscriber must receive the publisher's Payload Format
// Indicator, Content Type, Response Topic, Correlation Data and every User
// Property unaltered and in order, together with the Server's own additions
// (the matching subscription's Subscription Identifier and the decremented
// Message Expiry Interval). Before the relay existed, the responder in a
// request/response flow never saw the Response Topic (§4.10) and the flow
// could not complete.
func TestPublishPropertiesRelayedToV50Subscriber(t *testing.T) {
	brk := testBroker(t)

	sub := dialTestBroker(t, brk)
	defer sub.Close()
	// A codec must be bound to the version it speaks to encode a v5-only field
	// such as a property section: NewCodec starts at 3.1.1.
	subCodec := protocol.NewCodecForVersion(0, protocol.Version50)
	connectWithVersion(t, sub, subCodec, "props-sub", protocol.Version50, protocol.ProtocolNameMQTT)
	subID := uint32(7)
	subscribeWithFilterOptions(t, sub, subCodec,
		protocol.TopicFilter{Topic: "props/relay", QoS: 1},
		&protocol.Properties{SubscriptionIdentifier: &subID}, 1)

	pub := dialTestBroker(t, brk)
	defer pub.Close()
	pubCodec := protocol.NewCodecForVersion(0, protocol.Version50)
	connectWithVersion(t, pub, pubCodec, "props-pub", protocol.Version50, protocol.ProtocolNameMQTT)

	const payload = `{"hello":"world"}`
	pfi := byte(1)
	expiry := uint32(30)
	// The same key appears twice on purpose: §3.3.2.3.7 requires every User
	// Property to survive with its relative order intact, so a map-shaped
	// relay would fail here.
	userProps := []protocol.UserProperty{
		{Key: "trace", Value: "1"},
		{Key: "tenant", Value: "acme"},
		{Key: "trace", Value: "2"},
	}
	publish := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    1,
		Topic:       "props/relay",
		Payload:     []byte(payload),
		Properties: &protocol.Properties{
			PayloadFormat:         &pfi,
			MessageExpiryInterval: &expiry,
			ContentType:           "application/json",
			ResponseTopic:         "props/reply",
			CorrelationData:       []byte{0xDE, 0xAD, 0xBE, 0xEF},
			UserProperties:        userProps,
		},
	}
	pub.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pub, publish); err != nil {
		t.Fatalf("PUBLISH: %v", err)
	}
	pub.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := pubCodec.Decode(pub); err != nil { // PUBACK
		t.Fatalf("PUBACK: %v", err)
	}

	got := receivePublish(t, sub, subCodec)
	if string(got.Payload) != payload {
		t.Errorf("payload = %q, want %q", string(got.Payload), payload)
	}
	props := got.Properties
	if props == nil {
		t.Fatal("delivered PUBLISH carried no properties at all")
	}
	if props.PayloadFormat == nil || *props.PayloadFormat != pfi {
		t.Errorf("PayloadFormat = %v, want %d", props.PayloadFormat, pfi)
	}
	if props.ContentType != "application/json" {
		t.Errorf("ContentType = %q, want %q", props.ContentType, "application/json")
	}
	if props.ResponseTopic != "props/reply" {
		t.Errorf("ResponseTopic = %q, want %q", props.ResponseTopic, "props/reply")
	}
	if string(props.CorrelationData) != string([]byte{0xDE, 0xAD, 0xBE, 0xEF}) {
		t.Errorf("CorrelationData = %x, want deadbeef", props.CorrelationData)
	}
	if len(props.UserProperties) != len(userProps) {
		t.Fatalf("UserProperties = %+v, want %+v (count and order must survive)",
			props.UserProperties, userProps)
	}
	for i, want := range userProps {
		if props.UserProperties[i] != want {
			t.Errorf("UserProperties[%d] = %+v, want %+v (order must be preserved)",
				i, props.UserProperties[i], want)
		}
	}
	// Topic Alias names a mapping in the *publisher's* inbound alias space
	// (§3.3.2.3.4) and is meaningless to a subscriber.
	if props.TopicAlias != nil {
		t.Errorf("TopicAlias = %v, must not be relayed", *props.TopicAlias)
	}
	// Server-owned additions: the matching subscription's identifier and the
	// Message Expiry Interval decremented by the time held in the Server
	// (§3.3.2.3.3 / §3.3.2.3.7).
	if props.SubscriptionIdentifier == nil || *props.SubscriptionIdentifier != subID {
		t.Errorf("SubscriptionIdentifier = %v, want %d", props.SubscriptionIdentifier, subID)
	}
	if props.MessageExpiryInterval == nil {
		t.Error("MessageExpiryInterval missing: the publisher's 30s deadline must be relayed")
	} else if v := *props.MessageExpiryInterval; v < 28 || v > 30 {
		t.Errorf("MessageExpiryInterval = %d, want the remaining ~30s", v)
	}
}

// TestPublishPropertiesNotEmittedToV311Subscriber pins the audit finding that
// v5.0 property bytes must never reach a v3.1.1 subscriber: the v3.1.1 wire
// format has no property section, so emitting one would be absorbed into the
// payload and silently corrupt the stream. The v3.1.1 codec below decodes the
// delivered packet, which is exactly what would expose that corruption.
func TestPublishPropertiesNotEmittedToV311Subscriber(t *testing.T) {
	brk := testBroker(t)

	sub := dialTestBroker(t, brk)
	defer sub.Close()
	subCodec := protocol.NewCodec(0) // a codec only upgrades to v5 when it decodes a v5 CONNECT
	connectWithVersion(t, sub, subCodec, "props-sub-311", protocol.Version311, protocol.ProtocolNameMQTT)
	subscribeToTopic(t, sub, subCodec, "props/311", 1, 1)

	pub := dialTestBroker(t, brk)
	defer pub.Close()
	// The publisher speaks v5, so its codec is bound to v5: that is what lets it
	// put a property section on the wire in the first place.
	pubCodec := protocol.NewCodecForVersion(0, protocol.Version50)
	connectWithVersion(t, pub, pubCodec, "props-pub-311", protocol.Version50, protocol.ProtocolNameMQTT)

	const payload = "clean-payload"
	pfi := byte(1)
	expiry := uint32(30)
	publish := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    1,
		Topic:       "props/311",
		Payload:     []byte(payload),
		Properties: &protocol.Properties{
			PayloadFormat:         &pfi,
			MessageExpiryInterval: &expiry,
			ContentType:           "text/plain",
			ResponseTopic:         "props/reply-311",
			UserProperties:        []protocol.UserProperty{{Key: "trace", Value: "1"}},
		},
	}
	pub.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pub, publish); err != nil {
		t.Fatalf("PUBLISH: %v", err)
	}
	pub.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := pubCodec.Decode(pub); err != nil { // PUBACK
		t.Fatalf("PUBACK: %v", err)
	}

	got := receivePublish(t, sub, subCodec)
	if string(got.Payload) != payload {
		t.Errorf("payload = %q, want %q: v5 property bytes leaked into a v3.1.1 PUBLISH",
			string(got.Payload), payload)
	}
}
