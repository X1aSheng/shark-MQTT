package bench

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/pkg/bufferpool"
	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store/memory"
)

// ---------------------------------------------------------------------------
// TopicTree benchmarks
// ---------------------------------------------------------------------------

func BenchmarkTopicTree_Subscribe(b *testing.B) {
	tt := broker.NewTopicTree()
	topics := []string{
		"sensors/room1/temperature",
		"sensors/room2/temperature",
		"sensors/+/humidity",
		"sensors/#",
		"cmd/device/+/status",
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tt.Subscribe(topics[i%len(topics)], "client-1", 0)
	}
}

func BenchmarkTopicTree_Match_Exact(b *testing.B) {
	tt := broker.NewTopicTree()
	tt.Subscribe("sensors/room1/temperature", "c1", 0)
	tt.Subscribe("sensors/room1/humidity", "c2", 0)
	tt.Subscribe("sensors/room2/temperature", "c3", 0)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tt.Match("sensors/room1/temperature")
	}
}

func BenchmarkTopicTree_Match_WildcardPlus(b *testing.B) {
	tt := broker.NewTopicTree()
	tt.Subscribe("sensors/+/temperature", "c1", 0)
	tt.Subscribe("sensors/+/humidity", "c2", 0)
	tt.Subscribe("sensors/#", "c3", 0)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tt.Match("sensors/room1/temperature")
	}
}

func BenchmarkTopicTree_Match_WildcardHash(b *testing.B) {
	tt := broker.NewTopicTree()
	tt.Subscribe("sensors/#", "c1", 0)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tt.Match("sensors/room1/temperature")
	}
}

func BenchmarkTopicTree_Match_ManySubscribers(b *testing.B) {
	tt := broker.NewTopicTree()
	for i := 0; i < 100; i++ {
		tt.Subscribe("sensors/#", "client-"+string(rune(i)), 0)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tt.Match("sensors/room1/temperature")
	}
}

func BenchmarkTopicTree_Unsubscribe(b *testing.B) {
	tt := broker.NewTopicTree()
	for i := 0; i < 1000; i++ {
		tt.Subscribe("topic/sub", "c"+string(rune(i%256)), 0)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		tt.Unsubscribe("topic/sub", "c"+string(rune(i%256)))
	}
}

// ---------------------------------------------------------------------------
// Protocol Codec benchmarks
// ---------------------------------------------------------------------------

func BenchmarkCodec_EncodeConnect(b *testing.B) {
	codec := protocol.NewCodec(0)
	var buf bytes.Buffer

	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "bench-client",
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		buf.Reset()
		codec.Encode(&buf, pkt)
	}
}

func BenchmarkCodec_DecodeConnect(b *testing.B) {
	codec := protocol.NewCodec(0)
	var buf bytes.Buffer

	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "bench-client",
	}
	codec.Encode(&buf, pkt)
	raw := buf.Bytes()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		codec.Decode(bytes.NewReader(raw))
	}
}

func BenchmarkCodec_EncodePublish(b *testing.B) {
	codec := protocol.NewCodec(0)
	var buf bytes.Buffer

	payload := make([]byte, 256)
	pkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
		Topic:       "bench/topic",
		Payload:     payload,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		buf.Reset()
		codec.Encode(&buf, pkt)
	}
}

func BenchmarkCodec_DecodePublish(b *testing.B) {
	codec := protocol.NewCodec(0)
	var buf bytes.Buffer

	payload := make([]byte, 256)
	pkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
		Topic:       "bench/topic",
		Payload:     payload,
	}
	codec.Encode(&buf, pkt)
	raw := buf.Bytes()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		codec.Decode(bytes.NewReader(raw))
	}
}

func BenchmarkCodec_EncodePublishQos1(b *testing.B) {
	codec := protocol.NewCodec(0)
	var buf bytes.Buffer

	payload := make([]byte, 256)
	pkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    1,
		Topic:       "bench/topic",
		Payload:     payload,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		buf.Reset()
		codec.Encode(&buf, pkt)
	}
}

func BenchmarkCodec_RoundTripPublish(b *testing.B) {
	codec := protocol.NewCodec(0)
	var buf bytes.Buffer

	payload := make([]byte, 256)
	pkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
		Topic:       "bench/topic",
		Payload:     payload,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		buf.Reset()
		codec.Encode(&buf, pkt)
		codec.Decode(bytes.NewReader(buf.Bytes()))
	}
}

func BenchmarkCodec_EncodeLargePayload(b *testing.B) {
	codec := protocol.NewCodec(0)
	var buf bytes.Buffer

	payload := make([]byte, 16*1024)
	pkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
		Topic:       "bench/large",
		Payload:     payload,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		buf.Reset()
		codec.Encode(&buf, pkt)
	}
}

// ---------------------------------------------------------------------------
// QoSEngine benchmarks
// ---------------------------------------------------------------------------

func BenchmarkQoSEngine_TrackQoS1(b *testing.B) {
	qe := broker.NewQoSEngine(
		broker.WithMaxInflight(10000),
		broker.WithRetryInterval(time.Hour),
		broker.WithMaxRetries(3),
	)
	qe.SetCallbacks(
		func(string, uint16) error { return nil },
		func(string, uint16) error { return nil },
		func(string, uint16) error { return nil },
		func(string, uint16, string, []byte, uint8, bool) error { return nil },
	)
	qe.Start()
	defer qe.Stop()

	payload := make([]byte, 128)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pid := uint16(i%65534 + 1)
		qe.TrackQoS1("client-1", pid, "bench/topic", payload, false)
	}
}

func BenchmarkQoSEngine_TrackAckQoS1(b *testing.B) {
	qe := broker.NewQoSEngine(
		broker.WithMaxInflight(10000),
		broker.WithRetryInterval(time.Hour),
		broker.WithMaxRetries(3),
	)
	qe.SetCallbacks(
		func(string, uint16) error { return nil },
		func(string, uint16) error { return nil },
		func(string, uint16) error { return nil },
		func(string, uint16, string, []byte, uint8, bool) error { return nil },
	)
	qe.Start()
	defer qe.Stop()

	payload := make([]byte, 128)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pid := uint16(i%65534 + 1)
		qe.TrackQoS1("client-1", pid, "bench/topic", payload, false)
		qe.AckQoS1("client-1", pid)
	}
}

func BenchmarkQoSEngine_TrackQoS2(b *testing.B) {
	qe := broker.NewQoSEngine(
		broker.WithMaxInflight(10000),
		broker.WithRetryInterval(time.Hour),
		broker.WithMaxRetries(3),
	)
	qe.SetCallbacks(
		func(string, uint16) error { return nil },
		func(string, uint16) error { return nil },
		func(string, uint16) error { return nil },
		func(string, uint16, string, []byte, uint8, bool) error { return nil },
	)
	qe.Start()
	defer qe.Stop()

	payload := make([]byte, 128)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pid := uint16(i%65534 + 1)
		qe.TrackQoS2("client-1", pid, "bench/topic", payload, false)
	}
}

func BenchmarkQoSEngine_MultiClient(b *testing.B) {
	qe := broker.NewQoSEngine(
		broker.WithMaxInflight(10000),
		broker.WithRetryInterval(time.Hour),
		broker.WithMaxRetries(3),
	)
	qe.SetCallbacks(
		func(string, uint16) error { return nil },
		func(string, uint16) error { return nil },
		func(string, uint16) error { return nil },
		func(string, uint16, string, []byte, uint8, bool) error { return nil },
	)
	qe.Start()
	defer qe.Stop()

	payload := make([]byte, 128)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		clientID := "client-" + string(rune(i%100))
		pid := uint16(i%65534 + 1)
		qe.TrackQoS1(clientID, pid, "bench/topic", payload, false)
	}
}

// ---------------------------------------------------------------------------
// Session Manager benchmarks
// ---------------------------------------------------------------------------

func BenchmarkManager_CreateSession(b *testing.B) {
	store := memory.NewSessionStore()
	mgr := broker.NewManager(store)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		connectPkt := &protocol.ConnectPacket{
			FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
			ProtocolName:    protocol.ProtocolNameMQTT,
			ProtocolVersion: protocol.Version50,
			Flags:           protocol.ConnectFlags{CleanSession: true},
			KeepAlive:       60,
			ClientID:        "bench-session",
		}
		mgr.CreateSession("bench-session", connectPkt, false)
		mgr.RemoveSession("bench-session")
	}
}

func BenchmarkManager_GetSession(b *testing.B) {
	store := memory.NewSessionStore()
	mgr := broker.NewManager(store)

	connectPkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "bench-session",
	}
	mgr.CreateSession("bench-session", connectPkt, false)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		mgr.GetSession("bench-session")
	}
}

func BenchmarkManager_MultiClientLookup(b *testing.B) {
	store := memory.NewSessionStore()
	mgr := broker.NewManager(store)

	for i := 0; i < 1000; i++ {
		id := "client-" + string(rune(i))
		connectPkt := &protocol.ConnectPacket{
			FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
			ProtocolName:    protocol.ProtocolNameMQTT,
			ProtocolVersion: protocol.Version50,
			Flags:           protocol.ConnectFlags{CleanSession: true},
			KeepAlive:       60,
			ClientID:        id,
		}
		mgr.CreateSession(id, connectPkt, false)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		mgr.GetSession("client-" + string(rune(i%1000)))
	}
}

func BenchmarkManager_RestoreSession(b *testing.B) {
	store := memory.NewSessionStore()
	mgr := broker.NewManager(store)
	ctx := context.Background()

	connectPkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: false},
		KeepAlive:       60,
		ClientID:        "persist-client",
	}
	sess := mgr.CreateSession("persist-client", connectPkt, false)
	sess.SetState(broker.StateConnected)
	sess.AddSubscription("topic/a", 1)
	sess.AddSubscription("topic/b", 0)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		mgr.Restore(ctx, "persist-client")
	}
}

// ---------------------------------------------------------------------------
// BufferPool benchmarks
// ---------------------------------------------------------------------------

func BenchmarkBufferPool_GetPut(b *testing.B) {
	pool := bufferpool.New(4096)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		buf := pool.Get()
		pool.Put(buf)
	}
}

func BenchmarkBufferPool_GetPut_Default(b *testing.B) {
	pool := bufferpool.Default()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		buf := pool.Get()
		pool.Put(buf)
	}
}

func BenchmarkBufferPool_Parallel(b *testing.B) {
	pool := bufferpool.New(4096)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf := pool.Get()
			pool.Put(buf)
		}
	})
}

func BenchmarkBufferPool_AllocVsPool(b *testing.B) {
	b.Run("Make", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = make([]byte, 4096)
		}
	})
	b.Run("Pool", func(b *testing.B) {
		pool := bufferpool.New(4096)
		for i := 0; i < b.N; i++ {
			buf := pool.Get()
			pool.Put(buf)
		}
	})
}

// ---------------------------------------------------------------------------
// Store benchmarks
// ---------------------------------------------------------------------------

func BenchmarkMemoryStore_SessionSave(b *testing.B) {
	store := memory.NewSessionStore()
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		store.SaveSession(ctx, "bench-session", nil)
	}
}

func BenchmarkMemoryStore_SessionGet(b *testing.B) {
	store := memory.NewSessionStore()
	ctx := context.Background()
	store.SaveSession(ctx, "bench-session", nil)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		store.GetSession(ctx, "bench-session")
	}
}
