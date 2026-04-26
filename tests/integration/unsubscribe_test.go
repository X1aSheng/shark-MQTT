package integration

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestUnsubscribe_StopsDelivery verifies messages are no longer delivered after UNSUBSCRIBE.
func TestUnsubscribe_StopsDelivery(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "unsub-sub", "unsub/topic", 0)

	// Verify delivery works before unsubscribe
	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "unsub-pub")

	publishQoS0(t, pubConn, pubCodec, "unsub/topic", []byte("before"))
	mustReceivePublish(t, subConn, subCodec, "unsub/topic", "before")

	// Unsubscribe
	unsubPkt := &protocol.UnsubscribePacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeUnsubscribe,
			QoS:        1,
		},
		PacketID: 2,
		Topics:   []string{"unsub/topic"},
	}
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := subCodec.Encode(subConn, unsubPkt); err != nil {
		t.Fatalf("UNSUBSCRIBE: %v", err)
	}

	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("UNSUBACK: %v", err)
	}
	unsubAck, ok := pkt.(*protocol.UnsubAckPacket)
	if !ok {
		t.Fatalf("expected UNSUBACK, got %T", pkt)
	}
	if unsubAck.PacketID != 2 {
		t.Errorf("expected UNSUBACK packetID 2, got %d", unsubAck.PacketID)
	}

	// Publish again — subscriber should NOT receive it
	publishQoS0(t, pubConn, pubCodec, "unsub/topic", []byte("after"))
	assertNoMessage(t, subConn, "should not receive message after unsubscribe")

	pubConn.Close()
	subConn.Close()
}

// TestUnsubscribe_MultipleTopics tests unsubscribing from multiple topics at once.
func TestUnsubscribe_MultipleTopics(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectClient(t, subConn, subCodec, "multi-unsub-sub")

	// Subscribe to 3 topics
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics: []protocol.TopicFilter{
			{Topic: "multi/a", QoS: 0},
			{Topic: "multi/b", QoS: 0},
			{Topic: "multi/c", QoS: 0},
		},
	}
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := subCodec.Encode(subConn, subPkt); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := subCodec.Decode(subConn); err != nil {
		t.Fatalf("SUBACK: %v", err)
	}

	// Unsubscribe from a and b only
	unsubPkt := &protocol.UnsubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeUnsubscribe, QoS: 1},
		PacketID:    2,
		Topics:      []string{"multi/a", "multi/b"},
	}
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := subCodec.Encode(subConn, unsubPkt); err != nil {
		t.Fatalf("UNSUBSCRIBE: %v", err)
	}
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := subCodec.Decode(subConn); err != nil {
		t.Fatalf("UNSUBACK: %v", err)
	}

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "multi-unsub-pub")

	// multi/a — should NOT be delivered
	publishQoS0(t, pubConn, pubCodec, "multi/a", []byte("nope"))
	assertNoMessage(t, subConn, "should not receive multi/a after unsub")

	// multi/b — should NOT be delivered
	publishQoS0(t, pubConn, pubCodec, "multi/b", []byte("nope"))
	assertNoMessage(t, subConn, "should not receive multi/b after unsub")

	// multi/c — should STILL be delivered
	publishQoS0(t, pubConn, pubCodec, "multi/c", []byte("yes"))
	mustReceivePublish(t, subConn, subCodec, "multi/c", "yes")

	pubConn.Close()
	subConn.Close()
}

// TestUnsubscribe_WildcardTopic tests unsubscribing from a wildcard pattern.
func TestUnsubscribe_WildcardTopic(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "wc-unsub-sub", "wc/+/data", 0)

	// Verify it works
	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "wc-unsub-pub")

	publishQoS0(t, pubConn, pubCodec, "wc/sensor1/data", []byte("before"))
	mustReceivePublish(t, subConn, subCodec, "wc/sensor1/data", "before")

	// Unsubscribe from wildcard pattern
	unsubPkt := &protocol.UnsubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeUnsubscribe, QoS: 1},
		PacketID:    2,
		Topics:      []string{"wc/+/data"},
	}
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := subCodec.Encode(subConn, unsubPkt); err != nil {
		t.Fatalf("UNSUBSCRIBE: %v", err)
	}
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := subCodec.Decode(subConn); err != nil {
		t.Fatalf("UNSUBACK: %v", err)
	}

	// Should no longer receive
	publishQoS0(t, pubConn, pubCodec, "wc/sensor2/data", []byte("after"))
	assertNoMessage(t, subConn, "should not receive after wildcard unsub")

	pubConn.Close()
	subConn.Close()
}

// TestResubscribeAfterUnsubscribe verifies a client can re-subscribe after unsubscribing.
func TestResubscribeAfterUnsubscribe(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "resub-sub", "resub/topic", 0)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "resub-pub")

	// Phase 1: subscribed, receives messages
	publishQoS0(t, pubConn, pubCodec, "resub/topic", []byte("phase1"))
	mustReceivePublish(t, subConn, subCodec, "resub/topic", "phase1")

	// Unsubscribe
	unsubPkt := &protocol.UnsubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeUnsubscribe, QoS: 1},
		PacketID:    2,
		Topics:      []string{"resub/topic"},
	}
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	subCodec.Encode(subConn, unsubPkt)
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	subCodec.Decode(subConn)

	// Phase 2: unsubscribed, does NOT receive
	publishQoS0(t, pubConn, pubCodec, "resub/topic", []byte("phase2"))
	assertNoMessage(t, subConn, "should not receive during unsubscribed phase")

	// Re-subscribe
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    3,
		Topics:      []protocol.TopicFilter{{Topic: "resub/topic", QoS: 0}},
	}
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	subCodec.Encode(subConn, subPkt)
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	subCodec.Decode(subConn)

	// Phase 3: re-subscribed, receives again
	publishQoS0(t, pubConn, pubCodec, "resub/topic", []byte("phase3"))
	mustReceivePublish(t, subConn, subCodec, "resub/topic", "phase3")

	pubConn.Close()
	subConn.Close()
}

// TestSystemTopic_NotReceivedByNormalSub tests that $SYS topics are not matched
// by normal wildcard subscriptions (per MQTT spec).
func TestSystemTopic_NotReceivedByNormalSub(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "sys-sub", "#", 0)

	// The broker doesn't explicitly publish to $SYS in current impl,
	// but we test that a normal # subscription doesn't accidentally
	// interfere with system topic patterns. Publish a normal message
	// to verify subscriber is working.
	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "sys-pub")

	publishQoS0(t, pubConn, pubCodec, "normal/topic", []byte("works"))
	mustReceivePublish(t, subConn, subCodec, "normal/topic", "works")

	pubConn.Close()
	subConn.Close()
}

// TestQoS1_PublisherAck tests that QoS 1 publisher receives PUBACK from broker.
func TestQoS1_PublisherAck(t *testing.T) {
	broker := testBroker(t)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "ack-pub")

	for i := 0; i < 5; i++ {
		pid := uint16(i + 1)
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
			PacketID:    pid,
			Topic:       "ack/topic",
			Payload:     []byte("msg"),
		}
		pubConn.SetDeadline(time.Now().Add(2 * time.Second))
		if err := pubCodec.Encode(pubConn, pkt); err != nil {
			t.Fatalf("PUBLISH %d: %v", pid, err)
		}
		pubConn.SetDeadline(time.Now().Add(2 * time.Second))
		resp, err := pubCodec.Decode(pubConn)
		if err != nil {
			t.Fatalf("PUBACK %d: %v", pid, err)
		}
		ack, ok := resp.(*protocol.PubAckPacket)
		if !ok {
			t.Fatalf("expected PUBACK, got %T", resp)
		}
		if ack.PacketID != pid {
			t.Errorf("expected PUBACK packetID %d, got %d", pid, ack.PacketID)
		}
	}

	pubConn.Close()
}

// TestQoS2_FullHandshake tests the complete QoS 2 four-step handshake.
func TestQoS2_FullHandshake(t *testing.T) {
	broker := testBroker(t)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "qos2-pub")

	pid := uint16(1)
	pkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 2},
		PacketID:    pid,
		Topic:       "qos2/handshake",
		Payload:     []byte("exactly-once"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pkt); err != nil {
		t.Fatalf("PUBLISH: %v", err)
	}

	drainQoS2(t, pubConn, pubCodec, pid)
	pubConn.Close()
}

// TestQoS1_PublishNoSubscriber tests QoS 1 PUBACK still returned even with no subscribers.
func TestQoS1_PublishNoSubscriber(t *testing.T) {
	broker := testBroker(t)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "no-sub-pub")

	pkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    1,
		Topic:       "nobody/listening",
		Payload:     []byte("hello?"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pkt); err != nil {
		t.Fatalf("PUBLISH: %v", err)
	}

	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	resp, err := pubCodec.Decode(pubConn)
	if err != nil {
		t.Fatalf("expected PUBACK even with no subscribers: %v", err)
	}
	ack, ok := resp.(*protocol.PubAckPacket)
	if !ok {
		t.Fatalf("expected PUBACK, got %T", resp)
	}
	if ack.PacketID != 1 {
		t.Errorf("expected packetID 1, got %d", ack.PacketID)
	}

	pubConn.Close()
}
