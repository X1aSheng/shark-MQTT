// Package memory provides in-memory implementations of store interfaces.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store"
)

// sessionStore implements store.SessionStore using in-memory maps.
type sessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*store.SessionData
}

// NewSessionStore creates a new in-memory session store.
func NewSessionStore() store.SessionStore {
	return &sessionStore{
		sessions: make(map[string]*store.SessionData),
	}
}

func (s *sessionStore) SaveSession(ctx context.Context, clientID string, data *store.SessionData) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[clientID] = data
	return nil
}

func (s *sessionStore) GetSession(ctx context.Context, clientID string) (*store.SessionData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.sessions[clientID]
	if !ok {
		return nil, store.ErrSessionNotFound
	}
	// Return a copy to prevent callers from modifying internal state without locking
	copied := *data
	return &copied, nil
}

func (s *sessionStore) DeleteSession(ctx context.Context, clientID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, clientID)
	return nil
}

func (s *sessionStore) ListSessions(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *sessionStore) IsSessionExists(ctx context.Context, clientID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.sessions[clientID]
	return ok, nil
}

// messageStore implements store.MessageStore using in-memory maps.
type messageStore struct {
	mu       sync.RWMutex
	messages map[string]map[string]*store.StoredMessage
}

// NewMessageStore creates a new in-memory message store.
func NewMessageStore() store.MessageStore {
	return &messageStore{
		messages: make(map[string]map[string]*store.StoredMessage),
	}
}

func (m *messageStore) SaveMessage(ctx context.Context, clientID string, msg *store.StoredMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.messages[clientID]; !ok {
		m.messages[clientID] = make(map[string]*store.StoredMessage)
	}
	msgCopy := *msg
	if msg.Payload != nil {
		msgCopy.Payload = make([]byte, len(msg.Payload))
		copy(msgCopy.Payload, msg.Payload)
	}
	m.messages[clientID][msg.ID] = &msgCopy
	return nil
}

func (m *messageStore) GetMessage(ctx context.Context, clientID, id string) (*store.StoredMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if msgs, ok := m.messages[clientID]; ok {
		if msg, ok := msgs[id]; ok {
			msgCopy := *msg
			return &msgCopy, nil
		}
	}
	return nil, store.ErrMessageNotFound
}

func (m *messageStore) DeleteMessage(ctx context.Context, clientID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if msgs, ok := m.messages[clientID]; ok {
		delete(msgs, id)
	}
	return nil
}

func (m *messageStore) ListMessages(ctx context.Context, clientID string) ([]*store.StoredMessage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	msgs, ok := m.messages[clientID]
	if !ok {
		return nil, nil
	}
	result := make([]*store.StoredMessage, 0, len(msgs))
	for _, msg := range msgs {
		msgCopy := *msg
		result = append(result, &msgCopy)
	}
	return result, nil
}

func (m *messageStore) ClearMessages(ctx context.Context, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.messages, clientID)
	return nil
}

// retainedStore implements store.RetainedStore using in-memory maps.
type retainedStore struct {
	mu       sync.RWMutex
	messages map[string]*store.RetainedMessage
}

// NewRetainedStore creates a new in-memory retained message store.
func NewRetainedStore() store.RetainedStore {
	return &retainedStore{
		messages: make(map[string]*store.RetainedMessage),
	}
}

func (r *retainedStore) SaveRetained(ctx context.Context, topic string, qos uint8, payload []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(payload) == 0 {
		delete(r.messages, topic)
		return nil
	}
	payloadCopy := make([]byte, len(payload))
	copy(payloadCopy, payload)
	r.messages[topic] = &store.RetainedMessage{
		Topic:     topic,
		QoS:       qos,
		Payload:   payloadCopy,
		Timestamp: time.Now(),
	}
	return nil
}

func (r *retainedStore) GetRetained(ctx context.Context, topic string) (*store.RetainedMessage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	msg, ok := r.messages[topic]
	if !ok {
		return nil, store.ErrRetainedNotFound
	}
	msgCopy := *msg
	return &msgCopy, nil
}

func (r *retainedStore) DeleteRetained(ctx context.Context, topic string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.messages, topic)
	return nil
}

func (r *retainedStore) MatchRetained(ctx context.Context, pattern string) ([]*store.RetainedMessage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var result []*store.RetainedMessage
	for topic, msg := range r.messages {
		if protocol.MatchTopic(pattern, topic) {
			msgCopy := *msg
			result = append(result, &msgCopy)
		}
	}
	return result, nil
}
