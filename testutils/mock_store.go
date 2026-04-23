// Package testutils provides mock implementations for testing shark-mqtt components.
package testutils

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/X1aSheng/shark-mqtt/store"
)

// MockSessionStore implements store.SessionStore.
type MockSessionStore struct {
	data    map[string]*store.SessionData
	mu      sync.Mutex
	latency time.Duration
}

var _ store.SessionStore = (*MockSessionStore)(nil)

// NewMockSessionStore creates a new mock session store.
func NewMockSessionStore() *MockSessionStore {
	return &MockSessionStore{
		data: make(map[string]*store.SessionData),
	}
}

// SetLatency simulates network/storage delay.
func (s *MockSessionStore) SetLatency(d time.Duration) {
	s.latency = d
}

func (s *MockSessionStore) SaveSession(ctx context.Context, clientID string, data *store.SessionData) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	time.Sleep(s.latency)
	s.data[clientID] = data
	return nil
}

func (s *MockSessionStore) GetSession(ctx context.Context, clientID string) (*store.SessionData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	time.Sleep(s.latency)
	if data, ok := s.data[clientID]; ok {
		return data, nil
	}
	return nil, nil
}

func (s *MockSessionStore) DeleteSession(ctx context.Context, clientID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	time.Sleep(s.latency)
	delete(s.data, clientID)
	return nil
}

func (s *MockSessionStore) ListSessions(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	time.Sleep(s.latency)
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys, nil
}

func (s *MockSessionStore) IsSessionExists(ctx context.Context, clientID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	time.Sleep(s.latency)
	_, ok := s.data[clientID]
	return ok, nil
}

// MockMessageStore implements store.MessageStore.
type MockMessageStore struct {
	data    map[string]map[string]*store.StoredMessage
	mu      sync.Mutex
	latency time.Duration
}

var _ store.MessageStore = (*MockMessageStore)(nil)

// NewMockMessageStore creates a new mock message store.
func NewMockMessageStore() *MockMessageStore {
	return &MockMessageStore{
		data: make(map[string]map[string]*store.StoredMessage),
	}
}

// SetLatency simulates network/storage delay.
func (s *MockMessageStore) SetLatency(d time.Duration) {
	s.latency = d
}

func (s *MockMessageStore) SaveMessage(ctx context.Context, clientID string, msg *store.StoredMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	time.Sleep(s.latency)
	if s.data[clientID] == nil {
		s.data[clientID] = make(map[string]*store.StoredMessage)
	}
	s.data[clientID][msg.ID] = msg
	return nil
}

func (s *MockMessageStore) GetMessage(ctx context.Context, clientID, id string) (*store.StoredMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	time.Sleep(s.latency)
	if clientMsgs, ok := s.data[clientID]; ok {
		if msg, ok := clientMsgs[id]; ok {
			return msg, nil
		}
	}
	return nil, nil
}

func (s *MockMessageStore) DeleteMessage(ctx context.Context, clientID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	time.Sleep(s.latency)
	if clientMsgs, ok := s.data[clientID]; ok {
		delete(clientMsgs, id)
	}
	return nil
}

func (s *MockMessageStore) ListMessages(ctx context.Context, clientID string) ([]*store.StoredMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	time.Sleep(s.latency)
	if clientMsgs, ok := s.data[clientID]; ok {
		msgs := make([]*store.StoredMessage, 0, len(clientMsgs))
		for _, msg := range clientMsgs {
			msgs = append(msgs, msg)
		}
		return msgs, nil
	}
	return nil, nil
}

func (s *MockMessageStore) ClearMessages(ctx context.Context, clientID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	time.Sleep(s.latency)
	delete(s.data, clientID)
	return nil
}

// MockRetainedStore implements store.RetainedStore.
type MockRetainedStore struct {
	data    map[string]*store.RetainedMessage
	mu      sync.Mutex
	latency time.Duration
}

var _ store.RetainedStore = (*MockRetainedStore)(nil)

// NewMockRetainedStore creates a new mock retained store.
func NewMockRetainedStore() *MockRetainedStore {
	return &MockRetainedStore{
		data: make(map[string]*store.RetainedMessage),
	}
}

// SetLatency simulates network/storage delay.
func (s *MockRetainedStore) SetLatency(d time.Duration) {
	s.latency = d
}

func (s *MockRetainedStore) SaveRetained(ctx context.Context, topic string, qos uint8, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	time.Sleep(s.latency)
	s.data[topic] = &store.RetainedMessage{
		Topic:   topic,
		QoS:     qos,
		Payload: payload,
	}
	return nil
}

func (s *MockRetainedStore) GetRetained(ctx context.Context, topic string) (*store.RetainedMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	time.Sleep(s.latency)
	if msg, ok := s.data[topic]; ok {
		return msg, nil
	}
	return nil, nil
}

func (s *MockRetainedStore) DeleteRetained(ctx context.Context, topic string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	time.Sleep(s.latency)
	delete(s.data, topic)
	return nil
}

func (s *MockRetainedStore) MatchRetained(ctx context.Context, pattern string) ([]*store.RetainedMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	time.Sleep(s.latency)
	var matched []*store.RetainedMessage
	for topic, msg := range s.data {
		if topicMatchesPattern(topic, pattern) {
			matched = append(matched, msg)
		}
	}
	return matched, nil
}

func topicMatchesPattern(topic, pattern string) bool {
	topicParts := strings.Split(topic, "/")
	patternParts := strings.Split(pattern, "/")

	if len(patternParts) == 1 && patternParts[0] == "#" {
		return true
	}

	for i, part := range patternParts {
		if part == "#" {
			return true
		}
		if i >= len(topicParts) {
			return false
		}
		if part == "+" {
			continue
		}
		if part != topicParts[i] {
			return false
		}
	}

	return len(topicParts) == len(patternParts)
}
