// Package bench provides performance benchmark tests for shark-mqtt.
// Run with: go test -bench=. -benchmem -benchtime=10s ./test/bench/...
package bench

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// setupBroker creates a broker for benchmarking
func setupBroker(b *testing.B) *api.Broker {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MaxInflight = 1000

	broker := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(auth.AllowAllAuth{}),
	)
	if err := broker.Start(); err != nil {
		b.Fatalf("failed to start broker: %v", err)
	}
	return broker
}

// dialBenchmarkBroker connects to the broker and returns the connection
func dialBenchmarkBroker(b *testing.B, broker *api.Broker) net.Conn {
	addr := broker.Addr()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		b.Fatalf("failed to dial broker at %s: %v", addr, err)
	}
	return conn
}

// BenchmarkConnectionEstablish measures TCP connection establishment throughput
func BenchmarkConnectionEstablish(b *testing.B) {
	broker := setupBroker(b)
	defer broker.Stop()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		conn, err := net.DialTimeout("tcp", broker.Addr(), time.Second)
		if err != nil {
			b.Fatalf("failed to dial: %v", err)
		}
		conn.Close()
	}
}

// BenchmarkMQTTConnect measures full MQTT CONNECT/CONNACK flow
func BenchmarkMQTTConnect(b *testing.B) {
	broker := setupBroker(b)
	defer broker.Stop()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		conn := dialBenchmarkBroker(b, broker)
		codec := protocol.NewCodec(65536)

		connectPkt := &protocol.ConnectPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypeConnect,
			},
			ProtocolName:    protocol.ProtocolNameMQTT,
			ProtocolVersion: protocol.Version50,
			Flags:           protocol.ConnectFlags{CleanSession: true},
			KeepAlive:       60,
			ClientID:        "bench-client",
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		conn.SetDeadline(time.Now().Add(time.Second))

		codec.Encode(conn, connectPkt)
		codec.Decode(conn)

		cancel()
		conn.Close()
	}
}

// BenchmarkPublishQos0 measures QoS 0 publish throughput (fire-and-forget)
func BenchmarkPublishQos0(b *testing.B) {
	broker := setupBroker(b)
	defer broker.Stop()

	// Setup publisher
	pubConn := dialBenchmarkBroker(b, broker)
	pubCodec := protocol.NewCodec(65536)

	connectClient(b, pubConn, pubCodec, "pub-client")

	// Setup subscriber
	subConn := dialBenchmarkBroker(b, broker)
	subCodec := protocol.NewCodec(65536)

	connectClient(b, subConn, subCodec, "sub-client")
	subscribe(b, subConn, subCodec, "bench/topic", 0)

	payload := make([]byte, 256)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		packetID := uint16(i%65535 + 1)
		publishQos0(b, pubConn, pubCodec, "bench/topic", payload, packetID)
	}
}

// BenchmarkPublishQos1 measures QoS 1 publish throughput with PUBACK
func BenchmarkPublishQos1(b *testing.B) {
	broker := setupBroker(b)
	defer broker.Stop()

	// Setup publisher
	pubConn := dialBenchmarkBroker(b, broker)
	pubCodec := protocol.NewCodec(65536)

	connectClient(b, pubConn, pubCodec, "pub-client")

	// Setup subscriber
	subConn := dialBenchmarkBroker(b, broker)
	subCodec := protocol.NewCodec(65536)

	connectClient(b, subConn, subCodec, "sub-client")
	subscribe(b, subConn, subCodec, "bench/topic", 1)

	payload := make([]byte, 256)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		packetID := uint16(i%65535 + 1)
		publishQos1(b, pubConn, pubCodec, "bench/topic", payload, packetID)
		readPubAck(b, pubConn, pubCodec)
	}
}

// BenchmarkPublishQos2 measures QoS 2 publish throughput (exactly-once)
func BenchmarkPublishQos2(b *testing.B) {
	broker := setupBroker(b)
	defer broker.Stop()

	// Setup publisher
	pubConn := dialBenchmarkBroker(b, broker)
	pubCodec := protocol.NewCodec(65536)

	connectClient(b, pubConn, pubCodec, "pub-client")

	// Setup subscriber
	subConn := dialBenchmarkBroker(b, broker)
	subCodec := protocol.NewCodec(65536)

	connectClient(b, subConn, subCodec, "sub-client")
	subscribe(b, subConn, subCodec, "bench/topic", 2)

	payload := make([]byte, 256)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		packetID := uint16(i%65535 + 1)
		publishQos2FullFlow(b, pubConn, pubCodec, subConn, subCodec, "bench/topic", payload, packetID)
	}
}

// BenchmarkConcurrentPublish measures throughput with multiple concurrent publishers
func BenchmarkConcurrentPublish(b *testing.B) {
	broker := setupBroker(b)
	defer broker.Stop()

	// Setup subscriber
	subConn := dialBenchmarkBroker(b, broker)
	subCodec := protocol.NewCodec(65536)
	connectClient(b, subConn, subCodec, "subscriber")
	subscribe(b, subConn, subCodec, "bench/topic", 0)

	payload := make([]byte, 256)
	concurrency := 10

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		conn := dialBenchmarkBroker(b, broker)
		codec := protocol.NewCodec(65536)
		clientID := "parallel-publisher"
		connectClient(b, conn, codec, clientID)

		i := 0
		for pb.Next() {
			packetID := uint16(i%65535 + 1)
			publishQos0(b, conn, codec, "bench/topic", payload, packetID)
			i++
		}
		conn.Close()
	})

	// Calculate operations per second
	b.ReportMetric(float64(b.N*concurrency)/b.Elapsed().Seconds(), "ops/sec")
}

// BenchmarkTopicWildcardMatch measures topic tree wildcard matching performance
func BenchmarkTopicWildcardMatch(b *testing.B) {
	// This benchmark tests the topic tree matching without network overhead
	broker := setupBroker(b)
	defer broker.Stop()

	// Setup subscriber with wildcard
	subConn := dialBenchmarkBroker(b, broker)
	subCodec := protocol.NewCodec(65536)
	connectClient(b, subConn, subCodec, "wildcard-subscriber")
	subscribe(b, subConn, subCodec, "bench/+/temperature", 0)

	payload := make([]byte, 256)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		topic := "bench/" + string(rune(i%10)+'0') + "/temperature"
		packetID := uint16(i%65535 + 1)
		publishQos0(b, subConn, subCodec, topic, payload, packetID)
	}
}

// BenchmarkMemoryUsage measures memory allocation patterns
func BenchmarkMemoryUsage(b *testing.B) {
	broker := setupBroker(b)
	defer broker.Stop()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		conn := dialBenchmarkBroker(b, broker)
		codec := protocol.NewCodec(65536)

		connectPkt := &protocol.ConnectPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypeConnect,
			},
			ProtocolName:    protocol.ProtocolNameMQTT,
			ProtocolVersion: protocol.Version50,
			Flags:           protocol.ConnectFlags{CleanSession: true},
			KeepAlive:       60,
			ClientID:        "mem-bench-client",
		}

		conn.SetDeadline(time.Now().Add(time.Second))
		codec.Encode(conn, connectPkt)
		codec.Decode(conn)
		conn.Close()
	}
}

// BenchmarkPersistentSession measures session persistence performance
func BenchmarkPersistentSession(b *testing.B) {
	broker := setupBroker(b)
	defer broker.Stop()

	clientID := "persistent-client"
	payload := make([]byte, 256)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Connect with persistent session
		conn := dialBenchmarkBroker(b, broker)
		codec := protocol.NewCodec(65536)

		connectPkt := &protocol.ConnectPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypeConnect,
			},
			ProtocolName:    protocol.ProtocolNameMQTT,
			ProtocolVersion: protocol.Version50,
			Flags:           protocol.ConnectFlags{CleanSession: false}, // Persistent session
			KeepAlive:       60,
			ClientID:        clientID,
		}

		codec.Encode(conn, connectPkt)
		codec.Decode(conn)

		// Subscribe
		subscribe(b, conn, codec, "bench/topic", 1)

		// Publish
		publishQos1(b, conn, codec, "bench/topic", payload, 1)
		readPubAck(b, conn, codec)

		// Disconnect
		conn.Close()
	}
}

// Helper functions for benchmarks

func connectClient(b *testing.B, conn net.Conn, codec *protocol.Codec, clientID string) {
	connectPkt := &protocol.ConnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnect,
		},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        clientID,
	}

	conn.SetDeadline(time.Now().Add(time.Second))
	codec.Encode(conn, connectPkt)
	codec.Decode(conn)
}

func subscribe(b *testing.B, conn net.Conn, codec *protocol.Codec, topic string, qos uint8) {
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeSubscribe,
			QoS:        1,
		},
		PacketID: 1,
		Topics: []protocol.TopicSubscription{
			{Topic: topic, QoS: qos},
		},
	}

	conn.SetDeadline(time.Now().Add(time.Second))
	codec.Encode(conn, subPkt)
	codec.Decode(conn)
}

func publishQos0(b *testing.B, conn net.Conn, codec *protocol.Codec, topic string, payload []byte, packetID uint16) {
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        0,
		},
		Topic:   topic,
		Payload: payload,
	}

	conn.SetDeadline(time.Now().Add(time.Second))
	codec.Encode(conn, pubPkt)
}

func publishQos1(b *testing.B, conn net.Conn, codec *protocol.Codec, topic string, payload []byte, packetID uint16) {
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        1,
		},
		Topic:    topic,
		Payload:  payload,
		PacketID: packetID,
	}

	conn.SetDeadline(time.Now().Add(time.Second))
	codec.Encode(conn, pubPkt)
}

func readPubAck(b *testing.B, conn net.Conn, codec *protocol.Codec) {
	conn.SetDeadline(time.Now().Add(time.Second))
	codec.Decode(conn)
}

func publishQos2FullFlow(b *testing.B, pubConn net.Conn, pubCodec *protocol.Codec, subConn net.Conn, subCodec *protocol.Codec, topic string, payload []byte, packetID uint16) {
	// Publish
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        2,
		},
		Topic:    topic,
		Payload:  payload,
		PacketID: packetID,
	}

	pubConn.SetDeadline(time.Now().Add(time.Second))
	pubCodec.Encode(pubConn, pubPkt)

	// Read PUBREC from subscriber
	subConn.SetDeadline(time.Now().Add(time.Second))
	subCodec.Decode(subConn)

	// Send PUBREL to publisher
	pubRelPkt := &protocol.PubRelPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePubRel,
			QoS:        1,
		},
		PacketID: packetID,
	}
	pubConn.SetDeadline(time.Now().Add(time.Second))
	pubCodec.Encode(pubConn, pubRelPkt)

	// Read PUBCOMP from subscriber
	subConn.SetDeadline(time.Now().Add(time.Second))
	subCodec.Decode(subConn)
}
