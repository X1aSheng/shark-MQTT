package integration

// The client's Maximum Packet Size bounds everything the server sends it
// (MQTT 5.0 §3.1.2.11.4, [MQTT-3.1.2-24]/[-25]).
//
// The codec unit tests prove a packet over the limit is refused without being
// written. What they cannot prove is that the value the client declared in its
// CONNECT ever reaches the codec of the connection that later delivers to it --
// a wiring mistake there would leave every unit test green while the broker went
// on sending oversized packets.

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// connectWithMaximumPacketSize performs a v5 CONNECT declaring maxPacketSize and
// returns the CONNACK.
func connectWithMaximumPacketSize(t *testing.T, conn net.Conn, codec *protocol.Codec, clientID string, maxPacketSize uint32) {
	t.Helper()

	connectPkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        clientID,
		Properties:      &protocol.Properties{MaximumPacketSize: &maxPacketSize},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, connectPkt); err != nil {
		t.Fatalf("CONNECT with Maximum Packet Size %d: %v", maxPacketSize, err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	ca, ok := pkt.(*protocol.ConnAckPacket)
	if !ok || ca.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("CONNECT rejected: %v", pkt)
	}
}

// subscribeTopic subscribes on an established connection and waits for the
// SUBACK, so the subscription is in place before the publisher runs.
func subscribeTopic(t *testing.T, conn net.Conn, codec *protocol.Codec, topic string, qos byte) {
	t.Helper()

	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: topic, QoS: qos}},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, subPkt); err != nil {
		t.Fatalf("SUBSCRIBE %s: %v", topic, err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("SUBACK for %s: %v", topic, err)
	}
	if _, ok := pkt.(*protocol.SubAckPacket); !ok {
		t.Fatalf("expected SUBACK for %s, got %T", topic, pkt)
	}
}

// publishAndAwaitPubAck publishes and blocks until the broker's PUBACK arrives, which is
// the barrier that makes the delivery decision (and its suppression) final
// before the subscriber is inspected.
func publishAndAwaitPubAck(t *testing.T, conn net.Conn, codec *protocol.Codec, topic, payload string, packetID uint16) {
	t.Helper()

	pkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    packetID,
		Topic:       topic,
		Payload:     []byte(payload),
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		t.Fatalf("PUBLISH %s: %v", topic, err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pktAck, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("PUBACK for %s (packetID %d): %v", topic, packetID, err)
	}
	ack, ok := pktAck.(*protocol.PubAckPacket)
	if !ok {
		t.Fatalf("expected PUBACK for %s, got %T", topic, pktAck)
	}
	if ack.PacketID != packetID {
		t.Fatalf("PUBACK packetID = %d, want %d", ack.PacketID, packetID)
	}
}

// TestMaximumPacketSizeSuppressesOversizeDelivery pins the whole chain: the
// CONNECT property reaches the codec, an oversized delivery is withheld, and the
// connection keeps working.
//
// The second, small publish is what makes the assertion deterministic rather
// than a race against the writer goroutine. Deliveries to one client keep their
// order, so if the oversized packet had been sent it would be the *first*
// PUBLISH the subscriber reads; requiring the first PUBLISH to carry the small
// payload proves both that the oversized one was withheld and that the
// connection stayed in sync.
func TestMaximumPacketSizeSuppressesOversizeDelivery(t *testing.T) {
	const topic = "mps/topic"

	brk := testBroker(t)

	pubConn := dialTestBroker(t, brk)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "mps-publisher")

	// 64 bytes is comfortably above this session's CONNACK, so the session
	// establishes normally and the cap only bites on the oversized delivery.
	subConn := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectWithMaximumPacketSize(t, subConn, subCodec, "mps-subscriber", 64)
	subscribeTopic(t, subConn, subCodec, topic, 1)

	publishAndAwaitPubAck(t, pubConn, pubCodec, topic, string(make([]byte, 512)), 1)
	publishAndAwaitPubAck(t, pubConn, pubCodec, topic, "ok", 2)

	subConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("expected the small payload to be delivered: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("got %T, want *protocol.PublishPacket", pkt)
	}
	if string(pub.Payload) != "ok" {
		t.Fatalf("first delivery carried %d bytes, want the 2-byte payload: the %d-byte packet over the client's 64-byte limit was sent",
			len(pub.Payload), 512)
	}

	// And the session is still usable: a second small publish reaches it too.
	publishAndAwaitPubAck(t, pubConn, pubCodec, topic, "ok2", 3)
	subConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	pkt, err = subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("second small publish not delivered: %v", err)
	}
	if pub2, ok := pkt.(*protocol.PublishPacket); !ok || string(pub2.Payload) != "ok2" {
		t.Fatalf("second delivery = %v, want payload ok2", pkt)
	}
}
