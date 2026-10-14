package broker

// Outbound Maximum Packet Size on the write path.
//
// Codec.Encode refuses an oversized packet by returning ErrPacketTooLarge, but
// that sentinel must not reach the connection-teardown logic: [MQTT-3.1.2-25]
// says the server discards the packet and carries on as if it had sent it. The
// only place that distinction can be made is classifyWriteError, which both
// write paths go through.

import (
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/pkg/metrics"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// oversizeMetrics counts only the drops this feature produces, so a stray drop
// counted for another reason cannot make an assertion pass.
type oversizeMetrics struct {
	metrics.Metrics
	dropped atomic.Int64
}

func (m *oversizeMetrics) IncMessagesDropped(reason string) {
	if reason == "packet_too_large" {
		m.dropped.Add(1)
	}
}

// cappedClient registers one connection whose codec enforces maxBytes and
// returns the peer end of its socket. The socket is a net.Pipe with no reader,
// so any byte the broker actually writes would block rather than be silently
// absorbed -- which is what makes "nothing was sent" observable.
func cappedClient(t *testing.T, maxBytes int) (*Broker, *oversizeMetrics, net.Conn) {
	t.Helper()

	m := &oversizeMetrics{Metrics: metrics.Default()}
	b := New(WithAuth(AllowAllAuth{}), WithMetrics(m))

	codec := protocol.NewCodec(0)
	codec.SetOutboundMaxPacketSize(maxBytes)
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		serverConn.Close()
		clientConn.Close()
	})

	// out stays nil so writeOrEnqueue takes the synchronous path, which keeps
	// the assertion free of writer-goroutine scheduling.
	cs := &clientState{conn: serverConn, codec: codec, metrics: m}
	b.mu.Lock()
	b.connections["capped"] = cs
	b.mu.Unlock()

	return b, m, clientConn
}

func TestOversizePublishIsDiscardedWithoutClosingTheConnection(t *testing.T) {
	b, m, peer := cappedClient(t, 64)

	pub := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    1,
		Topic:       "too/large",
		Payload:     make([]byte, 512),
	}
	b.writePacket("capped", pub)

	if got := m.dropped.Load(); got != 1 {
		t.Errorf("packet_too_large drops = %d, want 1", got)
	}

	// The packet must not be on the wire, and the connection must still be up:
	// a read has to time out rather than return bytes or EOF.
	peer.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	n, err := peer.Read(make([]byte, 64))
	if err == nil {
		t.Fatalf("read %d bytes for a refused packet, want none", n)
	}
	if errors.Is(err, io.EOF) {
		t.Fatal("connection was closed for a refused packet; §3.1.2.11.4 keeps the session alive")
	}
}

func TestPacketWithinTheLimitIsStillWritten(t *testing.T) {
	b, m, peer := cappedClient(t, 4096)

	pub := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    2,
		Topic:       "fits",
		Payload:     make([]byte, 8),
	}
	// net.Pipe writes block until the peer reads, so the write has to run
	// concurrently with the read below.
	go b.writePacket("capped", pub)

	peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := peer.Read(make([]byte, 64)); err != nil {
		t.Fatalf("packet within the limit was not written: %v", err)
	}
	if got := m.dropped.Load(); got != 0 {
		t.Errorf("packet_too_large drops = %d, want 0 for a packet that fits", got)
	}
}

// TestOversizeConnAckClosesInsteadOfStrandingTheClient covers the one packet
// whose refusal cannot be treated as a no-op. Every other packet has a defined
// outcome when dropped, but a client that never receives its CONNACK waits for
// a reply that will never come, so the connection is closed instead.
func TestOversizeConnAckClosesInsteadOfStrandingTheClient(t *testing.T) {
	b, _, peer := cappedClient(t, 4)

	ack := &protocol.ConnAckPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeConnAck},
		ReasonCode:  protocol.ConnAckAccepted,
		Properties:  &protocol.Properties{ResponseInfo: "response/info"},
	}
	b.writePacket("capped", ack)

	peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := peer.Read(make([]byte, 64)); !errors.Is(err, io.EOF) {
		t.Fatalf("read error = %v, want io.EOF: a refused CONNACK must close the connection", err)
	}
}
