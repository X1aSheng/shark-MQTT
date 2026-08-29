package protocol

import (
	"bytes"
	"testing"
)

// seedPackets adds a representative valid packet of every type as fuzz seeds.
func seedPackets(f *testing.F) {
	seeds := [][]byte{
		{0x10, 0x0C, 0x00, 0x04, 'M', 'Q', 'T', 'T', 0x04, 0x02, 0x00, 0x3C, 0x00, 0x00},                                     // CONNECT v3.1.1
		{0x10, 0x12, 0x00, 0x04, 'M', 'Q', 'T', 'T', 0x05, 0x02, 0x00, 0x3C, 0x00, 0x00, 0x05, 0x24, 0x00, 0x00, 0x00, 0x00}, // CONNECT v5
		{0x20, 0x02, 0x00, 0x00},                                      // CONNACK
		{0x30, 0x08, 0x00, 0x03, 'a', '/', 'b', 'h', 'i'},             // PUBLISH qos0
		{0x32, 0x0A, 0x00, 0x03, 'a', '/', 'b', 0x00, 0x01, 'h', 'i'}, // PUBLISH qos1
		{0x40, 0x02, 0x00, 0x01},                                      // PUBACK
		{0x50, 0x02, 0x00, 0x01},                                      // PUBREC
		{0x62, 0x02, 0x00, 0x01},                                      // PUBREL
		{0x70, 0x02, 0x00, 0x01},                                      // PUBCOMP
		{0x82, 0x08, 0x00, 0x01, 0x00, 0x03, 'a', '/', 'b', 0x01},     // SUBSCRIBE
		{0x90, 0x03, 0x00, 0x01, 0x01},                                // SUBACK
		{0xA2, 0x07, 0x00, 0x01, 0x00, 0x03, 'a', '/', 'b'},           // UNSUBSCRIBE
		{0xB0, 0x02, 0x00, 0x01},                                      // UNSUBACK
		{0xC0, 0x00},                                                  // PINGREQ
		{0xD0, 0x00},                                                  // PINGRESP
		{0xE0, 0x00},                                                  // DISCONNECT
		{0xF0, 0x02, 0x00, 0x18},                                      // AUTH continue
		{0x30, 0x00},                                                  // PUBLISH empty (invalid: empty topic)
		{0x00, 0x00},                                                  // reserved packet type
		{0xFF, 0xFF, 0xFF, 0xFF, 0xFF},                                // garbage
		{0x10},                                                        // truncated CONNECT
		{0x12, 0x02, 0x00, 0x00},                                      // CONNECT invalid flags
	}
	for _, s := range seeds {
		f.Add(s)
	}
}

// FuzzDecodeNeverPanics feeds arbitrary bytes to the codec; the decoder must
// reject malformed input with an error and never panic (L-008).
func FuzzDecodeNeverPanics(f *testing.F) {
	seedPackets(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		codec := NewCodec(256 * 1024)
		r := bytes.NewReader(data)
		_, _ = codec.Decode(r) // must not panic
	})
}

// FuzzPublishRoundTrip fuzzes the PUBLISH encode→decode path with structured
// inputs: any valid encoding must decode back to the same topic/payload/qos,
// and malformed inputs must never panic.
func FuzzPublishRoundTrip(f *testing.F) {
	seeds := []struct {
		topic   string
		payload []byte
		qos     byte
		retain  bool
	}{
		{"a/b", []byte("hi"), 0, false},
		{"sport/tennis/player1", []byte(""), 1, false},
		{"$SYS/broker/uptime", []byte("5s"), 0, true},
		{"/finance", []byte{0x00, 0x01, 0xFF}, 2, true},
	}
	for _, s := range seeds {
		f.Add(s.topic, s.payload, s.qos, s.retain)
	}
	f.Fuzz(func(t *testing.T, topic string, payload []byte, qos byte, retain bool) {
		if qos > 2 {
			return // QoS 3 is invalid; the encoder must reject it
		}
		pkt := &PublishPacket{
			FixedHeader: FixedHeader{PacketType: PacketTypePublish, QoS: qos, Retain: retain},
			Topic:       topic,
			Payload:     payload,
		}
		if qos > 0 {
			pkt.PacketID = 1
		}
		var buf bytes.Buffer
		codec := NewCodec(256 * 1024)
		codec.protocolVersion = Version311
		if err := codec.Encode(&buf, pkt); err != nil {
			return // encoder rejected the input (e.g. wildcard topic); fine
		}
		decoded, err := codec.Decode(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatalf("round trip decode failed: %v (input topic=%q qos=%d)", err, topic, qos)
		}
		got, ok := decoded.(*PublishPacket)
		if !ok {
			t.Fatalf("expected PublishPacket, got %T", decoded)
		}
		if got.Topic != topic {
			t.Errorf("topic mismatch: got %q want %q", got.Topic, topic)
		}
		if !bytes.Equal(got.Payload, payload) {
			t.Errorf("payload mismatch: got %q want %q", got.Payload, payload)
		}
		if got.FixedHeader.QoS != qos {
			t.Errorf("qos mismatch: got %d want %d", got.FixedHeader.QoS, qos)
		}
	})
}
