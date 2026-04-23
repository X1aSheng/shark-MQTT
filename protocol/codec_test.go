package protocol

import (
	"bytes"
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
		KeepAlive: 30,
		ClientID:  "will-client",
		WillTopic: "last/will",
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

func TestConnectWithAuth(t *testing.T) {
	pkt := &ConnectPacket{
		FixedHeader: FixedHeader{
			PacketType: PacketTypeConnect,
		},
		ProtocolName:    "MQTT",
		ProtocolVersion: Version311,
		Flags: ConnectFlags{
			CleanSession:  true,
			UsernameFlag:  true,
			PasswordFlag:  true,
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
