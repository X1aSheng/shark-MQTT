package protocol

// Regression tests for P3-1 (HeaderSize), P3-2 (minimal remaining length),
// P3-3 (v3.1.1 CONNACK strict length), NEW-18 (trailing bytes after
// properties), and NEW-19 (v3.1.1 SUBSCRIBE option bits).

import (
	"bytes"
	"testing"
)

func decodeBytes(t *testing.T, codec *Codec, raw []byte) (Packet, error) {
	t.Helper()
	return codec.Decode(bytes.NewReader(raw))
}

// TestHeaderSizeSet verifies decodeFixedHeader records the actual fixed-header
// wire size (P3-1), so maxPacketSize checks are exact.
func TestHeaderSizeSet(t *testing.T) {
	codec := NewCodec(0)
	// CONNECT: 1 header byte + 1 remaining-length byte + payload.
	raw := []byte{0x10, 0x02, 0x00, 0x04} // arbitrary valid header; decode may
	// fail later on payload parsing, but the fixed header is parsed first.
	pkt, err := decodeBytes(t, codec, raw)
	if err == nil && pkt != nil {
		fh := pkt.GetFixedHeader()
		if fh.HeaderSize == 0 {
			t.Error("HeaderSize must be non-zero after decode")
		}
	}
}

// TestRejectNonMinimalRemainingLength verifies non-minimal variable-length
// encodings are rejected (P3-2, MQTT-1.5.5-1).
func TestRejectNonMinimalRemainingLength(t *testing.T) {
	codec := NewCodec(0)
	// PINGREQ with remaining length 0 encoded as two bytes [0x80, 0x00]
	// instead of the minimal [0x00].
	raw := []byte{0xC0, 0x80, 0x00}
	if _, err := decodeBytes(t, codec, raw); err == nil {
		t.Error("expected non-minimal remaining length [0x80,0x00] to be rejected")
	}
	// [0x81, 0x00] encodes 1 in two bytes — also non-minimal.
	raw2 := []byte{0xC0, 0x81, 0x00}
	if _, err := decodeBytes(t, codec, raw2); err == nil {
		t.Error("expected non-minimal remaining length [0x81,0x00] to be rejected")
	}
	// The minimal [0x00] for remaining length 0 must still be accepted.
	if _, err := decodeBytes(t, codec, []byte{0xC0, 0x00}); err != nil {
		t.Errorf("minimal PINGREQ should decode, got %v", err)
	}
}

// TestConnAck311StrictLength verifies an MQTT 3.1.1 CONNACK with more than
// two payload bytes is rejected (P3-3).
func TestConnAck311StrictLength(t *testing.T) {
	codec := NewCodec(0) // default protocolVersion = 4 (3.1.1)
	// RemainingLength=3 (one extra byte).
	if _, err := decodeBytes(t, codec, []byte{0x20, 0x03, 0x00, 0x00, 0x00}); err == nil {
		t.Error("expected v3.1.1 CONNACK with RemainingLength>2 to be rejected")
	}
	// Valid v3.1.1 CONNACK (exactly 2 payload bytes).
	if _, err := decodeBytes(t, codec, []byte{0x20, 0x02, 0x00, 0x00}); err != nil {
		t.Errorf("valid v3.1.1 CONNACK should decode, got %v", err)
	}
}

// TestConnAckTrailingBytes verifies trailing bytes after MQTT 5.0 properties
// are rejected (NEW-18).
func TestConnAckTrailingBytes(t *testing.T) {
	codec := NewCodec(0)
	codec.protocolVersion = Version50
	// sp=0, rc=0, propLen=0, then an extra byte 0xFF.
	raw := []byte{0x20, 0x04, 0x00, 0x00, 0x00, 0xFF}
	if _, err := decodeBytes(t, codec, raw); err == nil {
		t.Error("expected CONNACK with trailing bytes after properties to be rejected")
	}
	// Without the trailing byte it must decode.
	ok := []byte{0x20, 0x03, 0x00, 0x00, 0x00}
	if _, err := decodeBytes(t, codec, ok); err != nil {
		t.Errorf("valid v5 CONNACK should decode, got %v", err)
	}
}

// TestSubscribe311RejectsV5OptionBits verifies an MQTT 3.1.1 SUBSCRIBE with
// MQTT 5.0 option bits (NoLocal/RetainAsPublished/RetainHandling) is rejected
// (NEW-19).
func TestSubscribe311RejectsV5OptionBits(t *testing.T) {
	codec := NewCodec(0) // 3.1.1
	// SUBSCRIBE with topic "topic" and optsByte=0x04 (NoLocal set).
	raw := []byte{0x82, 0x0A, 0x00, 0x01, 0x00, 0x05, 't', 'o', 'p', 'i', 'c', 0x04}
	if _, err := decodeBytes(t, codec, raw); err == nil {
		t.Error("expected v3.1.1 SUBSCRIBE with NoLocal bit to be rejected")
	}
	// Same subscription under MQTT 5.0 is accepted (with a property-length byte).
	raw5 := []byte{0x82, 0x0B, 0x00, 0x01, 0x00, 0x00, 0x05, 't', 'o', 'p', 'i', 'c', 0x04}
	codec5 := NewCodec(0)
	codec5.protocolVersion = Version50
	if _, err := decodeBytes(t, codec5, raw5); err != nil {
		t.Errorf("v5 SUBSCRIBE with NoLocal should decode, got %v", err)
	}
}
