package bench

// End-to-end data delivery benchmarks.
//
// Unlike broker_bench_test.go which uses drainConn() to discard subscriber data,
// these benchmarks read and verify every received PUBLISH packet, measuring
// the full publish→broker→subscribe→verify round-trip.
//
// Run:
//
//	go test -v -bench=BenchmarkE2E -benchmem ./tests/bench/...

import (
	"bytes"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store/memory"
)

// ---------------------------------------------------------------------------
// Data verification helpers
// ---------------------------------------------------------------------------

// verifyPublish reads a PUBLISH from the subscriber, verifies topic and payload.

func setupBrokerWithRetain(b *testing.B) *api.Broker {
	b.Helper()
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	brk := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
		api.WithRetainedStore(memory.NewRetainedStore()),
	)
	if err := brk.Start(); err != nil {
		b.Fatalf("start broker: %v", err)
	}
	return brk
}
func verifyPublish(b *testing.B, conn net.Conn, codec *protocol.Codec, wantTopic string, wantPayload []byte) {
	b.Helper()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		b.Fatalf("subscriber decode: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		b.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if pub.Topic != wantTopic {
		b.Fatalf("topic mismatch: want %q got %q", wantTopic, pub.Topic)
	}
	if !bytes.Equal(pub.Payload, wantPayload) {
		b.Fatalf("payload mismatch: want %d bytes got %d bytes", len(wantPayload), len(pub.Payload))
	}
}

// verifyAndAckQoS1 receives a QoS 1 PUBLISH, verifies data, sends PUBACK.
func verifyAndAckQoS1(b *testing.B, conn net.Conn, codec *protocol.Codec, wantTopic string, wantPayload []byte) {
	b.Helper()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		b.Fatalf("subscriber decode: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		b.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if pub.Topic != wantTopic {
		b.Fatalf("topic mismatch: want %q got %q", wantTopic, pub.Topic)
	}
	if !bytes.Equal(pub.Payload, wantPayload) {
		b.Fatalf("payload mismatch: want %d bytes got %d bytes", len(wantPayload), len(pub.Payload))
	}
	ack := &protocol.PubAckPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubAck},
		PacketID:    pub.PacketID,
	}
	conn.SetDeadline(time.Now().Add(time.Second))
	if err := codec.Encode(conn, ack); err != nil {
		b.Fatalf("PUBACK encode: %v", err)
	}
}

// verifyAndAckQoS2 receives a QoS 2 PUBLISH, verifies data, completes 4-way handshake.
func verifyAndAckQoS2(b *testing.B, conn net.Conn, codec *protocol.Codec, wantTopic string, wantPayload []byte) {
	b.Helper()
	// 1. Receive PUBLISH
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		b.Fatalf("subscriber receive PUBLISH: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		b.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if pub.Topic != wantTopic {
		b.Fatalf("topic mismatch: want %q got %q", wantTopic, pub.Topic)
	}
	if !bytes.Equal(pub.Payload, wantPayload) {
		b.Fatalf("payload mismatch: want %d bytes got %d bytes", len(wantPayload), len(pub.Payload))
	}
	// 2. Send PUBREC
	rec := &protocol.PubRecPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubRec},
		PacketID:    pub.PacketID,
	}
	conn.SetDeadline(time.Now().Add(time.Second))
	if err := codec.Encode(conn, rec); err != nil {
		b.Fatalf("PUBREC encode: %v", err)
	}
	// 3. Receive PUBREL
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err = codec.Decode(conn)
	if err != nil {
		b.Fatalf("subscriber receive PUBREL: %v", err)
	}
	if _, ok := pkt.(*protocol.PubRelPacket); !ok {
		b.Fatalf("expected PUBREL, got %T", pkt)
	}
	// 4. Send PUBCOMP
	comp := &protocol.PubCompPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubComp},
		PacketID:    pub.PacketID,
	}
	conn.SetDeadline(time.Now().Add(time.Second))
	if err := codec.Encode(conn, comp); err != nil {
		b.Fatalf("PUBCOMP encode: %v", err)
	}
}

// ---------------------------------------------------------------------------
// E2E QoS 0 — publish, subscribe receives and verifies payload
// ---------------------------------------------------------------------------

func BenchmarkE2E_QoS0_DataVerify(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	subConn, subCodec := connectedClient(b, brk, "e2e-sub-qos0")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "e2e/qos0", 0)

	pubConn, pubCodec := connectedClient(b, brk, "e2e-pub-qos0")
	defer pubConn.Close()

	payload := []byte("e2e-verify-data-qos0")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
			Topic:       "e2e/qos0",
			Payload:     payload,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if err := pubCodec.Encode(pubConn, pkt); err != nil {
			b.Fatalf("publish: %v", err)
		}
		verifyPublish(b, subConn, subCodec, "e2e/qos0", payload)
	}

	b.StopTimer()
	b.Logf("data delivery verified: %d msgs, topic=e2e/qos0, payload=%d bytes, qos=0", b.N, len(payload))
}

// ---------------------------------------------------------------------------
// E2E QoS 1 — full publish→PUBACK with data verification on subscriber
// ---------------------------------------------------------------------------

func BenchmarkE2E_QoS1_DataVerify(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	subConn, subCodec := connectedClient(b, brk, "e2e-sub-qos1")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "e2e/qos1", 1)

	pubConn, pubCodec := connectedClient(b, brk, "e2e-pub-qos1")
	defer pubConn.Close()

	payload := []byte("e2e-verify-data-qos1")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pid := uint16(i%65534 + 1)
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
			PacketID:    pid,
			Topic:       "e2e/qos1",
			Payload:     payload,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if err := pubCodec.Encode(pubConn, pkt); err != nil {
			b.Fatalf("publish: %v", err)
		}
		// Publisher receives PUBACK
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if _, err := pubCodec.Decode(pubConn); err != nil {
			b.Fatalf("PUBACK: %v", err)
		}
		// Subscriber receives and verifies
		verifyAndAckQoS1(b, subConn, subCodec, "e2e/qos1", payload)
	}

	b.StopTimer()
	b.Logf("data delivery verified: %d msgs, topic=e2e/qos1, payload=%d bytes, qos=1 (PUBACK)", b.N, len(payload))
}

// ---------------------------------------------------------------------------
// E2E QoS 2 — full 4-packet handshake with data verification
// ---------------------------------------------------------------------------

func BenchmarkE2E_QoS2_DataVerify(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	subConn, subCodec := connectedClient(b, brk, "e2e-sub-qos2")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "e2e/qos2", 2)

	pubConn, pubCodec := connectedClient(b, brk, "e2e-pub-qos2")
	defer pubConn.Close()

	payload := []byte("e2e-verify-data-qos2")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pid := uint16(i%65534 + 1)

		// Publisher: PUBLISH
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 2},
			PacketID:    pid,
			Topic:       "e2e/qos2",
			Payload:     payload,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if err := pubCodec.Encode(pubConn, pkt); err != nil {
			b.Fatalf("publish: %v", err)
		}
		// Publisher: receive PUBREC
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if _, err := pubCodec.Decode(pubConn); err != nil {
			b.Fatalf("PUBREC: %v", err)
		}
		// Publisher: send PUBREL
		rel := &protocol.PubRelPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubRel, QoS: 1},
			PacketID:    pid,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if err := pubCodec.Encode(pubConn, rel); err != nil {
			b.Fatalf("PUBREL: %v", err)
		}
		// Publisher: receive PUBCOMP
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if _, err := pubCodec.Decode(pubConn); err != nil {
			b.Fatalf("PUBCOMP: %v", err)
		}

		// Subscriber: full QoS 2 verification
		verifyAndAckQoS2(b, subConn, subCodec, "e2e/qos2", payload)
	}

	b.StopTimer()
	b.Logf("data delivery verified: %d msgs, topic=e2e/qos2, payload=%d bytes, qos=2 (PUBREC/PUBREL/PUBCOMP)", b.N, len(payload))
}

// ---------------------------------------------------------------------------
// E2E Retained message — subscriber receives retained publish, verifies data
// ---------------------------------------------------------------------------

func BenchmarkE2E_RetainedMessage(b *testing.B) {
	brk := setupBrokerWithRetain(b)
	defer brk.Stop()

	// Subscriber connects first, subscribes to wildcard
	subConn, subCodec := connectedClient(b, brk, "retain-sub")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "retain/#", 0)

	// Publisher sets retained message (stays connected)
	pubConn, pubCodec := connectedClient(b, brk, "retain-pub")
	defer pubConn.Close()

	payload := []byte("retained-benchmark-data")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Publish with retain flag — subscriber receives it immediately
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePublish,
				Retain:     true,
			},
			Topic:   fmt.Sprintf("retain/bench/%d", i%10),
			Payload: payload,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if err := pubCodec.Encode(pubConn, pkt); err != nil {
			b.Fatalf("retained publish: %v", err)
		}
		// Subscriber verifies the data
		expectedTopic := fmt.Sprintf("retain/bench/%d", i%10)
		verifyPublish(b, subConn, subCodec, expectedTopic, payload)
	}

	b.StopTimer()
	b.Logf("retained publish+delivery verified: %d msgs, payload=%d bytes, subscriber received all retained publishes", b.N, len(payload))
}

// ---------------------------------------------------------------------------
// E2E Will message — client dies, subscriber receives will
// Reuses subscriber to avoid port exhaustion on Windows.
// ---------------------------------------------------------------------------

func BenchmarkE2E_WillMessage(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	willPayload := []byte("will-benchmark-data")

	// Single subscriber reused across iterations
	subConn, subCodec := connectedClient(b, brk, "will-sub")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "will/bench", 0)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Client connects with will message
		dieConn := dialBroker(b, brk)
		dieCodec := protocol.NewCodec(0)
		connectPkt := &protocol.ConnectPacket{
			FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
			ProtocolName:    protocol.ProtocolNameMQTT,
			ProtocolVersion: protocol.Version50,
			Flags: protocol.ConnectFlags{
				CleanSession: true,
				WillFlag:     true,
				WillQoS:      0,
			},
			KeepAlive:   1,
			ClientID:    fmt.Sprintf("will-client-%d", i),
			WillTopic:   "will/bench",
			WillMessage: willPayload,
		}
		dieConn.SetDeadline(time.Now().Add(2 * time.Second))
		dieCodec.Encode(dieConn, connectPkt)
		dieCodec.Decode(dieConn)

		// Kill client without DISCONNECT (abnormal close)
		dieConn.Close()

		// Subscriber receives will message
		verifyPublish(b, subConn, subCodec, "will/bench", willPayload)
	}

	b.StopTimer()
	b.Logf("will delivery verified: %d cycles, topic=will/bench, payload=%d bytes", b.N, len(willPayload))
}

// ---------------------------------------------------------------------------
// E2E Wildcard delivery — subscribe with wildcard, verify data from exact topic
// ---------------------------------------------------------------------------

func BenchmarkE2E_WildcardDelivery(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	subConn, subCodec := connectedClient(b, brk, "e2e-wild-sub")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "e2e/sensor/+/temperature", 0)

	pubConn, pubCodec := connectedClient(b, brk, "e2e-wild-pub")
	defer pubConn.Close()

	rooms := []string{"room0", "room1", "room2", "room3", "room4",
		"room5", "room6", "room7", "room8", "room9"}
	payload := []byte("22.5C")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		topic := "e2e/sensor/" + rooms[i%10] + "/temperature"
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
			Topic:       topic,
			Payload:     payload,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if err := pubCodec.Encode(pubConn, pkt); err != nil {
			b.Fatalf("publish: %v", err)
		}
		verifyPublish(b, subConn, subCodec, topic, payload)
	}

	b.StopTimer()
	b.Logf("wildcard delivery verified: %d msgs, pattern=e2e/sensor/+/temperature, payload=%s", b.N, payload)
}

// ---------------------------------------------------------------------------
// E2E Fan-out — 1 publisher, N subscribers, all verify received data
// ---------------------------------------------------------------------------

func BenchmarkE2E_FanOut_VerifyAll(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	subs := 5
	var subConns []net.Conn
	var subCodecs []*protocol.Codec
	for i := 0; i < subs; i++ {
		conn, codec := connectedClient(b, brk, fmt.Sprintf("fanout-sub-%d", i))
		defer conn.Close()
		subscribeTopic(b, conn, codec, "fanout/e2e", 0)
		subConns = append(subConns, conn)
		subCodecs = append(subCodecs, codec)
	}

	pubConn, pubCodec := connectedClient(b, brk, "fanout-pub")
	defer pubConn.Close()

	payload := []byte("fanout-verify-data")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
			Topic:       "fanout/e2e",
			Payload:     payload,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if err := pubCodec.Encode(pubConn, pkt); err != nil {
			b.Fatalf("publish: %v", err)
		}
		for j := 0; j < subs; j++ {
			verifyPublish(b, subConns[j], subCodecs[j], "fanout/e2e", payload)
		}
	}

	b.StopTimer()
	b.Logf("fanout verified: %d msgs x %d subscribers = %d deliveries, topic=fanout/e2e, payload=%d bytes",
		b.N, subs, b.N*subs, len(payload))
}

// ---------------------------------------------------------------------------
// E2E Payload types — string, binary, unicode, empty
// ---------------------------------------------------------------------------

func BenchmarkE2E_Payload_String(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	subConn, subCodec := connectedClient(b, brk, "str-sub")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "e2e/string", 0)

	pubConn, pubCodec := connectedClient(b, brk, "str-pub")
	defer pubConn.Close()

	payload := []byte("Hello MQTT 5.0 — benchmark data verification test")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
			Topic:       "e2e/string",
			Payload:     payload,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		pubCodec.Encode(pubConn, pkt)
		verifyPublish(b, subConn, subCodec, "e2e/string", payload)
	}

	b.StopTimer()
	b.Logf("string payload verified: %d msgs, data=%q", b.N, payload)
}

func BenchmarkE2E_Payload_Binary(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	subConn, subCodec := connectedClient(b, brk, "bin-sub")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "e2e/binary", 0)

	pubConn, pubCodec := connectedClient(b, brk, "bin-pub")
	defer pubConn.Close()

	// Structured binary: sensor ID (4 bytes) + timestamp (8 bytes) + value (4 bytes)
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x42, 0x00, 0x00, 0x00}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
			Topic:       "e2e/binary",
			Payload:     payload,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		pubCodec.Encode(pubConn, pkt)
		verifyPublish(b, subConn, subCodec, "e2e/binary", payload)
	}

	b.StopTimer()
	b.Logf("binary payload verified: %d msgs, size=%d bytes, hex=%x", b.N, len(payload), payload)
}

func BenchmarkE2E_Payload_Unicode(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	subConn, subCodec := connectedClient(b, brk, "uni-sub")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "e2e/unicode", 0)

	pubConn, pubCodec := connectedClient(b, brk, "uni-pub")
	defer pubConn.Close()

	payload := []byte("温度:22.5°C 湿度:65% 状态:正常 中文测试 日本語テスト")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
			Topic:       "e2e/unicode",
			Payload:     payload,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		pubCodec.Encode(pubConn, pkt)
		verifyPublish(b, subConn, subCodec, "e2e/unicode", payload)
	}

	b.StopTimer()
	b.Logf("unicode payload verified: %d msgs, data=%s", b.N, payload)
}

func BenchmarkE2E_Payload_Empty(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	subConn, subCodec := connectedClient(b, brk, "empty-sub")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "e2e/empty", 0)

	pubConn, pubCodec := connectedClient(b, brk, "empty-pub")
	defer pubConn.Close()

	payload := []byte{}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
			Topic:       "e2e/empty",
			Payload:     payload,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		pubCodec.Encode(pubConn, pkt)
		verifyPublish(b, subConn, subCodec, "e2e/empty", payload)
	}

	b.StopTimer()
	b.Logf("empty payload verified: %d msgs, size=0", b.N)
}

// ---------------------------------------------------------------------------
// E2E Large payload — verify 64KB data integrity
// ---------------------------------------------------------------------------

func BenchmarkE2E_Payload_64KB(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	subConn, subCodec := connectedClient(b, brk, "64k-sub")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "e2e/large", 0)

	pubConn, pubCodec := connectedClient(b, brk, "64k-pub")
	defer pubConn.Close()

	// Pattern-filled payload for integrity checking
	payload := make([]byte, 64*1024)
	for i := range payload {
		payload[i] = byte(i % 251) // prime for pattern variety
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
			Topic:       "e2e/large",
			Payload:     payload,
		}
		pubConn.SetDeadline(time.Now().Add(5 * time.Second))
		pubCodec.Encode(pubConn, pkt)
		verifyPublish(b, subConn, subCodec, "e2e/large", payload)
	}

	b.StopTimer()
	b.Logf("64KB payload integrity verified: %d msgs, every byte checked", b.N)
}
