package protocol

import (
	"bytes"
	"errors"
	"testing"
)

func TestPacketTypeString(t *testing.T) {
	tests := []struct {
		pt   PacketType
		want string
	}{
		{PacketTypeConnect, "CONNECT"},
		{PacketTypeConnAck, "CONNACK"},
		{PacketTypePublish, "PUBLISH"},
		{PacketTypeSubscribe, "SUBSCRIBE"},
		{PacketTypePingReq, "PINGREQ"},
		{PacketTypeDisconnect, "DISCONNECT"},
		{PacketTypeAuth, "AUTH"},
		{PacketType(99), "UNKNOWN(99)"},
	}

	for _, tt := range tests {
		if got := tt.pt.String(); got != tt.want {
			t.Errorf("PacketType(%d).String() = %q, want %q", tt.pt, got, tt.want)
		}
	}
}

func TestConnectEncodeDecode(t *testing.T) {
	pkt := &ConnectPacket{
		FixedHeader: FixedHeader{
			PacketType: PacketTypeConnect,
		},
		ProtocolName:    "MQTT",
		ProtocolVersion: Version311,
		Flags: ConnectFlags{
			CleanSession: true,
		},
		KeepAlive: 60,
		ClientID:  "test-client",
	}

	codec := NewCodec(256 * 1024)
	var buf bytes.Buffer

	if err := codec.Encode(&buf, pkt); err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := codec.Decode(&buf)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	cp, ok := decoded.(*ConnectPacket)
	if !ok {
		t.Fatalf("expected *ConnectPacket, got %T", decoded)
	}

	if cp.ProtocolName != "MQTT" {
		t.Errorf("protocol name: got %q, want MQTT", cp.ProtocolName)
	}
	if cp.ProtocolVersion != Version311 {
		t.Errorf("protocol version: got %d, want %d", cp.ProtocolVersion, Version311)
	}
	if cp.KeepAlive != 60 {
		t.Errorf("keep alive: got %d, want 60", cp.KeepAlive)
	}
	if cp.ClientID != "test-client" {
		t.Errorf("client ID: got %q, want test-client", cp.ClientID)
	}
}

func TestConnectWithWill(t *testing.T) {
	pkt := &ConnectPacket{
		FixedHeader: FixedHeader{
			PacketType: PacketTypeConnect,
		},
		ProtocolName:    "MQTT",
		ProtocolVersion: Version311,
		Flags: ConnectFlags{
			CleanSession:  true,
			WillFlag:      true,
			WillQoS:       1,
			WillTopicFlag: true,
		},
		KeepAlive:   30,
		ClientID:    "will-client",
		WillTopic:   "last/will",
		WillMessage: []byte("gone"),
	}

	codec := NewCodec(256 * 1024)
	var buf bytes.Buffer

	if err := codec.Encode(&buf, pkt); err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := codec.Decode(&buf)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	cp, ok := decoded.(*ConnectPacket)
	if !ok {
		t.Fatalf("expected *ConnectPacket, got %T", decoded)
	}

	if !cp.Flags.WillFlag {
		t.Error("will flag not set")
	}
	if cp.WillTopic != "last/will" {
		t.Errorf("will topic: got %q, want last/will", cp.WillTopic)
	}
	if string(cp.WillMessage) != "gone" {
		t.Errorf("will message: got %q, want gone", cp.WillMessage)
	}
}

func TestValidateConnectRejectsWillFlagsWithoutWillFlag(t *testing.T) {
	tests := []struct {
		name  string
		flags ConnectFlags
	}{
		{
			name: "will retain without will flag",
			flags: ConnectFlags{
				CleanSession: true,
				WillRetain:   true,
			},
		},
		{
			name: "will qos without will flag",
			flags: ConnectFlags{
				CleanSession: true,
				WillQoS:      1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConnect(&ConnectPacket{
				ProtocolName:    ProtocolNameMQTT,
				ProtocolVersion: Version311,
				Flags:           tt.flags,
				ClientID:        "client",
			})
			if err == nil {
				t.Fatal("expected invalid CONNECT flags to be rejected")
			}
		})
	}
}

func TestConnectWithAuth(t *testing.T) {
	pkt := &ConnectPacket{
		FixedHeader: FixedHeader{
			PacketType: PacketTypeConnect,
		},
		ProtocolName:    "MQTT",
		ProtocolVersion: Version311,
		Flags: ConnectFlags{
			CleanSession: true,
			UsernameFlag: true,
			PasswordFlag: true,
		},
		KeepAlive: 60,
		ClientID:  "auth-client",
		Username:  "admin",
		Password:  []byte("secret"),
	}

	codec := NewCodec(256 * 1024)
	var buf bytes.Buffer

	if err := codec.Encode(&buf, pkt); err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := codec.Decode(&buf)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	cp, ok := decoded.(*ConnectPacket)
	if !ok {
		t.Fatalf("expected *ConnectPacket, got %T", decoded)
	}

	if cp.Username != "admin" {
		t.Errorf("username: got %q, want admin", cp.Username)
	}
	if string(cp.Password) != "secret" {
		t.Errorf("password: got %q, want secret", cp.Password)
	}
}

func TestConnAckEncodeDecode(t *testing.T) {
	pkt := &ConnAckPacket{
		FixedHeader: FixedHeader{
			PacketType: PacketTypeConnAck,
		},
		ReasonCode:     ConnAckAccepted,
		SessionPresent: true,
	}

	codec := NewCodec(256 * 1024)
	var buf bytes.Buffer

	if err := codec.Encode(&buf, pkt); err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := codec.Decode(&buf)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	cap, ok := decoded.(*ConnAckPacket)
	if !ok {
		t.Fatalf("expected *ConnAckPacket, got %T", decoded)
	}

	if cap.ReasonCode != ConnAckAccepted {
		t.Errorf("reason code: got %d, want %d", cap.ReasonCode, ConnAckAccepted)
	}
	if !cap.SessionPresent {
		t.Error("session present not set")
	}
}

func TestPublishEncodeDecode(t *testing.T) {
	pkt := &PublishPacket{
		FixedHeader: FixedHeader{
			PacketType: PacketTypePublish,
			QoS:        1,
			Retain:     true,
		},
		PacketID: 42,
		Topic:    "test/topic",
		Payload:  []byte("hello world"),
	}

	codec := NewCodec(256 * 1024)
	var buf bytes.Buffer

	if err := codec.Encode(&buf, pkt); err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := codec.Decode(&buf)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	pp, ok := decoded.(*PublishPacket)
	if !ok {
		t.Fatalf("expected *PublishPacket, got %T", decoded)
	}

	if pp.Topic != "test/topic" {
		t.Errorf("topic: got %q, want test/topic", pp.Topic)
	}
	if string(pp.Payload) != "hello world" {
		t.Errorf("payload: got %q, want hello world", pp.Payload)
	}
	if pp.PacketID != 42 {
		t.Errorf("packet ID: got %d, want 42", pp.PacketID)
	}
}

func TestSubscribeEncodeDecode(t *testing.T) {
	pkt := &SubscribePacket{
		FixedHeader: FixedHeader{
			PacketType: PacketTypeSubscribe,
			QoS:        1,
		},
		PacketID: 100,
		Topics: []TopicFilter{
			{Topic: "test/#", QoS: 0},
			{Topic: "sensor/+/temp", QoS: 1},
		},
	}

	codec := NewCodec(256 * 1024)
	var buf bytes.Buffer

	if err := codec.Encode(&buf, pkt); err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := codec.Decode(&buf)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	sp, ok := decoded.(*SubscribePacket)
	if !ok {
		t.Fatalf("expected *SubscribePacket, got %T", decoded)
	}

	if sp.PacketID != 100 {
		t.Errorf("packet ID: got %d, want 100", sp.PacketID)
	}
	if len(sp.Topics) != 2 {
		t.Fatalf("topics count: got %d, want 2", len(sp.Topics))
	}
	if sp.Topics[0].Topic != "test/#" {
		t.Errorf("topic 0: got %q, want test/#", sp.Topics[0].Topic)
	}
	if sp.Topics[1].QoS != 1 {
		t.Errorf("topic 1 QoS: got %d, want 1", sp.Topics[1].QoS)
	}
}

func TestPingReqEncodeDecode(t *testing.T) {
	pkt := &PingReqPacket{
		FixedHeader: FixedHeader{
			PacketType: PacketTypePingReq,
		},
	}

	codec := NewCodec(256 * 1024)
	var buf bytes.Buffer

	if err := codec.Encode(&buf, pkt); err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := codec.Decode(&buf)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	_, ok := decoded.(*PingReqPacket)
	if !ok {
		t.Fatalf("expected *PingReqPacket, got %T", decoded)
	}
}

func TestDisconnectEncodeDecode(t *testing.T) {
	pkt := &DisconnectPacket{
		FixedHeader: FixedHeader{
			PacketType: PacketTypeDisconnect,
		},
		ReasonCode: 0x00,
	}

	codec := NewCodec(256 * 1024)
	var buf bytes.Buffer

	if err := codec.Encode(&buf, pkt); err != nil {
		t.Fatalf("encode error: %v", err)
	}

	decoded, err := codec.Decode(&buf)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	dp, ok := decoded.(*DisconnectPacket)
	if !ok {
		t.Fatalf("expected *DisconnectPacket, got %T", decoded)
	}

	if dp.ReasonCode != 0x00 {
		t.Errorf("reason code: got %d, want 0", dp.ReasonCode)
	}
}

func TestPacketTooLarge(t *testing.T) {
	codec := NewCodec(10) // 10 byte limit
	pkt := &PublishPacket{
		FixedHeader: FixedHeader{
			PacketType: PacketTypePublish,
		},
		Topic:   "test",
		Payload: make([]byte, 100),
	}

	var buf bytes.Buffer
	// Encode should succeed (encoder doesn't enforce max size)
	if err := codec.Encode(&buf, pkt); err != nil {
		t.Fatalf("encode error: %v", err)
	}

	// Decode should fail due to size limit
	_, err := codec.Decode(&buf)
	if err == nil {
		t.Fatal("expected error for large packet")
	}
	if err != ErrPacketTooLarge {
		t.Errorf("expected ErrPacketTooLarge, got %v", err)
	}
}

func TestDecodeRejectsInvalidFixedHeaderFlags(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		{
			name: "PINGREQ with QoS bit set",
			raw:  []byte{0xC2, 0x00},
		},
		{
			name: "PUBLISH with QoS 3",
			raw:  []byte{0x36, 0x08, 0x00, 0x04, 't', 'e', 's', 't', 0x00, 0x01},
		},
		{
			// 0b0001_1000: reserved nibble 0b1000 (DUP). Deriving the bitmask
			// from QoS and RETAIN alone used to let this through.
			name: "CONNECT with DUP bit set",
			raw:  []byte{0x18, 0x00},
		},
		{
			name: "PINGREQ with DUP bit set",
			raw:  []byte{0xC8, 0x00},
		},
		{
			name: "PUBACK with DUP bit set",
			raw:  []byte{0x48, 0x02, 0x00, 0x01},
		},
		{
			name: "SUBACK with RETAIN bit set",
			raw:  []byte{0x91, 0x03, 0x00, 0x01, 0x00},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codec := NewCodec(256 * 1024)
			_, err := codec.Decode(bytes.NewReader(tt.raw))
			if err == nil {
				t.Fatal("expected invalid fixed header flags to be rejected")
			}
			if err != ErrInvalidPacket {
				t.Fatalf("Decode() error = %v, want %v", err, ErrInvalidPacket)
			}
		})
	}
}

// TestDecodeAcceptsDupOnQoS2ControlPackets pins the deliberate leniency on the
// three packet types whose reserved nibble is 0b0010. Their DUP bit is not
// checked because the reference implementation sets it when it retransmits
// (smart-mqtt InflightQueue: PUB_REL_HEADER_DUP / SUBSCRIBE_HEADER_DUP /
// UNSUBSCRIBE_HEADER_DUP), so a strict check would drop those peers mid-flight.
// QoS and RETAIN on the same packets are still rejected above.
func TestDecodeAcceptsDupOnQoS2ControlPackets(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
	}{
		// 0x6A = 0b0110_1010: PUBREL, DUP set, QoS 1.
		{name: "PUBREL with DUP bit set", raw: []byte{0x6A, 0x02, 0x00, 0x01}},
		// 0x8A = 0b1000_1010: SUBSCRIBE, DUP set, QoS 1.
		{name: "SUBSCRIBE with DUP bit set", raw: []byte{0x8A, 0x06, 0x00, 0x01, 0x00, 0x01, 'a', 0x00}},
		// 0xAA = 0b1010_1010: UNSUBSCRIBE, DUP set, QoS 1.
		{name: "UNSUBSCRIBE with DUP bit set", raw: []byte{0xAA, 0x05, 0x00, 0x01, 0x00, 0x01, 'a'}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codec := NewCodec(256 * 1024)
			pkt, err := codec.Decode(bytes.NewReader(tt.raw))
			if err != nil {
				t.Fatalf("Decode() unexpected error: %v", err)
			}
			if pkt == nil {
				t.Fatal("Decode() returned nil packet")
			}
		})
	}
}

// TestEncodeRejectsInvalidFixedHeaderFlags is the encode-side twin of
// TestDecodeRejectsInvalidFixedHeaderFlags. Without the guard in Encode a wrong
// nibble is written straight to the socket and only the peer notices: the QoS 2
// PUBREC defect (QoS bit set on a packet whose reserved nibble is 0b0000)
// compiled, passed every unit test, and made the peer close the connection
// after the first QoS 2 delivery.
func TestEncodeRejectsInvalidFixedHeaderFlags(t *testing.T) {
	tests := []struct {
		name   string
		packet Packet
	}{
		{
			// The historical defect, reproduced on purpose.
			name:   "PUBREC with QoS bit set",
			packet: &PubRecPacket{FixedHeader: FixedHeader{PacketType: PacketTypePubRec, QoS: 1}, PacketID: 7},
		},
		{
			name:   "PUBREC with DUP bit set",
			packet: &PubRecPacket{FixedHeader: FixedHeader{PacketType: PacketTypePubRec, Dup: true}, PacketID: 7},
		},
		{
			name:   "PINGREQ with RETAIN bit set",
			packet: &PingReqPacket{FixedHeader: FixedHeader{PacketType: PacketTypePingReq, Retain: true}},
		},
		{
			// Forgetting the mandatory QoS 1 used to fail silently here.
			name:   "PUBREL with QoS 0",
			packet: &PubRelPacket{FixedHeader: FixedHeader{PacketType: PacketTypePubRel}, PacketID: 7},
		},
		{
			name:   "SUBSCRIBE with QoS 0",
			packet: &SubscribePacket{FixedHeader: FixedHeader{PacketType: PacketTypeSubscribe}, PacketID: 7, Topics: []TopicFilter{{Topic: "a"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewCodec(256*1024).Encode(&bytes.Buffer{}, tt.packet)
			if !errors.Is(err, ErrInvalidPacket) {
				t.Fatalf("Encode() error = %v, want %v", err, ErrInvalidPacket)
			}
		})
	}
}

// TestEncodeAcceptsSpecCompliantFixedHeaderFlags is the positive control for the
// guard above: it must leave the correct forms alone, including a retransmitted
// PUBREL that carries DUP -- the leniency locked in by
// TestDecodeAcceptsDupOnQoS2ControlPackets.
func TestEncodeAcceptsSpecCompliantFixedHeaderFlags(t *testing.T) {
	packets := []Packet{
		&PubRecPacket{FixedHeader: FixedHeader{PacketType: PacketTypePubRec}, PacketID: 7},
		&PingReqPacket{FixedHeader: FixedHeader{PacketType: PacketTypePingReq}},
		&PublishPacket{FixedHeader: FixedHeader{PacketType: PacketTypePublish, QoS: 1, Dup: true, Retain: true}, PacketID: 7, Topic: "a", Payload: []byte("x")},
		&PubRelPacket{FixedHeader: FixedHeader{PacketType: PacketTypePubRel, QoS: 1}, PacketID: 7},
		&PubRelPacket{FixedHeader: FixedHeader{PacketType: PacketTypePubRel, QoS: 1, Dup: true}, PacketID: 7},
		&SubscribePacket{FixedHeader: FixedHeader{PacketType: PacketTypeSubscribe, QoS: 1}, PacketID: 7, Topics: []TopicFilter{{Topic: "a"}}},
	}
	for i, p := range packets {
		if err := NewCodec(256*1024).Encode(&bytes.Buffer{}, p); err != nil {
			t.Errorf("Encode(packet %d, %T) unexpected error: %v", i, p, err)
		}
	}
}
