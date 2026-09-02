package store

import (
	"context"
	"time"
)

// SessionStore handles session persistence.
type SessionStore interface {
	SaveSession(ctx context.Context, clientID string, data *SessionData) error
	GetSession(ctx context.Context, clientID string) (*SessionData, error)
	DeleteSession(ctx context.Context, clientID string) error
	ListSessions(ctx context.Context) ([]string, error)
	IsSessionExists(ctx context.Context, clientID string) (bool, error)
	// RefreshSession extends the storage lifetime of a session while its
	// client is connected. Backends without per-key TTLs (memory, badger)
	// are no-ops; TTL backends (Redis) must never delete the key of a live
	// session, otherwise a later broker crash silently loses the session
	// (audit).
	RefreshSession(ctx context.Context, clientID string, ttl time.Duration) error
}

// MessageStore handles message persistence.
type MessageStore interface {
	SaveMessage(ctx context.Context, clientID string, msg *StoredMessage) error
	GetMessage(ctx context.Context, clientID, id string) (*StoredMessage, error)
	DeleteMessage(ctx context.Context, clientID, id string) error
	ListMessages(ctx context.Context, clientID string) ([]*StoredMessage, error)
	ClearMessages(ctx context.Context, clientID string) error
}

// RetainedStore handles retained message storage.
type RetainedStore interface {
	SaveRetained(ctx context.Context, topic string, qos uint8, payload []byte) error
	GetRetained(ctx context.Context, topic string) (*RetainedMessage, error)
	DeleteRetained(ctx context.Context, topic string) error
	MatchRetained(ctx context.Context, pattern string) ([]*RetainedMessage, error)
}
