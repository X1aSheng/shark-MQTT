package integration

// Regression tests for broker QoS 1 delivery and shutdown timing.

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestQoS1NoDuplicateDelivery verifies a single QoS 1 publish reaches a
// subscriber exactly once. Previously the QoS engine retried incoming QoS 1
// messages by re-routing to subscribers, duplicating delivery up to
// maxRetries extra times.
func TestQoS1NoDuplicateDelivery(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	cfg.QoSRetryInterval = 200 * time.Millisecond
	cfg.QoSMaxRetries = 2

	brk := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := brk.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	defer brk.Stop()

	// Subscriber
	sub := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectClient(t, sub, subCodec, "qos1-reg-sub")
	sub.SetDeadline(time.Now().Add(2 * time.Second))
	if err := subCodec.Encode(sub, &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "qos1/repro", QoS: 1}},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if pkt, err := subCodec.Decode(sub); err != nil {
		t.Fatalf("suback read: %v", err)
	} else if _, ok := pkt.(*protocol.SubAckPacket); !ok {
		t.Fatalf("expected SUBACK got %T", pkt)
	}

	// Publisher sends one QoS 1 message.
	pub := dialTestBroker(t, brk)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pub, pubCodec, "qos1-reg-pub")
	pub.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pub, &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    1,
		Topic:       "qos1/repro",
		Payload:     []byte("payload"),
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if pkt, err := pubCodec.Decode(pub); err != nil {
		t.Fatalf("puback read: %v", err)
	} else if _, ok := pkt.(*protocol.PubAckPacket); !ok {
		t.Fatalf("expected PUBACK got %T", pkt)
	}

	// Wait well past several retry intervals.
	time.Sleep(1 * time.Second)

	sub.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	count := 0
	for {
		pkt, err := subCodec.Decode(sub)
		if err != nil {
			break
		}
		if _, ok := pkt.(*protocol.PublishPacket); ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("subscriber received %d PUBLISH messages, want exactly 1", count)
	}
}

// TestBrokerStopDoesNotWaitForKeepAlive verifies that broker Stop returns
// promptly even with an idle connected client, instead of blocking until the
// 1.5x keep-alive read deadline expires.
func TestBrokerStopDoesNotWaitForKeepAlive(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"

	brk := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := brk.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}

	// Connect an idle client (keep-alive 30 -> broker read deadline 45s).
	conn := dialTestBroker(t, brk)
	codec := protocol.NewCodec(0)
	connectClient(t, conn, codec, "idle-client")

	start := time.Now()
	brk.Stop()
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("broker Stop took %v, want prompt shutdown (idle connection blocked WaitGroup)", elapsed)
	}
}
