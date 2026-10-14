package broker

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store/memory"
)

func TestSessionManagerCreateAndGet(t *testing.T) {
	memStore := memory.NewSessionStore()
	mgr := NewManager(memStore)

	connectPkt := &protocol.ConnectPacket{
		ProtocolName:    "MQTT",
		ProtocolVersion: protocol.Version311,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
		},
		KeepAlive: 60,
		ClientID:  "test-client",
		Username:  "admin",
	}

	sess := mgr.CreateSession("test-client", connectPkt, false)
	if sess == nil {
		t.Fatal("session is nil")
	}
	if sess.ClientID != "test-client" {
		t.Errorf("client ID: got %q, want test-client", sess.ClientID)
	}
	if sess.Username != "admin" {
		t.Errorf("username: got %q, want admin", sess.Username)
	}
	if sess.KeepAlive != 60 {
		t.Errorf("keep alive: got %d, want 60", sess.KeepAlive)
	}

	// Get session
	got, ok := mgr.GetSession("test-client")
	if !ok {
		t.Fatal("session not found")
	}
	if got.ClientID != "test-client" {
		t.Errorf("client ID: got %q, want test-client", got.ClientID)
	}
}

func TestSessionManagerRemoveSession(t *testing.T) {
	memStore := memory.NewSessionStore()
	mgr := NewManager(memStore)

	connectPkt := &protocol.ConnectPacket{
		ProtocolName:    "MQTT",
		ProtocolVersion: protocol.Version311,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
		},
		KeepAlive: 60,
		ClientID:  "temp-client",
	}

	mgr.CreateSession("temp-client", connectPkt, false)
	mgr.RemoveSession("temp-client")

	_, ok := mgr.GetSession("temp-client")
	if ok {
		t.Error("session should have been removed")
	}
}

func TestSessionManagerListSessions(t *testing.T) {
	memStore := memory.NewSessionStore()
	mgr := NewManager(memStore)

	for i := 0; i < 3; i++ {
		mgr.CreateSession("client"+string(rune('0'+i)), &protocol.ConnectPacket{
			ProtocolVersion: protocol.Version311,
			Flags:           protocol.ConnectFlags{CleanSession: true},
			KeepAlive:       60,
			ClientID:        "client" + string(rune('0'+i)),
		}, false)
	}

	sessions := mgr.ListSessions()
	if len(sessions) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(sessions))
	}
}

func TestSessionManagerSessionExists(t *testing.T) {
	memStore := memory.NewSessionStore()
	mgr := NewManager(memStore)

	mgr.CreateSession("exists-client", &protocol.ConnectPacket{
		ProtocolVersion: protocol.Version311,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "exists-client",
	}, false)

	if !mgr.SessionExists("exists-client") {
		t.Error("session should exist")
	}
	if mgr.SessionExists("nonexistent") {
		t.Error("session should not exist")
	}
}

func TestSessionSubscriptions(t *testing.T) {
	memStore := memory.NewSessionStore()
	mgr := NewManager(memStore)

	sess := mgr.CreateSession("sub-client", &protocol.ConnectPacket{
		ProtocolVersion: protocol.Version311,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "sub-client",
	}, false)

	sess.AddSubscription("test/#", 0)
	sess.AddSubscription("sensor/+/temp", 1)

	// Test matches subscription
	matches, qos, _, _ := sess.MatchesSubscription("test/device/data")
	if !matches {
		t.Error("expected match for test/#")
	}
	if qos != 0 {
		t.Errorf("expected QoS 0, got %d", qos)
	}

	matches, qos, _, _ = sess.MatchesSubscription("sensor/room1/temp")
	if !matches {
		t.Error("expected match for sensor/+/temp")
	}
	if qos != 1 {
		t.Errorf("expected QoS 1, got %d", qos)
	}

	// Test no match
	matches, _, _, _ = sess.MatchesSubscription("other/topic")
	if matches {
		t.Error("expected no match for other/topic")
	}

	// Test remove subscription
	sess.RemoveSubscription("test/#")
	matches, _, _, _ = sess.MatchesSubscription("test/device/data")
	if matches {
		t.Error("expected no match after removing subscription")
	}
}

func TestSessionMatchesSubscriptionMaxQoS(t *testing.T) {
	// Overlapping filters must yield a single match at the MAXIMUM QoS
	// (MQTT 3.1.1 §3.3.5 / 5.0 §3.3.5), independent of map iteration order.
	mgr := NewManager(nil)
	sess := mgr.CreateSession("maxqos-client", &protocol.ConnectPacket{
		ProtocolVersion: protocol.Version311,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "maxqos-client",
	}, false)

	sess.AddSubscription("home/#", 0)
	sess.AddSubscription("home/temp", 2)

	matches, qos, _, _ := sess.MatchesSubscription("home/temp")
	if !matches {
		t.Fatal("expected match")
	}
	if qos != 2 {
		t.Errorf("expected max QoS 2, got %d", qos)
	}

	// Shared subscription filters merge too.
	sess.AddSubscription("$share/g/home/#", 1)
	matches, qos, _, _ = sess.MatchesSubscription("home/temp")
	if !matches {
		t.Fatal("expected match with shared filter")
	}
	if qos != 2 {
		t.Errorf("expected max QoS 2 with shared filter present, got %d", qos)
	}

	// Retained variant applies the same rule.
	matches, qos, _, _ = sess.MatchesRetainedSubscription("home/temp")
	if !matches {
		t.Fatal("expected retained match")
	}
	if qos != 2 {
		t.Errorf("expected retained max QoS 2, got %d", qos)
	}
}

// TestMatchesSubscriptionCollectsEveryIdentifier covers the aggregate the
// delivery path needs for MQTT 5.0 [MQTT-3.3.4-4]: the broker sends one copy of
// a message that matched several subscriptions, and that copy has to name every
// subscription it matched.
//
// The dedup case is the one that cannot be reached from an integration test
// without the same identifier on two filters, and it is load-bearing: without
// it N overlapping filters sharing an identifier would put N identical property
// instances on every message, letting a client inflate its own deliveries.
func TestMatchesSubscriptionCollectsEveryIdentifier(t *testing.T) {
	mgr := NewManager(nil)
	sess := mgr.CreateSession("ids-client", &protocol.ConnectPacket{
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "ids-client",
	}, false)

	// Every filter below genuinely matches the three-level topic "ids/a/b":
	// "ids/#" (multi-level wildcard), "ids/+/b" (single-level wildcard) and the
	// literal "ids/a/b". A filter such as "ids/a" would NOT match, because MQTT
	// topic matching requires the level counts to agree.
	//
	// The identifiers are deliberately not in sorted order, so the assertion
	// covers the sort rather than accidentally matching insertion order.
	ids := map[string]uint32{"ids/#": 30, "ids/+/b": 10, "ids/a/b": 20}
	for filter, id := range ids {
		subID := id
		sess.AddSubscriptionFilter(protocol.TopicFilter{
			Topic: filter, QoS: 1, SubscriptionIdentifier: &subID,
		})
	}

	matches, _, _, got := sess.MatchesSubscription("ids/a/b")
	if !matches {
		t.Fatal("expected all three filters to match ids/a/b")
	}
	if !slices.Equal(got, []uint32{10, 20, 30}) {
		t.Errorf("identifiers = %v, want [10 20 30] (sorted, one per matching subscription)", got)
	}

	// A fourth, distinct filter that reuses an identifier already in the set
	// must not add a duplicate element: the values name subscriptions, and a
	// repeated value tells the client nothing while still costing packet bytes.
	dup := uint32(20)
	sess.AddSubscriptionFilter(protocol.TopicFilter{
		Topic: "ids/a/+", QoS: 1, SubscriptionIdentifier: &dup,
	})
	matches, _, _, got = sess.MatchesSubscription("ids/a/b")
	if !matches {
		t.Fatal("expected the four filters to still match")
	}
	if !slices.Equal(got, []uint32{10, 20, 30}) {
		t.Errorf("identifiers = %v, want [10 20 30]: a repeated identifier must appear once", got)
	}

	// A session with no identifiers at all must report an empty set rather than
	// inventing one from the QoS or the filter list.
	plain := mgr.CreateSession("plain-client", &protocol.ConnectPacket{
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "plain-client",
	}, false)
	plain.AddSubscription("plain/#", 0)
	if _, _, _, got = plain.MatchesSubscription("plain/x"); len(got) != 0 {
		t.Errorf("identifiers = %v, want none", got)
	}
}

func TestSessionNextPacketID(t *testing.T) {
	sess := &Session{
		packetIDSeq: 1,
	}

	for i := uint16(1); i <= 100; i++ {
		id := sess.NextPacketID()
		if id != i {
			t.Errorf("expected packet ID %d, got %d", i, id)
		}
	}
}

func TestSessionInflight(t *testing.T) {
	sess := &Session{
		Inflight: make(map[uint16]*InflightMsg),
	}

	msg := &InflightMsg{
		PacketID: 1,
		QoS:      1,
		Topic:    "test/topic",
		Payload:  []byte("data"),
	}

	sess.AddInflight(msg)

	got, ok := sess.GetInflight(1)
	if !ok {
		t.Fatal("inflight message not found")
	}
	if got.Topic != "test/topic" {
		t.Errorf("topic: got %q, want test/topic", got.Topic)
	}

	sess.RemoveInflight(1)
	_, ok = sess.GetInflight(1)
	if ok {
		t.Error("inflight message should be removed")
	}

	// RemoveInflight reports whether an entry was present (P2-16 guard).
	if sess.RemoveInflight(1) {
		t.Error("expected RemoveInflight to report false for a missing entry")
	}
}

// TestSessionOutboundBuffer verifies QoS 1/2 deliveries are buffered when the
// client's receive window is full and popped once a slot frees (P2-14).
func TestSessionOutboundBuffer(t *testing.T) {
	sess := &Session{OutboundWindow: 1}
	sess.IncOutboundUnacked()
	if sess.CanSendOutbound() {
		t.Fatal("expected receive window to be full with 1 unacked")
	}

	pkt := &protocol.PublishPacket{}
	pkt.FixedHeader.PacketType = protocol.PacketTypePublish
	pkt.FixedHeader.QoS = 1
	pkt.Topic = "buf/topic"
	pkt.Payload = []byte("data")

	sess.BufferOutbound(pkt, 1, SubscriptionOptions{QoS: 1}, nil, time.Time{})
	if sess.OutboundQueueLen() != 1 {
		t.Fatalf("expected 1 buffered message, got %d", sess.OutboundQueueLen())
	}

	// No window until an ack frees a slot.
	sess.DecOutboundUnacked()
	if !sess.CanSendOutbound() {
		t.Fatal("expected receive window open after ack")
	}
	msg, ok := sess.PopOutbound()
	if !ok {
		t.Fatal("expected buffered message available")
	}
	if msg.deliverQoS != 1 || msg.pkt.Topic != "buf/topic" {
		t.Errorf("unexpected buffered message: %+v", msg)
	}
	if sess.OutboundQueueLen() != 0 {
		t.Errorf("expected empty queue after pop, got %d", sess.OutboundQueueLen())
	}
}

// TestOutboundBufferBounded verifies the flow-control buffer rejects new
// messages once maxBufferedOutbound is reached, so a client that never
// acknowledges cannot exhaust broker memory (R6).
func TestOutboundBufferBounded(t *testing.T) {
	sess := &Session{OutboundWindow: 1}
	sess.IncOutboundUnacked() // window full
	pkt := &protocol.PublishPacket{}
	pkt.FixedHeader.PacketType = protocol.PacketTypePublish
	pkt.FixedHeader.QoS = 1
	pkt.Topic = "buf/bounded"
	pkt.Payload = []byte("x")

	for i := 0; i < maxBufferedOutbound; i++ {
		if !sess.BufferOutbound(pkt, 1, SubscriptionOptions{QoS: 1}, nil, time.Time{}) {
			t.Fatalf("buffer should accept message %d", i)
		}
	}
	if got := sess.OutboundQueueLen(); got != maxBufferedOutbound {
		t.Fatalf("expected buffer at %d, got %d", maxBufferedOutbound, got)
	}
	if sess.BufferOutbound(pkt, 1, SubscriptionOptions{QoS: 1}, nil, time.Time{}) {
		t.Error("buffer should reject messages beyond maxBufferedOutbound")
	}
	if got := sess.OutboundQueueLen(); got != maxBufferedOutbound {
		t.Errorf("buffer size must not grow past the bound, got %d", got)
	}
}

// TestOutboundUnackedFloor verifies spurious acknowledgments cannot drive the
// flow-control counter below zero (P2-16).
func TestOutboundUnackedFloor(t *testing.T) {
	sess := &Session{OutboundWindow: 5}
	sess.IncOutboundUnacked()
	sess.IncOutboundUnacked()

	// Spurious PUBACK for a packet never sent: RemoveInflight reports false and
	// the counter must not drop.
	if sess.RemoveInflight(999) {
		t.Error("expected RemoveInflight(999) to report false")
	}
	if got := atomic.LoadInt32(&sess.outboundUnacked); got != 2 {
		t.Errorf("expected outboundUnacked=2 after spurious ack, got %d", got)
	}

	// Floor: repeated decrements stop at zero.
	sess.DecOutboundUnacked()
	sess.DecOutboundUnacked()
	sess.DecOutboundUnacked()
	sess.DecOutboundUnacked()
	if got := atomic.LoadInt32(&sess.outboundUnacked); got != 0 {
		t.Errorf("expected outboundUnacked floor at 0, got %d", got)
	}
}

func TestSessionIsExpired(t *testing.T) {
	sess := &Session{
		KeepAlive:    10,
		LastActivity: time.Now().Add(-20 * time.Second),
	}

	if !sess.IsExpired() {
		t.Error("session should be expired")
	}

	sess = &Session{
		KeepAlive:    0, // No timeout
		LastActivity: time.Now().Add(-100 * time.Second),
	}
	if sess.IsExpired() {
		t.Error("session with keepalive 0 should never expire")
	}
}

func TestSessionSaveExpiryTime(t *testing.T) {
	ctx := context.Background()
	memStore := memory.NewSessionStore()

	sess := &Session{
		ClientID:       "expiry-test",
		IsClean:        false,
		ExpiryInterval: 10,
		KeepAlive:      60,
		ProtocolVer:    protocol.Version311,
		Subscriptions:  make(map[string]uint8),
		Inflight:       make(map[uint16]*InflightMsg),
	}

	if err := sess.Save(ctx, memStore); err != nil {
		t.Fatalf("save error: %v", err)
	}

	data, err := memStore.GetSession(ctx, "expiry-test")
	if err != nil {
		t.Fatalf("get session error: %v", err)
	}

	if data.ExpiryTime.IsZero() {
		t.Error("ExpiryTime should be set when ExpiryInterval > 0")
	}

	expectedExpiry := time.Now().Add(time.Duration(sess.ExpiryInterval) * time.Second)
	if data.ExpiryTime.Before(expectedExpiry.Add(-time.Second)) || data.ExpiryTime.After(expectedExpiry.Add(time.Second)) {
		t.Errorf("ExpiryTime mismatch: got %v, want ~%v", data.ExpiryTime, expectedExpiry)
	}
}

func TestSessionSaveExpiryTimeZeroWhenClean(t *testing.T) {
	ctx := context.Background()
	memStore := memory.NewSessionStore()

	sess := &Session{
		ClientID:       "clean-expiry",
		IsClean:        true,
		ExpiryInterval: 0,
		KeepAlive:      60,
		ProtocolVer:    protocol.Version311,
		Subscriptions:  make(map[string]uint8),
		Inflight:       make(map[uint16]*InflightMsg),
	}

	if err := sess.Save(ctx, memStore); err != nil {
		t.Fatalf("save error: %v", err)
	}

	data, err := memStore.GetSession(ctx, "clean-expiry")
	if err != nil {
		t.Fatalf("get session error: %v", err)
	}

	if !data.ExpiryTime.IsZero() {
		t.Error("ExpiryTime should be zero when ExpiryInterval is 0")
	}
}

func TestSessionSaveRestore(t *testing.T) {
	ctx := context.Background()
	memStore := memory.NewSessionStore()
	mgr := NewManager(memStore)

	sess := mgr.CreateSession("persist-client", &protocol.ConnectPacket{
		ProtocolVersion: protocol.Version311,
		Flags:           protocol.ConnectFlags{CleanSession: false},
		KeepAlive:       60,
		ClientID:        "persist-client",
	}, false)

	sess.AddSubscription("test/#", 1)

	// Save session
	if err := sess.Save(ctx, memStore); err != nil {
		t.Fatalf("save error: %v", err)
	}

	// Remove from memory
	mgr.RemoveSession("persist-client")

	// Restore
	restored, err := mgr.Restore(ctx, "persist-client")
	if err != nil {
		t.Fatalf("restore error: %v", err)
	}

	if restored.ClientID != "persist-client" {
		t.Errorf("client ID: got %q, want persist-client", restored.ClientID)
	}
	if len(restored.Subscriptions) != 1 {
		t.Errorf("expected 1 subscription, got %d", len(restored.Subscriptions))
	}
}
