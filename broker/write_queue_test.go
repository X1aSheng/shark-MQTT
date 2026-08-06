package broker

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/pkg/metrics"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// dropCountingMetrics counts QoS 0 drops caused by a full per-connection write
// queue (R1), so tests can assert the drop policy actually fired.
type dropCountingMetrics struct {
	metrics.Metrics
	dropped atomic.Int64
}

func (m *dropCountingMetrics) IncMessagesDropped(reason string) {
	if reason == "write_queue_full" {
		m.dropped.Add(1)
	}
}

// TestWriteQueue_QoS0DropsForStalledConsumer verifies a subscriber whose socket
// never drains cannot block the publishing client: once the per-connection write
// queue is full, QoS 0 publishes are dropped rather than blocking (R1). Without
// the write queue this loop would deadlock on the synchronous socket write.
func TestWriteQueue_QoS0DropsForStalledConsumer(t *testing.T) {
	m := &dropCountingMetrics{Metrics: metrics.Default()}
	b := New(WithAuth(AllowAllAuth{}), WithWriteQueueSize(2), WithMetrics(m))
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	// Register a connection whose writer is stalled: nothing reads clientConn,
	// so the writer goroutine blocks on its first socket write (net.Pipe is
	// unbuffered). The queue therefore stays full after a couple of publishes.
	cs := &clientState{
		conn:       serverConn,
		codec:      protocol.NewCodec(0),
		out:        make(chan protocol.Packet, 2),
		stopWrites: make(chan struct{}),
	}
	go cs.writeLoop()
	b.mu.Lock()
	b.connections["slow-sub"] = cs
	b.mu.Unlock()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			b.writePacket("slow-sub", &protocol.PublishPacket{
				FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 0},
				Topic:       "t",
				Payload:     []byte{byte(i)},
			})
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("QoS 0 publishes to a stalled subscriber blocked the producer (R1 not effective)")
	}
	if m.dropped.Load() == 0 {
		t.Fatal("expected QoS 0 publishes to be dropped when the write queue is full")
	}
}

// TestWriteQueue_QoS1BackpressureReleases verifies QoS 1 deliveries are never
// dropped when the write queue is full: they block (backpressure) and are
// delivered once the writer drains the queue (R1).
func TestWriteQueue_QoS1BackpressureReleases(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}), WithWriteQueueSize(1))
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	// cap=1 queue with no writer goroutine yet: the queue stays full after one
	// enqueue, so QoS 0 drops immediately and QoS 1 blocks.
	cs := &clientState{
		conn:       serverConn,
		codec:      protocol.NewCodec(0),
		out:        make(chan protocol.Packet, 1),
		stopWrites: make(chan struct{}),
	}
	b.mu.Lock()
	b.connections["slow-qos1"] = cs
	b.mu.Unlock()

	// Fill the queue.
	b.writePacket("slow-qos1", &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 0},
		Topic:       "t",
		Payload:     []byte("fill"),
	})

	// QoS 0 with a full queue drops immediately (fire-and-forget).
	b.writePacket("slow-qos1", &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 0},
		Topic:       "t",
		Payload:     []byte("drop"),
	})

	// QoS 1 with a full queue must block (backpressure), not drop.
	blocked := make(chan struct{})
	go func() {
		b.writePacket("slow-qos1", &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
			Topic:       "t",
			Payload:     []byte("qos1"),
		})
		close(blocked)
	}()

	select {
	case <-blocked:
		t.Fatal("QoS 1 write should block (backpressure) on a full queue, not drop")
	case <-time.After(100 * time.Millisecond):
	}

	// Start the writer and drain the pipe: the blocked QoS 1 write must be
	// released and delivered.
	go cs.writeLoop()
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := clientConn.Read(buf); err != nil {
				return
			}
		}
	}()

	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("QoS 1 write did not unblock after the writer drained the queue")
	}
}
