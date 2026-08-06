package protocol

import (
	"bytes"
	"testing"
)

// decodeAllocsPerRun returns the steady-state number of heap allocations for
// decoding one PUBLISH with the given protocol version and payload size.
func decodeAllocsPerRun(t *testing.T, version uint8, payloadSize int) float64 {
	codec := NewCodec(0)
	codec.protocolVersion = version
	var buf bytes.Buffer
	pkt := &PublishPacket{
		FixedHeader: FixedHeader{PacketType: PacketTypePublish},
		Topic:       "bench/topic",
		Payload:     make([]byte, payloadSize),
	}
	if err := codec.Encode(&buf, pkt); err != nil {
		t.Fatalf("encode: %v", err)
	}
	raw := buf.Bytes()
	return testing.AllocsPerRun(1000, func() {
		if _, err := codec.Decode(bytes.NewReader(raw)); err != nil {
			t.Fatalf("decode: %v", err)
		}
	})
}

// TestDecodePublishAllocs guards R2: decoding a small 3.1.1 PUBLISH must not
// allocate a transient body buffer plus a payload copy (previously 10 allocs;
// now the body is read straight into the packet-owned payload). The MQTT 5.0
// path keeps the intermediate (pooled) buffer because properties must be parsed
// out of the body first.
func TestDecodePublishAllocs(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	v4 := decodeAllocsPerRun(t, 4, 256)
	if v4 >= 10 {
		t.Errorf("3.1.1 DecodePublish allocated %.0f/op (R2 expected < 10)", v4)
	}
	v5 := decodeAllocsPerRun(t, 5, 256)
	if v5 > 11 {
		t.Errorf("5.0 DecodePublish allocated %.0f/op, expected <= 11", v5)
	}
}
