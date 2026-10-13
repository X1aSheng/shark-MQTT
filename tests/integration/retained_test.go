package integration

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store/memory"
)

// testBrokerWithRetain creates a broker with a retained message store.
func testBrokerWithRetain(t *testing.T) *api.Broker {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	brk := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
		api.WithRetainedStore(memory.NewRetainedStore()),
	)
	if err := brk.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(func() { brk.Stop() })
	return brk
}

// TestRetainedMessage_NewSubscriber receives the last retained message on a topic.
func TestRetainedMessage_NewSubscriber(t *testing.T) {
	broker := testBrokerWithRetain(t)

	// Publisher sends a retained message
	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "retain-pub")

	retainPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			Retain:     true,
		},
		Topic:   "device/status",
		Payload: []byte("online"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, retainPkt); err != nil {
		t.Fatalf("PUBLISH retained: %v", err)
	}

	// New subscriber connects AFTER retained message was published
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "retain-sub", "device/status", 0)

	// Should immediately receive the retained message
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber did not receive retained message: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if pub.Topic != "device/status" {
		t.Errorf("expected topic device/status, got %s", pub.Topic)
	}
	if string(pub.Payload) != "online" {
		t.Errorf("expected payload 'online', got %s", pub.Payload)
	}
	if !pub.FixedHeader.Retain {
		t.Error("expected Retain flag set on delivered message")
	}

	pubConn.Close()
	subConn.Close()
}

// TestRetainedMessage_Update replaces a retained message with a new one.
func TestRetainedMessage_Update(t *testing.T) {
	broker := testBrokerWithRetain(t)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "retain-update-pub")

	// First retained message
	retain1 := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, Retain: true},
		Topic:       "device/status",
		Payload:     []byte("online"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, retain1); err != nil {
		t.Fatalf("PUBLISH retained 1: %v", err)
	}

	// Update retained message
	retain2 := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, Retain: true},
		Topic:       "device/status",
		Payload:     []byte("offline"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, retain2); err != nil {
		t.Fatalf("PUBLISH retained 2: %v", err)
	}

	// New subscriber should get only the latest retained message
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "retain-update-sub", "device/status", 0)

	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber did not receive retained: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if string(pub.Payload) != "offline" {
		t.Errorf("expected updated payload 'offline', got %s", pub.Payload)
	}

	pubConn.Close()
	subConn.Close()
}

// TestRetainedMessage_Delete clears a retained message by publishing empty payload.
func TestRetainedMessage_Delete(t *testing.T) {
	broker := testBrokerWithRetain(t)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "retain-del-pub")

	// Set retained (QoS 1 so the PUBACK confirms the broker processed it).
	retain := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1, Retain: true},
		Topic:       "device/status",
		Payload:     []byte("online"),
		PacketID:    1,
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, retain); err != nil {
		t.Fatalf("PUBLISH retained: %v", err)
	}
	if _, err := pubCodec.Decode(pubConn); err != nil { // PUBACK
		t.Fatalf("retain PUBACK: %v", err)
	}

	// Delete retained with empty payload (QoS 1: the PUBACK confirms the
	// deletion was processed before the subscriber subscribes).
	delRetain := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1, Retain: true},
		Topic:       "device/status",
		Payload:     []byte{},
		PacketID:    2,
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, delRetain); err != nil {
		t.Fatalf("PUBLISH delete retained: %v", err)
	}
	if _, err := pubCodec.Decode(pubConn); err != nil { // PUBACK
		t.Fatalf("delete PUBACK: %v", err)
	}

	// New subscriber should NOT receive any retained message
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "retain-del-sub", "device/status", 0)

	subConn.SetDeadline(time.Now().Add(200 * time.Millisecond))
	_, err := subCodec.Decode(subConn)
	if err == nil {
		t.Error("expected no retained message after deletion, but got one")
	}

	pubConn.Close()
	subConn.Close()
}

// TestRetainedMessage_WildcardDelivery verifies retained messages are delivered
// when subscribing with a wildcard that matches the retained topic.
func TestRetainedMessage_WildcardDelivery(t *testing.T) {
	broker := testBrokerWithRetain(t)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "retain-wc-pub")

	// Publish retained messages on different topics
	topics := []struct {
		topic, payload string
	}{
		{"sensors/room1/temperature", "22.0"},
		{"sensors/room2/temperature", "24.0"},
		{"sensors/room1/humidity", "55%"},
	}
	for _, tc := range topics {
		pkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, Retain: true},
			Topic:       tc.topic,
			Payload:     []byte(tc.payload),
		}
		pubConn.SetDeadline(time.Now().Add(2 * time.Second))
		if err := pubCodec.Encode(pubConn, pkt); err != nil {
			t.Fatalf("PUBLISH retained %s: %v", tc.topic, err)
		}
	}

	// Subscribe with wildcard sensors/+/temperature
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "retain-wc-sub", "sensors/+/temperature", 0)

	// Should receive 2 retained messages (room1 and room2 temperature)
	received := collectPublishes(t, subConn, subCodec, 2, 2*time.Second)
	if len(received) != 2 {
		t.Fatalf("expected 2 retained messages, got %d", len(received))
	}

	topicsSeen := map[string]bool{}
	for _, p := range received {
		topicsSeen[p.Topic] = true
	}
	if !topicsSeen["sensors/room1/temperature"] {
		t.Error("missing retained sensors/room1/temperature")
	}
	if !topicsSeen["sensors/room2/temperature"] {
		t.Error("missing retained sensors/room2/temperature")
	}

	pubConn.Close()
	subConn.Close()
}

// TestRetainedMessage_QoSDowngrade tests that retained message QoS is downgraded
// when subscriber subscribes at a lower QoS.
func TestRetainedMessage_QoSDowngrade(t *testing.T) {
	broker := testBrokerWithRetain(t)

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "retain-qos-pub")

	// Publish retained at QoS 2
	retain := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        2,
			Retain:     true,
		},
		PacketID: 1,
		Topic:    "device/alert",
		Payload:  []byte("critical"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, retain); err != nil {
		t.Fatalf("PUBLISH retained QoS2: %v", err)
	}
	// Read PUBREC + send PUBREL + read PUBCOMP
	drainQoS2(t, pubConn, pubCodec, 1)

	// Subscribe at QoS 0 — should receive retained at QoS 0
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "retain-qos-sub", "device/alert", 0)

	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber did not receive retained: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if pub.FixedHeader.QoS != 0 {
		t.Errorf("expected QoS 0 (downgraded), got %d", pub.FixedHeader.QoS)
	}
	if string(pub.Payload) != "critical" {
		t.Errorf("expected payload 'critical', got %s", pub.Payload)
	}

	pubConn.Close()
	subConn.Close()
}

func TestRetainHandlingDoNotSend(t *testing.T) {
	broker := testBrokerWithRetain(t)
	publishRetained(t, broker, "retain-handling/policy", "stored")

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectClient(t, subConn, subCodec, "retain-handling-none")
	subscribeWithRetainHandling(t, subConn, subCodec, "retain-handling/policy", 2)

	assertNoMessage(t, subConn, "retained message should be suppressed when RetainHandling=2")
	subConn.Close()
}

func TestRetainHandlingSendOnlyOnNewSubscription(t *testing.T) {
	broker := testBrokerWithRetain(t)
	publishRetained(t, broker, "retain-handling/new-only", "stored")

	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectClient(t, subConn, subCodec, "retain-handling-new-only")

	subscribeWithRetainHandling(t, subConn, subCodec, "retain-handling/new-only", 1)
	mustReceivePublish(t, subConn, subCodec, "retain-handling/new-only", "stored")

	subscribeWithRetainHandling(t, subConn, subCodec, "retain-handling/new-only", 1)
	assertNoMessage(t, subConn, "retained message should only be sent for a new subscription when RetainHandling=1")
	subConn.Close()
}

// --- retained test helpers ---

func publishRetained(t *testing.T, broker *api.Broker, topic, payload string) {
	t.Helper()
	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "retain-helper-pub-"+topic)

	pkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, Retain: true},
		Topic:       topic,
		Payload:     []byte(payload),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pkt); err != nil {
		t.Fatalf("PUBLISH retained: %v", err)
	}
	pubConn.Close()
}

func subscribeWithRetainHandling(t *testing.T, conn net.Conn, codec *protocol.Codec, topic string, retainHandling uint8) {
	t.Helper()
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics: []protocol.TopicFilter{
			{Topic: topic, QoS: 0, RetainHandling: retainHandling},
		},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, subPkt); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("SUBACK: %v", err)
	}
	if _, ok := pkt.(*protocol.SubAckPacket); !ok {
		t.Fatalf("expected SUBACK, got %T", pkt)
	}
}

func collectPublishes(t *testing.T, conn net.Conn, codec *protocol.Codec, count int, timeout time.Duration) []*protocol.PublishPacket {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var results []*protocol.PublishPacket
	for len(results) < count {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		conn.SetDeadline(time.Now().Add(remaining))
		pkt, err := codec.Decode(conn)
		if err != nil {
			break
		}
		if pub, ok := pkt.(*protocol.PublishPacket); ok {
			results = append(results, pub)
		}
	}
	return results
}

func drainQoS2(t *testing.T, conn net.Conn, codec *protocol.Codec, packetID uint16) {
	t.Helper()
	// PUBREC
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("PUBREC: %v", err)
	}
	rec, ok := pkt.(*protocol.PubRecPacket)
	if !ok {
		t.Fatalf("expected PUBREC, got %T", pkt)
	}
	if rec.PacketID != packetID {
		t.Fatalf("expected PUBREC packetID %d, got %d", packetID, rec.PacketID)
	}

	// PUBREL
	pubRel := &protocol.PubRelPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubRel, QoS: 1},
		PacketID:    packetID,
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pubRel); err != nil {
		t.Fatalf("PUBREL: %v", err)
	}

	// PUBCOMP
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err = codec.Decode(conn)
	if err != nil {
		t.Fatalf("PUBCOMP: %v", err)
	}
	comp, ok := pkt.(*protocol.PubCompPacket)
	if !ok {
		t.Fatalf("expected PUBCOMP, got %T", pkt)
	}
	if comp.PacketID != packetID {
		t.Fatalf("expected PUBCOMP packetID %d, got %d", packetID, comp.PacketID)
	}
}

// sysRetainAuthorizer is a minimal authorizer used by the $SYS retained
// test: publishing is allowed only to the exact topic under test, which
// mirrors an operator explicitly granting a client a $SYS topic (the
// default allow-all authorizer refuses all $-prefixed publishes — audit).
type sysRetainAuthorizer struct{}

func (sysRetainAuthorizer) CanPublish(ctx context.Context, username, topic string) bool {
	return topic == "$SYS/retained"
}

func (sysRetainAuthorizer) CanSubscribe(ctx context.Context, username, topic string) bool {
	return true
}

// TestRetainedSystemTopicNotDeliveredToWildcard verifies a $SYS retained
// message is not delivered to a bare '#' subscription but is delivered to a
// '$SYS/#' subscription (P3-4, MQTT 4.7.2).
func TestRetainedSystemTopicNotDeliveredToWildcard(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	broker := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
		api.WithAuthorizer(sysRetainAuthorizer{}),
		api.WithRetainedStore(memory.NewRetainedStore()),
	)
	if err := broker.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(func() { broker.Stop() })

	// 1. Publish a retained message to a $SYS topic (explicitly granted).
	pub := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectTestClient(t, pub, pubCodec, "sys-pub")
	pub.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pub, &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1, Retain: true},
		PacketID:    1,
		Topic:       "$SYS/retained",
		Payload:     []byte("sys-data"),
	}); err != nil {
		t.Fatalf("PUBLISH: %v", err)
	}
	if _, err := pubCodec.Decode(pub); err != nil { // PUBACK
		t.Fatalf("puback: %v", err)
	}
	pub.Close()

	// 2. A '#' subscriber must NOT receive the $SYS retained message.
	hashSub := dialTestBroker(t, broker)
	hashCodec := protocol.NewCodec(0)
	connectTestClient(t, hashSub, hashCodec, "hash-sub")
	subscribeToTopic(t, hashSub, hashCodec, "#", 0, 1)
	hashSub.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if pkt, err := hashCodec.Decode(hashSub); err == nil {
		t.Fatalf("'#' subscriber should NOT receive $SYS retained, got %T", pkt)
	}
	hashSub.Close()

	// 3. A '$SYS/#' subscriber MUST receive it.
	sysSub := dialTestBroker(t, broker)
	sysCodec := protocol.NewCodec(0)
	connectTestClient(t, sysSub, sysCodec, "sys-sub")
	subscribeToTopic(t, sysSub, sysCodec, "$SYS/#", 1, 1)
	sysSub.SetReadDeadline(time.Now().Add(2 * time.Second))
	pkt, err := sysCodec.Decode(sysSub)
	if err != nil {
		t.Fatalf("$SYS/# subscriber should receive $SYS retained: %v", err)
	}
	delivered, ok := pkt.(*protocol.PublishPacket)
	if !ok || string(delivered.Payload) != "sys-data" {
		t.Fatalf("expected $SYS retained payload sys-data, got %T %v", pkt, pkt)
	}
	_ = sysCodec.Encode(sysSub, &protocol.PubAckPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubAck},
		PacketID:    delivered.PacketID,
	})
	sysSub.Close()
}

// TestRetainedMessageRelaysPublisherProperties covers MQTT 5.0 §3.3.1.3 read
// together with §3.3.2.3: a retained message is stored and later delivered to a
// *new* subscriber, and that subscriber is still "receiving the Application
// Message". The Server therefore has to store the publisher's relayable
// properties with the retained message and send them unaltered on replay
// ([MQTT-3.3.2-4] / [MQTT-3.3.2-15] / [MQTT-3.3.2-16] / [MQTT-3.3.2-17] /
// [MQTT-3.3.2-20]), with the Message Expiry Interval decremented by the time the
// retained copy has been waiting ([MQTT-3.3.2-6]).
//
// Before the fix the retained record held only topic/QoS/payload, so every
// retained delivery silently dropped all five property kinds and a
// request/response flow bootstrapped through a retained message could never
// work.
func TestRetainedMessageRelaysPublisherProperties(t *testing.T) {
	brk := testBrokerWithRetain(t)

	pub := dialTestBroker(t, brk)
	defer pub.Close()
	pubCodec := protocol.NewCodecForVersion(0, protocol.Version50)
	connectWithVersion(t, pub, pubCodec, "retain-props-pub", protocol.Version50, protocol.ProtocolNameMQTT)

	const topic = "retain/props"
	const payload = `{"state":"online"}`
	pfi := byte(1)
	expiry := uint32(300)
	publish := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1, Retain: true},
		PacketID:    1,
		Topic:       topic,
		Payload:     []byte(payload),
		Properties: &protocol.Properties{
			PayloadFormat:         &pfi,
			MessageExpiryInterval: &expiry,
			ContentType:           "application/json",
			ResponseTopic:         "retain/reply",
			CorrelationData:       []byte{0xCA, 0xFE},
			UserProperties: []protocol.UserProperty{
				{Key: "trace", Value: "a"},
				{Key: "tenant", Value: "acme"},
				{Key: "trace", Value: "b"}, // repeated key: order must survive
			},
		},
	}
	pub.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pub, publish); err != nil {
		t.Fatalf("PUBLISH retained: %v", err)
	}
	pub.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := pubCodec.Decode(pub); err != nil { // PUBACK
		t.Fatalf("PUBACK: %v", err)
	}

	// A subscriber that was not connected when the message was published still
	// has to receive the full property set with the retained message.
	sub := dialTestBroker(t, brk)
	defer sub.Close()
	subCodec := protocol.NewCodecForVersion(0, protocol.Version50)
	connectWithVersion(t, sub, subCodec, "retain-props-sub", protocol.Version50, protocol.ProtocolNameMQTT)
	subscribeWithFilterOptions(t, sub, subCodec,
		protocol.TopicFilter{Topic: topic, QoS: 1}, nil, 1)

	sub.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(sub)
	if err != nil {
		t.Fatalf("subscriber did not receive the retained message: %v", err)
	}
	got, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if !got.FixedHeader.Retain {
		t.Error("a retained delivery must carry RETAIN=1 (§3.3.1.3)")
	}
	if string(got.Payload) != payload {
		t.Errorf("Payload = %q, want %q", got.Payload, payload)
	}
	if got.Properties == nil {
		t.Fatal("retained delivery carried no properties: the retained record dropped them")
	}
	if got.Properties.PayloadFormat == nil || *got.Properties.PayloadFormat != 1 {
		t.Errorf("PayloadFormat = %v, want pointer to 1 [MQTT-3.3.2-4]", got.Properties.PayloadFormat)
	}
	if got.Properties.ContentType != "application/json" {
		t.Errorf("ContentType = %q, want application/json [MQTT-3.3.2-20]", got.Properties.ContentType)
	}
	if got.Properties.ResponseTopic != "retain/reply" {
		t.Errorf("ResponseTopic = %q, want retain/reply [MQTT-3.3.2-15]", got.Properties.ResponseTopic)
	}
	if string(got.Properties.CorrelationData) != string([]byte{0xCA, 0xFE}) {
		t.Errorf("CorrelationData = %v, want [202 254] [MQTT-3.3.2-16]", got.Properties.CorrelationData)
	}
	want := []protocol.UserProperty{
		{Key: "trace", Value: "a"},
		{Key: "tenant", Value: "acme"},
		{Key: "trace", Value: "b"},
	}
	if len(got.Properties.UserProperties) != len(want) {
		t.Fatalf("UserProperties = %v, want %v [MQTT-3.3.2-17]", got.Properties.UserProperties, want)
	}
	for i := range want {
		if got.Properties.UserProperties[i] != want[i] {
			t.Errorf("UserProperties[%d] = %+v, want %+v [MQTT-3.3.2-18]",
				i, got.Properties.UserProperties[i], want[i])
		}
	}
	// [MQTT-3.3.2-6]: the interval must be the received value minus the time the
	// message waited, so it can only have shrunk from the 300s that was sent.
	if got.Properties.MessageExpiryInterval == nil {
		t.Fatal("retained delivery carried no Message Expiry Interval [MQTT-3.3.2-6]")
	}
	if v := *got.Properties.MessageExpiryInterval; v == 0 || v > 300 {
		t.Errorf("MessageExpiryInterval = %d, want 1..300 (decremented by the waiting time)", v)
	}
}

// TestRetainedMessageReplayDecrementsExpiry pins [MQTT-3.3.2-6] for the retained
// path: the interval on the replayed PUBLISH is the received value minus the
// time the retained copy spent waiting, not the value the publisher originally
// sent. Publishing a 2 second lifetime and replaying it ~1.2s later must yield 1,
// never an echoed 2 (and never 0, which would read as "already expired").
func TestRetainedMessageReplayDecrementsExpiry(t *testing.T) {
	brk := testBrokerWithRetain(t)

	pub := dialTestBroker(t, brk)
	defer pub.Close()
	pubCodec := protocol.NewCodecForVersion(0, protocol.Version50)
	connectWithVersion(t, pub, pubCodec, "retain-exp-pub", protocol.Version50, protocol.ProtocolNameMQTT)

	const topic = "retain/expiry"
	expiry := uint32(2)
	pub.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pub, &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1, Retain: true},
		PacketID:    1,
		Topic:       topic,
		Payload:     []byte("value"),
		Properties:  &protocol.Properties{MessageExpiryInterval: &expiry},
	}); err != nil {
		t.Fatalf("PUBLISH retained: %v", err)
	}
	pub.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := pubCodec.Decode(pub); err != nil {
		t.Fatalf("PUBACK: %v", err)
	}

	// Wait between 1s and 2s: the remaining lifetime is therefore under a second,
	// which the encoder floors at 1 rather than rounding down to 0.
	time.Sleep(1200 * time.Millisecond)

	sub := dialTestBroker(t, brk)
	defer sub.Close()
	subCodec := protocol.NewCodecForVersion(0, protocol.Version50)
	connectWithVersion(t, sub, subCodec, "retain-exp-sub", protocol.Version50, protocol.ProtocolNameMQTT)
	subscribeWithFilterOptions(t, sub, subCodec, protocol.TopicFilter{Topic: topic, QoS: 1}, nil, 1)

	sub.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := subCodec.Decode(sub)
	if err != nil {
		t.Fatalf("subscriber did not receive the retained message: %v", err)
	}
	got, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if got.Properties == nil || got.Properties.MessageExpiryInterval == nil {
		t.Fatal("replayed retained message carried no Message Expiry Interval [MQTT-3.3.2-6]")
	}
	if v := *got.Properties.MessageExpiryInterval; v != 1 {
		t.Errorf("MessageExpiryInterval = %d, want 1 (2 sent minus the ~1.2s waited), not an echoed 2", v)
	}
}

// TestRetainedMessageExpiredIsNotDelivered covers the second half of §3.3.1.3:
// "If the current retained message for a Topic expires, it is discarded and
// there will be no retained message for that topic." A subscriber that arrives
// after the publisher's Message Expiry Interval has passed must get its SUBACK
// and then nothing, rather than a stale retained message.
func TestRetainedMessageExpiredIsNotDelivered(t *testing.T) {
	brk := testBrokerWithRetain(t)

	pub := dialTestBroker(t, brk)
	defer pub.Close()
	pubCodec := protocol.NewCodecForVersion(0, protocol.Version50)
	connectWithVersion(t, pub, pubCodec, "retain-gone-pub", protocol.Version50, protocol.ProtocolNameMQTT)

	const topic = "retain/gone"
	expiry := uint32(1)
	pub.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pub, &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1, Retain: true},
		PacketID:    1,
		Topic:       topic,
		Payload:     []byte("stale"),
		Properties:  &protocol.Properties{MessageExpiryInterval: &expiry},
	}); err != nil {
		t.Fatalf("PUBLISH retained: %v", err)
	}
	pub.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := pubCodec.Decode(pub); err != nil {
		t.Fatalf("PUBACK: %v", err)
	}

	time.Sleep(1300 * time.Millisecond) // let the 1s lifetime lapse

	sub := dialTestBroker(t, brk)
	defer sub.Close()
	subCodec := protocol.NewCodecForVersion(0, protocol.Version50)
	connectWithVersion(t, sub, subCodec, "retain-gone-sub", protocol.Version50, protocol.ProtocolNameMQTT)
	subscribeWithFilterOptions(t, sub, subCodec, protocol.TopicFilter{Topic: topic, QoS: 1}, nil, 1)

	// subscribeWithFilterOptions already encodes SUBSCRIBE and asserts the
	// SUBACK, so the subscription is proven valid; no retained PUBLISH may
	// follow it.
	sub.SetDeadline(time.Now().Add(400 * time.Millisecond))
	pkt, err := subCodec.Decode(sub)
	if err == nil {
		t.Fatalf("an expired retained message was delivered: %#v", pkt)
	}
	if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
		t.Fatalf("expected a read timeout after the SUBACK, got %v", err)
	}
}
