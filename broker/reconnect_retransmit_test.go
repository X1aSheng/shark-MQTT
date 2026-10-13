package broker

// Regression tests for the reconnect retransmission path.
//
// MQTT 5.0 §4.4 [MQTT-4.4.0-1] and 3.1.1 §4.4: "When a Client reconnects with
// Clean Start set to 0 and a session is present, both the Client and Server
// MUST resend any unacknowledged PUBLISH packets (where QoS > 0) and PUBREL
// packets using their original Packet Identifiers." A delivery that already
// reached the PUBREC phase is in the second group -- [MQTT-4.3.3-6] forbids
// re-sending its PUBLISH and [MQTT-4.3.3-5] keeps its PUBREL unacknowledged
// until PUBCOMP -- so the handshake stage has to survive the disconnect and
// pick the packet that goes back out.

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store/memory"
)

// TestSessionInflightPubRelStageSurvivesRestart is the narrow half of the fix:
// the stage recorded when a PUBREC is accepted has to come back out of the
// store, or a restored session cannot know which packet it owes.
func TestSessionInflightPubRelStageSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	sessStore := memory.NewSessionStore()
	const clientID = "pubrel-stage"

	first := New(WithAuth(AllowAllAuth{}), WithSessionStore(sessStore))
	sess := first.sessions.CreateSession(clientID, &protocol.ConnectPacket{
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: false},
		KeepAlive:       30,
		ClientID:        clientID,
	}, false)

	// A QoS 2 delivery the client acknowledged with a PUBREC, and a QoS 1
	// delivery that only proves the stage is not applied to every entry.
	sess.AddInflight(&InflightMsg{PacketID: 3, QoS: 2, Topic: "stage/qos2", Payload: []byte("two")})
	sess.AddInflight(&InflightMsg{PacketID: 4, QoS: 1, Topic: "stage/qos1", Payload: []byte("one")})

	if !sess.MarkInflightPubRel(3) {
		t.Fatal("MarkInflightPubRel(3) reported no outbound QoS 2 entry")
	}
	if sess.MarkInflightPubRel(4) {
		t.Error("MarkInflightPubRel must not mark a QoS 1 delivery PUBREL-pending")
	}
	if sess.MarkInflightPubRel(99) {
		t.Error("MarkInflightPubRel must not invent an entry for an unknown packet id")
	}
	if err := sess.Save(ctx, sessStore); err != nil {
		t.Fatalf("save session: %v", err)
	}

	second := New(WithAuth(AllowAllAuth{}), WithSessionStore(sessStore))
	restored, err := second.sessions.Restore(ctx, clientID)
	if err != nil {
		t.Fatalf("restore session: %v", err)
	}

	qos2, ok := restored.GetInflight(3)
	if !ok {
		t.Fatal("the QoS 2 delivery did not survive the restart")
	}
	if !qos2.PendingPubRel {
		t.Error("the PUBREL-pending stage did not survive the restart")
	}
	if qos2.QoS != 2 || qos2.Topic != "stage/qos2" || string(qos2.Payload) != "two" {
		t.Errorf("restored QoS 2 delivery = %+v, want the saved message", qos2)
	}
	qos1, ok := restored.GetInflight(4)
	if !ok {
		t.Fatal("the QoS 1 delivery did not survive the restart")
	}
	if qos1.PendingPubRel {
		t.Error("a QoS 1 delivery came back PUBREL-pending")
	}
}

// TestRestoreSessionInflightUsesTheSavedStage covers the wiring between the
// restored record and the QoS engine: the stage a delivery was saved in has to
// pick which entry point re-registers it, because that is what decides whether
// the retry loop sends a PUBREL or a PUBLISH.
func TestRestoreSessionInflightUsesTheSavedStage(t *testing.T) {
	b := New(
		WithAuth(AllowAllAuth{}),
		WithQoSOptions(WithRetryInterval(time.Hour)),
	)

	const clientID = "stage-wiring"
	restored := &Session{
		ClientID: clientID,
		Inflight: map[uint16]*InflightMsg{
			3: {PacketID: 3, QoS: 2, Topic: "w/pubrel", PendingPubRel: true},
			4: {PacketID: 4, QoS: 2, Topic: "w/publish"},
			5: {PacketID: 5, QoS: 1, Topic: "w/qos1"},
		},
	}

	b.restoreSessionInflight(clientID, restored)

	tests := []struct {
		packetID  uint16
		wantState InflightState
		why       string
	}{
		{3, StateAcked, "a delivery past PUBREC owes a PUBREL, not the PUBLISH it already replaced"},
		{4, StateSent, "a delivery still awaiting its PUBREC owes the PUBLISH"},
		{5, StateSent, "QoS 1 has no PUBREL phase"},
	}
	for _, tc := range tests {
		msg, ok := b.qos.GetInflight(clientID, tc.packetID)
		if !ok {
			t.Errorf("packet %d was not re-registered with the QoS engine", tc.packetID)
			continue
		}
		if msg.State != tc.wantState {
			t.Errorf("packet %d registered as %v, want %v: %s",
				tc.packetID, msg.State, tc.wantState, tc.why)
		}
		if !msg.IsOutbound {
			t.Errorf("packet %d was registered as an inbound delivery", tc.packetID)
		}
	}
}

// TestBroker_QoS2OutboundResendsPubRelOnReconnect drives the whole path over a
// real connection: a subscriber acknowledges a QoS 2 delivery with a PUBREC,
// disappears without its PUBCOMP, and must be met with the PUBREL -- not a
// replayed PUBLISH -- when it resumes.
func TestBroker_QoS2OutboundResendsPubRelOnReconnect(t *testing.T) {
	b := publishRelayBroker(t)
	addr := startTcpBroker(t, b)

	// A persistent subscriber with a QoS 2 subscription.
	sub, subCodec, _ := dialAndConnect(t, addr, "pubrel-sub", false)
	t.Cleanup(func() { _ = sub.Close() })
	if err := subCodec.Encode(sub, &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "d39/pubrel", QoS: 2}},
	}); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}
	if _, err := subCodec.Decode(sub); err != nil { // SUBACK
		t.Fatalf("SUBACK: %v", err)
	}

	publishQoS2AndComplete(t, addr, "d39/pubrel", []byte("exactly-once"))

	// The subscriber takes the QoS 2 delivery, answers with a PUBREC and reads
	// the PUBREL that follows -- then vanishes without its PUBCOMP.
	pkt, err := subCodec.Decode(sub)
	if err != nil {
		t.Fatalf("waiting for the QoS 2 delivery: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("delivery = %T, want a PUBLISH", pkt)
	}
	if pub.FixedHeader.QoS != 2 {
		t.Fatalf("delivered QoS = %d, want 2", pub.FixedHeader.QoS)
	}
	packetID := pub.PacketID
	if err := subCodec.Encode(sub, &protocol.PubRecPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubRec},
		PacketID:    packetID,
	}); err != nil {
		t.Fatalf("PUBREC: %v", err)
	}
	if pkt, err = subCodec.Decode(sub); err != nil {
		t.Fatalf("waiting for the PUBREL: %v", err)
	}
	if _, ok := pkt.(*protocol.PubRelPacket); !ok {
		t.Fatalf("packet after PUBREC = %T, want a PUBREL", pkt)
	}
	_ = sub.Close() // abnormal: the handshake is left one step short

	waitSessionPersisted(t, b, "pubrel-sub")

	// Resume. The first packet after the CONNACK must be the PUBREL the
	// session owes, carrying the original Packet Identifier.
	resumed, resumedCodec, connack := dialAndConnect(t, addr, "pubrel-sub", false)
	t.Cleanup(func() { _ = resumed.Close() })
	if !connack.SessionPresent {
		t.Fatal("the resumed session was not reported as present")
	}
	pkt, err = resumedCodec.Decode(resumed)
	if err != nil {
		t.Fatalf("waiting for the retransmission: %v", err)
	}
	retransmit, ok := pkt.(*protocol.PubRelPacket)
	if !ok {
		t.Fatalf("first packet after resume = %T, want the PUBREL [MQTT-4.3.3-6]: a PUBLISH "+
			"must not be re-sent once its PUBREL has gone out", pkt)
	}
	if retransmit.PacketID != packetID {
		t.Errorf("resent PUBREL PacketID = %d, want the original %d ([MQTT-4.4.0-1])",
			retransmit.PacketID, packetID)
	}

	// Completing the resumed handshake has to drain the delivery: a PUBREL for
	// a stale or invented Packet Identifier would leave it in flight forever.
	if err := resumedCodec.Encode(resumed, &protocol.PubCompPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubComp},
		PacketID:    packetID,
	}); err != nil {
		t.Fatalf("PUBCOMP: %v", err)
	}
	waitInflightDrained(t, b, "pubrel-sub")
}

// TestBroker_QoS2OutboundResendsPublishBeforePubRec is the companion guard: a
// delivery that had not reached PUBREC yet is still an unacknowledged PUBLISH
// and has to be resent as one, with the DUP flag set and the original Packet
// Identifier.
func TestBroker_QoS2OutboundResendsPublishBeforePubRec(t *testing.T) {
	b := publishRelayBroker(t)
	addr := startTcpBroker(t, b)

	sub, subCodec, _ := dialAndConnect(t, addr, "noack-sub", false)
	t.Cleanup(func() { _ = sub.Close() })
	if err := subCodec.Encode(sub, &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "d39/republish", QoS: 2}},
	}); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}
	if _, err := subCodec.Decode(sub); err != nil { // SUBACK
		t.Fatalf("SUBACK: %v", err)
	}

	publishQoS2AndComplete(t, addr, "d39/republish", []byte("not-acked"))

	pkt, err := subCodec.Decode(sub)
	if err != nil {
		t.Fatalf("waiting for the QoS 2 delivery: %v", err)
	}
	pub, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("delivery = %T, want a PUBLISH", pkt)
	}
	packetID := pub.PacketID
	_ = sub.Close() // abnormal: the PUBREC never went out

	waitSessionPersisted(t, b, "noack-sub")

	resumed, resumedCodec, connack := dialAndConnect(t, addr, "noack-sub", false)
	t.Cleanup(func() { _ = resumed.Close() })
	if !connack.SessionPresent {
		t.Fatal("the resumed session was not reported as present")
	}
	pkt, err = resumedCodec.Decode(resumed)
	if err != nil {
		t.Fatalf("waiting for the retransmission: %v", err)
	}
	retransmit, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("first packet after resume = %T, want a PUBLISH", pkt)
	}
	if retransmit.PacketID != packetID {
		t.Errorf("restored PUBLISH PacketID = %d, want the original %d ([MQTT-4.4.0-1])",
			retransmit.PacketID, packetID)
	}
	if !retransmit.FixedHeader.Dup {
		t.Error("the retransmitted PUBLISH must set DUP")
	}
	if retransmit.Topic != "d39/republish" || string(retransmit.Payload) != "not-acked" {
		t.Errorf("retransmitted PUBLISH = %q/%q, want d39/republish/not-acked",
			retransmit.Topic, retransmit.Payload)
	}
}

// publishRelayBroker builds a broker wired for QoS 2 delivery-path tests: a
// session store so a resumed session is really restored, and a retry tick pushed
// far past any test's patience so nothing can arrive from doRetry. Anything a
// test observes therefore comes from the path under test, not from the ticker.
func publishRelayBroker(t *testing.T) *Broker {
	t.Helper()
	b := New(
		WithAuth(AllowAllAuth{}),
		WithSessionStore(memory.NewSessionStore()),
		WithQoSOptions(WithRetryInterval(time.Hour)),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("broker start: %v", err)
	}
	t.Cleanup(b.Stop)
	return b
}

// dialAndConnect opens a TCP connection to addr, completes the MQTT 5.0
// handshake and returns the connection, its codec and the CONNACK.
func dialAndConnect(t *testing.T, addr, clientID string, clean bool) (net.Conn, *protocol.Codec, *protocol.ConnAckPacket) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", clientID, err)
	}
	codec := protocol.NewCodec(0)
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := codec.Encode(conn, v5Connect(clientID, "", "", clean)); err != nil {
		_ = conn.Close()
		t.Fatalf("CONNECT %s: %v", clientID, err)
	}
	pkt, err := codec.Decode(conn)
	if err != nil {
		_ = conn.Close()
		t.Fatalf("CONNACK %s: %v", clientID, err)
	}
	ack, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		_ = conn.Close()
		t.Fatalf("CONNACK %s: got %T", clientID, pkt)
	}
	if ack.ReasonCode != protocol.ConnAckAccepted {
		_ = conn.Close()
		t.Fatalf("CONNACK %s: reason %d", clientID, ack.ReasonCode)
	}
	return conn, codec, ack
}

// publishQoS2AndComplete publishes one QoS 2 message from a throwaway client
// and runs that client's half of the handshake to completion, which is what
// makes the broker route the message onward.
func publishQoS2AndComplete(t *testing.T, addr, topic string, payload []byte) {
	t.Helper()
	conn, codec, _ := dialAndConnect(t, addr, "d39-publisher", true)
	defer conn.Close()

	if err := codec.Encode(conn, &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 2},
		PacketID:    1,
		Topic:       topic,
		Payload:     payload,
	}); err != nil {
		t.Fatalf("PUBLISH %s: %v", topic, err)
	}
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("PUBREC for %s: %v", topic, err)
	}
	if _, ok := pkt.(*protocol.PubRecPacket); !ok {
		t.Fatalf("packet after QoS 2 PUBLISH = %T, want a PUBREC", pkt)
	}
	if err := codec.Encode(conn, &protocol.PubRelPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubRel, QoS: 1},
		PacketID:    1,
	}); err != nil {
		t.Fatalf("PUBREL for %s: %v", topic, err)
	}
	if pkt, err = codec.Decode(conn); err != nil {
		t.Fatalf("PUBCOMP for %s: %v", topic, err)
	}
	if _, ok := pkt.(*protocol.PubCompPacket); !ok {
		t.Fatalf("packet after PUBREL = %T, want a PUBCOMP", pkt)
	}
}

// waitSessionPersisted blocks until the broker has written clientID's session
// to the store, i.e. until the abnormal disconnect has been processed.
func waitSessionPersisted(t *testing.T, b *Broker, clientID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := b.sessionStore.GetSession(context.Background(), clientID); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %q was never persisted after the abnormal disconnect", clientID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitInflightDrained blocks until clientID's session has no unacknowledged
// delivery left.
func waitInflightDrained(t *testing.T, b *Broker, clientID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if sess, ok := b.sessions.GetSession(clientID); ok {
			if sess.Stats().InflightCount == 0 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %q still has unacknowledged deliveries after PUBCOMP", clientID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
