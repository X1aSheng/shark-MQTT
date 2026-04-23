// Package integration provides end-to-end integration tests for shark-mqtt.
// Run with: go test -race -tags=integration ./test/integration/...
package integration

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestPubSub tests end-to-end publish/subscribe flow.
func TestPubSub(t *testing.T) {
	broker := testBroker(t)

	// Create subscriber
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)

	connectAndSubscribe(t, subConn, subCodec, "subscriber", "test/topic", 1)

	// Create publisher
	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "publisher")

	// Publish message
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        1,
		},
		PacketID: 1,
		Topic:    "test/topic",
		Payload:  []byte("hello world"),
	}

	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pubPkt); err != nil {
		t.Fatalf("PUBLISH failed: %v", err)
	}

	// Wait for PUBACK to publisher
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := pubCodec.Decode(pubConn)
	if err != nil {
		t.Fatalf("failed to read PUBACK: %v", err)
	}
	pubAck, ok := pkt.(*protocol.PubAckPacket)
	if !ok {
		t.Fatalf("expected PUBACK, got %T", pkt)
	}
	if pubAck.PacketID != 1 {
		t.Errorf("expected PUBACK packetID 1, got %d", pubAck.PacketID)
	}

	// Wait for PUBLISH to subscriber
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err = subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber did not receive PUBLISH: %v", err)
	}
	delivered, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH to subscriber, got %T", pkt)
	}
	if delivered.Topic != "test/topic" {
		t.Errorf("expected topic test/topic, got %s", delivered.Topic)
	}
	if string(delivered.Payload) != "hello world" {
		t.Errorf("expected payload 'hello world', got %s", delivered.Payload)
	}

	pubConn.Close()
	subConn.Close()
}
