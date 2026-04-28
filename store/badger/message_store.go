package badger

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync/atomic"

	"github.com/dgraph-io/badger/v4"
	"github.com/X1aSheng/shark-mqtt/store"
)

// Ensure interface compliance.
var _ store.MessageStore = (*MessageStore)(nil)

// MessageStore implements store.MessageStore using BadgerDB.
type MessageStore struct {
	db        *badger.DB
	keyPrefix string
}

// MessageStoreConfig holds configuration for Badger message store.
type MessageStoreConfig struct {
	DB        *badger.DB
	KeyPrefix string
}

// NewMessageStore creates a new BadgerDB-backed message store.
func NewMessageStore(cfg MessageStoreConfig) *MessageStore {
	prefix := cfg.KeyPrefix
	if prefix == "" {
		prefix = "mqtt:message:"
	}
	return &MessageStore{
		db:        cfg.DB,
		keyPrefix: prefix,
	}
}

func (s *MessageStore) messageKey(clientID, id string) []byte {
	return []byte(s.keyPrefix + clientID + ":" + id)
}

func (s *MessageStore) clientPrefix(clientID string) []byte {
	return []byte(s.keyPrefix + clientID + ":")
}

func (s *MessageStore) SaveMessage(ctx context.Context, clientID string, msg *store.StoredMessage) error {
	serialized, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("serialize message: %w", err)
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(s.messageKey(clientID, msg.ID), serialized)
	})
}

func (s *MessageStore) GetMessage(ctx context.Context, clientID, id string) (*store.StoredMessage, error) {
	var msg store.StoredMessage
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(s.messageKey(clientID, id))
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return store.ErrMessageNotFound
			}
			return fmt.Errorf("get message: %w", err)
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &msg)
		})
	})
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

func (s *MessageStore) DeleteMessage(ctx context.Context, clientID, id string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		err := txn.Delete(s.messageKey(clientID, id))
		if err == badger.ErrKeyNotFound {
			return nil
		}
		return err
	})
}

func (s *MessageStore) ListMessages(ctx context.Context, clientID string) ([]*store.StoredMessage, error) {
	var messages []*store.StoredMessage
	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		prefix := s.clientPrefix(clientID)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			val, err := item.ValueCopy(nil)
			if err != nil {
				continue
			}
			var msg store.StoredMessage
			if err := json.Unmarshal(val, &msg); err == nil {
				messages = append(messages, &msg)
			}
		}
		return nil
	})
	return messages, err
}

func (s *MessageStore) ClearMessages(ctx context.Context, clientID string) error {
	// Collect keys in a read-only View transaction, then delete in an Update.
	prefix := s.clientPrefix(clientID)
	var keys [][]byte

	err := s.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			key := it.Item().KeyCopy(nil)
			keys = append(keys, key)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan messages: %w", err)
	}

	return s.db.Update(func(txn *badger.Txn) error {
		for _, key := range keys {
			if err := txn.Delete(key); err != nil {
				return fmt.Errorf("delete message: %w", err)
			}
		}
		return nil
	})
}

// GenerateMessageID generates a unique message ID for the store.
func (s *MessageStore) GenerateMessageID() string {
	return strconv.FormatUint(uint64(generateID()), 10)
}

var messageIDCounter uint64

func generateID() uint64 {
	return atomic.AddUint64(&messageIDCounter, 1)
}
