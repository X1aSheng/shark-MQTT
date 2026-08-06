package integration

// Integration tests for outbound (broker->subscriber) QoS retry (NEW-1).

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestQoS1OutboundRetryResends verifies the broker re-sends an unacknowledged
// QoS 1 delivery (at-least-once) instead of silently dropping it. Previously
// outbound QoS 1/2 messages were never tracked, so a subscriber that did not
// acknowledge would lose the message.
func TestQoS1OutboundRetryResends(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	cfg.QoSRetryInterval = 100 * time.Millisecond
	cfg.QoSMaxRetries = 3

	brk := api.NewBroker(api.WithConfig(cfg), api.WithAuth(broker.AllowAllAuth{}))
	if err := brk.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	defer brk.Stop()

	// Subscriber that never acknowledges the delivery.
	sub := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectClient(t, sub, subCodec, "noack-sub")
	sub.SetDeadline(time.Now().Add(2 * time.Second))
	if err := subCodec.Encode(sub, &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "retry/topic", QoS: 1}},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if _, err := subCodec.Decode(sub); err != nil { // SUBACK
		t.Fatalf("suback read: %v", err)
	}

	pub := dialTestBroker(t, brk)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pub, pubCodec, "retry-pub")
	pub.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pub, &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    1,
		Topic:       "retry/topic",
		Payload:     []byte("payload"),
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := pubCodec.Decode(pub); err != nil { // PUBACK to publisher
		t.Fatalf("puback read: %v", err)
	}

	// The subscriber never acks, so the retry loop must re-send the PUBLISH.
	sub.SetReadDeadline(time.Now().Add(600 * time.Millisecond))
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
	if count < 2 {
		t.Fatalf("expected the QoS 1 delivery to be retried (at-least-once), got %d PUBLISH", count)
	}
}
