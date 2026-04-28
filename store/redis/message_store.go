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
	return s.client.Set(ctx, s.messageKey(clientID, msg.ID), serialized, s.ttl).Err()
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
	var messages []*store.StoredMessage
	var cursor uint64
	pattern := s.clientPattern(clientID)

	for {
		keys, nextCursor, err := s.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan messages: %w", err)
		}
		for _, key := range keys {
			val, err := s.client.Get(ctx, key).Result()
			if err != nil {
				continue
			}
			var msg store.StoredMessage
			if err := json.Unmarshal([]byte(val), &msg); err == nil {
				messages = append(messages, &msg)
			}
		}
		cursor = nextCursor
		if cursor == 0 {
			break
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
