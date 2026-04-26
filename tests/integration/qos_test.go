// Package integration provides end-to-end QoS flow tests.
// Run with: go test -race -tags=integration ./tests/integration/...
package integration

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestQoS0 tests fire-and-forget delivery (at most once).
func TestQoS0(t *testing.T) {
	broker := testBroker(t)

	// Subscriber
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)

	connectAndSubscribe(t, subConn, subCodec, "qos0-sub", "qos0/topic", 0)

	// Publisher
	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "qos0-pub")

	// Publish QoS 0
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        0,
		},
		Topic:   "qos0/topic",
		Payload: []byte("qos0 message"),
	}

	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pubPkt); err != nil {
		t.Fatalf("PUBLISH failed: %v", err)
	}

	// Wait for PUBLISH at subscriber (no PUBACK expected for QoS 0)
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber did not receive PUBLISH: %v", err)
	}
	delivered, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if delivered.FixedHeader.QoS != 0 {
		t.Errorf("expected QoS 0, got %d", delivered.FixedHeader.QoS)
	}
	if string(delivered.Payload) != "qos0 message" {
		t.Errorf("expected 'qos0 message', got %s", delivered.Payload)
	}
}

// TestQoS1 tests acknowledged delivery (at least once).
func TestQoS1(t *testing.T) {
	broker := testBroker(t)

	// Subscriber
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "qos1-sub", "qos1/topic", 1)

	// Publisher
	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "qos1-pub")

	// Publish QoS 1
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        1,
		},
		PacketID: 1,
		Topic:    "qos1/topic",
		Payload:  []byte("qos1 message"),
	}

	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pubPkt); err != nil {
		t.Fatalf("PUBLISH failed: %v", err)
	}

	// Wait for PUBACK
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := pubCodec.Decode(pubConn)
	if err != nil {
		t.Fatalf("did not receive PUBACK: %v", err)
	}
	pubAck, ok := pkt.(*protocol.PubAckPacket)
	if !ok {
		t.Fatalf("expected PUBACK, got %T", pkt)
	}
	if pubAck.PacketID != 1 {
		t.Errorf("expected PUBACK packetID 1, got %d", pubAck.PacketID)
	}

	// Wait for PUBLISH at subscriber
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err = subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber did not receive PUBLISH: %v", err)
	}
	delivered, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if delivered.FixedHeader.QoS != 1 {
		t.Errorf("expected QoS 1, got %d", delivered.FixedHeader.QoS)
	}
}

// TestQoS2 tests assured delivery (exactly once).
func TestQoS2(t *testing.T) {
	broker := testBroker(t)

	// Subscriber
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "qos2-sub", "qos2/topic", 2)

	// Publisher
	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "qos2-pub")

	// Publish QoS 2
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        2,
		},
		PacketID: 1,
		Topic:    "qos2/topic",
		Payload:  []byte("qos2 message"),
	}

	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pubPkt); err != nil {
		t.Fatalf("PUBLISH failed: %v", err)
	}

	// Wait for PUBREC
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := pubCodec.Decode(pubConn)
	if err != nil {
		t.Fatalf("did not receive PUBREC: %v", err)
	}
	pubRec, ok := pkt.(*protocol.PubRecPacket)
	if !ok {
		t.Fatalf("expected PUBREC, got %T", pkt)
	}
	if pubRec.PacketID != 1 {
		t.Errorf("expected PUBREC packetID 1, got %d", pubRec.PacketID)
	}

	// Send PUBREL
	pubRel := &protocol.PubRelPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePubRel,
			QoS:        1,
		},
		PacketID: 1,
	}

	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pubRel); err != nil {
		t.Fatalf("PUBREL failed: %v", err)
	}

	// Wait for PUBCOMP
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err = pubCodec.Decode(pubConn)
	if err != nil {
		t.Fatalf("did not receive PUBCOMP: %v", err)
	}
	_, ok = pkt.(*protocol.PubCompPacket)
	if !ok {
		t.Fatalf("expected PUBCOMP, got %T", pkt)
	}

	// Wait for PUBLISH at subscriber
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err = subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber did not receive PUBLISH: %v", err)
	}
	delivered, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if delivered.FixedHeader.QoS != 2 {
		t.Errorf("expected QoS 2, got %d", delivered.FixedHeader.QoS)
	}
}

// connectAndSubscribe connects a client and subscribes to a topic.
func connectAndSubscribe(t *testing.T, conn net.Conn, codec *protocol.Codec, clientID, topic string, qos byte) {
	t.Helper()
	connectClient(t, conn, codec, clientID)

	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeSubscribe,
			QoS:        1,
		},
		PacketID: 1,
		Topics: []protocol.TopicFilter{
			{Topic: topic, QoS: qos},
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
}

// connectClient connects a client to the broker.
func connectClient(t *testing.T, conn net.Conn, codec *protocol.Codec, clientID string) {
	t.Helper()
	connectPkt := &protocol.ConnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnect,
		},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
		},
		KeepAlive: 30,
		ClientID:  clientID,
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, connectPkt); err != nil {
		t.Fatalf("CONNECT failed: %v", err)
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("CONNACK failed: %v", err)
	}
	if ca, ok := pkt.(*protocol.ConnAckPacket); !ok || ca.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("client rejected: %v", pkt)
	}
}
