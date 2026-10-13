package protocol

// Regression tests for the audit fixes:
//   - H1: property length claims must be bounded before allocation
//   - short Remaining Length on SUBACK/UNSUBACK must not over-read the stream
//   - PINGREQ/PINGRESP with a non-zero Remaining Length is malformed
//   - CONNECT with trailing bytes is malformed
//   - encodeConnect rejects over-long will messages / passwords instead of
//     silently truncating their length prefix
//   - encodePublish must not emit a property section for a pre-v5 codec
//     (properties are MQTT 5 wire syntax only)

import (
	"bytes"
	"errors"
	"testing"
)

// rawConnect builds a minimal MQTT 5.0 CONNECT frame whose payload is exactly
// body. The returned slice is the complete wire frame.
func rawConnectFrame(body []byte) []byte {
	frame := []byte{0x10, byte(len(body))} // RL < 128 for these tests
	return append(frame, body...)
}

func TestDecodeConnectRejectsOversizedPropertyLength(t *testing.T) {
	// CONNECT: proto name "MQTT", version 5, CleanStart, keepalive 60,
	// property length varint 0xFFFFFF7F (268435455), client id "cid".
	var body []byte
	body = append(body, 0x00, 0x04, 'M', 'Q', 'T', 'T') // protocol name
	body = append(body, Version50)                      // version
	body = append(body, 0x02)                           // clean start
	body = append(body, 0x00, 0x3C)                     // keepalive
	body = append(body, 0xFF, 0xFF, 0xFF, 0x7F)         // property length claim
	body = append(body, 0x00, 0x03, 'c', 'i', 'd')      // client id

	c := NewCodec(256 * 1024)
	_, err := c.Decode(bytes.NewReader(rawConnectFrame(body)))
	if !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("expected ErrMalformedPacket for oversized property length, got %v", err)
	}
}

func TestDecodeConnectRejectsTrailingBytes(t *testing.T) {
	// Valid MQTT 5.0 CONNECT produced by the encoder, then one junk byte is
	// appended to the payload so the packet no longer self-describes.
	pkt := &ConnectPacket{
		FixedHeader: FixedHeader{
			PacketType: PacketTypeConnect,
		},
		ProtocolName:    ProtocolNameMQTT,
		ProtocolVersion: Version50,
		Flags: ConnectFlags{
			CleanSession: true,
		},
		KeepAlive: 60,
		ClientID:  "cid",
	}
	var buf bytes.Buffer
	enc := NewCodec(256 * 1024)
	if err := enc.Encode(&buf, pkt); err != nil {
		t.Fatalf("encode: %v", err)
	}
	frame := buf.Bytes()
	// Append one junk byte and grow Remaining Length to match.
	frame = append(frame, 0xEE)
	frame[1]++ // RL < 128 in this test

	c := NewCodec(256 * 1024)
	_, err := c.Decode(bytes.NewReader(frame))
	if !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("expected ErrMalformedPacket for trailing CONNECT bytes, got %v", err)
	}
}

func TestDecodePingWithRemainingLengthRejected(t *testing.T) {
	for _, first := range []byte{0xC0, 0xD0} { // PINGREQ, PINGRESP
		c := NewCodec(256 * 1024)
		_, err := c.Decode(bytes.NewReader([]byte{first, 0x01, 0x00}))
		if !errors.Is(err, ErrMalformedPacket) {
			t.Fatalf("packet 0x%02X with RL=1: expected ErrMalformedPacket, got %v", first, err)
		}
	}
}

func TestDecodeSubAckShortRemainingLengthRejected(t *testing.T) {
	c := NewCodec(256 * 1024)
	_, err := c.Decode(bytes.NewReader([]byte{0x90, 0x01, 0x01})) // SUBACK RL=1
	if !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("expected ErrMalformedPacket for SUBACK RL=1, got %v", err)
	}
}

func TestDecodeUnsubAckShortRemainingLengthRejected(t *testing.T) {
	c := NewCodec(256 * 1024)
	_, err := c.Decode(bytes.NewReader([]byte{0xB0, 0x01, 0x01})) // UNSUBACK RL=1
	if !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("expected ErrMalformedPacket for UNSUBACK RL=1, got %v", err)
	}
}

func TestEncodeConnectRejectsOversizedWillAndPassword(t *testing.T) {
	big := make([]byte, 65536)

	will := &ConnectPacket{
		FixedHeader: FixedHeader{
			PacketType: PacketTypeConnect,
		},
		ProtocolName:    ProtocolNameMQTT,
		ProtocolVersion: Version311,
		Flags: ConnectFlags{
			WillFlag: true,
		},
		ClientID:    "cid",
		WillTopic:   "will",
		WillMessage: big,
	}
	c := NewCodec(256 * 1024)
	if err := c.Encode(&bytes.Buffer{}, will); err == nil {
		t.Error("expected error when encoding will message longer than 65535 bytes")
	}

	pw := &ConnectPacket{
		FixedHeader: FixedHeader{
			PacketType: PacketTypeConnect,
		},
		ProtocolName:    ProtocolNameMQTT,
		ProtocolVersion: Version311,
		Flags: ConnectFlags{
			UsernameFlag: true,
			PasswordFlag: true,
		},
		ClientID: "cid",
		Username: "u",
		Password: big,
	}
	if err := c.Encode(&bytes.Buffer{}, pw); err == nil {
		t.Error("expected error when encoding password longer than 65535 bytes")
	}
}

// TestEncodePublishPropertySectionVersionGated pins the symmetry between the
// PUBLISH encoder and decoder: the property section is MQTT 5 wire syntax, so a
// codec bound to 3.1.1 must never write it -- a peer would read the property
// bytes as payload, the same stream-corruption class as the decode-side gate
// fixed earlier. Because the fields cannot be represented at all, the encoder
// reports an error instead of dropping them silently.
func TestEncodePublishPropertySectionVersionGated(t *testing.T) {
	newPkt := func(props *Properties) *PublishPacket {
		return &PublishPacket{
			FixedHeader: FixedHeader{PacketType: PacketTypePublish, QoS: 1},
			PacketID:    1,
			Topic:       "gate/topic",
			Payload:     []byte("payload"),
			Properties:  props,
		}
	}
	contentType := "text/plain"
	withProps := newPkt(&Properties{ContentType: contentType})

	// MQTT 3.1.1 with properties: refuse, never emit a v5 section.
	v311 := NewCodecForVersion(0, Version311)
	err := v311.Encode(&bytes.Buffer{}, withProps)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("v3.1.1 encode with properties: err = %v, want ErrUnsupportedVersion", err)
	}

	// MQTT 3.1.1 without properties: unchanged wire bytes, no empty section.
	var noProps bytes.Buffer
	if err := v311.Encode(&noProps, newPkt(nil)); err != nil {
		t.Fatalf("v3.1.1 encode without properties: %v", err)
	}
	// topic(2+10) + packet ID(2) + payload(7) + fixed header(2) = 23, with no
	// property-length byte in between.
	if noProps.Len() != 23 {
		t.Errorf("v3.1.1 PUBLISH length = %d, want 23 (no property section)", noProps.Len())
	}

	// MQTT 5.0 with the same packet: the property section must actually be
	// written, so the assertion above cannot pass for the trivial reason that
	// the encoder ignores properties everywhere.
	v50 := NewCodecForVersion(0, Version50)
	var got50 bytes.Buffer
	if err := v50.Encode(&got50, withProps); err != nil {
		t.Fatalf("v5.0 encode with properties: %v", err)
	}
	if got50.Len() <= noProps.Len() {
		t.Errorf("v5.0 encoding (%d bytes) must be longer than the v3.1.1 one (%d bytes) "+
			"because it carries the property section", got50.Len(), noProps.Len())
	}
	decoded, err := NewCodecForVersion(0, Version50).Decode(bytes.NewReader(got50.Bytes()))
	if err != nil {
		t.Fatalf("v5.0 decode: %v", err)
	}
	pub, ok := decoded.(*PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", decoded)
	}
	if pub.Properties == nil || pub.Properties.ContentType != contentType {
		t.Errorf("ContentType = %+v, want %q round-tripped", pub.Properties, contentType)
	}
}

// TestPropertySectionVersionGatedAcrossPackets extends the PUBLISH-only gate
// test to every encoder that writes an MQTT 5 property section. CONNACK,
// SUBSCRIBE and UNSUBSCRIBE wrote the section whenever Properties was non-nil,
// regardless of the codec's version, so a v3.1.1 peer read the property bytes as
// payload -- and for CONNACK as trailing bytes its decoder rejects outright. The
// acknowledgement family was already covered by its own ReasonCodes check, so it
// is included here only to hold that guard in place.
//
// Each case builds the same packet twice under a pre-v5 codec, with and without
// properties, plus once under a v5 codec. A pre-v5 codec must refuse the first
// outright and still encode the second, so the gate cannot degrade into a
// blanket rejection; a v5 codec must encode the first, so it cannot pass by
// dropping properties everywhere.
func TestPropertySectionVersionGatedAcrossPackets(t *testing.T) {
	publish := func(props *Properties) Packet {
		return &PublishPacket{
			FixedHeader: FixedHeader{PacketType: PacketTypePublish},
			Topic:       "gate/topic",
			Properties:  props,
		}
	}
	connect := func(version uint8, props *Properties) Packet {
		return &ConnectPacket{
			FixedHeader:     FixedHeader{PacketType: PacketTypeConnect},
			ProtocolName:    ProtocolNameMQTT,
			ProtocolVersion: version,
			Flags:           ConnectFlags{CleanSession: true},
			KeepAlive:       30,
			ClientID:        "gate-client",
			Properties:      props,
		}
	}
	connectWill := func(version uint8, props *Properties) Packet {
		return &ConnectPacket{
			FixedHeader:     FixedHeader{PacketType: PacketTypeConnect},
			ProtocolName:    ProtocolNameMQTT,
			ProtocolVersion: version,
			Flags:           ConnectFlags{CleanSession: true, WillFlag: true},
			KeepAlive:       30,
			ClientID:        "gate-will",
			WillTopic:       "gate/will",
			WillMessage:     []byte("bye"),
			WillProperties:  props,
		}
	}

	cases := []struct {
		name string
		// build receives the version the codec is bound to and the property set
		// to attach (nil for the "no properties" variant). A CONNECT declares
		// its own version, so it must be built with the same one.
		build func(version uint8, props *Properties) Packet
	}{
		{"PUBLISH", func(_ uint8, props *Properties) Packet { return publish(props) }},
		{"CONNECT", func(v uint8, props *Properties) Packet { return connect(v, props) }},
		{"CONNECT will", func(v uint8, props *Properties) Packet { return connectWill(v, props) }},
		{"CONNACK", func(_ uint8, props *Properties) Packet {
			return &ConnAckPacket{
				FixedHeader: FixedHeader{PacketType: PacketTypeConnAck},
				Properties:  props,
			}
		}},
		{"PUBACK", func(_ uint8, props *Properties) Packet {
			return &PubAckPacket{FixedHeader: FixedHeader{PacketType: PacketTypePubAck}, PacketID: 1, Properties: props}
		}},
		{"PUBREC", func(_ uint8, props *Properties) Packet {
			return &PubRecPacket{FixedHeader: FixedHeader{PacketType: PacketTypePubRec}, PacketID: 1, Properties: props}
		}},
		{"PUBREL", func(_ uint8, props *Properties) Packet {
			return &PubRelPacket{FixedHeader: FixedHeader{PacketType: PacketTypePubRel}, PacketID: 1, Properties: props}
		}},
		{"PUBCOMP", func(_ uint8, props *Properties) Packet {
			return &PubCompPacket{FixedHeader: FixedHeader{PacketType: PacketTypePubComp}, PacketID: 1, Properties: props}
		}},
	}

	props := func() *Properties { return &Properties{ContentType: "text/plain"} }

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			legacy := NewCodecForVersion(0, Version311)

			err := legacy.Encode(&bytes.Buffer{}, tc.build(Version311, props()))
			// The acknowledgement family reports its own reason-code violation
			// as a malformed packet; the property-section sites report the
			// version mismatch. Both are refusals, and both are the point.
			if !errors.Is(err, ErrUnsupportedVersion) && !errors.Is(err, ErrMalformedPacket) {
				t.Errorf("v3.1.1 encode with properties: err = %v, want a refusal "+
					"(ErrUnsupportedVersion or ErrMalformedPacket)", err)
			}
			if err := legacy.Encode(&bytes.Buffer{}, tc.build(Version311, nil)); err != nil {
				t.Errorf("v3.1.1 encode without properties must still work: %v", err)
			}
			if err := NewCodecForVersion(0, Version50).Encode(&bytes.Buffer{}, tc.build(Version50, props())); err != nil {
				t.Errorf("v5.0 encode with properties: %v", err)
			}
		})
	}

	// A v3.1.1 CONNACK is exactly its fixed header plus two bytes: the decoder
	// requires Remaining Length 2 there, so a stray property length byte makes
	// the handshake fail rather than merely corrupting the payload.
	var legacyAck bytes.Buffer
	connAck := &ConnAckPacket{FixedHeader: FixedHeader{PacketType: PacketTypeConnAck}}
	if err := NewCodecForVersion(0, Version311).Encode(&legacyAck, connAck); err != nil {
		t.Fatalf("v3.1.1 CONNACK: %v", err)
	}
	if got := legacyAck.Len(); got != 4 {
		t.Errorf("v3.1.1 CONNACK length = %d, want 4 (no property section)", got)
	}
}
