package client

import (
	"bytes"
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// ─── PUBREC fixed-header flags ────────────────────────────────
//
// MQTT 3.1.1 Table 2.2 / MQTT 5.0 Table 2-2 list the PUBREC fixed-header flags
// as reserved 0b0000 (only PUBREL, SUBSCRIBE and UNSUBSCRIBE carry 0b0010).
// protocol.validateFixedHeaderFlags enforces exactly that on every decoded
// packet, so a PUBREC sent with the QoS bit set is rejected by the peer as
// ErrInvalidPacket -- which tears down the whole connection.

// recordingConn captures every byte written to it so a test can decode the
// acknowledgements the client emits. Read blocks until Close so nothing else
// can consume the writes.
type recordingConn struct {
	mu     sync.Mutex
	writes [][]byte
	closed chan struct{}
	once   sync.Once
}

func newRecordingConn() *recordingConn {
	return &recordingConn{closed: make(chan struct{})}
}

func (c *recordingConn) Read([]byte) (int, error) {
	<-c.closed
	return 0, net.ErrClosed
}

func (c *recordingConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]byte, len(p))
	copy(cp, p)
	c.writes = append(c.writes, cp)
	return len(p), nil
}

func (c *recordingConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *recordingConn) LocalAddr() net.Addr                { return nil }
func (c *recordingConn) RemoteAddr() net.Addr               { return nil }
func (c *recordingConn) SetDeadline(_ time.Time) error      { return nil }
func (c *recordingConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *recordingConn) SetWriteDeadline(_ time.Time) error { return nil }

func (c *recordingConn) received() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(c.writes))
	copy(out, c.writes)
	return out
}

// TestHandlePublishQoS2PubRecUsesReservedFlags covers both PUBREC send sites in
// handlePublish: the first delivery and the duplicate-detection path.
func TestHandlePublishQoS2PubRecUsesReservedFlags(t *testing.T) {
	conn := newRecordingConn()
	c := New()
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	pkt := &protocol.PublishPacket{PacketID: 42, Topic: "t", Payload: []byte("x")}
	pkt.FixedHeader.PacketType = protocol.PacketTypePublish
	pkt.FixedHeader.QoS = 2

	c.handlePublish(conn, pkt) // first delivery
	c.handlePublish(conn, pkt) // duplicate: must still answer with PUBREC

	// The encoder writes the fixed header and the body as two separate
	// writes, so a PUBREC arrives here as two chunks; the first byte of the
	// header chunk is the one that carries the flags.
	var stream bytes.Buffer
	pubrecs := 0
	for _, raw := range conn.received() {
		if len(raw) > 0 && raw[0]>>4 == byte(protocol.PacketTypePubRec) {
			pubrecs++
			if got := raw[0] & 0x0F; got != 0x0 {
				t.Errorf("PUBREC #%d flags nibble = 0x%x, want 0x0 (reserved)", pubrecs, got)
			}
		}
		stream.Write(raw)
	}
	if pubrecs != 2 {
		t.Fatalf("expected 2 PUBREC packets, saw %d", pubrecs)
	}

	// The decisive check: a peer must be able to decode what we sent. Decode
	// with the version the broker uses for this 3.1.1 client -- the flags
	// check is version-independent.
	codec := protocol.NewCodecForVersion(0, protocol.Version311)
	for i := 1; i <= 2; i++ {
		pkt, err := codec.Decode(&stream)
		if err != nil {
			t.Fatalf("PUBREC #%d is not decodable by a peer: %v", i, err)
		}
		pr, ok := pkt.(*protocol.PubRecPacket)
		if !ok {
			t.Fatalf("packet #%d = %T, want *protocol.PubRecPacket", i, pkt)
		}
		if pr.PacketID != 42 {
			t.Errorf("PUBREC #%d PacketID = %d, want 42", i, pr.PacketID)
		}
	}
}

// TestQoS2InboundDeliverySurvivesPubRec is the end-to-end regression: a client
// that receives a QoS 2 message must acknowledge it in a way its own broker
// accepts, otherwise the broker drops the connection after the first message
// and the whole subscription goes silent.
func TestQoS2InboundDeliverySurvivesPubRec(t *testing.T) {
	_, addr := startTestBroker(t)
	host, port := splitAddr(addr)

	pub := New(WithHostPort(host, port), WithClientID("qos2-pub"), WithConnectTimeout(5*time.Second))
	sub := New(WithHostPort(host, port), WithClientID("qos2-sub"), WithConnectTimeout(5*time.Second))

	ctx := context.Background()
	if err := pub.Connect(ctx); err != nil {
		t.Fatalf("publisher Connect failed: %v", err)
	}
	defer pub.Disconnect(ctx)
	if err := sub.Connect(ctx); err != nil {
		t.Fatalf("subscriber Connect failed: %v", err)
	}
	defer sub.Disconnect(ctx)

	if _, err := sub.Subscribe(ctx, []TopicSubscription{{Topic: "qos2/inbound", QoS: 2}}); err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	msgs := make(chan string, 4)
	sub.SetOnMessage(func(_ string, qos byte, payload []byte) {
		if qos != 2 {
			t.Errorf("delivered QoS = %d, want 2", qos)
		}
		msgs <- string(payload)
	})

	// First message: the subscription must still be alive afterwards.
	if err := pub.Publish(ctx, "qos2/inbound", 2, false, []byte("first")); err != nil {
		t.Fatalf("publish #1 failed: %v", err)
	}
	select {
	case got := <-msgs:
		if got != "first" {
			t.Fatalf("first payload = %q, want %q", got, "first")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the first QoS 2 message")
	}

	// The broker processes our PUBREC asynchronously; give a malformed one
	// time to kill the connection before asserting it is still up.
	time.Sleep(300 * time.Millisecond)
	if !sub.IsConnected() {
		t.Fatal("subscriber was disconnected by the broker after acknowledging a QoS 2 delivery")
	}

	// Second message: proves the subscription survived the PUBREC.
	if err := pub.Publish(ctx, "qos2/inbound", 2, false, []byte("second")); err != nil {
		t.Fatalf("publish #2 failed: %v", err)
	}
	select {
	case got := <-msgs:
		if got != "second" {
			t.Fatalf("second payload = %q, want %q", got, "second")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the second QoS 2 message: the subscription did not survive the PUBREC")
	}
}
