package broker

import (
	"context"
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
	matches, qos, _ := sess.MatchesSubscription("test/device/data")
	if !matches {
		t.Error("expected match for test/#")
	}
	if qos != 0 {
		t.Errorf("expected QoS 0, got %d", qos)
	}

	matches, qos, _ = sess.MatchesSubscription("sensor/room1/temp")
	if !matches {
		t.Error("expected match for sensor/+/temp")
	}
	if qos != 1 {
		t.Errorf("expected QoS 1, got %d", qos)
	}

	// Test no match
	matches, _, _ = sess.MatchesSubscription("other/topic")
	if matches {
		t.Error("expected no match for other/topic")
	}

	// Test remove subscription
	sess.RemoveSubscription("test/#")
	matches, _, _ = sess.MatchesSubscription("test/device/data")
	if matches {
		t.Error("expected no match after removing subscription")
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
	sess := &Session{ReceiveMax: 1}
	sess.IncOutboundUnacked()
	if sess.CanSendOutbound() {
		t.Fatal("expected receive window to be full with 1 unacked")
	}

	pkt := &protocol.PublishPacket{}
	pkt.FixedHeader.PacketType = protocol.PacketTypePublish
	pkt.FixedHeader.QoS = 1
	pkt.Topic = "buf/topic"
	pkt.Payload = []byte("data")

	sess.BufferOutbound(pkt, 1, SubscriptionOptions{QoS: 1}, time.Time{})
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
	sess := &Session{ReceiveMax: 1}
	sess.IncOutboundUnacked() // window full
	pkt := &protocol.PublishPacket{}
	pkt.FixedHeader.PacketType = protocol.PacketTypePublish
	pkt.FixedHeader.QoS = 1
	pkt.Topic = "buf/bounded"
	pkt.Payload = []byte("x")

	for i := 0; i < maxBufferedOutbound; i++ {
		if !sess.BufferOutbound(pkt, 1, SubscriptionOptions{QoS: 1}, time.Time{}) {
			t.Fatalf("buffer should accept message %d", i)
		}
	}
	if got := sess.OutboundQueueLen(); got != maxBufferedOutbound {
		t.Fatalf("expected buffer at %d, got %d", maxBufferedOutbound, got)
	}
	if sess.BufferOutbound(pkt, 1, SubscriptionOptions{QoS: 1}, time.Time{}) {
		t.Error("buffer should reject messages beyond maxBufferedOutbound")
	}
	if got := sess.OutboundQueueLen(); got != maxBufferedOutbound {
		t.Errorf("buffer size must not grow past the bound, got %d", got)
	}
}

// TestOutboundUnackedFloor verifies spurious acknowledgments cannot drive the
// flow-control counter below zero (P2-16).
func TestOutboundUnackedFloor(t *testing.T) {
	sess := &Session{ReceiveMax: 5}
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
