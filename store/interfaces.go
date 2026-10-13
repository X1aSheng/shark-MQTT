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
	// SaveRetained stores msg as the retained message for msg.Topic. A
	// zero-length payload deletes the retained message instead
	// ([MQTT-3.3.1-7] / [MQTT-3.3.1-8]).
	//
	// The whole record is passed rather than (topic, qos, payload) so the
	// backend cannot invent or drop fields: an MQTT 5 retained message has to
	// remember its relay properties and its expiry deadline, and a backend that
	// rebuilt the record from three arguments would silently lose them.
	SaveRetained(ctx context.Context, msg *RetainedMessage) error
	GetRetained(ctx context.Context, topic string) (*RetainedMessage, error)
	DeleteRetained(ctx context.Context, topic string) error
	MatchRetained(ctx context.Context, pattern string) ([]*RetainedMessage, error)
}
