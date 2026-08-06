package integration

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// connectPersistent connects a client with cleanSession=false (persistent
// session) and reads the CONNACK.
func connectPersistent(t *testing.T, conn net.Conn, codec *protocol.Codec, clientID string) {
	t.Helper()
	connectPkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: false,
		},
		KeepAlive: 30,
		ClientID:  clientID,
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, connectPkt); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	if _, ok := pkt.(*protocol.ConnAckPacket); !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}
}

// subscribeQoS1 subscribes to a topic at QoS 1 and reads the SUBACK.
func subscribeQoS1(t *testing.T, conn net.Conn, codec *protocol.Codec, topic string) {
	t.Helper()
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: topic, QoS: 1}},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, subPkt); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("SUBACK: %v", err)
	}
	if _, ok := pkt.(*protocol.SubAckPacket); !ok {
		t.Fatalf("expected SUBACK, got %T", pkt)
	}
}

// TestMessageExpiry_DropExpiredOfflineDelivery verifies a QoS 1 message queued
// for an offline persistent session is not delivered once its Message Expiry
// Interval has passed (MQTT 5.0 §3.3.2.3.2).
func TestMessageExpiry_DropExpiredOfflineDelivery(t *testing.T) {
	broker := testBroker(t)

	// Persistent subscriber subscribes, then goes offline.
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectPersistent(t, subConn, subCodec, "exp-sub")
	subscribeQoS1(t, subConn, subCodec, "exp/topic")
	var dbuf bytes.Buffer
	if err := subCodec.Encode(&dbuf, &protocol.DisconnectPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeDisconnect},
	}); err != nil {
		t.Fatalf("DISCONNECT encode: %v", err)
	}
	subConn.Write(dbuf.Bytes())
	subConn.Close()

	// Publish QoS 1 with a 1-second expiry while the subscriber is offline.
	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "exp-pub")
	expiry := uint32(1)
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    1,
		Topic:       "exp/topic",
		Payload:     []byte("short-lived"),
		Properties:  &protocol.Properties{MessageExpiryInterval: &expiry},
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pubPkt); err != nil {
		t.Fatalf("PUBLISH: %v", err)
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if pkt, err := pubCodec.Decode(pubConn); err != nil || !isPubAck(pkt) {
		t.Fatalf("expected PUBACK (err=%v), got %T", err, pkt)
	}

	// Wait past the expiry before reconnecting.
	time.Sleep(1500 * time.Millisecond)

	// Reconnect the persistent session: the expired queued message must NOT be
	// delivered.
	subConn2 := dialTestBroker(t, broker)
	subCodec2 := protocol.NewCodec(0)
	connectPersistent(t, subConn2, subCodec2, "exp-sub")

	subConn2.SetReadDeadline(time.Now().Add(600 * time.Millisecond))
	if pkt, err := subCodec2.Decode(subConn2); err == nil {
		t.Fatalf("expired queued message was delivered to reconnected subscriber: %T", pkt)
	}
}

// TestMessageExpiry_LiveDeliveryCarriesRemaining verifies a live QoS 1 delivery
// carries a decremented (remaining) Message Expiry Interval on the forwarded
// PUBLISH (§3.3.2.3.2).
func TestMessageExpiry_LiveDeliveryCarriesRemaining(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectClient(t, subConn, subCodec, "live-sub")
	subscribeQoS1(t, subConn, subCodec, "live/topic")

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "live-pub")

	expiry := uint32(10)
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    1,
		Topic:       "live/topic",
		Payload:     []byte("payload"),
		Properties:  &protocol.Properties{MessageExpiryInterval: &expiry},
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pubPkt); err != nil {
		t.Fatalf("PUBLISH: %v", err)
	}
	// Drain the publisher's PUBACK.
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if pkt, err := pubCodec.Decode(pubConn); err != nil || !isPubAck(pkt) {
		t.Fatalf("expected PUBACK (err=%v), got %T", err, pkt)
	}

	// The subscriber's delivered PUBLISH must carry a remaining expiry.
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber PUBLISH: %v", err)
	}
	delivered, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if delivered.Properties == nil || delivered.Properties.MessageExpiryInterval == nil {
		t.Fatal("delivered PUBLISH missing MessageExpiryInterval")
	}
	got := *delivered.Properties.MessageExpiryInterval
	if got < 1 || got > 10 {
		t.Errorf("expected remaining expiry 1..10, got %d", got)
	}
}

func isPubAck(pkt protocol.Packet) bool {
	_, ok := pkt.(*protocol.PubAckPacket)
	return ok
}
