package broker

import (
	"context"
	"fmt"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store"
	"github.com/X1aSheng/shark-mqtt/store/memory"
)

// TestForwardedPublishProperties covers the MQTT 5.0 §3.3.2.3 relay rules: the
// publisher's per-message properties reach the subscriber unaltered (User
// Property order included), the Subscription Identifier and the decremented
// Message Expiry Interval are the Server's own additions, and the Topic Alias
// -- which names a mapping in the publisher's inbound alias space -- is never
// relayed.
func TestForwardedPublishProperties(t *testing.T) {
	pfi := byte(1)
	alias := uint16(7)
	userProps := []protocol.UserProperty{
		{Key: "trace", Value: "1"},
		{Key: "tenant", Value: "acme"},
		{Key: "trace", Value: "2"}, // same key twice: order and duplicates MUST survive
	}
	publisher := &protocol.Properties{
		PayloadFormat:   &pfi,
		ContentType:     "application/json",
		ResponseTopic:   "reply/to/me",
		CorrelationData: []byte{0xDE, 0xAD, 0xBE, 0xEF},
		UserProperties:  userProps,
		TopicAlias:      &alias,
	}

	t.Run("publisher properties relayed unaltered", func(t *testing.T) {
		got := forwardedPublishProperties(publisher, nil, time.Time{})
		if got == nil {
			t.Fatal("expected the publisher's properties to be forwarded")
		}
		if got.PayloadFormat == nil || *got.PayloadFormat != pfi {
			t.Errorf("PayloadFormat = %v, want %d", got.PayloadFormat, pfi)
		}
		if got.ContentType != "application/json" {
			t.Errorf("ContentType = %q, want %q", got.ContentType, "application/json")
		}
		if got.ResponseTopic != "reply/to/me" {
			t.Errorf("ResponseTopic = %q, want %q", got.ResponseTopic, "reply/to/me")
		}
		if string(got.CorrelationData) != string([]byte{0xDE, 0xAD, 0xBE, 0xEF}) {
			t.Errorf("CorrelationData = %x, want deadbeef", got.CorrelationData)
		}
		if len(got.UserProperties) != len(userProps) {
			t.Fatalf("UserProperties count = %d, want %d", len(got.UserProperties), len(userProps))
		}
		for i, want := range userProps {
			if got.UserProperties[i] != want {
				t.Errorf("UserProperties[%d] = %+v, want %+v (order must be preserved)",
					i, got.UserProperties[i], want)
			}
		}
		if got.TopicAlias != nil {
			t.Error("Topic Alias names the publisher's inbound alias mapping and must not be relayed")
		}
		if len(got.SubscriptionIdentifier) != 0 {
			t.Error("SubscriptionIdentifier must only be set from the matching subscription")
		}
		if got.MessageExpiryInterval != nil {
			t.Error("MessageExpiryInterval must only be set when the publisher sent one")
		}
	})

	t.Run("nothing to relay and nothing to add stays nil", func(t *testing.T) {
		if got := forwardedPublishProperties(nil, nil, time.Time{}); got != nil {
			t.Errorf("nil publisher properties: got %+v, want nil so the encoded packet is unchanged", got)
		}
		if got := forwardedPublishProperties(&protocol.Properties{}, nil, time.Time{}); got != nil {
			t.Errorf("empty publisher properties: got %+v, want nil", got)
		}
	})

	t.Run("server additions combine with relayed properties", func(t *testing.T) {
		subID := uint32(42)
		expiresAt := time.Now().Add(30 * time.Second)
		got := forwardedPublishProperties(publisher, []uint32{subID}, expiresAt)
		if got == nil {
			t.Fatal("expected a property set")
		}
		if !slices.Equal(got.SubscriptionIdentifier, []uint32{subID}) {
			t.Errorf("SubscriptionIdentifier = %v, want [%d]", got.SubscriptionIdentifier, subID)
		}
		if got.MessageExpiryInterval == nil {
			t.Fatal("expected the remaining Message Expiry Interval to be advertised")
		}
		if v := *got.MessageExpiryInterval; v < 25 || v > 30 {
			t.Errorf("MessageExpiryInterval = %d, want the remaining ~30s", v)
		}
		if got.ResponseTopic != "reply/to/me" || got.ContentType != "application/json" {
			t.Error("adding the server's own properties must not drop the relayed ones")
		}
	})

	t.Run("a live sub-second remainder advertises 1s, never 0", func(t *testing.T) {
		// 0 means "already expired" on the wire, so a message that only
		// survives because the clock has not passed the deadline yet must not
		// announce 0 -- the receiver would be told to discard a message we are
		// still delivering.
		got := forwardedPublishProperties(nil, nil, time.Now().Add(200*time.Millisecond))
		if got == nil || got.MessageExpiryInterval == nil {
			t.Fatal("expected a Message Expiry Interval")
		}
		if *got.MessageExpiryInterval != 1 {
			t.Errorf("MessageExpiryInterval = %d, want 1", *got.MessageExpiryInterval)
		}
	})

	t.Run("an elapsed deadline advertises 0", func(t *testing.T) {
		// [MQTT-3.3.2-6] owes the receiver the received value minus the time
		// waited; once the wait outlasts the value the honest answer is 0
		// ("stale on arrival"), which is what tells the receiver not to hand
		// the payload to its application. The 1s floor above is for a *live*
		// remainder only -- there is nothing left to keep alive here.
		got := forwardedPublishProperties(nil, nil, time.Now().Add(-time.Hour))
		if got == nil || got.MessageExpiryInterval == nil {
			t.Fatal("expected a Message Expiry Interval")
		}
		if *got.MessageExpiryInterval != 0 {
			t.Errorf("MessageExpiryInterval = %d, want 0 for an elapsed deadline",
				*got.MessageExpiryInterval)
		}
	})
}

// TestRepublishKeepsForwardedProperties guards the retry path: a QoS 1/2
// retransmission is the same Application Message, so it must still carry the
// properties the relay rules require. Before this, republish rebuilt a bare
// PUBLISH and a subscriber whose first copy was lost would never see the
// Response Topic or the publisher's User Properties.
func TestRepublishKeepsForwardedProperties(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	const clientID = "retry-props"
	sess := b.sessions.CreateSession(clientID, &protocol.ConnectPacket{
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        clientID,
	}, false)
	if !sess.Spec().Properties {
		t.Fatalf("test setup: session negotiated version %d, want MQTT 5.0", sess.ProtocolVer)
	}

	subID := uint32(11)
	sess.AddInflight(&InflightMsg{
		PacketID:  7,
		QoS:       1,
		Topic:     "props/retry",
		Payload:   []byte("payload"),
		ExpiresAt: time.Now().Add(30 * time.Second),
		Properties: &protocol.Properties{
			ContentType:            "application/json",
			ResponseTopic:          "reply/here",
			CorrelationData:        []byte{0x01, 0x02},
			UserProperties:         []protocol.UserProperty{{Key: "trace", Value: "abc"}},
			SubscriptionIdentifier: []uint32{subID},
		},
	})

	// A buffered outbound queue lets the test read the packet object itself
	// instead of decoding wire bytes, so the assertion is on what the broker
	// decided to send.
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	out := make(chan protocol.Packet, 4)
	b.mu.Lock()
	b.connections[clientID] = &clientState{
		conn:       serverConn,
		codec:      protocol.NewCodec(0),
		out:        out,
		stopWrites: make(chan struct{}),
	}
	b.mu.Unlock()

	if err := b.republish(clientID, 7, "props/retry", []byte("payload"), 1, false); err != nil {
		t.Fatalf("republish: %v", err)
	}

	select {
	case pkt := <-out:
		pub, ok := pkt.(*protocol.PublishPacket)
		if !ok {
			t.Fatalf("expected a PUBLISH, got %T", pkt)
		}
		if !pub.Dup {
			t.Error("a retransmission must set DUP")
		}
		props := pub.Properties
		if props == nil {
			t.Fatal("retransmission dropped the forwarded properties")
		}
		if props.ContentType != "application/json" {
			t.Errorf("ContentType = %q, want application/json", props.ContentType)
		}
		if props.ResponseTopic != "reply/here" {
			t.Errorf("ResponseTopic = %q, want reply/here", props.ResponseTopic)
		}
		if string(props.CorrelationData) != string([]byte{0x01, 0x02}) {
			t.Errorf("CorrelationData = %x, want 0102", props.CorrelationData)
		}
		if len(props.UserProperties) != 1 || props.UserProperties[0].Key != "trace" {
			t.Errorf("UserProperties = %+v, want the publisher's single trace pair", props.UserProperties)
		}
		if !slices.Equal(props.SubscriptionIdentifier, []uint32{subID}) {
			t.Errorf("SubscriptionIdentifier = %v, want [%d]", props.SubscriptionIdentifier, subID)
		}
		if props.MessageExpiryInterval == nil || *props.MessageExpiryInterval < 25 {
			t.Errorf("MessageExpiryInterval = %v, want the recomputed remaining ~30s",
				props.MessageExpiryInterval)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("republish wrote no packet")
	}
}

// TestSessionRestoreKeepsForwardedProperties covers the restart path: a session
// saved with an unacknowledged delivery must come back with the relay data and
// the Message Expiry deadline intact, so the retransmission after a broker
// restart is the same PUBLISH (§3.3.2.3) and §3.3.2.3.3 is still enforceable for
// it. Before the store carried either, a restart silently downgraded the
// retransmission to a bare packet, and an expired message could be retried.
func TestSessionRestoreKeepsForwardedProperties(t *testing.T) {
	ctx := context.Background()
	sessStore := memory.NewSessionStore()

	const clientID = "restart-props"
	pfi := byte(1)
	subID := uint32(11)
	deadline := time.Now().Add(30 * time.Second)

	// First broker: a QoS 1 delivery that was sent but never acknowledged.
	first := New(WithAuth(AllowAllAuth{}), WithSessionStore(sessStore))
	sess := first.sessions.CreateSession(clientID, &protocol.ConnectPacket{
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: false},
		KeepAlive:       30,
		ClientID:        clientID,
	}, false)
	if !sess.Spec().Properties {
		t.Fatalf("test setup: session negotiated version %d, want MQTT 5.0", sess.ProtocolVer)
	}
	sess.AddInflight(&InflightMsg{
		PacketID:  7,
		QoS:       1,
		Topic:     "props/restart",
		Payload:   []byte("body"),
		ExpiresAt: deadline,
		Properties: &protocol.Properties{
			PayloadFormat:          &pfi,
			ContentType:            "application/json",
			ResponseTopic:          "reply/after-restart",
			CorrelationData:        []byte{0x01, 0x02},
			UserProperties:         []protocol.UserProperty{{Key: "trace", Value: "restart"}},
			SubscriptionIdentifier: []uint32{subID},
		},
	})
	if err := sess.Save(ctx, sessStore); err != nil {
		t.Fatalf("save session: %v", err)
	}

	// Second broker, same store: this is what a restart looks like here.
	second := New(WithAuth(AllowAllAuth{}), WithSessionStore(sessStore))
	restored, err := second.sessions.Restore(ctx, clientID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	msg, ok := restored.GetInflight(7)
	if !ok {
		t.Fatal("restored session lost the unacknowledged delivery")
	}
	if !msg.ExpiresAt.Equal(deadline) {
		t.Errorf("restored ExpiresAt = %v, want %v", msg.ExpiresAt, deadline)
	}
	if msg.Properties == nil {
		t.Fatal("restored delivery lost its properties")
	}
	if msg.Properties.ContentType != "application/json" {
		t.Errorf("ContentType = %q, want application/json", msg.Properties.ContentType)
	}
	if msg.Properties.ResponseTopic != "reply/after-restart" {
		t.Errorf("ResponseTopic = %q, want reply/after-restart", msg.Properties.ResponseTopic)
	}
	if string(msg.Properties.CorrelationData) != string([]byte{0x01, 0x02}) {
		t.Errorf("CorrelationData = %x, want 0102", msg.Properties.CorrelationData)
	}
	if len(msg.Properties.UserProperties) != 1 || msg.Properties.UserProperties[0].Value != "restart" {
		t.Errorf("UserProperties = %+v, want the single trace pair", msg.Properties.UserProperties)
	}
	if !slices.Equal(msg.Properties.SubscriptionIdentifier, []uint32{subID}) {
		t.Errorf("SubscriptionIdentifier = %v, want [%d]", msg.Properties.SubscriptionIdentifier, subID)
	}
	// The interval itself is not persisted: the absolute deadline is, and
	// republish derives the remaining time from it so the retry advertises the
	// time actually left rather than the value from the first attempt.
	if msg.Properties.MessageExpiryInterval != nil {
		t.Errorf("restored MessageExpiryInterval = %d, want it re-derived at retry time",
			*msg.Properties.MessageExpiryInterval)
	}

	// The retransmission must actually put the restored data back on the wire.
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	out := make(chan protocol.Packet, 4)
	second.mu.Lock()
	second.connections[clientID] = &clientState{
		conn:       serverConn,
		codec:      protocol.NewCodecForVersion(0, protocol.Version50),
		out:        out,
		stopWrites: make(chan struct{}),
	}
	second.mu.Unlock()

	if err := second.republish(clientID, 7, "props/restart", []byte("body"), 1, false); err != nil {
		t.Fatalf("republish: %v", err)
	}
	select {
	case pkt := <-out:
		pub, ok := pkt.(*protocol.PublishPacket)
		if !ok {
			t.Fatalf("expected a PUBLISH, got %T", pkt)
		}
		if pub.Properties == nil {
			t.Fatal("retransmission after restore dropped the properties")
		}
		if pub.Properties.ResponseTopic != "reply/after-restart" ||
			pub.Properties.ContentType != "application/json" {
			t.Errorf("retransmission properties = %+v, want the restored relay data", pub.Properties)
		}
		if pub.Properties.MessageExpiryInterval == nil {
			t.Error("retransmission should advertise the remaining Message Expiry Interval")
		} else if v := *pub.Properties.MessageExpiryInterval; v < 25 || v > 30 {
			t.Errorf("MessageExpiryInterval = %d, want the remaining ~30s", v)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("republish wrote no packet")
	}
}

// TestRepublishKeepsExpiredDeliveryThatWasAlreadySent pins the rule that makes
// Message Expiry a *pre-delivery* filter and not a retry filter.
//
// [MQTT-3.3.2-5] only obliges the Server to delete the message when it "has not
// managed to start onward delivery to a matching subscriber"; [MQTT-4.3.3-7]
// then forbids applying Application Message expiry once a PUBLISH has been
// sent, and [MQTT-4.3.3-5] keeps the exchange unfinished until the peer answers.
// A delivery sitting in the session's Inflight has therefore already been sent,
// so the retry loop must keep re-sending it and only re-derive the interval it
// advertises ([MQTT-3.3.2-6]).
//
// The drop this replaces also broke the bookkeeping: it removed the session
// record while the QoS engine kept tracking the packet id, so the next tick
// missed the outbound branch and fell through to the inbound fallback, putting
// a PUBREC for an outbound QoS 1 delivery on the wire -- repeatedly, until
// maxRetries.
func TestRepublishKeepsExpiredDeliveryThatWasAlreadySent(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))
	const clientID = "expired-resend"

	sess := b.sessions.CreateSession(clientID, &protocol.ConnectPacket{
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: false},
		KeepAlive:       30,
		ClientID:        clientID,
	}, false)
	sess.AddInflight(&InflightMsg{
		PacketID:  7,
		QoS:       1,
		Topic:     "props/expired",
		Payload:   []byte("stale"),
		ExpiresAt: time.Now().Add(-time.Minute), // sent, then left unanswered past its deadline
		Properties: &protocol.Properties{
			ResponseTopic: "reply/too-late",
		},
	})

	out := attachCapturingConnection(t, b, clientID)

	// Two ticks on purpose: one proves the retransmission survives the
	// deadline, the second is what used to surface the stray PUBREC.
	for i := 1; i <= 2; i++ {
		if err := b.republish(clientID, 7, "props/expired", []byte("stale"), 1, false); err != nil {
			t.Fatalf("republish #%d: %v", i, err)
		}
	}
	if got := drainPackets(out); len(got) != 2 {
		t.Fatalf("wrote %d packets %v, want 2 PUBLISH retransmissions", len(got), packetTypes(got))
	} else {
		for _, pkt := range got {
			if _, ok := pkt.(*protocol.PublishPacket); !ok {
				t.Errorf("wrote %T, want a PUBLISH retransmission for an already-sent delivery", pkt)
			}
		}
	}
	if _, still := sess.GetInflight(7); !still {
		t.Fatal("the delivery was dropped from the session instead of being retransmitted")
	}
}

// TestRepublishRetriesExpiredDeliveryWithoutStrayPubRec drives the same rule
// through the QoS engine, which is where the packet id kept being retried after
// the session record had gone: every tick must produce a PUBLISH and never a
// PUBREC, because no inbound publish ever carried that packet id.
func TestRepublishRetriesExpiredDeliveryWithoutStrayPubRec(t *testing.T) {
	// Retry interval 0 makes every tracked packet eligible, so the two doRetry
	// calls below are the whole story.
	b := New(WithAuth(AllowAllAuth{}), WithQoSOptions(WithRetryInterval(0)))
	const clientID = "expired-engine"

	sess := b.sessions.CreateSession(clientID, &protocol.ConnectPacket{
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: false},
		KeepAlive:       30,
		ClientID:        clientID,
	}, false)
	sess.AddInflight(&InflightMsg{
		PacketID:  9,
		QoS:       2,
		Topic:     "props/expired-engine",
		Payload:   []byte("stale"),
		ExpiresAt: time.Now().Add(-time.Minute),
	})
	if err := b.qos.TrackOutboundQoS2(clientID, 9, "props/expired-engine", []byte("stale"), false); err != nil {
		t.Fatalf("track outbound QoS 2: %v", err)
	}

	out := attachCapturingConnection(t, b, clientID)

	b.qos.doRetry()
	b.qos.doRetry()

	got := drainPackets(out)
	if len(got) != 2 {
		t.Fatalf("wrote %d packets over two ticks %v, want 2 PUBLISH", len(got), packetTypes(got))
	}
	for i, pkt := range got {
		pub, ok := pkt.(*protocol.PublishPacket)
		if !ok {
			t.Fatalf("tick %d wrote %T, want a PUBLISH retransmission: a PUBREC for a "+
				"packet id that no inbound publish ever used is a stray packet", i+1, pkt)
		}
		if !pub.FixedHeader.Dup {
			t.Errorf("tick %d: retransmitted PUBLISH must set DUP", i+1)
		}
		if pub.PacketID != 9 {
			t.Errorf("tick %d: retransmitted PacketID = %d, want the original 9", i+1, pub.PacketID)
		}
	}
}

// TestDoDeliverStillDropsExpiryBeforeTheFirstAttempt is the counterpart guard:
// taking Message Expiry out of the retry path must not turn it into a no-op.
// [MQTT-3.3.2-5] obliges the Server to delete the copy of a message whose
// deadline passed before it "managed to start onward delivery to a matching
// subscriber" -- that is the first-attempt check in doDeliver, and it is exactly
// what republish must not do for a delivery it already sent.
func TestDoDeliverStillDropsExpiryBeforeTheFirstAttempt(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))
	const clientID = "expiry-first-attempt"

	sess := b.sessions.CreateSession(clientID, &protocol.ConnectPacket{
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: false},
		KeepAlive:       30,
		ClientID:        clientID,
	}, false)

	out := attachCapturingConnection(t, b, clientID)

	b.doDeliver(clientID, &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		Topic:       "props/never-sent",
		Payload:     []byte("too late"),
	}, 1, time.Now().Add(-time.Minute), SubscriptionOptions{}, nil, false)

	if got := drainPackets(out); len(got) != 0 {
		t.Errorf("wrote %v, want nothing: onward delivery never started, so the copy "+
			"must be deleted rather than sent", packetTypes(got))
	}
	if n := sess.Stats().InflightCount; n != 0 {
		t.Errorf("inflight count = %d, want 0: a delivery that was never sent must not "+
			"be tracked as outstanding", n)
	}
}

// attachCapturingConnection registers a connection whose queued output the test
// reads, so a republish path can be driven without a network round trip.
func attachCapturingConnection(t *testing.T, b *Broker, clientID string) chan protocol.Packet {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		_ = serverConn.Close()
		_ = clientConn.Close()
	})
	out := make(chan protocol.Packet, 8)
	b.mu.Lock()
	b.connections[clientID] = &clientState{
		conn:       serverConn,
		codec:      protocol.NewCodecForVersion(0, protocol.Version50),
		out:        out,
		stopWrites: make(chan struct{}),
	}
	b.mu.Unlock()
	return out
}

// drainPackets returns everything already queued without waiting: the calls
// under test are synchronous, so a missing packet is a failure and not a race.
func drainPackets(out chan protocol.Packet) []protocol.Packet {
	var got []protocol.Packet
	for {
		select {
		case pkt := <-out:
			got = append(got, pkt)
		default:
			return got
		}
	}
}

// packetTypes names the packets for a failure message; printing the values
// themselves would only show pointers.
func packetTypes(pkts []protocol.Packet) []string {
	types := make([]string, 0, len(pkts))
	for _, pkt := range pkts {
		types = append(types, fmt.Sprintf("%T", pkt))
	}
	return types
}

var _ = store.ErrSessionNotFound
