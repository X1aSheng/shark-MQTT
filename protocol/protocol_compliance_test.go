package protocol

import (
	"bytes"
	"errors"
	"testing"
)

func mqtt5Codec() *Codec {
	codec := NewCodec(256 * 1024)
	codec.protocolVersion = Version50
	return codec
}

func TestMQTT5AckPacketsRoundTripWithProperties(t *testing.T) {
	reason := byte(ReasonCodeUnspecifiedError)
	props := &Properties{ReasonString: "not accepted"}

	tests := []struct {
		name   string
		packet Packet
		check  func(t *testing.T, packet Packet)
	}{
		{
			name: "PUBACK",
			packet: &PubAckPacket{
				FixedHeader: FixedHeader{PacketType: PacketTypePubAck},
				PacketID:    10,
				ReasonCode:  reason,
				Properties:  props,
			},
			check: func(t *testing.T, packet Packet) {
				p := packet.(*PubAckPacket)
				if p.PacketID != 10 || p.ReasonCode != reason || p.Properties.ReasonString != props.ReasonString {
					t.Fatalf("decoded PUBACK = %#v", p)
				}
			},
		},
		{
			name: "PUBREC",
			packet: &PubRecPacket{
				FixedHeader: FixedHeader{PacketType: PacketTypePubRec},
				PacketID:    11,
				ReasonCode:  reason,
				Properties:  props,
			},
			check: func(t *testing.T, packet Packet) {
				p := packet.(*PubRecPacket)
				if p.PacketID != 11 || p.ReasonCode != reason || p.Properties.ReasonString != props.ReasonString {
					t.Fatalf("decoded PUBREC = %#v", p)
				}
			},
		},
		{
			name: "PUBREL",
			packet: &PubRelPacket{
				FixedHeader: FixedHeader{PacketType: PacketTypePubRel},
				PacketID:    12,
				ReasonCode:  reason,
				Properties:  props,
			},
			check: func(t *testing.T, packet Packet) {
				p := packet.(*PubRelPacket)
				if p.PacketID != 12 || p.ReasonCode != reason || p.Properties.ReasonString != props.ReasonString {
					t.Fatalf("decoded PUBREL = %#v", p)
				}
			},
		},
		{
			name: "PUBCOMP",
			packet: &PubCompPacket{
				FixedHeader: FixedHeader{PacketType: PacketTypePubComp},
				PacketID:    13,
				ReasonCode:  reason,
				Properties:  props,
			},
			check: func(t *testing.T, packet Packet) {
				p := packet.(*PubCompPacket)
				if p.PacketID != 13 || p.ReasonCode != reason || p.Properties.ReasonString != props.ReasonString {
					t.Fatalf("decoded PUBCOMP = %#v", p)
				}
			},
		},
		{
			name: "UNSUBACK",
			packet: &UnsubAckPacket{
				FixedHeader: FixedHeader{PacketType: PacketTypeUnsubAck},
				PacketID:    14,
				ReasonCodes: []byte{ReasonCodeSuccess, ReasonCodeTopicFilterInvalid},
				Properties:  props,
			},
			check: func(t *testing.T, packet Packet) {
				p := packet.(*UnsubAckPacket)
				if p.PacketID != 14 || len(p.ReasonCodes) != 2 || p.Properties.ReasonString != props.ReasonString {
					t.Fatalf("decoded UNSUBACK = %#v", p)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codec := mqtt5Codec()
			var buf bytes.Buffer
			if err := codec.Encode(&buf, tt.packet); err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			decoded, err := codec.Decode(&buf)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			tt.check(t, decoded)
		})
	}
}

func TestMQTT311RejectsMQTT5OnlyAckFieldsAndAuth(t *testing.T) {
	codec := NewCodec(256 * 1024)

	var buf bytes.Buffer
	err := codec.Encode(&buf, &PubAckPacket{
		FixedHeader: FixedHeader{PacketType: PacketTypePubAck},
		PacketID:    1,
		ReasonCode:  ReasonCodeUnspecifiedError,
	})
	if !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("Encode(PUBACK with reason) error = %v, want %v", err, ErrMalformedPacket)
	}

	_, err = codec.Decode(bytes.NewReader([]byte{0x40, 0x03, 0x00, 0x01, ReasonCodeUnspecifiedError}))
	if !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("Decode(PUBACK with reason) error = %v, want %v", err, ErrMalformedPacket)
	}

	_, err = codec.Decode(bytes.NewReader([]byte{0xF0, 0x00}))
	if !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("Decode(AUTH in MQTT 3.1.1) error = %v, want %v", err, ErrInvalidPacket)
	}
}

func TestMQTT5AuthReasonCodes(t *testing.T) {
	codec := mqtt5Codec()

	var success bytes.Buffer
	if err := codec.Encode(&success, &AuthPacket{FixedHeader: FixedHeader{PacketType: PacketTypeAuth}, ReasonCode: AuthSuccess}); err != nil {
		t.Fatalf("Encode(success AUTH): %v", err)
	}
	if got := success.Bytes(); !bytes.Equal(got, []byte{0xF0, 0x00}) {
		t.Fatalf("success AUTH bytes = % X, want F0 00", got)
	}

	var cont bytes.Buffer
	if err := codec.Encode(&cont, &AuthPacket{
		FixedHeader: FixedHeader{PacketType: PacketTypeAuth},
		ReasonCode:  AuthContinueAuth,
		Properties:  &Properties{AuthenticationMethod: "scram"},
	}); err != nil {
		t.Fatalf("Encode(continue AUTH): %v", err)
	}
	decoded, err := codec.Decode(&cont)
	if err != nil {
		t.Fatalf("Decode(continue AUTH): %v", err)
	}
	auth := decoded.(*AuthPacket)
	if auth.ReasonCode != AuthContinueAuth || auth.Properties.AuthenticationMethod != "scram" {
		t.Fatalf("decoded AUTH = %#v", auth)
	}

	if err := codec.Encode(&bytes.Buffer{}, &AuthPacket{FixedHeader: FixedHeader{PacketType: PacketTypeAuth}, ReasonCode: 0x7F}); !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("Encode(invalid AUTH) error = %v, want %v", err, ErrMalformedPacket)
	}
}

func TestPropertiesValidateMQTT5ScalarConstraints(t *testing.T) {
	codec := mqtt5Codec()

	invalidByte := byte(2)
	zeroUint16 := uint16(0)
	zeroUint32 := uint32(0)

	tests := []struct {
		name  string
		props *Properties
	}{
		{name: "payload format", props: &Properties{PayloadFormat: &invalidByte}},
		{name: "request problem info", props: &Properties{RequestProblemInfo: &invalidByte}},
		{name: "request response info", props: &Properties{RequestResponseInfo: &invalidByte}},
		{name: "receive maximum", props: &Properties{ReceiveMaximum: &zeroUint16}},
		{name: "topic alias", props: &Properties{TopicAlias: &zeroUint16}},
		{name: "maximum qos", props: &Properties{MaximumQoS: &invalidByte}},
		{name: "retain available", props: &Properties{RetainAvailable: &invalidByte}},
		{name: "maximum packet size", props: &Properties{MaximumPacketSize: &zeroUint32}},
		{name: "wildcard available", props: &Properties{WildcardSubAvailable: &invalidByte}},
		{name: "subscription id available", props: &Properties{SubIDAvailable: &invalidByte}},
		{name: "shared subscription available", props: &Properties{SharedSubAvailable: &invalidByte}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := codec.encodeProperties(&bytes.Buffer{}, tt.props)
			if !errors.Is(err, ErrMalformedPacket) {
				t.Fatalf("encodeProperties() error = %v, want %v", err, ErrMalformedPacket)
			}
		})
	}

	_, err := codec.decodeProperties(bytes.NewReader([]byte{0x02, PropMaximumQoS, 0x02}))
	if !errors.Is(err, ErrMalformedPacket) {
		t.Fatalf("decode invalid MaximumQoS error = %v, want %v", err, ErrMalformedPacket)
	}

	_, err = codec.decodeProperties(bytes.NewReader([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0x7F}))
	if err == nil {
		t.Fatal("expected overlong property length varint to fail")
	}
}

func TestMQTT5RequestResponsePropertiesRoundTrip(t *testing.T) {
	codec := mqtt5Codec()

	requestInfo := byte(1)
	responseInfo := "response/aBc123"

	// Test RequestResponseInfo in CONNECT properties
	connPkt := &ConnectPacket{
		FixedHeader:     FixedHeader{PacketType: PacketTypeConnect},
		ProtocolName:    ProtocolNameMQTT,
		ProtocolVersion: Version50,
		Flags:           ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "test-req-resp",
		Properties: &Properties{
			RequestResponseInfo: &requestInfo,
		},
	}

	var buf bytes.Buffer
	if err := codec.Encode(&buf, connPkt); err != nil {
		t.Fatalf("Encode CONNECT with RequestResponseInfo: %v", err)
	}

	decoded, err := codec.Decode(&buf)
	if err != nil {
		t.Fatalf("Decode CONNECT: %v", err)
	}
	cp := decoded.(*ConnectPacket)
	if cp.Properties == nil || cp.Properties.RequestResponseInfo == nil || *cp.Properties.RequestResponseInfo != 1 {
		t.Fatalf("RequestResponseInfo not decoded correctly: %+v", cp.Properties)
	}

	// Test ResponseInfo in CONNACK encoding
	connAckPkt := &ConnAckPacket{
		FixedHeader:    FixedHeader{PacketType: PacketTypeConnAck},
		ReasonCode:     ConnAckAccepted,
		SessionPresent: false,
		Properties: &Properties{
			ResponseInfo: responseInfo,
		},
	}

	buf.Reset()
	if err := codec.Encode(&buf, connAckPkt); err != nil {
		t.Fatalf("Encode CONNACK with ResponseInfo: %v", err)
	}

	decoded2, err := codec.Decode(&buf)
	if err != nil {
		t.Fatalf("Decode CONNACK: %v", err)
	}
	ca := decoded2.(*ConnAckPacket)
	if ca.Properties == nil || ca.Properties.ResponseInfo != responseInfo {
		t.Fatalf("ResponseInfo not decoded correctly: %+v", ca.Properties)
	}
}

func TestEncodeRejectsMalformedPacketIdentifiersAndTopics(t *testing.T) {
	codec := NewCodec(256 * 1024)

	tests := []struct {
		name   string
		packet Packet
	}{
		{
			name: "QoS 0 publish with packet id",
			packet: &PublishPacket{
				FixedHeader: FixedHeader{PacketType: PacketTypePublish},
				Topic:       "a/b",
				PacketID:    1,
			},
		},
		{
			name: "QoS 1 publish without packet id",
			packet: &PublishPacket{
				FixedHeader: FixedHeader{PacketType: PacketTypePublish, QoS: 1},
				Topic:       "a/b",
			},
		},
		{
			name: "publish wildcard topic",
			packet: &PublishPacket{
				FixedHeader: FixedHeader{PacketType: PacketTypePublish},
				Topic:       "a/+",
			},
		},
		{
			name: "subscribe empty filters",
			packet: &SubscribePacket{
				FixedHeader: FixedHeader{PacketType: PacketTypeSubscribe},
				PacketID:    1,
			},
		},
		{
			name: "unsubscribe empty filters",
			packet: &UnsubscribePacket{
				FixedHeader: FixedHeader{PacketType: PacketTypeUnsubscribe},
				PacketID:    1,
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
