//go:build store_redis

package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store"
	"github.com/redis/go-redis/v9"
)

// Ensure interface compliance.
var _ store.RetainedStore = (*RetainedStore)(nil)

// RetainedStore implements store.RetainedStore using Redis.
type RetainedStore struct {
	client    *redis.Client
	keyPrefix string
}

// RetainedStoreConfig holds configuration for Redis retained store.
type RetainedStoreConfig struct {
	Client    *redis.Client
	KeyPrefix string
}

// NewRetainedStore creates a new Redis-backed retained message store.
func NewRetainedStore(cfg RetainedStoreConfig) *RetainedStore {
	prefix := cfg.KeyPrefix
	if prefix == "" {
		prefix = "mqtt:retained:"
	}
	return &RetainedStore{
		client:    cfg.Client,
		keyPrefix: prefix,
	}
}

func (s *RetainedStore) topicKey(topic string) string {
	return s.keyPrefix + topic
}

func (s *RetainedStore) SaveRetained(ctx context.Context, topic string, qos uint8, payload []byte) error {
	// Per MQTT spec, a retained message with zero-length payload deletes the existing retained message.
	if len(payload) == 0 {
		return s.DeleteRetained(ctx, topic)
	}
	retained := &store.RetainedMessage{
		Topic:     topic,
		QoS:       qos,
		Payload:   payload,
		Timestamp: time.Now(), // persist the store time so retained TTLs survive restarts (audit)
	}
	serialized, err := json.Marshal(retained)
	if err != nil {
		return fmt.Errorf("serialize retained: %w", err)
	}
	return s.client.Set(ctx, s.topicKey(topic), serialized, 0).Err()
}

func (s *RetainedStore) GetRetained(ctx context.Context, topic string) (*store.RetainedMessage, error) {
	val, err := s.client.Get(ctx, s.topicKey(topic)).Result()
	if err == redis.Nil {
		return nil, store.ErrRetainedNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get retained: %w", err)
	}
	var retained store.RetainedMessage
	if err := json.Unmarshal([]byte(val), &retained); err != nil {
		return nil, fmt.Errorf("deserialize retained: %w", err)
	}
	return &retained, nil
}

func (s *RetainedStore) DeleteRetained(ctx context.Context, topic string) error {
	return s.client.Del(ctx, s.topicKey(topic)).Err()
}

func (s *RetainedStore) MatchRetained(ctx context.Context, pattern string) ([]*store.RetainedMessage, error) {
	// Convert MQTT topic pattern to Redis glob pattern
	redisPattern := s.keyPrefix + topicPatternToRedis(pattern)

	// Collect matching keys first, then batch-fetch values with a single MGet
	// (S2): a wildcard retained match previously cost one GET round-trip per
	// candidate key.
	var keys []string
	var cursor uint64
	for {
		batch, nextCursor, err := s.client.Scan(ctx, cursor, redisPattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan retained: %w", err)
		}
		keys = append(keys, batch...)
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}

	vals, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("get retained: %w", err)
	}

	var messages []*store.RetainedMessage
	for _, v := range vals {
		raw, ok := v.(string)
		if !ok {
			continue // key expired or was deleted between SCAN and MGet
		}
		var msg store.RetainedMessage
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			continue
		}
		// Verify topic matches MQTT pattern (not just Redis pattern).
		if protocol.MatchTopic(pattern, msg.Topic) {
			messages = append(messages, &msg)
		}
	}
	return messages, nil
}

// topicPatternToRedis converts an MQTT topic pattern to a Redis glob pattern.
// MQTT literal topic characters that are Redis glob metacharacters ('\', '*',
// '?', '[') are escaped first so an exact or '#'-free subscription still
// matches a stored topic such as "a[0]" or "b?x" (audit). '+' and '#' are
// MQTT wildcards (legal topics never contain them), so they map to '*'; the
// secondary protocol.MatchTopic call in MatchRetained filters false positives.
func topicPatternToRedis(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '#', '+':
			b.WriteByte('*')
		case '\\', '*', '?', '[':
			b.WriteByte('\\')
			b.WriteByte(pattern[i])
		default:
			b.WriteByte(pattern[i])
		}
	}
	return b.String()
}
