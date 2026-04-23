package redis

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/store"
	"github.com/redis/go-redis/v9"
)

func TestRetainedStore_CRUD(t *testing.T) {
	skipIfNoRedis(t)

	client := newTestClient(t)
	defer client.Close()
	cleanupTestDB(t, client)

	ctx := context.Background()
	rs := NewRetainedStore(RetainedStoreConfig{
		Client:    client,
		KeyPrefix: "test:retained:",
	})

	topic := "test/retained"
	payload := []byte("retained message")

	// Test SaveRetained
	err := rs.SaveRetained(ctx, topic, 1, payload)
	if err != nil {
		t.Fatalf("SaveRetained failed: %v", err)
	}

	// Test GetRetained
	retrieved, err := rs.GetRetained(ctx, topic)
	if err != nil {
		t.Fatalf("GetRetained failed: %v", err)
	}
	if string(retrieved.Payload) != string(payload) {
		t.Errorf("Payload mismatch: got %s, want %s", string(retrieved.Payload), string(payload))
	}
	if retrieved.QoS != 1 {
		t.Errorf("QoS mismatch: got %d, want 1", retrieved.QoS)
	}

	// Test DeleteRetained (via empty payload)
	err = rs.SaveRetained(ctx, topic, 0, []byte{})
	if err != nil {
		t.Fatalf("SaveRetained (delete) failed: %v", err)
	}

	_, err = rs.GetRetained(ctx, topic)
	if err != store.ErrRetainedNotFound {
		t.Errorf("expected ErrRetainedNotFound, got %v", err)
	}
}

func TestRetainedStore_MultipleTopics(t *testing.T) {
	skipIfNoRedis(t)

	client := newTestClient(t)
	defer client.Close()
	cleanupTestDB(t, client)

	ctx := context.Background()
	rs := NewRetainedStore(RetainedStoreConfig{
		Client:    client,
		KeyPrefix: "test:retained:",
	})

	// Save multiple retained messages
	topics := []struct {
		topic   string
		payload []byte
		qos     uint8
	}{
		{"test/temp", []byte("25.5"), 0},
		{"test/humidity", []byte("65%"), 1},
		{"test/status", []byte("online"), 2},
	}

	for _, tt := range topics {
		err := rs.SaveRetained(ctx, tt.topic, tt.qos, tt.payload)
		if err != nil {
			t.Fatalf("SaveRetained failed for %s: %v", tt.topic, err)
		}
	}

	// Verify each topic
	for _, tt := range topics {
		retrieved, err := rs.GetRetained(ctx, tt.topic)
		if err != nil {
			t.Errorf("GetRetained failed for %s: %v", tt.topic, err)
			continue
		}
		if string(retrieved.Payload) != string(tt.payload) {
			t.Errorf("Payload mismatch for %s: got %s, want %s", tt.topic, string(retrieved.Payload), string(tt.payload))
		}
	}

	// Delete one and verify others remain
	err := rs.DeleteRetained(ctx, "test/temp")
	if err != nil {
		t.Fatalf("DeleteRetained failed: %v", err)
	}

	_, err = rs.GetRetained(ctx, "test/temp")
	if err != store.ErrRetainedNotFound {
		t.Errorf("expected ErrRetainedNotFound for test/temp, got %v", err)
	}

	// Other topics should still exist
	_, err = rs.GetRetained(ctx, "test/humidity")
	if err != nil {
		t.Errorf("test/humidity should still exist, got: %v", err)
	}

	_, err = rs.GetRetained(ctx, "test/status")
	if err != nil {
		t.Errorf("test/status should still exist, got: %v", err)
	}
}

func TestRetainedStore_MatchRetained(t *testing.T) {
	skipIfNoRedis(t)

	client := newTestClient(t)
	defer client.Close()
	cleanupTestDB(t, client)

	ctx := context.Background()
	rs := NewRetainedStore(RetainedStoreConfig{
		Client:    client,
		KeyPrefix: "test:retained:",
	})

	// Save retained messages for various topics
	testCases := []struct {
		topic   string
		payload []byte
	}{
		{"home/living-room/temp", []byte("22")},
		{"home/living-room/humidity", []byte("45")},
		{"home/kitchen/temp", []byte("24")},
		{"home/bedroom/temp", []byte("20")},
		{"office/floor1/temp", []byte("21")},
	}

	for _, tc := range testCases {
		err := rs.SaveRetained(ctx, tc.topic, 1, tc.payload)
		if err != nil {
			t.Fatalf("SaveRetained failed: %v", err)
		}
	}

	// Test wildcard matching
	tests := []struct {
		pattern     string
		expectCount int
		expectKeys []string
	}{
		{
			pattern:     "home/+/temp",
			expectCount: 3,
			expectKeys:  []string{"home/living-room/temp", "home/kitchen/temp", "home/bedroom/temp"},
		},
		{
			pattern:     "home/#",
			expectCount: 4,
			expectKeys:  []string{"home/living-room/temp", "home/living-room/humidity", "home/kitchen/temp", "home/bedroom/temp"},
		},
		{
			pattern:     "home/living-room/#",
			expectCount: 2,
			expectKeys:  []string{"home/living-room/temp", "home/living-room/humidity"},
		},
		{
			pattern:     "office/#",
			expectCount: 1,
			expectKeys:  []string{"office/floor1/temp"},
		},
		{
			pattern:     "non-existent/#",
			expectCount: 0,
			expectKeys:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			matched, err := rs.MatchRetained(ctx, tt.pattern)
			if err != nil {
				t.Fatalf("MatchRetained failed: %v", err)
			}

			if len(matched) != tt.expectCount {
				t.Errorf("expected %d matches, got %d", tt.expectCount, len(matched))
			}

			// Build a set of matched topics for comparison
			matchedMap := make(map[string]bool)
			for _, m := range matched {
				matchedMap[m.Topic] = true
			}

			for _, expected := range tt.expectKeys {
				if !matchedMap[expected] {
					t.Errorf("expected topic %s not found in matches", expected)
				}
			}
		})
	}
}

func TestRetainedStore_DeleteNonExistent(t *testing.T) {
	skipIfNoRedis(t)

	client := newTestClient(t)
	defer client.Close()
	cleanupTestDB(t, client)

	ctx := context.Background()
	rs := NewRetainedStore(RetainedStoreConfig{
		Client:    client,
		KeyPrefix: "test:retained:",
	})

	// Delete non-existent topic should not error
	err := rs.DeleteRetained(ctx, "non-existent-topic")
	if err != nil {
		t.Errorf("DeleteRetained for non-existent should not error, got: %v", err)
	}
}

func TestRetainedStore_UpdateExisting(t *testing.T) {
	skipIfNoRedis(t)

	client := newTestClient(t)
	defer client.Close()
	cleanupTestDB(t, client)

	ctx := context.Background()
	rs := NewRetainedStore(RetainedStoreConfig{
		Client:    client,
		KeyPrefix: "test:retained:",
	})

	topic := "test/update"

	// Save initial
	err := rs.SaveRetained(ctx, topic, 1, []byte("v1"))
	if err != nil {
		t.Fatalf("SaveRetained v1 failed: %v", err)
	}

	// Update with new value
	err = rs.SaveRetained(ctx, topic, 2, []byte("v2"))
	if err != nil {
		t.Fatalf("SaveRetained v2 failed: %v", err)
	}

	// Verify updated
	retrieved, err := rs.GetRetained(ctx, topic)
	if err != nil {
		t.Fatalf("GetRetained failed: %v", err)
	}
	if string(retrieved.Payload) != "v2" {
		t.Errorf("expected v2, got %s", string(retrieved.Payload))
	}
	if retrieved.QoS != 2 {
		t.Errorf("expected QoS 2, got %d", retrieved.QoS)
	}
}

func BenchmarkRetainedStore_Save(b *testing.B) {
	addr := os.Getenv("MQTT_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()

	ctx := context.Background()
	rs := NewRetainedStore(RetainedStoreConfig{
		Client:    client,
		KeyPrefix: "bench:retained:",
	})

	payload := make([]byte, 256)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		topic := "bench/topic-" + string(rune(i))
		rs.SaveRetained(ctx, topic, 1, payload)
	}
}

func BenchmarkRetainedStore_Get(b *testing.B) {
	addr := os.Getenv("MQTT_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()

	ctx := context.Background()
	rs := NewRetainedStore(RetainedStoreConfig{
		Client:    client,
		KeyPrefix: "bench:retained:",
	})

	// Pre-populate
	for i := 0; i < 1000; i++ {
		topic := "bench/topic-" + string(rune(i))
		rs.SaveRetained(ctx, topic, 1, []byte("data"))
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		topic := "bench/topic-" + string(rune(i%1000))
		rs.GetRetained(ctx, topic)
	}
}

func BenchmarkRetainedStore_Match(b *testing.B) {
	addr := os.Getenv("MQTT_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()

	ctx := context.Background()
	rs := NewRetainedStore(RetainedStoreConfig{
		Client:    client,
		KeyPrefix: "bench:retained:",
	})

	// Pre-populate with 100 topics
	for i := 0; i < 100; i++ {
		topic := "bench/device-" + string(rune(i%10)) + "/sensor-" + string(rune(i))
		rs.SaveRetained(ctx, topic, 1, []byte("data"))
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		rs.MatchRetained(ctx, "bench/device-"+string(rune(i%10))+"/#")
	}
}

func BenchmarkRetainedStore_Delete(b *testing.B) {
	addr := os.Getenv("MQTT_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()

	ctx := context.Background()
	rs := NewRetainedStore(RetainedStoreConfig{
		Client:    client,
		KeyPrefix: "bench:retained:",
	})

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		topic := "bench/topic-" + string(rune(i))
		rs.SaveRetained(ctx, topic, 1, []byte("data"))
		rs.DeleteRetained(ctx, topic)
	}
}

func init() {
	// Suppress benchmark logs
	_ = time.Second
}
