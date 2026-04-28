package badger

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store"
	"github.com/dgraph-io/badger/v4"
)

// Ensure interface compliance.
var _ store.RetainedStore = (*RetainedStore)(nil)

// RetainedStore implements store.RetainedStore using BadgerDB.
type RetainedStore struct {
	db        *badger.DB
	keyPrefix string
}

// RetainedStoreConfig holds configuration for Badger retained store.
type RetainedStoreConfig struct {
	DB        *badger.DB
	KeyPrefix string
}

// NewRetainedStore creates a new BadgerDB-backed retained message store.
func NewRetainedStore(cfg RetainedStoreConfig) *RetainedStore {
	prefix := cfg.KeyPrefix
	if prefix == "" {
		prefix = "mqtt:retained:"
	}
	return &RetainedStore{
		db:        cfg.DB,
		keyPrefix: prefix,
	}
}

func (s *RetainedStore) topicKey(topic string) []byte {
	return []byte(s.keyPrefix + topic)
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
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(s.topicKey(topic), serialized)
	})
}

func (s *RetainedStore) GetRetained(ctx context.Context, topic string) (*store.RetainedMessage, error) {
	var retained store.RetainedMessage
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(s.topicKey(topic))
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return store.ErrRetainedNotFound
			}
			return fmt.Errorf("get retained: %w", err)
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &retained)
		})
	})
	if err != nil {
		return nil, err
	}
	return &retained, nil
}

func (s *RetainedStore) DeleteRetained(ctx context.Context, topic string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		err := txn.Delete(s.topicKey(topic))
		if err == badger.ErrKeyNotFound {
			return nil
		}
		return err
	})
}

func (s *RetainedStore) MatchRetained(ctx context.Context, pattern string) ([]*store.RetainedMessage, error) {
	var messages []*store.RetainedMessage
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		prefix := []byte(s.keyPrefix)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			key := string(item.Key())
			topic := strings.TrimPrefix(key, s.keyPrefix)

			if protocol.MatchTopic(pattern, topic) {
				val, err := item.ValueCopy(nil)
				if err != nil {
					continue
				}
				var msg store.RetainedMessage
				if err := json.Unmarshal(val, &msg); err == nil {
					messages = append(messages, &msg)
				}
			}
		}
		return nil
	})
	return messages, err
}
