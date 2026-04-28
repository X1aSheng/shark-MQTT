package redis

import (
	"context"
	"encoding/json"
	"fmt"

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
	retained := &store.RetainedMessage{
		Topic:   topic,
		QoS:     qos,
		Payload: payload,
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

	var messages []*store.RetainedMessage
	var cursor uint64

	for {
		keys, nextCursor, err := s.client.Scan(ctx, cursor, redisPattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan retained: %w", err)
		}
		for _, key := range keys {
			val, err := s.client.Get(ctx, key).Result()
			if err != nil {
				continue
			}
			var msg store.RetainedMessage
			if err := json.Unmarshal([]byte(val), &msg); err == nil {
				// Verify topic matches MQTT pattern (not just Redis pattern)
				if protocol.MatchTopic(pattern, msg.Topic) {
					messages = append(messages, &msg)
				}
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return messages, nil
}

// topicPatternToRedis converts an MQTT topic pattern to a Redis glob pattern.
func topicPatternToRedis(pattern string) string {
	// MQTT: # matches everything below, + matches single level
	// Redis: * matches anything, ? matches single char
	result := pattern
	// Replace MQTT wildcards with Redis equivalents
	result = replaceHash(result, "*")    // # -> *
	result = replacePlus(result, "[^/]") // + -> [^/] (match any char except /)
	return result
}

func replaceHash(s, replacement string) string {
	// Simple string replacement for # -> *
	result := ""
	for i := 0; i < len(s); i++ {
		if s[i] == '#' {
			result += replacement
		} else {
			result += string(s[i])
		}
	}
	return result
}

func replacePlus(s, replacement string) string {
	result := ""
	for i := 0; i < len(s); i++ {
		if s[i] == '+' {
			result += replacement
		} else {
			result += string(s[i])
		}
	}
	return result
}
