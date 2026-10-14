package protocol

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

// minimalPropertyValue returns the smallest well-formed encoding of a
// property's value.
//
// The matrix test below places every Identifier in every property section, so
// the value must never be the reason a decode fails: each one is chosen to
// satisfy its own value rule as well (the 0/1 flags use 0, every property that
// must be non-zero uses 1).
func minimalPropertyValue(id byte) []byte {
	switch id {
	case PropPayloadFormat, PropRequestProblemInfo, PropRequestResponseInfo,
		PropMaximumQoS, PropRetainAvailable, PropWildcardSubAvailable,
		PropSubIDAvailable, PropSharedSubAvailable:
		return []byte{0x00}
	case PropMessageExpiryInterval, PropSessionExpiryInterval,
		PropWillDelayInterval, PropMaximumPacketSize:
		return []byte{0x00, 0x00, 0x00, 0x01}
	case PropReceiveMaximum, PropTopicAliasMax, PropTopicAlias, PropServerKeepAlive:
		return []byte{0x00, 0x01}
	case PropSubscriptionIdentifier:
		return []byte{0x01}
	case PropContentType, PropResponseTopic, PropAssignedClientID, PropAuthMethod,
		PropResponseInfo, PropServerReference, PropReasonString:
		return []byte{0x00, 0x01, 'x'}
	case PropCorrelationData, PropAuthData:
		return []byte{0x00, 0x01, 0xAA}
	case PropUserProperty:
		return []byte{0x00, 0x01, 'k', 0x00, 0x01, 'v'}
	}
	panic(fmt.Sprintf("test helper: no value for property 0x%02X", id))
}

// TestPropertyLegalityMatchesTable24 walks every (Identifier, property section)
// pair and asserts the decoder accepts exactly the combinations Table 2-4 lists.
//
// Both directions matter. A missing rejection lets a peer send a property the
// packet type has no room for, which a conformant implementation treats as a
// Malformed Packet (§2.2.2.2). A spurious rejection breaks the packets that are
// legal, which no single-property test would notice. Driving the loop off
// propertyLoci itself keeps the expectations in step with the table instead of
// duplicating it, so adding an Identifier forces a decision here.
func TestPropertyLegalityMatchesTable24(t *testing.T) {
	loci := []propertyLocus{
		locusConnect, locusWill, locusConnAck, locusPublish, locusAck,
		locusSubscribe, locusSubAck, locusUnsubscribe, locusUnsubAck,
		locusDisconnect, locusAuth,
	}

	codec := mqtt5Codec()
	for id, allowed := range propertyLoci {
		for _, locus := range loci {
			t.Run(fmt.Sprintf("0x%02X_in_locus_%d", id, locus), func(t *testing.T) {
				raw := propSection(append([]byte{id}, minimalPropertyValue(id)...)...)
				_, err := codec.decodeProperties(bytes.NewReader(raw), locus)

				if allowed&locusBit(locus) != 0 {
					if err != nil {
						t.Fatalf("decodeProperties() error = %v, want the Identifier accepted in this packet type", err)
					}
					return
				}
				if !errors.Is(err, ErrMalformedPacket) {
					t.Fatalf("decodeProperties() error = %v, want %v", err, ErrMalformedPacket)
				}
			})
		}
	}
}

// TestDecodeRejectsPropertyNotValidForPacketType names the wrong-packet-type
// cases that have a known history or a known peer behaviour. The exhaustive
// matrix above covers the rest.
func TestDecodeRejectsPropertyNotValidForPacketType(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{
			// The CONNACK this broker used to send before buildConnAckProperties
			// stopped echoing the request: Table 2-4 lists Request Response
			// Information for CONNECT only, so a conformant client had to treat
			// every MQTT 5 CONNACK as a Malformed Packet and disconnect. The
			// local codec accepted it, which is why only a wire-level assertion
			// catches it.
			name: "CONNACK carrying Request Response Information",
			raw: v5Packet(0x20, append([]byte{0x00, 0x00},
				propSection(PropRequestResponseInfo, 0x01)...)...),
		},
		{
			name: "SUBACK carrying a Message Expiry Interval",
			raw: v5Packet(0x90, append(append([]byte{0x00, 0x01},
				propSection(PropMessageExpiryInterval, 0x00, 0x00, 0x00, 0x01)...), 0x00)...),
		},
		{
			name: "PUBLISH carrying an Assigned Client Identifier",
			raw:  v5Packet(0x30, publishBody(propSection(PropAssignedClientID, 0x00, 0x01, 'x'))...),
		},
		{
			name: "CONNECT carrying a Topic Alias",
			raw:  v5Packet(0x10, connectBody(propSection(PropTopicAlias, 0x00, 0x01))...),
		},
		{
			name: "CONNECT carrying a Server Reference",
			raw: v5Packet(0x10, connectBody(propSection(
				PropServerReference, 0x00, 0x01, 'x'))...),
		},
		{
			name: "SUBSCRIBE carrying a Reason String",
			raw: v5Packet(0x82, subscribeBody(propSection(
				PropReasonString, 0x00, 0x01, 'x'))...),
		},
		{
			// 0x1B is one of the gaps in Table 2-4: not a defined Identifier at
			// all, so it is not valid for any packet type and there is nothing to
			// skip for forward compatibility.
			name: "PUBLISH carrying a reserved Identifier",
			raw:  v5Packet(0x30, publishBody(propSection(0x1B, 0x00))...),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := mqtt5Codec().Decode(bytes.NewReader(tt.raw))
			if !errors.Is(err, ErrMalformedPacket) {
				t.Fatalf("Decode() error = %v, want %v", err, ErrMalformedPacket)
			}
			// §2.2.2.2 answers a wrong-Identifier with 0x81; a repeat is a
			// Protocol Error with 0x82. The two must not collapse together.
			if errors.Is(err, ErrProtocolError) {
				t.Errorf("a property in the wrong packet type is a malformed packet, not a Protocol Error: %v", err)
			}
		})
	}
}

// TestDecodeRoundTripsServerReference covers the Identifier that used to be
// missing from the codec entirely.
//
// 0x1C was absent from the constants, the type table, the Properties struct and
// the decode switch, so an Identifier Table 2-4 defines was handled as an
// unknown one and the whole packet failed to decode. A CONNACK or DISCONNECT
// carrying a Server Reference is how a server redirects a client, so the failure
// broke that flow for any peer that used it.
func TestDecodeRoundTripsServerReference(t *testing.T) {
	const reference = "mqtts://backup.example:8883"

	prop := []byte{PropServerReference, 0x00, byte(len(reference))}
	prop = append(prop, reference...)

	body := []byte{0x00, 0x00} // Connect Acknowledge Flags, Reason Code
	body = append(body, propSection(prop...)...)

	pkt, err := mqtt5Codec().Decode(bytes.NewReader(v5Packet(0x20, body...)))
	if err != nil {
		t.Fatalf("Decode() error = %v, want nil", err)
	}
	ca, ok := pkt.(*ConnAckPacket)
	if !ok {
		t.Fatalf("Decode() returned %T, want *ConnAckPacket", pkt)
	}
	if ca.Properties == nil || ca.Properties.ServerReference != reference {
		t.Fatalf("ServerReference = %+v, want %q", ca.Properties, reference)
	}

	// And back out again: the encoder has to be able to produce what the decoder
	// now accepts, or the two halves disagree about the property.
	var buf bytes.Buffer
	if err := mqtt5Codec().Encode(&buf, ca); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	round, err := mqtt5Codec().Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("re-Decode() error = %v", err)
	}
	got, ok := round.(*ConnAckPacket)
	if !ok {
		t.Fatalf("re-Decode() returned %T, want *ConnAckPacket", round)
	}
	if got.Properties == nil || got.Properties.ServerReference != reference {
		t.Fatalf("ServerReference after the round trip = %+v, want %q", got.Properties, reference)
	}
}
