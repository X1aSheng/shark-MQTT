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
var _ store.SessionStore = (*SessionStore)(nil)

// SessionStore implements store.SessionStore using Redis.
type SessionStore struct {
	client    *redis.Client
	keyPrefix string
	ttl       time.Duration
}

// SessionStoreConfig holds configuration for Redis session store.
type SessionStoreConfig struct {
	Client    *redis.Client
	KeyPrefix string
	TTL       time.Duration
}

// NewSessionStore creates a new Redis-backed session store.
func NewSessionStore(cfg SessionStoreConfig) *SessionStore {
	prefix := cfg.KeyPrefix
	if prefix == "" {
		prefix = "mqtt:session:"
	}
	ttl := cfg.TTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	return &SessionStore{
		client:    cfg.Client,
		keyPrefix: prefix,
		ttl:       ttl,
	}
}

func (s *SessionStore) sessionKey(clientID string) string {
	return s.keyPrefix + clientID
}

func (s *SessionStore) SaveSession(ctx context.Context, clientID string, data *store.SessionData) error {
	serialized, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("serialize session: %w", err)
	}
	return s.client.Set(ctx, s.sessionKey(clientID), serialized, s.ttl).Err()
}

func (s *SessionStore) GetSession(ctx context.Context, clientID string) (*store.SessionData, error) {
	val, err := s.client.Get(ctx, s.sessionKey(clientID)).Result()
	if err == redis.Nil {
		return nil, store.ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	var data store.SessionData
	if err := json.Unmarshal([]byte(val), &data); err != nil {
		return nil, fmt.Errorf("deserialize session: %w", err)
	}
	return &data, nil
}

func (s *SessionStore) DeleteSession(ctx context.Context, clientID string) error {
	return s.client.Del(ctx, s.sessionKey(clientID)).Err()
}

func (s *SessionStore) ListSessions(ctx context.Context) ([]string, error) {
	if s.keyPrefix == "" {
		return nil, fmt.Errorf("cannot list sessions with empty key prefix: would scan entire database")
	}
	var clientIDs []string
	var cursor uint64
	pattern := s.keyPrefix + "*"

	for {
		keys, nextCursor, err := s.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("scan sessions: %w", err)
		}
		for _, key := range keys {
			clientID := key[len(s.keyPrefix):]
			clientIDs = append(clientIDs, clientID)
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return clientIDs, nil
}

func (s *SessionStore) IsSessionExists(ctx context.Context, clientID string) (bool, error) {
	exists, err := s.client.Exists(ctx, s.sessionKey(clientID)).Result()
	if err != nil {
		return false, fmt.Errorf("check session exists: %w", err)
	}
	return exists > 0, nil
}
