package integration

// Integration tests for outbound (broker->subscriber) QoS retry (NEW-1), and
// for the protocol-version gate on it.
//
// MQTT 3.1.1 §4.4 caps what a peer is REQUIRED to redeliver without forbidding
// anything -- "This is the only circumstance where a Client or Server is
// REQUIRED to redeliver messages [MQTT-4.4.0-1]" -- and its non-normative note
// keeps the historic retransmission legitimate. MQTT 5.0 §4.4 keeps the clause
// and adds the prohibition: "Clients and Servers MUST NOT resend messages at
// any other time [MQTT-4.4.0-1]." The two tests below pin one side each.

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
//
// The subscriber negotiates MQTT 3.1.1 deliberately: in-connection
// retransmission is a 3.1/3.1.1 behavior, and the 5.0 side of §4.4 is pinned by
// TestQoS1OutboundRetrySuppressedForMQTT5.
func TestQoS1OutboundRetryResends(t *testing.T) {
	if count := noAckQoS1DeliveryCount(t, protocol.Version311, "noack-sub-311"); count < 2 {
		t.Fatalf("PUBLISH count = %d, want the 3.1.1 delivery to be retried "+
			"(at-least-once)", count)
	}
}

// TestQoS1OutboundRetrySuppressedForMQTT5 is the counterpart: a 5.0 subscriber
// must see its delivery exactly once. [MQTT-4.4.0-1] makes resending outside
// session resumption a MUST NOT, so the retry loop has no work to do -- the
// delivery is re-sent only if the session is resumed.
func TestQoS1OutboundRetrySuppressedForMQTT5(t *testing.T) {
	if count := noAckQoS1DeliveryCount(t, protocol.Version50, "noack-sub-50"); count != 1 {
		t.Fatalf("PUBLISH count = %d, want exactly the original delivery: "+
			"[MQTT-4.4.0-1] forbids resending at any other time", count)
	}
}

// noAckQoS1DeliveryCount delivers one QoS 1 message to a subscriber that
// negotiates the given version and never acknowledges it, then counts the
// PUBLISH packets the broker puts on the wire inside an observation window
// several retry intervals long.
func noAckQoS1DeliveryCount(t *testing.T, version protocol.Version, clientID string) int {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	cfg.QoSRetryInterval = config.Duration(100 * time.Millisecond)
	cfg.QoSMaxRetries = 3

	brk := api.NewBroker(api.WithConfig(cfg), api.WithAuth(broker.AllowAllAuth{}))
	if err := brk.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(brk.Stop)

	sub := dialTestBroker(t, brk)
	subCodec := protocol.NewCodecForVersion(0, version)
	connectWithVersion(t, sub, subCodec, clientID, version, protocol.ProtocolNameMQTT)
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
	connectClient(t, pub, pubCodec, "retry-pub-"+clientID)
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

	// The subscriber stays silent, so every packet after the first is the
	// retry loop's doing.
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
	return count
}
