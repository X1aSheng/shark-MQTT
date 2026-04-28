package redis

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/store"
	"github.com/redis/go-redis/v9"
)

func TestMessageStore_CRUD(t *testing.T) {
	skipIfNoRedis(t)

	client := newTestClient(t)
	defer client.Close()
	cleanupTestDB(t, client)

	ctx := context.Background()
	ms := NewMessageStore(MessageStoreConfig{
		Client:    client,
		KeyPrefix: "test:message:",
		TTL:       time.Hour,
	})

	clientID := "test-client-1"
	msg := &store.StoredMessage{
		ID:        "msg-001",
		Topic:     "test/topic",
		QoS:       1,
		Payload:   []byte("hello world"),
		Timestamp: time.Now(),
	}

	// Test SaveMessage
	err := ms.SaveMessage(ctx, clientID, msg)
	if err != nil {
		t.Fatalf("SaveMessage failed: %v", err)
	}

	// Test GetMessage
	retrieved, err := ms.GetMessage(ctx, clientID, "msg-001")
	if err != nil {
		t.Fatalf("GetMessage failed: %v", err)
	}
	if retrieved.ID != msg.ID {
		t.Errorf("ID mismatch: got %s, want %s", retrieved.ID, msg.ID)
	}
	if string(retrieved.Payload) != string(msg.Payload) {
		t.Errorf("Payload mismatch: got %s, want %s", string(retrieved.Payload), string(msg.Payload))
	}
	if retrieved.Topic != msg.Topic {
		t.Errorf("Topic mismatch: got %s, want %s", retrieved.Topic, msg.Topic)
	}
	if retrieved.QoS != msg.QoS {
		t.Errorf("QoS mismatch: got %d, want %d", retrieved.QoS, msg.QoS)
	}

	// Test GetMessage not found
	_, err = ms.GetMessage(ctx, clientID, "non-existent")
	if err != store.ErrMessageNotFound {
		t.Errorf("expected ErrMessageNotFound, got %v", err)
	}

	// Test ListMessages
	msg2 := &store.StoredMessage{
		ID:      "msg-002",
		Topic:   "test/topic2",
		QoS:     2,
		Payload: []byte("second message"),
	}
	ms.SaveMessage(ctx, clientID, msg2)

	msgs, err := ms.ListMessages(ctx, clientID)
	if err != nil {
		t.Fatalf("ListMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 messages, got %d", len(msgs))
	}

	// Test DeleteMessage
	err = ms.DeleteMessage(ctx, clientID, "msg-001")
	if err != nil {
		t.Fatalf("DeleteMessage failed: %v", err)
	}

	_, err = ms.GetMessage(ctx, clientID, "msg-001")
	if err != store.ErrMessageNotFound {
		t.Errorf("expected message to be deleted, got %v", err)
	}

	// Test ClearMessages
	err = ms.ClearMessages(ctx, clientID)
	if err != nil {
		t.Fatalf("ClearMessages failed: %v", err)
	}

	msgs, err = ms.ListMessages(ctx, clientID)
	if err != nil {
		t.Fatalf("ListMessages after clear failed: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after clear, got %d", len(msgs))
	}
}

func TestMessageStore_MultipleClients(t *testing.T) {
	skipIfNoRedis(t)

	client := newTestClient(t)
	defer client.Close()
	cleanupTestDB(t, client)

	ctx := context.Background()
	ms := NewMessageStore(MessageStoreConfig{
		Client:    client,
		KeyPrefix: "test:message:",
		TTL:       time.Hour,
	})

	// Save messages for different clients
	ms.SaveMessage(ctx, "client-a", &store.StoredMessage{ID: "a-1", Payload: []byte("client-a data")})
	ms.SaveMessage(ctx, "client-b", &store.StoredMessage{ID: "b-1", Payload: []byte("client-b data")})
	ms.SaveMessage(ctx, "client-a", &store.StoredMessage{ID: "a-2", Payload: []byte("client-a data 2")})

	// List should only return messages for the specified client
	msgsA, _ := ms.ListMessages(ctx, "client-a")
	msgsB, _ := ms.ListMessages(ctx, "client-b")

	if len(msgsA) != 2 {
		t.Errorf("expected 2 messages for client-a, got %d", len(msgsA))
	}
	if len(msgsB) != 1 {
		t.Errorf("expected 1 message for client-b, got %d", len(msgsB))
	}

	// Clear should only affect specified client
	err := ms.ClearMessages(ctx, "client-a")
	if err != nil {
		t.Fatalf("ClearMessages failed: %v", err)
	}

	msgsA, _ = ms.ListMessages(ctx, "client-a")
	msgsB, _ = ms.ListMessages(ctx, "client-b")

	if len(msgsA) != 0 {
		t.Errorf("expected 0 messages for client-a after clear, got %d", len(msgsA))
	}
	if len(msgsB) != 1 {
		t.Errorf("expected 1 message for client-b still, got %d", len(msgsB))
	}
}

func TestMessageStore_TTL(t *testing.T) {
	skipIfNoRedis(t)

	client := newTestClient(t)
	defer client.Close()
	cleanupTestDB(t, client)

	ctx := context.Background()
	ms := NewMessageStore(MessageStoreConfig{
		Client:    client,
		KeyPrefix: "test:message:",
		TTL:       100 * time.Millisecond,
	})

	msg := &store.StoredMessage{
		ID:      "ttl-msg",
		Payload: []byte("expiring"),
	}

	err := ms.SaveMessage(ctx, "test-client", msg)
	if err != nil {
		t.Fatalf("SaveMessage failed: %v", err)
	}

	// Message should exist immediately
	_, err = ms.GetMessage(ctx, "test-client", "ttl-msg")
	if err != nil {
		t.Errorf("expected message to exist immediately, got: %v", err)
	}

	// Wait for TTL to expire
	time.Sleep(150 * time.Millisecond)

	// Message should be expired
	_, err = ms.GetMessage(ctx, "test-client", "ttl-msg")
	if err != store.ErrMessageNotFound {
		t.Errorf("expected message to be expired after TTL, got: %v", err)
	}
}

func TestMessageStore_LargePayload(t *testing.T) {
	skipIfNoRedis(t)

	client := newTestClient(t)
	defer client.Close()
	cleanupTestDB(t, client)

	ctx := context.Background()
	ms := NewMessageStore(MessageStoreConfig{
		Client:    client,
		KeyPrefix: "test:message:",
		TTL:       time.Hour,
	})

	// 1MB payload
	largePayload := make([]byte, 1024*1024)
	for i := range largePayload {
		largePayload[i] = byte(i % 256)
	}

	msg := &store.StoredMessage{
		ID:      "large-msg",
		Topic:   "large/topic",
		QoS:     1,
		Payload: largePayload,
	}

	err := ms.SaveMessage(ctx, "test-client", msg)
	if err != nil {
		t.Fatalf("SaveMessage failed: %v", err)
	}

	retrieved, err := ms.GetMessage(ctx, "test-client", "large-msg")
	if err != nil {
		t.Fatalf("GetMessage failed: %v", err)
	}

	if len(retrieved.Payload) != len(largePayload) {
		t.Errorf("payload size mismatch: got %d, want %d", len(retrieved.Payload), len(largePayload))
	}

	for i, b := range retrieved.Payload {
		if b != byte(i%256) {
			t.Errorf("payload byte %d mismatch: got %d, want %d", i, b, byte(i%256))
			break
		}
	}
}

func BenchmarkMessageStore_Save(b *testing.B) {
	addr := os.Getenv("MQTT_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()

	ctx := context.Background()
	ms := NewMessageStore(MessageStoreConfig{
		Client:    client,
		KeyPrefix: "bench:message:",
		TTL:       time.Hour,
	})

	payload := make([]byte, 1024)
	msg := &store.StoredMessage{
		ID:      "bench-msg",
		Topic:   "bench/topic",
		QoS:     1,
		Payload: payload,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		msg.ID = "bench-msg-" + string(rune(i))
		ms.SaveMessage(ctx, "bench-client", msg)
	}
}

func BenchmarkMessageStore_Get(b *testing.B) {
	addr := os.Getenv("MQTT_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()

	ctx := context.Background()
	ms := NewMessageStore(MessageStoreConfig{
		Client:    client,
		KeyPrefix: "bench:message:",
		TTL:       time.Hour,
	})

	// Pre-populate with 50 messages
	const prePop = 50
	payload := make([]byte, 256)
	for i := 0; i < prePop; i++ {
		msg := &store.StoredMessage{
			ID:      "bench-msg-" + string(rune(i)),
			Payload: payload,
		}
		ms.SaveMessage(ctx, "bench-client", msg)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		ms.GetMessage(ctx, "bench-client", "bench-msg-"+string(rune(i%prePop)))
	}
}

func BenchmarkMessageStore_List(b *testing.B) {
	addr := os.Getenv("MQTT_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()

	ctx := context.Background()
	ms := NewMessageStore(MessageStoreConfig{
		Client:    client,
		KeyPrefix: "bench:message:",
		TTL:       time.Hour,
	})

	// Pre-populate with 50 messages
	const prePop = 50
	payload := make([]byte, 256)
	for i := 0; i < prePop; i++ {
		msg := &store.StoredMessage{
			ID:      "bench-msg-" + string(rune(i)),
			Payload: payload,
		}
		ms.SaveMessage(ctx, "bench-client", msg)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		ms.ListMessages(ctx, "bench-client")
	}
}
