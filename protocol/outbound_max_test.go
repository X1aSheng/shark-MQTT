package protocol

// Outbound Maximum Packet Size (§3.1.2.11.4 / [MQTT-3.1.2-24], [MQTT-3.1.2-25]).
//
// The bound is enforced in Codec.Encode, which assembles the packet before
// handing it to the writer. These tests pin the two properties that make that
// placement the right one: the refusal happens before any byte reaches the
// writer, and the guard does not diverge from the unguarded path on anything
// else (the fixed-header check still runs first).

import (
	"bytes"
	"errors"
	"testing"
)

// sizedPublish builds a PUBLISH whose encoded size grows with payloadBytes.
func sizedPublish(payloadBytes int) *PublishPacket {
	return &PublishPacket{
		FixedHeader: FixedHeader{PacketType: PacketTypePublish},
		Topic:       "size/topic",
		Payload:     make([]byte, payloadBytes),
	}
}

func TestCodecOutboundMaxIsUnboundedByDefault(t *testing.T) {
	c := NewCodec(0)
	if got := c.OutboundMaxPacketSize(); got != 0 {
		t.Fatalf("OutboundMaxPacketSize() = %d, want 0 (unbounded)", got)
	}

	var buf bytes.Buffer
	if err := c.Encode(&buf, sizedPublish(4096)); err != nil {
		t.Fatalf("Encode with no bound: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("expected the packet on the wire when no bound is set")
	}
}

func TestCodecOutboundMaxRejectsBeforeWritingAnything(t *testing.T) {
	pkt := sizedPublish(256)

	// Measure the real encoded size rather than predicting it: Remaining Length
	// is variable, so a byte count written by hand in the test would drift.
	var ref bytes.Buffer
	if err := NewCodec(0).Encode(&ref, pkt); err != nil {
		t.Fatalf("reference encode: %v", err)
	}
	size := ref.Len()

	// Exactly at the limit: the packet fits, so it must go out unchanged.
	c := NewCodec(0)
	c.SetOutboundMaxPacketSize(size)
	var exact bytes.Buffer
	if err := c.Encode(&exact, pkt); err != nil {
		t.Fatalf("Encode at the %d-byte limit: %v", size, err)
	}
	if exact.Len() != size {
		t.Fatalf("Encode at the limit wrote %d bytes, want %d", exact.Len(), size)
	}

	// One byte over: refused, and the writer must stay untouched. A partial
	// write is what streaming the check into w would have produced, and it is
	// precisely what the MUST NOT send rule forbids.
	c.SetOutboundMaxPacketSize(size - 1)
	var short bytes.Buffer
	err := c.Encode(&short, pkt)
	if !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("Encode one byte over the limit: err = %v, want ErrPacketTooLarge", err)
	}
	if short.Len() != 0 {
		t.Fatalf("%d bytes reached the writer for a refused packet, want 0", short.Len())
	}
}

// TestCodecOutboundGuardKeepsFixedHeaderValidation guards against the guarded
// path becoming a second, weaker encoder: a packet whose reserved nibble is
// illegal must still be rejected, and rejected for that reason rather than
// accidentally passing or being reported as a size problem.
func TestCodecOutboundGuardKeepsFixedHeaderValidation(t *testing.T) {
	c := NewCodec(0)
	c.SetOutboundMaxPacketSize(4096)

	pkt := sizedPublish(1)
	pkt.FixedHeader.QoS = 3 // forbidden for PUBLISH ([MQTT-2.2.2-1])

	var buf bytes.Buffer
	if err := c.Encode(&buf, pkt); !errors.Is(err, ErrInvalidPacket) {
		t.Fatalf("err = %v, want ErrInvalidPacket", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("%d bytes written for an invalid fixed header, want 0", buf.Len())
	}
}

// TestCodecOutboundMaxAppliesToEveryPacketType checks the bound is a property of
// the codec rather than of PUBLISH: a CONNACK is bound too, because the client's
// limit applies to every packet the server sends it (§3.1.2.11.4).
func TestCodecOutboundMaxAppliesToEveryPacketType(t *testing.T) {
	// The CONNACK carries MQTT 5 properties, so the codec has to be bound to
	// version 5 explicitly: NewCodec starts at 3.1.1 and would reject the
	// property set before the size guard was ever consulted.
	c := NewCodecForVersion(0, Version50)
	c.SetOutboundMaxPacketSize(4)

	ack := &ConnAckPacket{
		FixedHeader: FixedHeader{PacketType: PacketTypeConnAck},
		ReasonCode:  ConnAckAccepted,
		Properties:  &Properties{ResponseInfo: "response/info"},
	}
	var buf bytes.Buffer
	if err := c.Encode(&buf, ack); !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("CONNACK over the limit: err = %v, want ErrPacketTooLarge", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("%d bytes written for a refused CONNACK, want 0", buf.Len())
	}
}
