package integration

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestWildcardPlus tests single-level wildcard (+) topic matching.
func TestWildcardPlus(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "wc-plus-sub", "sensors/+/temperature", 0)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "wc-plus-pub")

	// Should match: sensors/room1/temperature
	publishQoS0(t, pubConn, pubCodec, "sensors/room1/temperature", []byte("22.5"))
	mustReceivePublish(t, subConn, subCodec, "sensors/room1/temperature", "22.5")

	// Should match: sensors/room2/temperature
	publishQoS0(t, pubConn, pubCodec, "sensors/room2/temperature", []byte("24.1"))
	mustReceivePublish(t, subConn, subCodec, "sensors/room2/temperature", "24.1")

	// Should NOT match: sensors/room1/humidity
	publishQoS0(t, pubConn, pubCodec, "sensors/room1/humidity", []byte("55%"))
	assertNoMessage(t, subConn, "did not expect message for sensors/room1/humidity")

	// Should NOT match: sensors/room1/temperature/extra (too many levels)
	publishQoS0(t, pubConn, pubCodec, "sensors/room1/temperature/extra", []byte("nope"))
	assertNoMessage(t, subConn, "did not expect message for sensors/room1/temperature/extra")

	pubConn.Close()
	subConn.Close()
}

// TestWildcardHash tests multi-level wildcard (#) topic matching.
func TestWildcardHash(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "wc-hash-sub", "home/#", 0)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "wc-hash-pub")

	// Should match: home/living/light
	publishQoS0(t, pubConn, pubCodec, "home/living/light", []byte("on"))
	mustReceivePublish(t, subConn, subCodec, "home/living/light", "on")

	// Should match: home/bedroom/temperature/current (deep hierarchy)
	publishQoS0(t, pubConn, pubCodec, "home/bedroom/temperature/current", []byte("21.3"))
	mustReceivePublish(t, subConn, subCodec, "home/bedroom/temperature/current", "21.3")

	// Should match: home (exact prefix with trailing level)
	publishQoS0(t, pubConn, pubCodec, "home/living", []byte("root"))
	mustReceivePublish(t, subConn, subCodec, "home/living", "root")

	// Should NOT match: office/temperature (wrong prefix)
	publishQoS0(t, pubConn, pubCodec, "office/temperature", []byte("20.0"))
	assertNoMessage(t, subConn, "did not expect message for office/temperature")

	pubConn.Close()
	subConn.Close()
}

// TestWildcardHashRoot tests # alone matches everything.
func TestWildcardHashRoot(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "wc-root-sub", "#", 0)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "wc-root-pub")

	cases := []struct{ topic, payload string }{
		{"a/b/c", "deep"},
		{"single", "one"},
		{"device/sensor/temp", "23.5"},
	}
	for _, tc := range cases {
		publishQoS0(t, pubConn, pubCodec, tc.topic, []byte(tc.payload))
		mustReceivePublish(t, subConn, subCodec, tc.topic, tc.payload)
	}

	pubConn.Close()
	subConn.Close()
}

// TestWildcardMixed tests combined + and # in one subscription.
func TestWildcardMixed(t *testing.T) {
	broker := testBroker(t)

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "wc-mixed-sub", "home/+/status/#", 0)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "wc-mixed-pub")

	// Should match: home/living/status/light
	publishQoS0(t, pubConn, pubCodec, "home/living/status/light", []byte("on"))
	mustReceivePublish(t, subConn, subCodec, "home/living/status/light", "on")

	// Should match: home/kitchen/status
	publishQoS0(t, pubConn, pubCodec, "home/kitchen/status", []byte("ok"))
	mustReceivePublish(t, subConn, subCodec, "home/kitchen/status", "ok")

	// Should NOT match: home/living/light (no "status" level)
	publishQoS0(t, pubConn, pubCodec, "home/living/light", []byte("on"))
	assertNoMessage(t, subConn, "did not expect message for home/living/light")

	pubConn.Close()
	subConn.Close()
}

// TestWildcardMultipleSubscribers tests multiple subscribers with different wildcard patterns.
func TestWildcardMultipleSubscribers(t *testing.T) {
	broker := testBroker(t)

	sub1Conn := dialTestBroker(t, broker)
	sub1Codec := protocol.NewCodec(0)
	connectAndSubscribe(t, sub1Conn, sub1Codec, "mwc-sub1", "sensors/+/temperature", 0)

	sub2Conn := dialTestBroker(t, broker)
	sub2Codec := protocol.NewCodec(0)
	connectAndSubscribe(t, sub2Conn, sub2Codec, "mwc-sub2", "sensors/#", 0)

	sub3Conn := dialTestBroker(t, broker)
	sub3Codec := protocol.NewCodec(0)
	connectAndSubscribe(t, sub3Conn, sub3Codec, "mwc-sub3", "sensors/room1/+", 0)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "mwc-pub")

	// Publish sensors/room1/temperature → all 3
	publishQoS0(t, pubConn, pubCodec, "sensors/room1/temperature", []byte("22.0"))
	mustReceivePublish(t, sub1Conn, sub1Codec, "sensors/room1/temperature", "22.0")
	mustReceivePublish(t, sub2Conn, sub2Codec, "sensors/room1/temperature", "22.0")
	mustReceivePublish(t, sub3Conn, sub3Codec, "sensors/room1/temperature", "22.0")

	// Publish sensors/room1/humidity → sub2 and sub3 only
	publishQoS0(t, pubConn, pubCodec, "sensors/room1/humidity", []byte("55%"))
	assertNoMessage(t, sub1Conn, "sub1 should not receive humidity")
	mustReceivePublish(t, sub2Conn, sub2Codec, "sensors/room1/humidity", "55%")
	mustReceivePublish(t, sub3Conn, sub3Codec, "sensors/room1/humidity", "55%")

	// Publish sensors/room2/temperature → sub1 and sub2 only
	publishQoS0(t, pubConn, pubCodec, "sensors/room2/temperature", []byte("24.0"))
	mustReceivePublish(t, sub1Conn, sub1Codec, "sensors/room2/temperature", "24.0")
	mustReceivePublish(t, sub2Conn, sub2Codec, "sensors/room2/temperature", "24.0")
	assertNoMessage(t, sub3Conn, "sub3 should not receive room2")

	// Publish sensors/room2/voltage/current → sub2 only
	publishQoS0(t, pubConn, pubCodec, "sensors/room2/voltage/current", []byte("220V"))
	assertNoMessage(t, sub1Conn, "sub1 should not receive deep topic")
	mustReceivePublish(t, sub2Conn, sub2Codec, "sensors/room2/voltage/current", "220V")
	assertNoMessage(t, sub3Conn, "sub3 should not receive room2 deep topic")

	pubConn.Close()
	sub1Conn.Close()
	sub2Conn.Close()
	sub3Conn.Close()
}

// --- shared data communication helpers ---

func publishQoS0(t *testing.T, conn net.Conn, codec *protocol.Codec, topic string, payload []byte) {
	t.Helper()
	pkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
		Topic:       topic,
		Payload:     payload,
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		t.Fatalf("PUBLISH to %s failed: %v", topic, err)
	}
}

func mustReceivePublish(t *testing.T, conn net.Conn, codec *protocol.Codec, expectTopic, expectPayload string) {
	t.Helper()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("expected PUBLISH on %s, got error: %v", expectTopic, err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if pub.Topic != expectTopic {
		t.Errorf("expected topic %s, got %s", expectTopic, pub.Topic)
	}
	if string(pub.Payload) != expectPayload {
		t.Errorf("expected payload %q, got %q", expectPayload, pub.Payload)
	}
}

func assertNoMessage(t *testing.T, conn net.Conn, msg string) {
	t.Helper()
	conn.SetDeadline(time.Now().Add(150 * time.Millisecond))
	buf := make([]byte, 1)
	_, err := conn.Read(buf)
	if err == nil {
		t.Error(msg)
	}
}
