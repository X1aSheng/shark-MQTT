package integration

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestWriteQueue_TinyQueueDeliversQoS1 verifies the per-connection write queue
// (R1) delivers every QoS 1 message even with a minimal write_queue_size: QoS 1
// deliveries apply backpressure instead of being dropped, so nothing is lost.
// It also exercises config.WriteQueueSize plumbing through the api layer.
func TestWriteQueue_TinyQueueDeliversQoS1(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	cfg.WriteQueueSize = 2

	b := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { b.Stop() })

	subConn := dialTestBroker(t, b)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "wq-sub", "wq/topic", 1)

	pubConn := dialTestBroker(t, b)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "wq-pub")

	const count = 10
	for i := 1; i <= count; i++ {
		pubPkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePublish,
				QoS:        1,
			},
			PacketID: uint16(i),
			Topic:    "wq/topic",
			Payload:  []byte("msg"),
		}
		pubConn.SetDeadline(time.Now().Add(2 * time.Second))
		if err := pubCodec.Encode(pubConn, pubPkt); err != nil {
			t.Fatalf("PUBLISH %d failed: %v", i, err)
		}
		// Wait for the PUBACK so the broker has acked the delivery.
		pubConn.SetDeadline(time.Now().Add(2 * time.Second))
		pkt, err := pubCodec.Decode(pubConn)
		if err != nil {
			t.Fatalf("PUBACK %d: %v", i, err)
		}
		if _, ok := pkt.(*protocol.PubAckPacket); !ok {
			t.Fatalf("expected PUBACK, got %T", pkt)
		}
	}

	// Every QoS 1 delivery must arrive at the subscriber despite the tiny queue.
	received := make(map[uint16]bool)
	for i := 0; i < count; i++ {
		subConn.SetDeadline(time.Now().Add(2 * time.Second))
		pkt, err := subCodec.Decode(subConn)
		if err != nil {
			t.Fatalf("subscriber PUBLISH: %v", err)
		}
		delivered, ok := pkt.(*protocol.PublishPacket)
		if !ok {
			t.Fatalf("expected PUBLISH, got %T", pkt)
		}
		received[delivered.PacketID] = true
		// Acknowledge so the receive window and write queue keep draining.
		ack := &protocol.PubAckPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubAck},
			PacketID:    delivered.PacketID,
		}
		subConn.SetDeadline(time.Now().Add(2 * time.Second))
		if err := subCodec.Encode(subConn, ack); err != nil {
			t.Fatalf("PUBACK from subscriber: %v", err)
		}
	}

	if len(received) != count {
		t.Fatalf("expected %d deliveries, got %d", count, len(received))
	}
}
