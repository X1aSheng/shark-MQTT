package integration

import (
	"slices"
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
		Properties:  &protocol.Properties{SubscriptionIdentifier: []uint32{subID}},
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
	if delivered.Properties == nil || !slices.Equal(delivered.Properties.SubscriptionIdentifier, []uint32{subID}) {
		t.Errorf("expected SubscriptionIdentifier [%d] in delivered PUBLISH, got %+v",
			subID, delivered.Properties)
	}
	if string(delivered.Payload) != "subid-echo" {
		t.Errorf("payload mismatch: got %q", string(delivered.Payload))
	}
	subConn.Close()
	pubConn.Close()
}

// TestOverlappingSubscriptionsDeliverEveryIdentifier covers [MQTT-3.3.4-4].
//
// The broker collapses a client's overlapping subscriptions into one delivered
// copy (§3.3.5: deliver once, at the maximum QoS of the matching filters). MQTT
// 5.0 then requires that single copy to carry the Subscription Identifier of
// *every* subscription it matched, because those identifiers are how a client
// maps the message back to the subscriptions it registered. Sending only the
// best match's identifier -- or only the last one decoded -- silently lost the
// rest, and a client that had asked to be told about two of its own
// subscriptions could not tell that both had fired.
//
// The two filters are deliberately nested so that both match "subid/overlap/a":
// a wildcard tagged 11 and an exact filter tagged 22.
func TestOverlappingSubscriptionsDeliverEveryIdentifier(t *testing.T) {
	brk := testBroker(t)

	subConn := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectClient(t, subConn, subCodec, "subid-overlap-sub")

	subscribeWithFilterOptions(t, subConn, subCodec,
		protocol.TopicFilter{Topic: "subid/overlap/#", QoS: 1},
		&protocol.Properties{SubscriptionIdentifier: []uint32{11}}, 1)
	subscribeWithFilterOptions(t, subConn, subCodec,
		protocol.TopicFilter{Topic: "subid/overlap/a", QoS: 1},
		&protocol.Properties{SubscriptionIdentifier: []uint32{22}}, 2)

	pubConn := dialTestBroker(t, brk)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "subid-overlap-pub")
	publishAndAwaitPubAck(t, pubConn, pubCodec, "subid/overlap/a", "overlap", 1)

	subConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber receive PUBLISH: %v", err)
	}
	delivered, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if delivered.Properties == nil {
		t.Fatal("delivered PUBLISH carries no property section, so no identifier at all")
	}

	// Compared as a set: §3.3.2.3.8 states the order of multiple identifiers is
	// not significant, so the test must not pin one, or it would fail on a
	// conformant implementation that ordered them differently.
	got := slices.Clone(delivered.Properties.SubscriptionIdentifier)
	slices.Sort(got)
	if !slices.Equal(got, []uint32{11, 22}) {
		t.Errorf("SubscriptionIdentifier = %v, want [11 22]: the single delivered copy of a "+
			"message that matched two subscriptions must name both [MQTT-3.3.4-4]",
			delivered.Properties.SubscriptionIdentifier)
	}
	if string(delivered.Payload) != "overlap" {
		t.Errorf("payload mismatch: got %q", string(delivered.Payload))
	}
	subConn.Close()
	pubConn.Close()
}
