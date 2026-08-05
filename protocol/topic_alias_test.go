package protocol

import (
	"bytes"
	"testing"
)

// TestPublishEmptyTopicWithAliasV5 verifies a MQTT 5.0 PUBLISH with an empty
// Topic Name and a non-zero Topic Alias encodes and decodes (MQTT 5.0
// §3.3.2.3.4). Previously the codec rejected empty topics outright, making
// the broker's advertised Topic Alias support unusable.
func TestPublishEmptyTopicWithAliasV5(t *testing.T) {
	codec := NewCodec(0)
	codec.protocolVersion = Version50
	alias := uint16(7)

	pkt := &PublishPacket{
		FixedHeader: FixedHeader{
			PacketType: PacketTypePublish,
			QoS:        1,
		},
		PacketID: 1,
		// Topic intentionally empty: resolved via Topic Alias.
		Payload: []byte("hello"),
		Properties: &Properties{
			TopicAlias: &alias,
		},
	}

	var buf bytes.Buffer
	if err := codec.Encode(&buf, pkt); err != nil {
		t.Fatalf("encode empty-topic publish: %v", err)
	}

	dec := NewCodec(0)
	dec.protocolVersion = Version50
	got, err := dec.Decode(&buf)
	if err != nil {
		t.Fatalf("decode empty-topic publish: %v", err)
	}
	gotPub, ok := got.(*PublishPacket)
	if !ok {
		t.Fatalf("decoded packet = %T, want *PublishPacket", got)
	}
	if gotPub.Topic != "" {
		t.Fatalf("decoded topic = %q, want empty", gotPub.Topic)
	}
	if gotPub.Properties == nil || gotPub.Properties.TopicAlias == nil || *gotPub.Properties.TopicAlias != 7 {
		t.Fatalf("decoded TopicAlias not preserved: %+v", gotPub.Properties)
	}
	if string(gotPub.Payload) != "hello" {
		t.Fatalf("decoded payload = %q, want hello", gotPub.Payload)
	}
}

// TestPublishEmptyTopicWithoutAliasRejected verifies an empty Topic Name
// without a Topic Alias is still rejected for both protocol versions.
func TestPublishEmptyTopicWithoutAliasRejected(t *testing.T) {
	for _, ver := range []uint8{Version31, Version50} {
		codec := NewCodec(0)
		codec.protocolVersion = ver
		pkt := &PublishPacket{
			FixedHeader: FixedHeader{PacketType: PacketTypePublish},
			Payload:     []byte("x"),
		}
		var buf bytes.Buffer
		if err := codec.Encode(&buf, pkt); err != ErrMalformedPacket {
			t.Fatalf("v%d encode empty topic without alias error = %v, want %v", ver, err, ErrMalformedPacket)
		}
	}
}
