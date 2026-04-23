package broker

import (
	"context"
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
	matches, qos := sess.MatchesSubscription("test/device/data")
	if !matches {
		t.Error("expected match for test/#")
	}
	if qos != 0 {
		t.Errorf("expected QoS 0, got %d", qos)
	}

	matches, qos = sess.MatchesSubscription("sensor/room1/temp")
	if !matches {
		t.Error("expected match for sensor/+/temp")
	}
	if qos != 1 {
		t.Errorf("expected QoS 1, got %d", qos)
	}

	// Test no match
	matches, _ = sess.MatchesSubscription("other/topic")
	if matches {
		t.Error("expected no match for other/topic")
	}

	// Test remove subscription
	sess.RemoveSubscription("test/#")
	matches, _ = sess.MatchesSubscription("test/device/data")
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

func TestTopicMatch(t *testing.T) {
	tests := []struct {
		pattern string
		topic   string
		want    bool
	}{
		{"test/topic", "test/topic", true},
		{"test/#", "test/topic", true},
		{"test/#", "test/a/b/c", true},
		{"sensor/+/temp", "sensor/room1/temp", true},
		{"sensor/+/temp", "sensor/room1/humidity", false},
		{"#", "anything/goes/here", true},
		{"test/+", "test/topic", true},
		{"test/+", "test/a/b", false},
		{"+/test", "foo/test", true},
		{"+/test", "foo/other", false},
	}

	for _, tt := range tests {
		if got := topicMatch(tt.pattern, tt.topic); got != tt.want {
			t.Errorf("topicMatch(%q, %q) = %v, want %v", tt.pattern, tt.topic, got, tt.want)
		}
	}
}
