package protocol

import (
	"bytes"
	"errors"
	"testing"
)

// --- Malformed packet decoding (truncated/invalid inputs) ---

func TestDecodeRejectsTruncatedPackets(t *testing.T) {
	// Each case simulates a truncated or malformed MQTT packet.
	tests := []struct {
		name string
		raw  []byte
	}{
		// CONNECT with empty payload (no protocol name, version, etc.)
		{name: "connect empty", raw: []byte{0x10, 0x00}},
		// CONNECT truncated after protocol name length byte
		{name: "connect truncated after proto name len", raw: []byte{0x10, 0x0A, 0x00}},
		// PUBLISH QoS 1 missing packet ID
		{name: "qos1 publish missing packet id", raw: []byte{0x32, 0x04, 0x00, 0x02, 'a', '/'}},
		// PUBLISH truncated topic
		{name: "publish truncated topic", raw: []byte{0x30, 0x05, 0x00, 0x0A}},
		// SUBSCRIBE missing topic list
		{name: "subscribe missing topic list", raw: []byte{0x82, 0x02, 0x00, 0x01}},
		// UNSUBSCRIBE missing topic list
		{name: "unsubscribe missing topic list", raw: []byte{0xA2, 0x02, 0x00, 0x01}},
		// PUBACK missing packet ID
		{name: "puback missing packet id", raw: []byte{0x40, 0x01, 0x00}},
		// PUBREL missing packet ID
		{name: "pubrel missing packet id", raw: []byte{0x62, 0x01, 0x00}},
		// Fixed header only (no remaining length bytes)
		{name: "no remaining length at all", raw: []byte{0x10}},
		// CONNECT with remaining length but zero payload data
		{name: "connect with zero remaining length", raw: []byte{0x10, 0x00}},
		// 3-byte remaining length but payload too short
		{name: "connect remlen 300 but only 20 bytes", raw: append(
			[]byte{0x10, 0xAC, 0x02},
			make([]byte, 20)...,
		)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codec := NewCodec(256 * 1024)
			_, err := codec.Decode(bytes.NewReader(tt.raw))
			if err == nil {
				t.Fatal("expected error for malformed input")
			}
		})
	}
}

// --- Invalid remaining length boundary ---

func TestDecodeRejectsOversizedRemainingLength(t *testing.T) {
	codec := NewCodec(256 * 1024)
	// Craft a fixed header claiming ~269MB remaining length (over spec limit)
	// 0xFF, 0xFF, 0xFF, 0x7F = 268435455 (the max allowed)
	// 0xFF, 0xFF, 0xFF, 0xFF, ... would be malformed, but the varint parser
	// only allows 4 bytes. 0x80, 0x80, 0x80, 0x80 would be > 4 bytes.
	// Let's test with a 5-byte varint that would overflow the decoder.
	raw := []byte{0x10, 0x80, 0x80, 0x80, 0x80, 0x01}
	_, err := codec.Decode(bytes.NewReader(raw))
	if err == nil {
		t.Fatal("expected error for overlong varint")
	}
}

// --- Invalid UTF-8 strings ---

func TestDecodeRejectsInvalidUTF8(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{
			name: "invalid utf8 in client id",
			// CONNECT with client ID containing invalid UTF-8 byte 0xFF
			raw: []byte{
				0x10, 0x15, // fixed header
				0x00, 0x04, 'M', 'Q', 'T', 'T', // protocol name
				0x04,       // version 3.1.1
				0x02,       // clean session
				0x00, 0x3C, // keepalive 60
				0x00, 0x03, 0xFF, 0xFF, 0xFF, // client id length 3, invalid bytes
			},
		},
		{
			name: "null byte in topic",
			// PUBLISH with topic containing U+0000
			raw: []byte{
				0x30, 0x07, // fixed header
				0x00, 0x03, 0x00, 0x00, 'a', // topic containing null
				'x', // payload
			},
		},
		{
			name: "control character in string",
			// PUBLISH with topic containing U+001F (control char)
			raw: []byte{
				0x30, 0x06,
				0x00, 0x02, 0x00, 0x1F, // topic: \x00\x1f
				'x',
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codec := NewCodec(256 * 1024)
			_, err := codec.Decode(bytes.NewReader(tt.raw))
			if err == nil {
				t.Fatal("expected error for invalid UTF-8")
			}
		})
	}
}

// --- ValidateConnect edge cases ---

func TestValidateConnectEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		pkt  *ConnectPacket
	}{
		{
			name: "zero client id without clean session",
			pkt: &ConnectPacket{
				ProtocolName:    ProtocolNameMQTT,
				ProtocolVersion: Version311,
				Flags:           ConnectFlags{CleanSession: false},
				ClientID:        "",
			},
		},
		{
			name: "password without username",
			pkt: &ConnectPacket{
				ProtocolName:    ProtocolNameMQTT,
				ProtocolVersion: Version311,
				Flags:           ConnectFlags{CleanSession: true, PasswordFlag: true},
				ClientID:        "client",
				Password:        []byte("pw"),
			},
		},
		{
			name: "reserved flag set",
			pkt: &ConnectPacket{
				ProtocolName:    ProtocolNameMQTT,
				ProtocolVersion: Version311,
				Flags:           ConnectFlags{CleanSession: true, Reserved: true},
				ClientID:        "client",
			},
		},
		{
			name: "will qos 3",
			pkt: &ConnectPacket{
				ProtocolName:    ProtocolNameMQTT,
				ProtocolVersion: Version311,
				Flags:           ConnectFlags{CleanSession: true, WillFlag: true, WillQoS: 3},
				ClientID:        "client",
				WillTopic:       "test",
			},
		},
		{
			name: "invalid protocol name",
			pkt: &ConnectPacket{
				ProtocolName:    "HTTP",
				ProtocolVersion: Version311,
				Flags:           ConnectFlags{CleanSession: true},
				ClientID:        "client",
			},
		},
		{
			name: "unsupported protocol version",
			pkt: &ConnectPacket{
				ProtocolName:    ProtocolNameMQTT,
				ProtocolVersion: 6,
				Flags:           ConnectFlags{CleanSession: true},
				ClientID:        "client",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConnect(tt.pkt)
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

// --- Topic validation edge cases ---

func TestTopicValidationBoundary(t *testing.T) {
	tests := []struct {
		name  string
		topic string
		valid bool
	}{
		{name: "simple topic", topic: "a/b", valid: true},
		{name: "single level", topic: "sensors", valid: true},
		{name: "leading slash", topic: "/a/b", valid: true},
		{name: "trailing slash", topic: "a/b/", valid: true},
		{name: "empty string", topic: "", valid: false},
		{name: "plus wildcard", topic: "a/+/c", valid: false},
		{name: "hash wildcard", topic: "a/#", valid: false},
		{name: "hash only", topic: "#", valid: false},
		{name: "plus only", topic: "+", valid: false},
		{name: "hash mid topic", topic: "a/#/c", valid: false},
		{name: "system topic", topic: "$SYS/broker/uptime", valid: true},
		{name: "long topic", topic: string(make([]byte, 65535)), valid: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidatePublishTopic(tt.topic)
			if got != tt.valid {
				t.Errorf("ValidatePublishTopic(%q) = %v, want %v", tt.topic, got, tt.valid)
			}
		})
	}
}

func TestTopicFilterValidationBoundary(t *testing.T) {
	tests := []struct {
		name   string
		filter string
		valid  bool
	}{
		{name: "simple filter", filter: "a/b", valid: true},
		{name: "single plus", filter: "+", valid: true},
		{name: "single hash", filter: "#", valid: true},
		{name: "plus in middle", filter: "a/+/c", valid: true},
		{name: "hash at end", filter: "a/b/#", valid: true},
		{name: "hash alone after slash", filter: "/#", valid: true},
		{name: "multiple plus", filter: "a/+/+/c", valid: true},
		{name: "empty string", filter: "", valid: false},
		{name: "hash not at end", filter: "a/#/c", valid: false},
		{name: "hash without preceding slash", filter: "a#", valid: false},
		{name: "plus without level boundary prefix", filter: "a+", valid: false},
		{name: "plus without level boundary suffix", filter: "+a", valid: false},
		{name: "system topic filter", filter: "$SYS/#", valid: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateTopicFilter(tt.filter)
			if got != tt.valid {
				t.Errorf("ValidateTopicFilter(%q) = %v, want %v", tt.filter, got, tt.valid)
			}
		})
	}
}

// --- Ack packet decode with extra trailing data ---

func TestDecodeAckRejectsExtraTrailingData(t *testing.T) {
	// MQTT 3.1.1 ACK packets must be exactly 4 bytes (fixed header + 2-byte packet ID).
	// Extra bytes after the valid packet should be rejected.
	codec := NewCodec(256 * 1024)

	raw := []byte{0x40, 0x03, 0x00, 0x01, 0xFF}
	_, err := codec.Decode(bytes.NewReader(raw))
	if !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("Decode(PUBACK with trailing byte) error = %v, want %v", err, ErrMalformedPacket)
	}

	rawPubRel := []byte{0x62, 0x03, 0x00, 0x01, 0xFF}
	_, err = codec.Decode(bytes.NewReader(rawPubRel))
	if !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("Decode(PUBREL with trailing byte) error = %v, want %v", err, ErrMalformedPacket)
	}
}

// --- EncodeRejectsMalformedPacketIdentifiersAndTopics additional cases ---

func TestEncodeRejectsAdditionalMalformed(t *testing.T) {
	codec := NewCodec(256 * 1024)

	tests := []struct {
		name   string
		packet Packet
	}{
		{
			name: "puback zero packet id",
			packet: &PubAckPacket{
				FixedHeader: FixedHeader{PacketType: PacketTypePubAck},
				PacketID:    0,
			},
		},
		{
			name: "pubrec zero packet id",
			packet: &PubRecPacket{
				FixedHeader: FixedHeader{PacketType: PacketTypePubRec},
				PacketID:    0,
			},
		},
		{
			name: "pubrel zero packet id",
			packet: &PubRelPacket{
				FixedHeader: FixedHeader{PacketType: PacketTypePubRel},
				PacketID:    0,
			},
		},
		{
			name: "pubcomp zero packet id",
			packet: &PubCompPacket{
				FixedHeader: FixedHeader{PacketType: PacketTypePubComp},
				PacketID:    0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := codec.Encode(&bytes.Buffer{}, tt.packet)
			if !errors.Is(err, ErrMalformedPacket) {
				t.Fatalf("Encode() error = %v, want %v", err, ErrMalformedPacket)
			}
		})
	}
}

// --- Decode AUTH in MQTT 3.1.1 (already tested but as part of another test) ---

// --- Zero-length payload publish round-trip ---

func TestPublishZeroLengthPayload(t *testing.T) {
	codec := NewCodec(256 * 1024)

	pkt := &PublishPacket{
		FixedHeader: FixedHeader{
			PacketType: PacketTypePublish,
			QoS:        1,
		},
		Topic:    "status/online",
		PacketID: 5,
	}

	var buf bytes.Buffer
	if err := codec.Encode(&buf, pkt); err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	decoded, err := codec.Decode(&buf)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	pp := decoded.(*PublishPacket)
	if len(pp.Payload) != 0 {
		t.Errorf("payload length = %d, want 0", len(pp.Payload))
	}
	if pp.Topic != "status/online" {
		t.Errorf("topic = %q", pp.Topic)
	}
}

// --- MQTT 5.0 SUBSCRIBE with properties round-trip ---

func TestMQTT5SubscribeWithProperties(t *testing.T) {
	codec := mqtt5Codec()

	subID := uint32(42)
	pkt := &SubscribePacket{
		FixedHeader: FixedHeader{PacketType: PacketTypeSubscribe},
		PacketID:    99,
		Topics: []TopicFilter{
			{Topic: "sensor/+/temp", QoS: 2},
		},
		Properties: &Properties{
			SubscriptionIdentifier: &subID,
		},
	}

	var buf bytes.Buffer
	if err := codec.Encode(&buf, pkt); err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	decoded, err := codec.Decode(&buf)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	sp := decoded.(*SubscribePacket)
	if sp.PacketID != 99 {
		t.Errorf("packet id = %d, want 99", sp.PacketID)
	}
	if len(sp.Topics) != 1 || sp.Topics[0].QoS != 2 {
		t.Fatalf("topics = %+v", sp.Topics)
	}
	if sp.Properties == nil || sp.Properties.SubscriptionIdentifier == nil || *sp.Properties.SubscriptionIdentifier != 42 {
		t.Fatalf("subscription identifier = %v", sp.Properties)
	}
}

// --- DISCONNECT with reason code and properties (MQTT 5.0) ---

func TestMQTT5DisconnectWithProperties(t *testing.T) {
	codec := mqtt5Codec()

	pkt := &DisconnectPacket{
		FixedHeader: FixedHeader{PacketType: PacketTypeDisconnect},
		ReasonCode:  DisconnectAdministrativeAction,
		Properties:  &Properties{ReasonString: "maintenance"},
	}

	var buf bytes.Buffer
	if err := codec.Encode(&buf, pkt); err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	decoded, err := codec.Decode(&buf)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	dp := decoded.(*DisconnectPacket)
	if dp.ReasonCode != DisconnectAdministrativeAction {
		t.Errorf("reason code = %d", dp.ReasonCode)
	}
	if dp.Properties == nil || dp.Properties.ReasonString != "maintenance" {
		t.Fatalf("properties = %+v", dp.Properties)
	}
}

// --- SUBACK with multiple reason codes (MQTT 5.0) ---

func TestMQTT5SubAckWithReasonCodes(t *testing.T) {
	codec := mqtt5Codec()

	pkt := &SubAckPacket{
		FixedHeader: FixedHeader{PacketType: PacketTypeSubAck},
		PacketID:    77,
		ReasonCodes: []byte{2, 0x80, 1},
		Properties:  &Properties{},
	}

	var buf bytes.Buffer
	if err := codec.Encode(&buf, pkt); err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	decoded, err := codec.Decode(&buf)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	sa := decoded.(*SubAckPacket)
	if sa.PacketID != 77 {
		t.Errorf("packet id = %d", sa.PacketID)
	}
	if len(sa.ReasonCodes) != 3 || sa.ReasonCodes[0] != 2 || sa.ReasonCodes[1] != 0x80 || sa.ReasonCodes[2] != 1 {
		t.Fatalf("reason codes = %v", sa.ReasonCodes)
	}
}
