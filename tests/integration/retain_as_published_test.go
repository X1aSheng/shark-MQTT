package integration

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// connectWithVersion sends a CONNECT for an explicit protocol version and
// verifies the CONNACK. connectClient/connectTestClient both hard-code MQTT
// 5.0, so the Retain As Published tests need their own entry point to also
// cover MQTT 3.1.1 (which has no subscription options).
func connectWithVersion(t *testing.T, conn net.Conn, codec *protocol.Codec, clientID string, version byte, protoName string) {
	t.Helper()
	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protoName,
		ProtocolVersion: version,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        clientID,
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	ack, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	ca, ok := ack.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", ack)
	}
	if ca.ReasonCode != protocol.ReasonCodeSuccess {
		t.Fatalf("expected CONNACK accepted, got %d", ca.ReasonCode)
	}
}

// subscribeWithFilterOptions sends one SUBSCRIBE carrying the full MQTT 5.0
// subscription options (plus optional packet-level properties, where the
// SubscriptionIdentifier belongs per §3.8.2.1.2) and waits for the SUBACK.
func subscribeWithFilterOptions(t *testing.T, conn net.Conn, codec *protocol.Codec, filter protocol.TopicFilter, props *protocol.Properties, packetID uint16) {
	t.Helper()
	pkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    packetID,
		Topics:      []protocol.TopicFilter{filter},
		Properties:  props,
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	ack, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("SUBACK: %v", err)
	}
	if _, ok := ack.(*protocol.SubAckPacket); !ok {
		t.Fatalf("expected SUBACK, got %T", ack)
	}
}

// publishQoS1 publishes at QoS 1 with the requested RETAIN flag and waits for
// the PUBACK. Waiting for the acknowledgement makes the retained-store update
// deterministic, unlike the fire-and-forget publishRetained helper.
func publishQoS1(t *testing.T, brk *api.Broker, clientID, topic, payload string, retain bool) {
	t.Helper()
	conn := dialTestBroker(t, brk)
	codec := protocol.NewCodec(0)
	connectTestClient(t, conn, codec, clientID)

	pkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1, Retain: retain},
		PacketID:    1,
		Topic:       topic,
		Payload:     []byte(payload),
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		t.Fatalf("PUBLISH: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := codec.Decode(conn); err != nil { // PUBACK
		t.Fatalf("PUBACK: %v", err)
	}
	conn.Close()
}

// receivePublish decodes until the next PUBLISH arrives.
func receivePublish(t *testing.T, conn net.Conn, codec *protocol.Codec) *protocol.PublishPacket {
	t.Helper()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("expected PUBLISH: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	return pub
}

// TestRetainAsPublished_LiveForwardPreservesRetain covers MQTT 5.0 §3.8.3.1:
// with RAP=1 a live forward keeps the RETAIN flag the publisher used. The
// subscription is QoS 0 on purpose -- a QoS 0 match previously left the
// subscription options at their zero value, so RAP never took effect there.
func TestRetainAsPublished_LiveForwardPreservesRetain(t *testing.T) {
	brk := testBrokerWithRetain(t)

	sub := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectWithVersion(t, sub, subCodec, "rap-live-sub", protocol.Version50, protocol.ProtocolNameMQTT)
	subscribeWithFilterOptions(t, sub, subCodec,
		protocol.TopicFilter{Topic: "rap/live", QoS: 0, RetainAsPublished: true}, nil, 1)

	publishQoS1(t, brk, "rap-live-pub", "rap/live", "hello", true)

	got := receivePublish(t, sub, subCodec)
	if !got.FixedHeader.Retain {
		t.Error("RAP=1 subscription must keep RETAIN=1 on a live forward")
	}
	sub.Close()
}

// TestRetainAsPublished_LiveForwardClearsRetainByDefault covers MQTT-3.3.1-9:
// without RAP the server must clear the flag on a live forward, even though the
// publisher sent RETAIN=1.
func TestRetainAsPublished_LiveForwardClearsRetainByDefault(t *testing.T) {
	brk := testBrokerWithRetain(t)

	sub := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectWithVersion(t, sub, subCodec, "rap-default-sub", protocol.Version50, protocol.ProtocolNameMQTT)
	subscribeWithFilterOptions(t, sub, subCodec, protocol.TopicFilter{Topic: "rap/default", QoS: 0}, nil, 1)

	publishQoS1(t, brk, "rap-default-pub", "rap/default", "hello", true)

	got := receivePublish(t, sub, subCodec)
	if got.FixedHeader.Retain {
		t.Error("RAP=0 subscription must see RETAIN=0 on a live forward")
	}
	sub.Close()
}

// TestRetainAsPublished_RetainedReplayAlwaysSetsRetain covers MQTT-3.3.1-8: a
// retained message replayed because a subscription was established always
// carries RETAIN=1, whatever Retain As Published says.
func TestRetainAsPublished_RetainedReplayAlwaysSetsRetain(t *testing.T) {
	brk := testBrokerWithRetain(t)
	publishQoS1(t, brk, "rap-replay-pub", "rap/replay", "stored", true)

	sub := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectWithVersion(t, sub, subCodec, "rap-replay-sub", protocol.Version50, protocol.ProtocolNameMQTT)
	subscribeWithFilterOptions(t, sub, subCodec, protocol.TopicFilter{Topic: "rap/replay", QoS: 0}, nil, 1)

	got := receivePublish(t, sub, subCodec)
	if !got.FixedHeader.Retain {
		t.Error("retained replay must carry RETAIN=1 regardless of Retain As Published")
	}
	if string(got.Payload) != "stored" {
		t.Errorf("payload = %q, want %q", got.Payload, "stored")
	}
	sub.Close()
}

// TestRetainAsPublished_MQTT311LiveForwardClearsRetain: MQTT 3.1.1 has no
// subscription options, so a live forward must always carry RETAIN=0.
func TestRetainAsPublished_MQTT311LiveForwardClearsRetain(t *testing.T) {
	brk := testBrokerWithRetain(t)

	sub := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectWithVersion(t, sub, subCodec, "rap-v311-sub", protocol.Version311, protocol.ProtocolNameMQTT)
	subscribeToTopic(t, sub, subCodec, "rap/v311", 0, 1)

	publishQoS1(t, brk, "rap-v311-pub", "rap/v311", "hello", true)

	got := receivePublish(t, sub, subCodec)
	if got.FixedHeader.Retain {
		t.Error("MQTT 3.1.1 live forward must carry RETAIN=0")
	}
	sub.Close()
}

// TestSubscriptionIdentifier_QoS0Subscription guards the same root cause from
// the other side: the SubscriptionIdentifier of a QoS 0 subscription must reach
// the delivered PUBLISH (MQTT 5.0 §3.3.2.3.7).
func TestSubscriptionIdentifier_QoS0Subscription(t *testing.T) {
	brk := testBrokerWithRetain(t)

	sub := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectWithVersion(t, sub, subCodec, "subid-qos0-sub", protocol.Version50, protocol.ProtocolNameMQTT)

	subID := uint32(42)
	subscribeWithFilterOptions(t, sub, subCodec,
		protocol.TopicFilter{Topic: "subid/qos0", QoS: 0},
		&protocol.Properties{SubscriptionIdentifier: &subID}, 1)

	publishQoS1(t, brk, "subid-qos0-pub", "subid/qos0", "hello", false)

	got := receivePublish(t, sub, subCodec)
	if got.Properties == nil || got.Properties.SubscriptionIdentifier == nil {
		t.Fatal("expected SubscriptionIdentifier in the delivered PUBLISH")
	}
	if *got.Properties.SubscriptionIdentifier != subID {
		t.Errorf("SubscriptionIdentifier = %d, want %d", *got.Properties.SubscriptionIdentifier, subID)
	}
	sub.Close()
}
