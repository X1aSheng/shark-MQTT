// Package integration provides end-to-end integration tests for shark-mqtt.
// Run with: go test -race -tags=integration ./tests/integration/...
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

func TestSelfPublishDeliveredByDefault(t *testing.T) {
	broker := testBroker(t)

	conn := dialTestBroker(t, broker)
	codec := protocol.NewCodec(0)
	connectAndSubscribe(t, conn, codec, "self-pub-client", "self/topic", 0)

	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        0,
		},
		Topic:   "self/topic",
		Payload: []byte("loopback"),
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pubPkt); err != nil {
		t.Fatalf("PUBLISH failed: %v", err)
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("subscriber did not receive its own PUBLISH by default: %v", err)
	}
	delivered, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if delivered.Topic != "self/topic" || string(delivered.Payload) != "loopback" {
		t.Fatalf("delivered message = topic %q payload %q", delivered.Topic, delivered.Payload)
	}
}

func TestNoLocalSuppressesSelfPublish(t *testing.T) {
	broker := testBroker(t)

	conn := dialTestBroker(t, broker)
	codec := protocol.NewCodec(0)
	connectClient(t, conn, codec, "no-local-client")

	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeSubscribe,
			QoS:        1,
		},
		PacketID: 1,
		Topics: []protocol.TopicFilter{
			{Topic: "self/nolocal", QoS: 0, NoLocal: true},
		},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, subPkt); err != nil {
		t.Fatalf("SUBSCRIBE failed: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("failed to read SUBACK: %v", err)
	}
	if _, ok := pkt.(*protocol.SubAckPacket); !ok {
		t.Fatalf("expected SUBACK, got %T", pkt)
	}

	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
		Topic:       "self/nolocal",
		Payload:     []byte("suppressed"),
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pubPkt); err != nil {
		t.Fatalf("PUBLISH failed: %v", err)
	}

	conn.SetDeadline(time.Now().Add(200 * time.Millisecond))
	if pkt, err := codec.Decode(conn); err == nil {
		t.Fatalf("received self PUBLISH despite NoLocal: %T", pkt)
	}
}
