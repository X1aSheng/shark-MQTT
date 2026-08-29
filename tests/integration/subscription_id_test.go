package integration

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestSubscriptionIdentifierAdvertisedAndEchoed verifies the broker advertises
// Subscription Identifier support in the CONNACK (SubIDAvailable=1) and echoes
// the SUBSCRIBE packet-level SubscriptionIdentifier back in delivered PUBLISH
// packets (MQTT 5.0 §3.8.2.1.2 / §3.3.2.3.7).
func TestSubscriptionIdentifierAdvertisedAndEchoed(t *testing.T) {
	broker := testBroker(t)

	// Subscriber: CONNECT (MQTT 5.0) and check CONNACK SubIDAvailable=1.
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectSub := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "subid-sub",
	}
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := subCodec.Encode(subConn, connectSub); err != nil {
		t.Fatalf("subscriber CONNECT: %v", err)
	}
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber CONNACK: %v", err)
	}
	ca, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}
	if ca.Properties == nil || ca.Properties.SubIDAvailable == nil || *ca.Properties.SubIDAvailable != 1 {
		t.Error("expected SubIDAvailable=1 in CONNACK")
	}

	// SUBSCRIBE carrying a packet-level SubscriptionIdentifier.
	subID := uint32(42)
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "subid/test", QoS: 1}},
		Properties:  &protocol.Properties{SubscriptionIdentifier: &subID},
	}
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := subCodec.Encode(subConn, subPkt); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := subCodec.Decode(subConn); err != nil {
		t.Fatalf("SUBACK: %v", err)
	}

	// Publisher: CONNECT and PUBLISH to the subscribed topic.
	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectPub := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "subid-pub",
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, connectPub); err != nil {
		t.Fatalf("publisher CONNECT: %v", err)
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := pubCodec.Decode(pubConn); err != nil {
		t.Fatalf("publisher CONNACK: %v", err)
	}
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
		Topic:       "subid/test",
		Payload:     []byte("subid-echo"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pubPkt); err != nil {
		t.Fatalf("PUBLISH: %v", err)
	}

	// The delivered PUBLISH must carry the SubscriptionIdentifier.
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err = subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber receive PUBLISH: %v", err)
	}
	delivered, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if delivered.Properties == nil || delivered.Properties.SubscriptionIdentifier == nil ||
		*delivered.Properties.SubscriptionIdentifier != subID {
		t.Errorf("expected SubscriptionIdentifier %d in delivered PUBLISH, got %+v",
			subID, delivered.Properties)
	}
	if string(delivered.Payload) != "subid-echo" {
		t.Errorf("payload mismatch: got %q", string(delivered.Payload))
	}
	subConn.Close()
	pubConn.Close()
}
