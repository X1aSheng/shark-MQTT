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

// minimalPropertiesFor returns a property set carrying exactly the one
// Identifier id, with a value that satisfies that Identifier's own rule.
//
// It is the encoding-side mirror of minimalPropertyValue: the matrix test below
// has to fail for the locus and never for the value, or a rejection would be
// indistinguishable from a fixture mistake.
func minimalPropertiesFor(id byte) *Properties {
	zero := byte(0)
	one16 := uint16(1)
	one32 := uint32(1)
	const text = "x"
	binary := []byte{0xAA}

	switch id {
	case PropPayloadFormat:
		return &Properties{PayloadFormat: &zero}
	case PropMessageExpiryInterval:
		return &Properties{MessageExpiryInterval: &one32}
	case PropContentType:
		return &Properties{ContentType: text}
	case PropResponseTopic:
		return &Properties{ResponseTopic: text}
	case PropCorrelationData:
		return &Properties{CorrelationData: binary}
	case PropSubscriptionIdentifier:
		return &Properties{SubscriptionIdentifier: []uint32{one32}}
	case PropSessionExpiryInterval:
		return &Properties{SessionExpiryInterval: &one32}
	case PropAssignedClientID:
		return &Properties{AssignedClientID: text}
	case PropServerKeepAlive:
		return &Properties{ServerKeepAlive: &one16}
	case PropAuthMethod:
		return &Properties{AuthenticationMethod: text}
	case PropAuthData:
		return &Properties{AuthenticationData: binary}
	case PropRequestProblemInfo:
		return &Properties{RequestProblemInfo: &zero}
	case PropWillDelayInterval:
		return &Properties{WillDelayInterval: &one32}
	case PropRequestResponseInfo:
		return &Properties{RequestResponseInfo: &zero}
	case PropResponseInfo:
		return &Properties{ResponseInfo: text}
	case PropServerReference:
		return &Properties{ServerReference: text}
	case PropReceiveMaximum:
		return &Properties{ReceiveMaximum: &one16}
	case PropTopicAliasMax:
		return &Properties{TopicAliasMaximum: &one16}
	case PropTopicAlias:
		return &Properties{TopicAlias: &one16}
	case PropMaximumQoS:
		return &Properties{MaximumQoS: &zero}
	case PropRetainAvailable:
		return &Properties{RetainAvailable: &zero}
	case PropUserProperty:
		return &Properties{UserProperties: []UserProperty{{Key: "k", Value: "v"}}}
	case PropMaximumPacketSize:
		return &Properties{MaximumPacketSize: &one32}
	case PropWildcardSubAvailable:
		return &Properties{WildcardSubAvailable: &zero}
	case PropSubIDAvailable:
		return &Properties{SubIDAvailable: &zero}
	case PropSharedSubAvailable:
		return &Properties{SharedSubAvailable: &zero}
	case PropReasonString:
		return &Properties{ReasonString: text}
	}
	panic(fmt.Sprintf("test helper: no property set for Identifier 0x%02X", id))
}

// TestEncodeLocusMatchesTable24 is the encoding-side mirror of
// TestPropertyLegalityMatchesTable24: it walks every (Identifier, property
// section) pair and asserts the encoder writes exactly the combinations Table
// 2-4 lists.
//
// The two directions catch different mistakes, which is why both exist. A
// decoder that accepts too much lets a broken peer through; an encoder that
// emits too much produces the broken peer. Decoding alone cannot catch the
// second one -- the broker shipped a CONNACK carrying a CONNECT-only Identifier
// for as long as this side was unchecked -- so the rule has to be asserted from
// both ends.
func TestEncodeLocusMatchesTable24(t *testing.T) {
	loci := []propertyLocus{
		locusConnect, locusWill, locusConnAck, locusPublish, locusAck,
		locusSubscribe, locusSubAck, locusUnsubscribe, locusUnsubAck,
		locusDisconnect, locusAuth,
	}

	codec := mqtt5Codec()
	for id, allowed := range propertyLoci {
		for _, locus := range loci {
			t.Run(fmt.Sprintf("0x%02X_in_locus_%d", id, locus), func(t *testing.T) {
				err := codec.encodeProperties(&bytes.Buffer{}, minimalPropertiesFor(id), locus)

				if allowed&locusBit(locus) != 0 {
					if err != nil {
						t.Fatalf("encodeProperties() error = %v, want the Identifier written in this packet type", err)
					}
					return
				}
				if !errors.Is(err, ErrMalformedPacket) {
					t.Fatalf("encodeProperties() error = %v, want %v", err, ErrMalformedPacket)
				}
			})
		}
	}
}

// TestPropertyProbesCoverPropertyLoci keeps the encoder's probe table and Table
// 2-4 from drifting apart.
//
// validatePropertyLoci can only check the Identifiers it has a probe for, so a
// property added to Properties without a probe would be emitted unchecked in
// every packet type -- the exact shape of the defect the guard exists for.
// propertyLoci is the source of truth for which Identifiers exist, so the two
// lists must have the same members.
func TestPropertyProbesCoverPropertyLoci(t *testing.T) {
	probed := make(map[byte]bool, len(propertyProbes))
	for _, probe := range propertyProbes {
		if probed[probe.id] {
			t.Errorf("propertyProbes lists 0x%02X more than once", probe.id)
		}
		probed[probe.id] = true

		if _, defined := propertyLoci[probe.id]; !defined {
			t.Errorf("probe for 0x%02X has no entry in propertyLoci", probe.id)
			continue
		}
		if !probe.set(minimalPropertiesFor(probe.id)) {
			t.Errorf("probe for 0x%02X does not fire on the set that carries it", probe.id)
		}
		if probe.set(&Properties{}) {
			t.Errorf("probe for 0x%02X fires on the empty property set", probe.id)
		}
	}
	for id := range propertyLoci {
		if !probed[id] {
			t.Errorf("Identifier 0x%02X has no probe: the encoder would write it unchecked", id)
		}
	}
}

// TestEncodeRejectsConnectOnlyPropertyOnConnAck pins the defect the encode-side
// locus guard exists for.
//
// buildConnAckProperties used to echo the client's Request Response Information
// (0x19) back on the CONNACK. Table 2-4 lists 0x19 for CONNECT only, so §2.2.2.2
// made every such CONNACK a Malformed Packet a conformant client had to reject.
// Nothing local noticed: the decoder accepted the broker's own bytes, so only a
// guard on the encoding side can refuse to produce a packet the packet type has
// no room for.
func TestEncodeRejectsConnectOnlyPropertyOnConnAck(t *testing.T) {
	requested := byte(1)
	pkt := &ConnAckPacket{
		FixedHeader: FixedHeader{PacketType: PacketTypeConnAck},
		ReasonCode:  ConnAckAccepted,
		Properties:  &Properties{RequestResponseInfo: &requested},
	}

	var buf bytes.Buffer
	err := mqtt5Codec().Encode(&buf, pkt)
	if !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("Encode() error = %v, want %v", err, ErrMalformedPacket)
	}
	// §2.2.2.2 answers a wrong-packet-type Identifier with 0x81; a repeat is a
	// Protocol Error with 0x82. The two must not collapse together.
	if errors.Is(err, ErrProtocolError) {
		t.Errorf("a property in the wrong packet type is a malformed packet, not a Protocol Error: %v", err)
	}
	// The property section is buffered, so the rejection must land before the
	// fixed header is written: a half-emitted CONNACK would be worse than none.
	if buf.Len() != 0 {
		t.Errorf("Encode() wrote %d bytes before rejecting, want 0", buf.Len())
	}
}
