package integration

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store/memory"
)

// testBrokerWithRetain creates a broker with a retained message store.
func testBrokerWithRetain(t *testing.T) *api.Broker {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	brk := api.NewBroker(
		api.WithConfig(cfg),
		api.WithRetainedStore(memory.NewRetainedStore()),
	)
	if err := brk.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(func() { brk.Stop() })
	return brk
}

// TestRetainedMessage_NewSubscriber receives the last retained message on a topic.
func TestRetainedMessage_NewSubscriber(t *testing.T) {
	broker := testBrokerWithRetain(t)

	// Publisher sends a retained message
	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "retain-pub")

	retainPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			Retain:     true,
		},
		Topic:   "device/status",
		Payload: []byte("online"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, retainPkt); err != nil {
		t.Fatalf("PUBLISH retained: %v", err)
	}

	// New subscriber connects AFTER retained message was published
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "retain-sub", "device/status", 0)

	// Should immediately receive the retained message
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber did not receive retained message: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if pub.Topic != "device/status" {
		t.Errorf("expected topic device/status, got %s", pub.Topic)
	}
	if string(pub.Payload) != "online" {
		t.Errorf("expected payload 'online', got %s", pub.Payload)
	}
	if !pub.FixedHeader.Retain {
		t.Error("expected Retain flag set on delivered message")
	}

	pubConn.Close()
	subConn.Close()
}

// TestRetainedMessage_Update replaces a retained message with a new one.
func TestRetainedMessage_Update(t *testing.T) {
	broker := testBrokerWithRetain(t)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "retain-update-pub")

	// First retained message
	retain1 := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, Retain: true},
		Topic:       "device/status",
		Payload:     []byte("online"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, retain1); err != nil {
		t.Fatalf("PUBLISH retained 1: %v", err)
	}

	// Update retained message
	retain2 := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, Retain: true},
		Topic:       "device/status",
		Payload:     []byte("offline"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, retain2); err != nil {
		t.Fatalf("PUBLISH retained 2: %v", err)
	}

	// New subscriber should get only the latest retained message
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "retain-update-sub", "device/status", 0)

	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber did not receive retained: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if string(pub.Payload) != "offline" {
		t.Errorf("expected updated payload 'offline', got %s", pub.Payload)
	}

	pubConn.Close()
	subConn.Close()
}

// TestRetainedMessage_Delete clears a retained message by publishing empty payload.
func TestRetainedMessage_Delete(t *testing.T) {
	broker := testBrokerWithRetain(t)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "retain-del-pub")

	// Set retained
	retain := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, Retain: true},
		Topic:       "device/status",
		Payload:     []byte("online"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, retain); err != nil {
		t.Fatalf("PUBLISH retained: %v", err)
	}

	// Delete retained with empty payload
	delRetain := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, Retain: true},
		Topic:       "device/status",
		Payload:     []byte{},
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, delRetain); err != nil {
		t.Fatalf("PUBLISH delete retained: %v", err)
	}

	// New subscriber should NOT receive any retained message
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "retain-del-sub", "device/status", 0)

	subConn.SetDeadline(time.Now().Add(200 * time.Millisecond))
	_, err := subCodec.Decode(subConn)
	if err == nil {
		t.Error("expected no retained message after deletion, but got one")
	}

	pubConn.Close()
	subConn.Close()
}

// TestRetainedMessage_WildcardDelivery verifies retained messages are delivered
// when subscribing with a wildcard that matches the retained topic.
func TestRetainedMessage_WildcardDelivery(t *testing.T) {
	broker := testBrokerWithRetain(t)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "retain-wc-pub")

	// Publish retained messages on different topics
	topics := []struct {
		topic, payload string
	}{
		{"sensors/room1/temperature", "22.0"},
		{"sensors/room2/temperature", "24.0"},
		{"sensors/room1/humidity", "55%"},
	}
	for _, tc := range topics {
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, Retain: true},
			Topic:       tc.topic,
			Payload:     []byte(tc.payload),
		}
		pubConn.SetDeadline(time.Now().Add(2 * time.Second))
		if err := pubCodec.Encode(pubConn, pkt); err != nil {
			t.Fatalf("PUBLISH retained %s: %v", tc.topic, err)
		}
	}

	// Subscribe with wildcard sensors/+/temperature
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "retain-wc-sub", "sensors/+/temperature", 0)

	// Should receive 2 retained messages (room1 and room2 temperature)
	received := collectPublishes(t, subConn, subCodec, 2, 2*time.Second)
	if len(received) != 2 {
		t.Fatalf("expected 2 retained messages, got %d", len(received))
	}

	topicsSeen := map[string]bool{}
	for _, p := range received {
		topicsSeen[p.Topic] = true
	}
	if !topicsSeen["sensors/room1/temperature"] {
		t.Error("missing retained sensors/room1/temperature")
	}
	if !topicsSeen["sensors/room2/temperature"] {
		t.Error("missing retained sensors/room2/temperature")
	}

	pubConn.Close()
	subConn.Close()
}

// TestRetainedMessage_QoSDowngrade tests that retained message QoS is downgraded
// when subscriber subscribes at a lower QoS.
func TestRetainedMessage_QoSDowngrade(t *testing.T) {
	broker := testBrokerWithRetain(t)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "retain-qos-pub")

	// Publish retained at QoS 2
	retain := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        2,
			Retain:     true,
		},
		PacketID: 1,
		Topic:    "device/alert",
		Payload:  []byte("critical"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, retain); err != nil {
		t.Fatalf("PUBLISH retained QoS2: %v", err)
	}
	// Read PUBREC + send PUBREL + read PUBCOMP
	drainQoS2(t, pubConn, pubCodec, 1)

	// Subscribe at QoS 0 — should receive retained at QoS 0
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "retain-qos-sub", "device/alert", 0)

	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber did not receive retained: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if pub.FixedHeader.QoS != 0 {
		t.Errorf("expected QoS 0 (downgraded), got %d", pub.FixedHeader.QoS)
	}
	if string(pub.Payload) != "critical" {
		t.Errorf("expected payload 'critical', got %s", pub.Payload)
	}

	pubConn.Close()
	subConn.Close()
}

// --- retained test helpers ---

func collectPublishes(t *testing.T, conn net.Conn, codec *protocol.Codec, count int, timeout time.Duration) []*protocol.PublishPacket {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var results []*protocol.PublishPacket
	for len(results) < count {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		conn.SetDeadline(time.Now().Add(remaining))
		pkt, err := codec.Decode(conn)
		if err != nil {
			break
		}
		if pub, ok := pkt.(*protocol.PublishPacket); ok {
			results = append(results, pub)
		}
	}
	return results
}

func drainQoS2(t *testing.T, conn net.Conn, codec *protocol.Codec, packetID uint16) {
	t.Helper()
	// PUBREC
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("PUBREC: %v", err)
	}
	rec, ok := pkt.(*protocol.PubRecPacket)
	if !ok {
		t.Fatalf("expected PUBREC, got %T", pkt)
	}
	if rec.PacketID != packetID {
		t.Fatalf("expected PUBREC packetID %d, got %d", packetID, rec.PacketID)
	}

	// PUBREL
	pubRel := &protocol.PubRelPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubRel, QoS: 1},
		PacketID:    packetID,
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pubRel); err != nil {
		t.Fatalf("PUBREL: %v", err)
	}

	// PUBCOMP
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err = codec.Decode(conn)
	if err != nil {
		t.Fatalf("PUBCOMP: %v", err)
	}
	comp, ok := pkt.(*protocol.PubCompPacket)
	if !ok {
		t.Fatalf("expected PUBCOMP, got %T", pkt)
	}
	if comp.PacketID != packetID {
		t.Fatalf("expected PUBCOMP packetID %d, got %d", packetID, comp.PacketID)
	}
}
