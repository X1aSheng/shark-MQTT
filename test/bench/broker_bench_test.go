// Package bench provides performance benchmark tests for shark-mqtt.
//
// Run all benchmarks:
//
//	go test -bench=. -benchmem -benchtime=5s ./test/bench/...
//
// Run a specific benchmark:
//
//	go test -bench=BenchmarkPublishQos0 -benchmem ./test/bench/...
//
// With CPU profiling:
//
//	go test -bench=. -cpuprofile=cpu.prof ./test/bench/...
//	go tool pprof cpu.prof
package bench

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// ---------------------------------------------------------------------------
// Broker lifecycle helpers
// ---------------------------------------------------------------------------

func setupBroker(b *testing.B) *api.Broker {
	b.Helper()
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.QoSMaxInflight = 1000

	brk := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := brk.Start(); err != nil {
		b.Fatalf("start broker: %v", err)
	}
	return brk
}

func dialBroker(b *testing.B, brk *api.Broker) net.Conn {
	b.Helper()
	conn, err := net.DialTimeout("tcp", brk.Addr(), 2*time.Second)
	if err != nil {
		b.Fatalf("dial %s: %v", brk.Addr(), err)
	}
	return conn
}

func connectedClient(b *testing.B, brk *api.Broker, clientID string) (net.Conn, *protocol.Codec) {
	b.Helper()
	conn := dialBroker(b, brk)
	codec := protocol.NewCodec(0)

	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        clientID,
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		b.Fatalf("CONNECT encode: %v", err)
	}
	if _, err := codec.Decode(conn); err != nil {
		b.Fatalf("CONNACK decode: %v", err)
	}
	return conn, codec
}

func subscribeTopic(b *testing.B, conn net.Conn, codec *protocol.Codec, topic string, qos uint8) {
	b.Helper()
	pkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    100, // Use high ID to avoid collision with publish packet IDs
		Topics:      []protocol.TopicFilter{{Topic: topic, QoS: qos}},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		b.Fatalf("SUBSCRIBE encode: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := codec.Decode(conn); err != nil {
		b.Fatalf("SUBACK decode: %v", err)
	}
}

// drainConn starts a goroutine that discards all data read from conn.
// This prevents subscriber buffers from filling up during fire-and-forget
// publish benchmarks (QoS 0). Returns a stop function.
func drainConn(conn net.Conn) (stop func()) {
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			select {
			case <-done:
				return
			default:
				conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				_, err := conn.Read(buf)
				if err != nil {
					if !isTimeout(err) {
						return
					}
				}
			}
		}
	}()
	return func() { close(done) }
}

func isTimeout(err error) bool {
	if ne, ok := err.(net.Error); ok {
		return ne.Timeout()
	}
	return false
}

// ---------------------------------------------------------------------------
// Connection benchmarks
// ---------------------------------------------------------------------------

func BenchmarkConnectionEstablish(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		conn, err := net.DialTimeout("tcp", brk.Addr(), time.Second)
		if err != nil {
			b.Fatalf("dial: %v", err)
		}
		conn.Close()
	}
}

func BenchmarkMQTTConnect(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		conn, _ := connectedClient(b, brk, fmt.Sprintf("conn-%d", i))
		conn.Close()
	}
}

// ---------------------------------------------------------------------------
// Publish/Subscribe benchmarks
// ---------------------------------------------------------------------------

func BenchmarkPublishQos0(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	subConn, subCodec := connectedClient(b, brk, "sub-qos0")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "bench/topic", 0)
	stop := drainConn(subConn)
	defer stop()

	pubConn, pubCodec := connectedClient(b, brk, "pub-qos0")
	defer pubConn.Close()

	payload := make([]byte, 256)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
			Topic:       "bench/topic",
			Payload:     payload,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if err := pubCodec.Encode(pubConn, pkt); err != nil {
			b.Fatalf("PUBLISH encode: %v", err)
		}
	}
}

func BenchmarkPublishQos1(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	subConn, subCodec := connectedClient(b, brk, "sub-qos1")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "bench/topic", 1)
	stop := drainConn(subConn)
	defer stop()

	pubConn, pubCodec := connectedClient(b, brk, "pub-qos1")
	defer pubConn.Close()

	payload := make([]byte, 256)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pid := uint16(i%65534 + 1)
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
			PacketID:    pid,
			Topic:       "bench/topic",
			Payload:     payload,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if err := pubCodec.Encode(pubConn, pkt); err != nil {
			b.Fatalf("PUBLISH encode: %v", err)
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if _, err := pubCodec.Decode(pubConn); err != nil {
			b.Fatalf("PUBACK decode: %v", err)
		}
	}
}

func BenchmarkPublishQos2(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	subConn, subCodec := connectedClient(b, brk, "sub-qos2")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "bench/topic", 2)
	stop := drainConn(subConn)
	defer stop()

	pubConn, pubCodec := connectedClient(b, brk, "pub-qos2")
	defer pubConn.Close()

	payload := make([]byte, 256)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pid := uint16(i%65534 + 1)

		// PUBLISH
		pubPkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 2},
			PacketID:    pid,
			Topic:       "bench/topic",
			Payload:     payload,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if err := pubCodec.Encode(pubConn, pubPkt); err != nil {
			b.Fatalf("PUBLISH encode: %v", err)
		}

		// PUBREC
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if _, err := pubCodec.Decode(pubConn); err != nil {
			b.Fatalf("PUBREC decode: %v", err)
		}

		// PUBREL
		pubRel := &protocol.PubRelPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubRel, QoS: 1},
			PacketID:    pid,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if err := pubCodec.Encode(pubConn, pubRel); err != nil {
			b.Fatalf("PUBREL encode: %v", err)
		}

		// PUBCOMP
		pubConn.SetDeadline(time.Now().Add(time.Second))
		if _, err := pubCodec.Decode(pubConn); err != nil {
			b.Fatalf("PUBCOMP decode: %v", err)
		}
	}
}

func BenchmarkConcurrentPublish(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	subConn, subCodec := connectedClient(b, brk, "concurrent-sub")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "bench/topic", 0)
	stop := drainConn(subConn)
	defer stop()

	payload := make([]byte, 256)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		conn, codec := connectedClient(b, brk, fmt.Sprintf("par-%d", time.Now().UnixNano()))
		defer conn.Close()

		for pb.Next() {
			pkt := &protocol.PublishPacket{
				FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
				Topic:       "bench/topic",
				Payload:     payload,
			}
			conn.SetDeadline(time.Now().Add(time.Second))
			codec.Encode(conn, pkt)
		}
	})
}

// ---------------------------------------------------------------------------
// Topic wildcard matching
// ---------------------------------------------------------------------------

func BenchmarkTopicWildcardMatch(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	subConn, subCodec := connectedClient(b, brk, "wild-sub")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "bench/+/temperature", 0)
	stop := drainConn(subConn)
	defer stop()

	pubConn, pubCodec := connectedClient(b, brk, "wild-pub")
	defer pubConn.Close()

	payload := make([]byte, 64)

	rooms := []string{"room0", "room1", "room2", "room3", "room4",
		"room5", "room6", "room7", "room8", "room9"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
			Topic:       "bench/" + rooms[i%10] + "/temperature",
			Payload:     payload,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		pubCodec.Encode(pubConn, pkt)
	}
}

// ---------------------------------------------------------------------------
// Persistent session
// ---------------------------------------------------------------------------

func BenchmarkPersistentSession(b *testing.B) {
	brk := setupBroker(b)
	defer brk.Stop()

	payload := make([]byte, 256)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		conn := dialBroker(b, brk)
		codec := protocol.NewCodec(0)

		// CONNECT with CleanSession=false
		connectPkt := &protocol.ConnectPacket{
			FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
			ProtocolName:    protocol.ProtocolNameMQTT,
			ProtocolVersion: protocol.Version50,
			Flags:           protocol.ConnectFlags{CleanSession: false},
			KeepAlive:       60,
			ClientID:        "persist-bench",
		}
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		codec.Encode(conn, connectPkt)
		codec.Decode(conn)

		// SUBSCRIBE
		subscribeTopic(b, conn, codec, "bench/topic", 1)

		// PUBLISH QoS 1 + PUBACK
		pid := uint16(i%65534 + 1)
		pubPkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
			PacketID:    pid,
			Topic:       "bench/topic",
			Payload:     payload,
		}
		conn.SetDeadline(time.Now().Add(time.Second))
		codec.Encode(conn, pubPkt)
		conn.SetDeadline(time.Now().Add(time.Second))
		codec.Decode(conn) // PUBACK

		conn.Close()
	}
}

// ---------------------------------------------------------------------------
// Varying payload sizes
// ---------------------------------------------------------------------------

func benchPayload(b *testing.B, size int) {
	brk := setupBroker(b)
	defer brk.Stop()

	subConn, subCodec := connectedClient(b, brk, "sub-payload")
	defer subConn.Close()
	subscribeTopic(b, subConn, subCodec, "bench/payload", 0)
	stop := drainConn(subConn)
	defer stop()

	pubConn, pubCodec := connectedClient(b, brk, "pub-payload")
	defer pubConn.Close()

	payload := make([]byte, size)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
			Topic:       "bench/payload",
			Payload:     payload,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		pubCodec.Encode(pubConn, pkt)
	}
}

func BenchmarkPayload_64B(b *testing.B)   { benchPayload(b, 64) }
func BenchmarkPayload_256B(b *testing.B)  { benchPayload(b, 256) }
func BenchmarkPayload_1KB(b *testing.B)   { benchPayload(b, 1024) }
func BenchmarkPayload_4KB(b *testing.B)   { benchPayload(b, 4096) }
func BenchmarkPayload_16KB(b *testing.B)  { benchPayload(b, 16 * 1024) }
func BenchmarkPayload_128KB(b *testing.B) { benchPayload(b, 128 * 1024) }

// ---------------------------------------------------------------------------
// Fan-out (1 publisher -> N subscribers)
// ---------------------------------------------------------------------------

func benchFanOut(b *testing.B, subs int) {
	brk := setupBroker(b)
	defer brk.Stop()

	var stops []func()
	for i := 0; i < subs; i++ {
		subConn, subCodec := connectedClient(b, brk, fmt.Sprintf("fanout-sub-%d", i))
		defer subConn.Close()
		subscribeTopic(b, subConn, subCodec, "fanout/topic", 0)
		stops = append(stops, drainConn(subConn))
	}
	defer func() {
		for _, s := range stops {
			s()
		}
	}()

	pubConn, pubCodec := connectedClient(b, brk, "fanout-pub")
	defer pubConn.Close()

	payload := make([]byte, 256)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
			Topic:       "fanout/topic",
			Payload:     payload,
		}
		pubConn.SetDeadline(time.Now().Add(time.Second))
		pubCodec.Encode(pubConn, pkt)
	}
}

func BenchmarkFanOut_1Sub(b *testing.B)   { benchFanOut(b, 1) }
func BenchmarkFanOut_5Subs(b *testing.B)  { benchFanOut(b, 5) }
func BenchmarkFanOut_10Subs(b *testing.B) { benchFanOut(b, 10) }
func BenchmarkFanOut_50Subs(b *testing.B) { benchFanOut(b, 50) }
