//go:build store_redis

package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/X1aSheng/shark-mqtt/store"
	"github.com/redis/go-redis/v9"
)

// Ensure interface compliance.
var _ store.MessageStore = (*MessageStore)(nil)

// MessageStore implements store.MessageStore using Redis.
type MessageStore struct {
	client    *redis.Client
	keyPrefix string
	ttl       time.Duration
}

// MessageStoreConfig holds configuration for Redis message store.
type MessageStoreConfig struct {
	Client    *redis.Client
	KeyPrefix string
	TTL       time.Duration
}

// NewMessageStore creates a new Redis-backed message store.
func NewMessageStore(cfg MessageStoreConfig) *MessageStore {
	prefix := cfg.KeyPrefix
	if prefix == "" {
		prefix = "mqtt:message:"
	}
	ttl := cfg.TTL
	if ttl == 0 {
		ttl = 1 * time.Hour
	}
	return &MessageStore{
		client:    cfg.Client,
		keyPrefix: prefix,
		ttl:       ttl,
	}
}

func (s *MessageStore) messageKey(clientID, id string) string {
	return s.keyPrefix + clientID + ":" + id
}

func (s *MessageStore) clientPattern(clientID string) string {
	return s.keyPrefix + clientID + ":*"
}

func (s *MessageStore) SaveMessage(ctx context.Context, clientID string, msg *store.StoredMessage) error {
	serialized, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("serialize message: %w", err)
	}
	// Keep the key alive at least as long as the message's own expiry
	// deadline (Message Expiry Interval), so the broker's expiry handling —
	// not a storage-side TTL — decides when a queued message disappears (S5).
	// Messages without an expiry deadline keep the configured default TTL.
	ttl := s.ttl
	if msg != nil && !msg.ExpiresAt.IsZero() {
		if d := time.Until(msg.ExpiresAt); d > 0 {
			ttl = d
		} else {
			ttl = time.Second // already expired; dropped on next delivery pass
		}
	}
	return s.client.Set(ctx, s.messageKey(clientID, msg.ID), serialized, ttl).Err()
}

func (s *MessageStore) GetMessage(ctx context.Context, clientID, id string) (*store.StoredMessage, error) {
	val, err := s.client.Get(ctx, s.messageKey(clientID, id)).Result()
	if err == redis.Nil {
		return nil, store.ErrMessageNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get message: %w", err)
	}
	var msg store.StoredMessage
	if err := json.Unmarshal([]byte(val), &msg); err != nil {
		return nil, fmt.Errorf("deserialize message: %w", err)
	}
	return &msg, nil
}

func (s *MessageStore) DeleteMessage(ctx context.Context, clientID, id string) error {
	return s.client.Del(ctx, s.messageKey(clientID, id)).Err()
}

func (s *MessageStore) ListMessages(ctx context.Context, clientID string) ([]*store.StoredMessage, error) {
	pattern := s.clientPattern(clientID)

	// Collect the matching keys first, then fetch all values with a single
	// MGet instead of one GET per key: an offline persistent session's queue
	// is drained on reconnect, so a large queue would otherwise cost one
	// round-trip per message (S2).
	var keys []string
	var cursor uint64
	for {
		batch, nextCursor, err := s.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan messages: %w", err)
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
		return nil, fmt.Errorf("get messages: %w", err)
	}
	messages := make([]*store.StoredMessage, 0, len(keys))
	for _, v := range vals {
		raw, ok := v.(string)
		if !ok {
			continue // key expired or was deleted between SCAN and MGet
		}
		var msg store.StoredMessage
		if err := json.Unmarshal([]byte(raw), &msg); err == nil {
			messages = append(messages, &msg)
		}
	}
	return messages, nil
}

func (s *MessageStore) ClearMessages(ctx context.Context, clientID string) error {
	var cursor uint64
	pattern := s.clientPattern(clientID)

	for {
		keys, nextCursor, err := s.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return fmt.Errorf("scan messages for clear: %w", err)
		}
		if len(keys) > 0 {
			if err := s.client.Del(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("delete messages: %w", err)
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return nil
}
