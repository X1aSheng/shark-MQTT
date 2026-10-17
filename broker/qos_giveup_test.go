package broker

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// noopQoSCallbacks satisfies SetCallbacks for tests that only care about
// give-up accounting.
func noopQoSCallbacks(q *QoSEngine) {
	q.SetCallbacks(
		func(string, uint16) error { return nil },
		func(string, uint16) error { return nil },
		func(string, uint16) error { return nil },
		func(string, uint16, string, []byte, uint8, bool) error { return nil },
	)
}

// TestQoSEngineGiveUpCallbackFiresAndDropsEntry pins the give-up contract.
//
// The engine drops its own tracking entry when retries run out, but the session
// holds the same packet id and counts it against the outbound window. Without a
// give-up callback the session entry and the unacked count were never released:
// each abandoned delivery permanently narrowed the window (and was replayed on
// the next reconnect), with no log line and no metric.
func TestQoSEngineGiveUpCallbackFiresAndDropsEntry(t *testing.T) {
	q := NewQoSEngine(WithRetryInterval(time.Millisecond), WithMaxRetries(0))
	noopQoSCallbacks(q)

	type giveUp struct {
		clientID string
		packetID uint16
	}
	var got giveUp
	var called atomic.Bool
	q.SetGiveUpCallback(func(clientID string, packetID uint16) {
		got = giveUp{clientID: clientID, packetID: packetID}
		called.Store(true)
	})

	if err := q.TrackOutboundQoS1("c1", 7, "t", []byte("x"), false); err != nil {
		t.Fatalf("TrackOutboundQoS1: %v", err)
	}
	if q.InflightCount("c1") != 1 {
		t.Fatalf("inflight count = %d, want 1", q.InflightCount("c1"))
	}

	// The retry loop only considers entries older than retryInterval.
	time.Sleep(5 * time.Millisecond)
	q.doRetry()

	if !called.Load() {
		t.Fatal("give-up callback was not invoked after retries were exhausted")
	}
	if got.clientID != "c1" || got.packetID != 7 {
		t.Fatalf("give-up callback got %+v, want {c1 7}", got)
	}
	if n := q.InflightCount("c1"); n != 0 {
		t.Fatalf("inflight count after give-up = %d, want 0", n)
	}
}

// TestQoSEngineGiveUpNotCalledWhileRetriesRemain is the control: a delivery that
// still has retries left must not be given up.
func TestQoSEngineGiveUpNotCalledWhileRetriesRemain(t *testing.T) {
	q := NewQoSEngine(WithRetryInterval(time.Millisecond), WithMaxRetries(3))
	noopQoSCallbacks(q)

	var called atomic.Bool
	q.SetGiveUpCallback(func(string, uint16) { called.Store(true) })

	if err := q.TrackOutboundQoS1("c1", 9, "t", []byte("x"), false); err != nil {
		t.Fatalf("TrackOutboundQoS1: %v", err)
	}

	q.doRetry()

	if called.Load() {
		t.Fatal("give-up callback fired with retries still available")
	}
	if n := q.InflightCount("c1"); n != 1 {
		t.Fatalf("inflight count = %d, want 1 (entry must survive a retry)", n)
	}
}

// TestRepublishWithoutSessionEntrySendsNothing pins the second half of the
// give-up story: when the session no longer holds the packet id there is
// nothing to retransmit, and the broker must not answer with a PUBREC.
//
// A PUBREC acknowledges an inbound PUBLISH, whose packet id belongs to the peer.
// Sending one for a delivery the broker itself sent puts a packet on the wire
// that the client never asked for, and the client then answers PUBREL, which the
// broker completes -- an exchange for a message that does not exist.
func TestRepublishWithoutSessionEntrySendsNothing(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}), WithWriteQueueSize(4))

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	cs := &clientState{
		conn:       serverConn,
		codec:      protocol.NewCodec(0),
		out:        make(chan protocol.Packet, 4),
		stopWrites: make(chan struct{}),
		syncWrites: make(chan syncWrite),
	}
	b.mu.Lock()
	b.connections["gone"] = cs
	b.mu.Unlock()

	// No session exists for "gone", so republish has nothing to resend.
	if err := b.republish(b.ctx, "gone", 42, "t", []byte("x"), 1, false); err != nil {
		t.Fatalf("republish returned %v, want nil", err)
	}

	select {
	case pkt := <-cs.out:
		t.Fatalf("republish queued %T, want nothing", pkt)
	case <-time.After(100 * time.Millisecond):
	}
}
