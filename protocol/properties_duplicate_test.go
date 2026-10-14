package protocol

import (
	"bytes"
	"errors"
	"testing"
)

// The frames in this file are assembled by hand and fed to Decode.
//
// That is deliberate. A repeated property is accepted long before any
// round-trip would notice: properties decode into a flat struct with no
// occurrence tracking, so encoding a Properties value and reading it back can
// never produce a duplicate and can never expose the gap. Only bytes on the
// wire can.

// v5Packet prepends a fixed header to a body, using the one-byte form of
// Remaining Length. Every body below is well under 128 bytes.
func v5Packet(first byte, body ...byte) []byte {
	if len(body) > 127 {
		panic("test helper: Remaining Length does not fit in one byte")
	}
	return append([]byte{first, byte(len(body))}, body...)
}

// propSection prefixes an already-encoded property list with its Property
// Length.
func propSection(props ...byte) []byte {
	if len(props) > 127 {
		panic("test helper: Property Length does not fit in one byte")
	}
	return append([]byte{byte(len(props))}, props...)
}

// connectBody builds a v5 CONNECT up to the Client Identifier, so a test only
// supplies the property section.
func connectBody(props []byte) []byte {
	body := []byte{
		0x00, 0x04, 'M', 'Q', 'T', 'T', // Protocol Name
		0x05,       // Protocol Version 5
		0x00,       // Connect Flags: no Will, no User Name/Password, Clean Start 0
		0x00, 0x00, // Keep Alive
	}
	body = append(body, props...)
	return append(body, 0x00, 0x01, 'c') // Client Identifier
}

// publishBody builds a v5 QoS 0 PUBLISH body -- Topic Name, property section,
// no Payload. QoS 0 means no Packet Identifier, which keeps the frame short.
func publishBody(props []byte) []byte {
	body := []byte{0x00, 0x01, 't'} // Topic Name
	return append(body, props...)
}

// ackBody builds the body shared by PUBACK, PUBREC, PUBREL and PUBCOMP: the
// Packet Identifier, the Reason Code, then the property section. Table 2-4
// gives all four the same property set, which is why one locus covers them.
func ackBody(props []byte) []byte {
	body := []byte{0x00, 0x01, 0x00} // Packet Identifier, Reason Code 0
	return append(body, props...)
}

// subscribeBody builds a SUBSCRIBE body: Packet Identifier, property section,
// then one Topic Filter with its options byte.
func subscribeBody(props []byte) []byte {
	body := []byte{0x00, 0x01} // Packet Identifier
	body = append(body, props...)
	return append(body, 0x00, 0x03, 'a', '/', 'b', 0x00) // "a/b", QoS 0
}

// TestDecodeRejectsDuplicateAtMostOnceProperty covers MQTT 5.0's rule that a
// property may appear only once unless Table 2-4 marks it repeatable. Each
// frame sends a property Table 2-4 DOES allow in that packet type, so the
// repetition is the only thing wrong with it -- a property sent to the wrong
// packet type is a separate rule with a separate reason code, and would make
// these cases pass for the wrong reason.
func TestDecodeRejectsDuplicateAtMostOnceProperty(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{
			// The repetition with a real downstream consequence: decodeSubscribe
			// copies the packet-level Subscription Identifier onto every Topic
			// Filter in the list, so "last one wins" would silently re-tag a
			// filter the client never attached that identifier to.
			name: "SUBSCRIBE with two Subscription Identifiers",
			raw: v5Packet(0x82, subscribeBody(propSection(
				PropSubscriptionIdentifier, 0x2A,
				PropSubscriptionIdentifier, 0x2B,
			))...),
		},
		{
			name: "CONNECT with two Session Expiry Intervals",
			raw: v5Packet(0x10, connectBody(propSection(
				PropSessionExpiryInterval, 0x00, 0x00, 0x00, 0x01,
				PropSessionExpiryInterval, 0x00, 0x00, 0x00, 0x02,
			))...),
		},
		{
			// The reproduction from EMQX issue #8029: with "last value wins" a
			// client could advertise one Maximum Packet Size and be held to
			// another, discarding messages it had every reason to expect.
			name: "CONNECT with two Maximum Packet Sizes",
			raw: v5Packet(0x10, connectBody(propSection(
				PropMaximumPacketSize, 0x00, 0x00, 0x08, 0x34, // 2100
				PropMaximumPacketSize, 0x00, 0x00, 0x04, 0x4C, // 1100
			))...),
		},
		{
			name: "CONNACK with two Maximum QoS values",
			raw: v5Packet(0x20, append([]byte{0x00, 0x00}, propSection(
				PropMaximumQoS, 0x01,
				PropMaximumQoS, 0x00,
			)...)...),
		},
		{
			name: "PUBLISH with two Payload Format Indicators",
			raw: v5Packet(0x30, publishBody(propSection(
				PropPayloadFormat, 0x00,
				PropPayloadFormat, 0x01,
			))...),
		},
		{
			name: "PUBLISH with two Response Topics",
			raw: v5Packet(0x30, publishBody(propSection(
				PropResponseTopic, 0x00, 0x01, 'a',
				PropResponseTopic, 0x00, 0x01, 'b',
			))...),
		},
		{
			name: "PUBACK with two Reason Strings",
			raw: v5Packet(0x40, ackBody(propSection(
				PropReasonString, 0x00, 0x01, 'a',
				PropReasonString, 0x00, 0x01, 'b',
			))...),
		},
		{
			// PUBREL shares locusAck with PUBACK but sits in the fixed-header
			// group whose reserved bits are 0b0010, so it is worth its own case.
			name: "PUBREL with two Reason Strings",
			raw: v5Packet(0x62, ackBody(propSection(
				PropReasonString, 0x00, 0x01, 'a',
				PropReasonString, 0x00, 0x01, 'b',
			))...),
		},
		{
			name: "DISCONNECT with two Session Expiry Intervals",
			raw: v5Packet(0xE0, append([]byte{0x00}, propSection(
				PropSessionExpiryInterval, 0x00, 0x00, 0x00, 0x01,
				PropSessionExpiryInterval, 0x00, 0x00, 0x00, 0x02,
			)...)...),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := mqtt5Codec().Decode(bytes.NewReader(tt.raw))
			if !errors.Is(err, ErrProtocolError) {
				t.Fatalf("Decode() error = %v, want %v", err, ErrProtocolError)
			}
			// §2.2.2.2 answers an Identifier that is not valid for the packet
			// type with 0x81 Malformed Packet; a repeat is a Protocol Error and
			// gets 0x82. Telling the two apart is the whole reason the sentinel
			// exists, so a case that drifts back to ErrMalformedPacket has to
			// fail here rather than quietly widen the malformed class.
			if errors.Is(err, ErrMalformedPacket) {
				t.Errorf("a repeated property must not be reported as a malformed packet: %v", err)
			}
		})
	}
}

// TestDecodeAcceptsRepeatedSubscriptionIdentifierOnlyInPublish pins the one
// Identifier whose repeatability depends on the packet type.
//
// Both halves are load-bearing. Without the PUBLISH half an over-tightened
// check would look correct while refusing a legal packet -- §3.3.2.3.8 uses the
// repetition to report that one publication matched more than one subscription.
// Without the SUBSCRIBE half the check would miss the repetition that actually
// corrupts meaning.
func TestDecodeAcceptsRepeatedSubscriptionIdentifierOnlyInPublish(t *testing.T) {
	repeated := propSection(
		PropSubscriptionIdentifier, 0x2A,
		PropSubscriptionIdentifier, 0x2B,
	)

	t.Run("PUBLISH accepts them", func(t *testing.T) {
		raw := v5Packet(0x30, publishBody(repeated)...)
		pkt, err := mqtt5Codec().Decode(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("Decode() error = %v, want nil", err)
		}
		pub, ok := pkt.(*PublishPacket)
		if !ok {
			t.Fatalf("Decode() returned %T, want *PublishPacket", pkt)
		}
		if pub.Properties == nil || pub.Properties.SubscriptionIdentifier == nil {
			t.Fatal("expected a Subscription Identifier to survive")
		}
		// Properties.SubscriptionIdentifier is a single *uint32, so a legal
		// repetition is collapsed to the last identifier instead of being
		// retained as a list. This is a known lossy limitation, recorded on the
		// field: no broker path can observe the loss, because the only way the
		// broker puts a Subscription Identifier on the wire is
		// forwardedPublishProperties, which sets exactly one.
		if got := *pub.Properties.SubscriptionIdentifier; got != 0x2B {
			t.Errorf("SubscriptionIdentifier = %d, want %d (the last one)", got, 0x2B)
		}
	})

	t.Run("SUBSCRIBE rejects them", func(t *testing.T) {
		raw := v5Packet(0x82, subscribeBody(repeated)...)
		if _, err := mqtt5Codec().Decode(bytes.NewReader(raw)); !errors.Is(err, ErrProtocolError) {
			t.Fatalf("Decode() error = %v, want %v", err, ErrProtocolError)
		}
	})
}

// TestDecodeAcceptsRepeatedUserProperty pins the other repeatable Identifier.
// §3.3.2.3.7 makes the relative order of the pairs significant, so the count
// and the order both have to survive.
func TestDecodeAcceptsRepeatedUserProperty(t *testing.T) {
	raw := v5Packet(0x30, publishBody(propSection(
		PropUserProperty, 0x00, 0x01, 'k', 0x00, 0x01, '1',
		PropUserProperty, 0x00, 0x01, 'k', 0x00, 0x01, '2',
		PropUserProperty, 0x00, 0x01, 'k', 0x00, 0x01, '3',
	))...)

	pkt, err := mqtt5Codec().Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Decode() error = %v, want nil", err)
	}
	pub, ok := pkt.(*PublishPacket)
	if !ok {
		t.Fatalf("Decode() returned %T, want *PublishPacket", pkt)
	}
	if pub.Properties == nil {
		t.Fatal("expected properties to survive")
	}
	got := pub.Properties.UserProperties
	if len(got) != 3 {
		t.Fatalf("len(UserProperties) = %d, want 3", len(got))
	}
	for i, want := range []string{"1", "2", "3"} {
		if got[i].Key != "k" || got[i].Value != want {
			t.Errorf("UserProperties[%d] = %q=%q, want k=%q", i, got[i].Key, got[i].Value, want)
		}
	}
}

// TestDecodeDuplicatePropertyCheckDoesNotRunForPreV5 guards the v5-only scope
// of the check. A 3.1.1 PUBLISH has no property section: everything after the
// Topic Name is payload, so bytes that would read as two Payload Format
// Indicators under v5 are ordinary payload here and must come back untouched.
func TestDecodeDuplicatePropertyCheckDoesNotRunForPreV5(t *testing.T) {
	raw := v5Packet(0x30,
		0x00, 0x01, 't', // Topic Name
		0x01, 0x01, // payload, not a property section
	)

	pkt, err := NewCodec(256 * 1024).Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Decode() error = %v, want nil", err)
	}
	pub, ok := pkt.(*PublishPacket)
	if !ok {
		t.Fatalf("Decode() returned %T, want *PublishPacket", pkt)
	}
	if !bytes.Equal(pub.Payload, []byte{0x01, 0x01}) {
		t.Errorf("Payload = %v, want [1 1]", pub.Payload)
	}
	if pub.Properties != nil {
		t.Errorf("Properties = %+v, want nil for a 3.1.1 packet", pub.Properties)
	}
}
