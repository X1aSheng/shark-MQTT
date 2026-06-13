package protocol

import (
	"bytes"
	"testing"

	"github.com/X1aSheng/shark-mqtt/pkg/bufferpool"
)

// ─── Topic function tests ──────────────────────────────────────

func TestValidatePublishTopic(t *testing.T) {
	tests := []struct {
		topic string
		want  bool
	}{
		{"", false},
		{"sport/tennis/player1", true},
		{"sport/tennis/+", false},
		{"sport/tennis/#", false},
		{"+", false},
		{"#", false},
		{"a/b/c", true},
		{"/", true},
		{"$SYS/uptime", true},
	}
	for _, tt := range tests {
		if got := ValidatePublishTopic(tt.topic); got != tt.want {
			t.Errorf("ValidatePublishTopic(%q) = %v, want %v", tt.topic, got, tt.want)
		}
	}
}

func TestSplitTopic(t *testing.T) {
	tests := []struct {
		topic string
		want  []string
	}{
		{"sport/tennis/player1", []string{"sport", "tennis", "player1"}},
		{"", []string{""}},
		{"/", []string{"", ""}},
		{"a/b/c", []string{"a", "b", "c"}},
		{"single", []string{"single"}},
		{"a//c", []string{"a", "", "c"}},
		{"/leading", []string{"", "leading"}},
		{"trailing/", []string{"trailing", ""}},
		{"$SYS/broker/uptime", []string{"$SYS", "broker", "uptime"}},
	}
	for _, tt := range tests {
		got := SplitTopic(tt.topic)
		if len(got) != len(tt.want) {
			t.Errorf("SplitTopic(%q) = %v, len=%d want len=%d", tt.topic, got, len(got), len(tt.want))
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("SplitTopic(%q) = %v, want %v", tt.topic, got, tt.want)
				break
			}
		}
	}
}

func TestMatchTopic(t *testing.T) {
	tests := []struct {
		pattern string
		topic   string
		want    bool
	}{
		// Exact match
		{"a/b/c", "a/b/c", true},
		{"a/b/c", "a/b/d", false},
		{"a/b/c", "a/b", false},
		{"a/b/c", "a/b/c/d", false},

		// Single-level wildcard (+)
		{"a/+/c", "a/b/c", true},
		{"a/+/c", "a/x/c", true},
		{"a/+/c", "a/b/d", false},
		{"+/b/c", "x/b/c", true},
		{"a/b/+", "a/b/c", true},
		{"a/b/+", "a/b/c/d", false},
		{"+", "anything", true},
		{"+", "", true},     // empty string is a valid topic level
		{"+/+", "a/b", true},
		{"+/+", "a", false},

		// Multi-level wildcard (#)
		{"a/#", "a/b/c", true},
		{"a/#", "a/b", true},
		{"a/#", "a", true},
		{"a/#", "b", false},
		{"#", "anything/at/all", true},
		{"#", "", true},
		{"sport/tennis/#", "sport/tennis/player1/ranking", true},
		{"sport/tennis/#", "sport/tennis/player1", true},
		{"sport/tennis/#", "sport/other", false},

		// Mixed
		{"+/monitor/#", "sensor1/monitor/temp", true},
		{"+/monitor/#", "sensor1/monitor/temp/humidity", true},
		{"+/monitor/#", "sensor1/other/temp", false},

		// $SYS topics (should not be matched by # in some use cases, but per spec # still matches)
		// This is a broker-level enforcement, not topic matching
		{"#", "$SYS/uptime", true},
	}
	for _, tt := range tests {
		if got := MatchTopic(tt.pattern, tt.topic); got != tt.want {
			t.Errorf("MatchTopic(%q, %q) = %v, want %v", tt.pattern, tt.topic, got, tt.want)
		}
	}
}

func TestValidateTopicFilter(t *testing.T) {
	tests := []struct {
		filter string
		want   bool
	}{
		{"", false},
		{"sport/tennis/player1", true},
		{"sport/tennis/+", true},
		{"sport/tennis/#", true},
		{"#", true},
		{"+", true},
		{"+/#", true},
		{"sport/#", true},

		// Invalid: # with something after
		{"sport/#/extra", false},
		// Invalid: # not preceded by /
		{"sport#", false},
		// Invalid: + with something after within same level
		{"sport/tennis+/player1", false},
		// Invalid: + with something before within same level
		{"sport/ten+nis/player1", false},

		// Edge cases
		{"/", true},
		{"a//c", true},
		{"a/+/#", true},
	}
	for _, tt := range tests {
		if got := ValidateTopicFilter(tt.filter); got != tt.want {
			t.Errorf("ValidateTopicFilter(%q) = %v, want %v", tt.filter, got, tt.want)
		}
	}
}

// ─── SetPool test ──────────────────────────────────────────────

func TestSetPool(t *testing.T) {
	codec := NewCodec(1024)

	// Default pool should be set
	if codec.pool == nil {
		t.Fatal("expected default pool to be set")
	}

	// Override with a custom pool
	pool := bufferpool.New(512)
	codec.SetPool(pool)
	if codec.pool != pool {
		t.Error("SetPool did not update the pool reference")
	}

	// Set to nil
	codec.SetPool(nil)
	if codec.pool != nil {
		t.Error("SetPool(nil) should set pool to nil")
	}
}

// ─── GetFixedHeader tests ──────────────────────────────────────

func TestGetFixedHeader(t *testing.T) {
	tests := []struct {
		name string
		pkt  Packet
	}{
		{"Connect", &ConnectPacket{FixedHeader: FixedHeader{PacketType: PacketTypeConnect}}},
		{"ConnAck", &ConnAckPacket{FixedHeader: FixedHeader{PacketType: PacketTypeConnAck}}},
		{"Publish", &PublishPacket{FixedHeader: FixedHeader{PacketType: PacketTypePublish}}},
		{"PubAck", &PubAckPacket{FixedHeader: FixedHeader{PacketType: PacketTypePubAck}}},
		{"PubRec", &PubRecPacket{FixedHeader: FixedHeader{PacketType: PacketTypePubRec}}},
		{"PubRel", &PubRelPacket{FixedHeader: FixedHeader{PacketType: PacketTypePubRel}}},
		{"PubComp", &PubCompPacket{FixedHeader: FixedHeader{PacketType: PacketTypePubComp}}},
		{"Subscribe", &SubscribePacket{FixedHeader: FixedHeader{PacketType: PacketTypeSubscribe}}},
		{"SubAck", &SubAckPacket{FixedHeader: FixedHeader{PacketType: PacketTypeSubAck}}},
		{"Unsubscribe", &UnsubscribePacket{FixedHeader: FixedHeader{PacketType: PacketTypeUnsubscribe}}},
		{"UnsubAck", &UnsubAckPacket{FixedHeader: FixedHeader{PacketType: PacketTypeUnsubAck}}},
		{"PingReq", &PingReqPacket{FixedHeader: FixedHeader{PacketType: PacketTypePingReq}}},
		{"PingResp", &PingRespPacket{FixedHeader: FixedHeader{PacketType: PacketTypePingResp}}},
		{"Disconnect", &DisconnectPacket{FixedHeader: FixedHeader{PacketType: PacketTypeDisconnect}}},
		{"Auth", &AuthPacket{FixedHeader: FixedHeader{PacketType: PacketTypeAuth}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fh := tt.pkt.GetFixedHeader()
			if fh == nil {
				t.Fatal("GetFixedHeader() returned nil")
			}
			if fh.PacketType == 0 {
				t.Error("GetFixedHeader().PacketType is zero (unset)")
			}
		})
	}
}

// ─── PingResp encode/decode round-trip ─────────────────────────

func TestPingRespEncodeDecode(t *testing.T) {
	pkt := &PingRespPacket{
		FixedHeader: FixedHeader{
			PacketType: PacketTypePingResp,
		},
	}

	codec := NewCodec(1024)
	var buf bytes.Buffer

	if err := codec.Encode(&buf, pkt); err != nil {
		t.Fatalf("encode error: %v", err)
	}

	if buf.Len() == 0 {
		t.Fatal("expected non-empty encoded PINGRESP")
	}

	decoded, err := codec.Decode(&buf)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if _, ok := decoded.(*PingRespPacket); !ok {
		t.Fatalf("expected *PingRespPacket, got %T", decoded)
	}
}

// ─── Disconnect encode with MQTT 5.0 properties ────────────────

func TestDisconnectEncodeDecode_WithAndWithoutProps(t *testing.T) {
	tests := []struct {
		name string
		pkt  *DisconnectPacket
	}{
		{
			name: "no properties",
			pkt: &DisconnectPacket{
				FixedHeader: FixedHeader{PacketType: PacketTypeDisconnect},
				ReasonCode:  0,
			},
		},
		{
			name: "with reason code",
			pkt: &DisconnectPacket{
				FixedHeader: FixedHeader{PacketType: PacketTypeDisconnect},
				ReasonCode:  0x84, // Session taken over
			},
		},
		{
			name: "with session expiry",
			pkt: &DisconnectPacket{
				FixedHeader: FixedHeader{PacketType: PacketTypeDisconnect},
				ReasonCode:  0,
				Properties: &Properties{
					SessionExpiryInterval: func() *uint32 { v := uint32(3600); return &v }(),
				},
			},
		},
	}

	codec := NewCodec(1024)
	// Set protocol version to 5.0 to allow non-zero reason codes and properties
	codec.protocolVersion = Version50
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := codec.Encode(&buf, tt.pkt); err != nil {
				t.Fatalf("encode error: %v", err)
			}
			if buf.Len() == 0 {
				t.Fatal("expected non-empty encoded DISCONNECT")
			}

			decoded, err := codec.Decode(&buf)
			if err != nil {
				t.Fatalf("decode error: %v", err)
			}
			dp, ok := decoded.(*DisconnectPacket)
			if !ok {
				t.Fatalf("expected *DisconnectPacket, got %T", decoded)
			}
			if dp.ReasonCode != tt.pkt.ReasonCode {
				t.Errorf("ReasonCode = 0x%02x, want 0x%02x", dp.ReasonCode, tt.pkt.ReasonCode)
			}
		})
	}
}

// ─── NewCodec edge cases ───────────────────────────────────────

func TestNewCodecDefaults(t *testing.T) {
	// Zero max packet size should use default
	c := NewCodec(0)
	if c.maxPacketSize != 256*1024 {
		t.Errorf("expected default max packet size 256KB, got %d", c.maxPacketSize)
	}

	// Negative should also use default
	c = NewCodec(-1)
	if c.maxPacketSize != 256*1024 {
		t.Errorf("expected default max packet size for negative, got %d", c.maxPacketSize)
	}

	// Explicit value should be preserved
	c = NewCodec(65536)
	if c.maxPacketSize != 65536 {
		t.Errorf("expected 65536, got %d", c.maxPacketSize)
	}
}

// ─── PacketType String completeness ────────────────────────────

func TestPacketTypeString_AllTypes(t *testing.T) {
	tests := []struct {
		pt   PacketType
		want string
	}{
		{PacketTypeReserved, "UNKNOWN(0)"},
		{PacketTypeConnect, "CONNECT"},
		{PacketTypeConnAck, "CONNACK"},
		{PacketTypePublish, "PUBLISH"},
		{PacketTypePubAck, "PUBACK"},
		{PacketTypePubRec, "PUBREC"},
		{PacketTypePubRel, "PUBREL"},
		{PacketTypePubComp, "PUBCOMP"},
		{PacketTypeSubscribe, "SUBSCRIBE"},
		{PacketTypeSubAck, "SUBACK"},
		{PacketTypeUnsubscribe, "UNSUBSCRIBE"},
		{PacketTypeUnsubAck, "UNSUBACK"},
		{PacketTypePingReq, "PINGREQ"},
		{PacketTypePingResp, "PINGRESP"},
		{PacketTypeDisconnect, "DISCONNECT"},
		{PacketTypeAuth, "AUTH"},
		{PacketType(99), "UNKNOWN(99)"},
		{PacketType(255), "UNKNOWN(255)"},
	}
	for _, tt := range tests {
		if got := tt.pt.String(); got != tt.want {
			t.Errorf("PacketType(%d).String() = %q, want %q", tt.pt, got, tt.want)
		}
	}
}
