package badger

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dgraph-io/badger/v4"
	"github.com/X1aSheng/shark-mqtt/store"
)

// Ensure interface compliance.
var _ store.SessionStore = (*SessionStore)(nil)

// SessionStore implements store.SessionStore using BadgerDB.
type SessionStore struct {
	db        *badger.DB
	keyPrefix string
}

// SessionStoreConfig holds configuration for Badger session store.
type SessionStoreConfig struct {
	DB        *badger.DB
	KeyPrefix string
}

// NewSessionStore creates a new BadgerDB-backed session store.
func NewSessionStore(cfg SessionStoreConfig) *SessionStore {
	prefix := cfg.KeyPrefix
	if prefix == "" {
		prefix = "mqtt:session:"
	}
	return &SessionStore{
		db:        cfg.DB,
		keyPrefix: prefix,
	}
}

func (s *SessionStore) sessionKey(clientID string) []byte {
	return []byte(s.keyPrefix + clientID)
}

func (s *SessionStore) SaveSession(ctx context.Context, clientID string, data *store.SessionData) error {
	serialized, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("serialize session: %w", err)
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(s.sessionKey(clientID), serialized)
	})
}

func (s *SessionStore) GetSession(ctx context.Context, clientID string) (*store.SessionData, error) {
	var data store.SessionData
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(s.sessionKey(clientID))
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return store.ErrSessionNotFound
			}
			return fmt.Errorf("get session: %w", err)
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &data)
		})
	})
	if err != nil {
		return nil, err
	}
	return &data, nil
}

func (s *SessionStore) DeleteSession(ctx context.Context, clientID string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		err := txn.Delete(s.sessionKey(clientID))
		if err == badger.ErrKeyNotFound {
			return nil
		}
		return err
	})
}

func (s *SessionStore) ListSessions(ctx context.Context) ([]string, error) {
	var clientIDs []string
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		prefix := []byte(s.keyPrefix)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			key := string(item.Key())
			clientID := strings.TrimPrefix(key, s.keyPrefix)
			clientIDs = append(clientIDs, clientID)
		}
		return nil
	})
	return clientIDs, err
}

func (s *SessionStore) IsSessionExists(ctx context.Context, clientID string) (bool, error) {
	err := s.db.View(func(txn *badger.Txn) error {
		_, err := txn.Get(s.sessionKey(clientID))
		return err
	})
	if err == badger.ErrKeyNotFound {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check session exists: %w", err)
	}
	return true, nil
}
