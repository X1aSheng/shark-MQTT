package integration

import (
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestMultipleSubscribers_SameTopic verifies a message is delivered to all subscribers.
func TestMultipleSubscribers_SameTopic(t *testing.T) {
	broker := testBroker(t)

	const numSubs = 5
	type subClient struct {
		conn  net.Conn
		codec *protocol.Codec
	}
	subs := make([]subClient, numSubs)
	for i := 0; i < numSubs; i++ {
		subs[i].conn = dialTestBroker(t, broker)
		subs[i].codec = protocol.NewCodec(0)
		connectAndSubscribe(t, subs[i].conn, subs[i].codec, fmt.Sprintf("multi-sub-%d", i), "fanout/topic", 0)
	}

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "multi-pub")

	publishQoS0(t, pubConn, pubCodec, "fanout/topic", []byte("broadcast"))

	for _, s := range subs {
		mustReceivePublish(t, s.conn, s.codec, "fanout/topic", "broadcast")
		s.conn.Close()
	}
	pubConn.Close()
}

// TestMixedQoSSubscribers tests one topic with subscribers at different QoS levels.
func TestMixedQoSSubscribers(t *testing.T) {
	broker := testBroker(t)

	sub0Conn := dialTestBroker(t, broker)
	sub0Codec := protocol.NewCodec(0)
	connectAndSubscribe(t, sub0Conn, sub0Codec, "mix-qos0-sub", "mix/topic", 0)

	sub1Conn := dialTestBroker(t, broker)
	sub1Codec := protocol.NewCodec(0)
	connectAndSubscribe(t, sub1Conn, sub1Codec, "mix-qos1-sub", "mix/topic", 1)

	sub2Conn := dialTestBroker(t, broker)
	sub2Codec := protocol.NewCodec(0)
	connectAndSubscribe(t, sub2Conn, sub2Codec, "mix-qos2-sub", "mix/topic", 2)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "mix-pub")

	// Publish QoS 2 — broker should downgrade per subscription
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 2},
		PacketID:    1,
		Topic:       "mix/topic",
		Payload:     []byte("hello"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pubPkt); err != nil {
		t.Fatalf("PUBLISH: %v", err)
	}
	drainQoS2(t, pubConn, pubCodec, 1)

	// QoS 0 subscriber — receives with QoS 0
	sub0Conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := sub0Codec.Decode(sub0Conn)
	if err != nil {
		t.Fatalf("qos0 sub: %v", err)
	}
	p0 := pkt.(*protocol.PublishPacket)
	if p0.FixedHeader.QoS != 0 {
		t.Errorf("qos0 sub: expected QoS 0, got %d", p0.FixedHeader.QoS)
	}

	// QoS 1 subscriber — receives with QoS 1
	sub1Conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err = sub1Codec.Decode(sub1Conn)
	if err != nil {
		t.Fatalf("qos1 sub: %v", err)
	}
	p1 := pkt.(*protocol.PublishPacket)
	if p1.FixedHeader.QoS != 1 {
		t.Errorf("qos1 sub: expected QoS 1, got %d", p1.FixedHeader.QoS)
	}

	// QoS 2 subscriber — receives with QoS 2
	sub2Conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err = sub2Codec.Decode(sub2Conn)
	if err != nil {
		t.Fatalf("qos2 sub: %v", err)
	}
	p2 := pkt.(*protocol.PublishPacket)
	if p2.FixedHeader.QoS != 2 {
		t.Errorf("qos2 sub: expected QoS 2, got %d", p2.FixedHeader.QoS)
	}

	pubConn.Close()
	sub0Conn.Close()
	sub1Conn.Close()
	sub2Conn.Close()
}

// TestMessageOrdering verifies messages arrive in publish order (QoS 0).
func TestMessageOrdering(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "order-sub", "order/topic", 0)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "order-pub")

	const count = 20
	for i := 0; i < count; i++ {
		payload := fmt.Sprintf("msg-%03d", i)
		publishQoS0(t, pubConn, pubCodec, "order/topic", []byte(payload))
	}

	time.Sleep(200 * time.Millisecond)

	for i := 0; i < count; i++ {
		expected := fmt.Sprintf("msg-%03d", i)
		subConn.SetDeadline(time.Now().Add(2 * time.Second))
		pkt, err := subCodec.Decode(subConn)
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		pub, ok := pkt.(*protocol.PublishPacket)
		if !ok {
			t.Fatalf("message %d: expected PUBLISH, got %T", i, pkt)
		}
		if string(pub.Payload) != expected {
			t.Errorf("message %d: expected %q, got %q", i, expected, pub.Payload)
		}
	}

	pubConn.Close()
	subConn.Close()
}

// TestBurstPublish tests rapid-fire message publishing.
func TestBurstPublish(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "burst-sub", "burst/topic", 0)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "burst-pub")

	const burst = 50
	for i := 0; i < burst; i++ {
		publishQoS0(t, pubConn, pubCodec, "burst/topic", []byte(fmt.Sprintf("burst-%d", i)))
	}

	time.Sleep(500 * time.Millisecond)

	received := 0
	for {
		subConn.SetDeadline(time.Now().Add(500 * time.Millisecond))
		pkt, err := subCodec.Decode(subConn)
		if err != nil {
			break
		}
		if _, ok := pkt.(*protocol.PublishPacket); ok {
			received++
		}
	}
	if received != burst {
		t.Errorf("expected %d messages, received %d", burst, received)
	}

	pubConn.Close()
	subConn.Close()
}

// TestLargePayload tests message delivery with a large payload.
func TestLargePayload(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "large-sub", "large/topic", 0)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "large-pub")

	// 64KB payload with known pattern
	const size = 64 * 1024
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	publishQoS0(t, pubConn, pubCodec, "large/topic", payload)

	subConn.SetDeadline(time.Now().Add(5 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber did not receive large payload: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if len(pub.Payload) != size {
		t.Fatalf("expected payload size %d, got %d", size, len(pub.Payload))
	}
	for i, b := range pub.Payload {
		if b != byte(i%256) {
			t.Fatalf("payload corruption at byte %d: expected %d, got %d", i, byte(i%256), b)
		}
	}

	pubConn.Close()
	subConn.Close()
}

// TestBinaryPayload tests delivery of binary data including null bytes.
func TestBinaryPayload(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "binary-sub", "binary/topic", 0)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "binary-pub")

	// Payload with all byte values including null, control chars, high bits
	payload := make([]byte, 512)
	for i := range payload {
		payload[i] = byte(i)
	}

	publishQoS0(t, pubConn, pubCodec, "binary/topic", payload)

	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber did not receive binary payload: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if len(pub.Payload) != len(payload) {
		t.Fatalf("expected %d bytes, got %d", len(payload), len(pub.Payload))
	}
	for i, b := range pub.Payload {
		if b != payload[i] {
			t.Fatalf("byte mismatch at %d: expected 0x%02x, got 0x%02x", i, payload[i], b)
		}
	}

	pubConn.Close()
	subConn.Close()
}

// TestEmptyPayload tests delivery of a message with an empty payload.
func TestEmptyPayload(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "empty-sub", "empty/topic", 0)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "empty-pub")

	publishQoS0(t, pubConn, pubCodec, "empty/topic", []byte{})

	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber did not receive empty payload: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if len(pub.Payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(pub.Payload))
	}

	pubConn.Close()
	subConn.Close()
}

// TestUnicodePayload tests delivery of UTF-8 and emoji content.
func TestUnicodePayload(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "unicode-sub", "unicode/topic", 0)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "unicode-pub")

	payloads := []string{
		"Hello 世界 🌍",
		"مرحبا",
		"こんにちは",
		"🎉🎊🎈",
	}
	for _, p := range payloads {
		publishQoS0(t, pubConn, pubCodec, "unicode/topic", []byte(p))
	}

	for _, expected := range payloads {
		mustReceivePublish(t, subConn, subCodec, "unicode/topic", expected)
	}

	pubConn.Close()
	subConn.Close()
}

// TestOverlappingSubscriptions tests a client with multiple subscriptions
// that match the same topic — message should be delivered once.
func TestOverlappingSubscriptions(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectClient(t, subConn, subCodec, "overlap-sub")

	// Subscribe to two overlapping patterns
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics: []protocol.TopicFilter{
			{Topic: "sensors/room1/temperature", QoS: 0},
			{Topic: "sensors/+/temperature", QoS: 0},
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

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "overlap-pub")

	publishQoS0(t, pubConn, pubCodec, "sensors/room1/temperature", []byte("22.5"))

	// Collect all arriving publishes within a short window
	time.Sleep(200 * time.Millisecond)
	received := collectPublishes(t, subConn, subCodec, 5, 300*time.Millisecond)

	if len(received) != 1 {
		t.Errorf("overlapping subs: expected 1 delivery, got %d", len(received))
		for i, p := range received {
			t.Logf("  received[%d]: topic=%s payload=%s", i, p.Topic, p.Payload)
		}
	}

	pubConn.Close()
	subConn.Close()
}

// TestMultiTopicSubscribe tests subscribing to multiple topics in one SUBSCRIBE packet.
func TestMultiTopicSubscribe(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectClient(t, subConn, subCodec, "multi-topic-sub")

	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics: []protocol.TopicFilter{
			{Topic: "topic/a", QoS: 0},
			{Topic: "topic/b", QoS: 1},
			{Topic: "topic/c", QoS: 0},
		},
	}
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := subCodec.Encode(subConn, subPkt); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("SUBACK: %v", err)
	}
	subAck, ok := pkt.(*protocol.SubAckPacket)
	if !ok {
		t.Fatalf("expected SUBACK, got %T", pkt)
	}
	if len(subAck.ReasonCodes) != 3 {
		t.Fatalf("expected 3 reason codes, got %d", len(subAck.ReasonCodes))
	}

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "multi-topic-pub")

	// Publish to each topic
	publishQoS0(t, pubConn, pubCodec, "topic/a", []byte("msg-a"))
	publishQoS0(t, pubConn, pubCodec, "topic/b", []byte("msg-b"))
	publishQoS0(t, pubConn, pubCodec, "topic/c", []byte("msg-c"))

	mustReceivePublish(t, subConn, subCodec, "topic/a", "msg-a")
	mustReceivePublish(t, subConn, subCodec, "topic/b", "msg-b")
	mustReceivePublish(t, subConn, subCodec, "topic/c", "msg-c")

	pubConn.Close()
	subConn.Close()
}

// TestPublishToSelf verifies a publisher does NOT receive its own message
// when it is also subscribed to the same topic.
func TestPublishToSelf(t *testing.T) {
	broker := testBroker(t)

	conn := dialTestBroker(t, broker)
	codec := protocol.NewCodec(0)
	connectAndSubscribe(t, conn, codec, "self-pub-sub", "self/topic", 0)

	publishQoS0(t, conn, codec, "self/topic", []byte("echo"))

	assertNoMessage(t, conn, "publisher should not receive its own message")
	conn.Close()
}

// TestStructuredBinaryPayload tests a realistic binary protocol payload.
func TestStructuredBinaryPayload(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "struct-sub", "device/telemetry", 0)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "struct-pub")

	// Build a structured binary payload: [4-byte float64 temperature][2-byte uint16 humidity][1-byte status]
	buf := make([]byte, 7)
	binary.BigEndian.PutUint16(buf[0:2], 2225) // temp * 100 = 22.25
	binary.BigEndian.PutUint16(buf[2:4], 5510) // humidity * 100 = 55.10
	binary.BigEndian.PutUint16(buf[4:6], 1013) // pressure * 10 = 101.3
	buf[6] = 0x01                              // status OK

	publishQoS0(t, pubConn, pubCodec, "device/telemetry", buf)

	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if len(pub.Payload) != 7 {
		t.Fatalf("expected 7 bytes, got %d", len(pub.Payload))
	}
	if binary.BigEndian.Uint16(pub.Payload[0:2]) != 2225 {
		t.Error("temperature field mismatch")
	}
	if binary.BigEndian.Uint16(pub.Payload[2:4]) != 5510 {
		t.Error("humidity field mismatch")
	}
	if pub.Payload[6] != 0x01 {
		t.Error("status byte mismatch")
	}

	pubConn.Close()
	subConn.Close()
}
