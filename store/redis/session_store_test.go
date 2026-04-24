package redis

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/store"
	"github.com/redis/go-redis/v9"
)

func skipIfNoRedis(t *testing.T) {
	if os.Getenv("MQTT_REDIS_ADDR") == "" {
		t.Skip("skipping: MQTT_REDIS_ADDR not set")
	}
}

func newTestClient(t *testing.T) *redis.Client {
	addr := os.Getenv("MQTT_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	return redis.NewClient(&redis.Options{
		Addr: addr,
	})
}

func cleanupTestDB(t *testing.T, client *redis.Client) {
	ctx := context.Background()
	iter := client.Scan(ctx, 0, "test:*", 100).Iterator()
	for iter.Next(ctx) {
		client.Del(ctx, iter.Val())
	}
	if err := iter.Err(); err != nil {
		t.Logf("cleanup warning: %v", err)
	}
}

func TestSessionStore_CRUD(t *testing.T) {
	skipIfNoRedis(t)

	client := newTestClient(t)
	defer client.Close()
	cleanupTestDB(t, client)

	ctx := context.Background()
	ss := NewSessionStore(SessionStoreConfig{
		Client:    client,
		KeyPrefix: "test:session:",
		TTL:       time.Hour,
	})

	// Test SaveSession
	data := &store.SessionData{
		ClientID: "test-client-1",
		IsClean:  false,
		Subscriptions: []store.Subscription{
			{Topic: "test/topic1", QoS: 1},
			{Topic: "test/topic2", QoS: 0},
		},
	}

	err := ss.SaveSession(ctx, "test-client-1", data)
	if err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// Test GetSession
	retrieved, err := ss.GetSession(ctx, "test-client-1")
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if retrieved.ClientID != data.ClientID {
		t.Errorf("ClientID mismatch: got %s, want %s", retrieved.ClientID, data.ClientID)
	}
	if retrieved.IsClean != data.IsClean {
		t.Errorf("IsClean mismatch: got %v, want %v", retrieved.IsClean, data.IsClean)
	}
	if len(retrieved.Subscriptions) != 2 {
		t.Errorf("Subscriptions count mismatch: got %d, want 2", len(retrieved.Subscriptions))
	}

	// Test GetSession not found
	_, err = ss.GetSession(ctx, "non-existent")
	if err != store.ErrSessionNotFound {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}

	// Test IsSessionExists
	exists, err := ss.IsSessionExists(ctx, "test-client-1")
	if err != nil {
		t.Fatalf("IsSessionExists failed: %v", err)
	}
	if !exists {
		t.Error("expected session to exist")
	}

	exists, err = ss.IsSessionExists(ctx, "non-existent")
	if err != nil {
		t.Fatalf("IsSessionExists failed: %v", err)
	}
	if exists {
		t.Error("expected session to not exist")
	}

	// Test ListSessions
	ss.SaveSession(ctx, "test-client-2", &store.SessionData{ClientID: "test-client-2"})
	sessions, err := ss.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if len(sessions) < 2 {
		t.Errorf("expected at least 2 sessions, got %d", len(sessions))
	}

	// Test DeleteSession
	err = ss.DeleteSession(ctx, "test-client-1")
	if err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}

	exists, err = ss.IsSessionExists(ctx, "test-client-1")
	if err != nil {
		t.Fatalf("IsSessionExists failed: %v", err)
	}
	if exists {
		t.Error("expected session to be deleted")
	}
}

func TestSessionStore_TTL(t *testing.T) {
	skipIfNoRedis(t)

	client := newTestClient(t)
	defer client.Close()
	cleanupTestDB(t, client)

	ctx := context.Background()
	ss := NewSessionStore(SessionStoreConfig{
		Client:    client,
		KeyPrefix: "test:session:",
		TTL:       100 * time.Millisecond,
	})

	data := &store.SessionData{ClientID: "test-ttl"}

	err := ss.SaveSession(ctx, "test-ttl", data)
	if err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// Session should exist immediately
	exists, _ := ss.IsSessionExists(ctx, "test-ttl")
	if !exists {
		t.Error("expected session to exist immediately after save")
	}

	// Wait for TTL to expire
	time.Sleep(150 * time.Millisecond)

	// Session should be expired
	exists, _ = ss.IsSessionExists(ctx, "test-ttl")
	if exists {
		t.Error("expected session to be expired after TTL")
	}
}

func TestSessionStore_EmptySubscriptions(t *testing.T) {
	skipIfNoRedis(t)

	client := newTestClient(t)
	defer client.Close()
	cleanupTestDB(t, client)

	ctx := context.Background()
	ss := NewSessionStore(SessionStoreConfig{
		Client:    client,
		KeyPrefix: "test:session:",
		TTL:       time.Hour,
	})

	data := &store.SessionData{
		ClientID:     "test-no-subs",
		IsClean:      true,
		Subscriptions: []store.Subscription{},
	}

	err := ss.SaveSession(ctx, "test-no-subs", data)
	if err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	retrieved, err := ss.GetSession(ctx, "test-no-subs")
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if len(retrieved.Subscriptions) != 0 {
		t.Errorf("expected empty subscriptions, got %d", len(retrieved.Subscriptions))
	}
}

func TestSessionStore_InflightMessages(t *testing.T) {
	skipIfNoRedis(t)

	client := newTestClient(t)
	defer client.Close()
	cleanupTestDB(t, client)

	ctx := context.Background()
	ss := NewSessionStore(SessionStoreConfig{
		Client:    client,
		KeyPrefix: "test:session:",
		TTL:       time.Hour,
	})

	data := &store.SessionData{
		ClientID: "test-inflight",
		IsClean:  false,
		Inflight: map[uint16]*store.InflightMessage{
			1: {PacketID: 1, QoS: 1, Topic: "test/topic", Payload: []byte("hello")},
			2: {PacketID: 2, QoS: 2, Topic: "test/topic2", Payload: []byte("world")},
		},
	}

	err := ss.SaveSession(ctx, "test-inflight", data)
	if err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	retrieved, err := ss.GetSession(ctx, "test-inflight")
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}

	if len(retrieved.Inflight) != 2 {
		t.Errorf("expected 2 inflight messages, got %d", len(retrieved.Inflight))
	}

	msg1, ok := retrieved.Inflight[1]
	if !ok {
		t.Fatal("expected inflight message 1")
	}
	if string(msg1.Payload) != "hello" {
		t.Errorf("expected payload 'hello', got '%s'", string(msg1.Payload))
	}
}

func BenchmarkSessionStore_Save(b *testing.B) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	defer client.Close()

	ctx := context.Background()
	ss := NewSessionStore(SessionStoreConfig{
		Client:    client,
		KeyPrefix: "bench:session:",
		TTL:       time.Hour,
	})

	data := &store.SessionData{
		ClientID: "bench-client",
		IsClean:  false,
		Subscriptions: []store.Subscription{
			{Topic: "bench/topic1", QoS: 1},
			{Topic: "bench/topic2", QoS: 0},
			{Topic: "bench/topic3", QoS: 2},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		clientID := "bench-client-" + string(rune(i))
		data.ClientID = clientID
		ss.SaveSession(ctx, clientID, data)
	}
}

func BenchmarkSessionStore_Get(b *testing.B) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	defer client.Close()

	ctx := context.Background()
	ss := NewSessionStore(SessionStoreConfig{
		Client:    client,
		KeyPrefix: "bench:session:",
		TTL:       time.Hour,
	})

	// Pre-populate
	data := &store.SessionData{
		ClientID:     "bench-get",
		IsClean:      false,
		Subscriptions: []store.Subscription{{Topic: "bench/topic", QoS: 1}},
	}
	for i := 0; i < 1000; i++ {
		ss.SaveSession(ctx, "bench-get-"+string(rune(i)), data)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		ss.GetSession(ctx, "bench-get-"+string(rune(i%1000)))
	}
}