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
	if data == nil {
		delete(s.sessions, clientID)
		return nil
	}
	// Store a copy to prevent callers from modifying internal state
	copied := *data
	if data.Inflight != nil {
		copied.Inflight = make(map[uint16]*store.InflightMessage, len(data.Inflight))
		for k, v := range data.Inflight {
			msgCopy := *v
			copied.Inflight[k] = &msgCopy
		}
	}
	s.sessions[clientID] = &copied
	return nil
}

func (s *sessionStore) GetSession(ctx context.Context, clientID string) (*store.SessionData, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	data, ok := s.sessions[clientID]
	if !ok {
		return nil, store.ErrSessionNotFound
	}
	// Return a deep copy to prevent callers from modifying internal state
	copied := *data
	if data.Inflight != nil {
		copied.Inflight = make(map[uint16]*store.InflightMessage, len(data.Inflight))
		for k, v := range data.Inflight {
			msgCopy := *v
			msgCopy.Payload = make([]byte, len(v.Payload))
			copy(msgCopy.Payload, v.Payload)
			copied.Inflight[k] = &msgCopy
		}
	}
	if data.Subscriptions != nil {
		copied.Subscriptions = make([]store.Subscription, len(data.Subscriptions))
		copy(copied.Subscriptions, data.Subscriptions)
	}
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
			if msg.Payload != nil {
				msgCopy.Payload = make([]byte, len(msg.Payload))
				copy(msgCopy.Payload, msg.Payload)
			}
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
		if msg.Payload != nil {
			msgCopy.Payload = make([]byte, len(msg.Payload))
			copy(msgCopy.Payload, msg.Payload)
		}
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

// retainedTrie is a prefix tree for retained message topics to enable
// O(log n) matching with wildcard support instead of linear scan.
type retainedTrie struct {
	children map[string]*retainedTrie
	msg      *store.RetainedMessage // non-nil only at leaf nodes
}

// retainedStore implements store.RetainedStore using in-memory maps
// with a trie index for efficient pattern matching.
type retainedStore struct {
	mu       sync.RWMutex
	messages map[string]*store.RetainedMessage
	index    *retainedTrie
}

// NewRetainedStore creates a new in-memory retained message store.
func NewRetainedStore() store.RetainedStore {
	return &retainedStore{
		messages: make(map[string]*store.RetainedMessage),
		index:    &retainedTrie{children: make(map[string]*retainedTrie)},
	}
}

func (r *retainedStore) SaveRetained(ctx context.Context, topic string, qos uint8, payload []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(payload) == 0 {
		delete(r.messages, topic)
		r.removeFromIndex(topic)
		return nil
	}
	payloadCopy := make([]byte, len(payload))
	copy(payloadCopy, payload)
	msg := &store.RetainedMessage{
		Topic:     topic,
		QoS:       qos,
		Payload:   payloadCopy,
		Timestamp: time.Now(),
	}
	r.messages[topic] = msg
	r.addToIndex(topic, msg)
	return nil
}

func (r *retainedStore) addToIndex(topic string, msg *store.RetainedMessage) {
	parts := protocol.SplitTopic(topic)
	node := r.index
	for _, part := range parts {
		if node.children[part] == nil {
			node.children[part] = &retainedTrie{children: make(map[string]*retainedTrie)}
		}
		node = node.children[part]
	}
	node.msg = msg
}

func (r *retainedStore) removeFromIndex(topic string) {
	parts := protocol.SplitTopic(topic)
	r.index.remove(parts, 0)
}

func (n *retainedTrie) remove(parts []string, depth int) bool {
	if depth == len(parts) {
		n.msg = nil
		return len(n.children) == 0
	}
	part := parts[depth]
	child, ok := n.children[part]
	if !ok {
		return false
	}
	if child.remove(parts, depth+1) {
		delete(n.children, part)
	}
	return n.msg == nil && len(n.children) == 0
}

func (r *retainedStore) GetRetained(ctx context.Context, topic string) (*store.RetainedMessage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	msg, ok := r.messages[topic]
	if !ok {
		return nil, store.ErrRetainedNotFound
	}
	msgCopy := *msg
	if msg.Payload != nil {
		msgCopy.Payload = make([]byte, len(msg.Payload))
		copy(msgCopy.Payload, msg.Payload)
	}
	return &msgCopy, nil
}

func (r *retainedStore) DeleteRetained(ctx context.Context, topic string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.messages, topic)
	r.removeFromIndex(topic)
	return nil
}

func (r *retainedStore) MatchRetained(ctx context.Context, pattern string) ([]*store.RetainedMessage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.messages) == 0 {
		return nil, nil
	}

	parts := protocol.SplitTopic(pattern)
	var result []*store.RetainedMessage
	visited := make(map[string]struct{})
	r.index.match(parts, 0, &result, visited)
	return result, nil
}

func (n *retainedTrie) match(parts []string, depth int, result *[]*store.RetainedMessage, visited map[string]struct{}) {
	if depth == len(parts) {
		if n.msg != nil {
			if _, ok := visited[n.msg.Topic]; !ok {
				visited[n.msg.Topic] = struct{}{}
				msgCopy := *n.msg
				if n.msg.Payload != nil {
					msgCopy.Payload = make([]byte, len(n.msg.Payload))
					copy(msgCopy.Payload, n.msg.Payload)
				}
				*result = append(*result, &msgCopy)
			}
		}
		return
	}

	part := parts[depth]

	if part == "#" {
		n.collectAll(result, visited)
		return
	}

	if part == "+" {
		for _, child := range n.children {
			child.match(parts, depth+1, result, visited)
		}
		return
	}

	if child, ok := n.children[part]; ok {
		child.match(parts, depth+1, result, visited)
	}
}

func (n *retainedTrie) collectAll(result *[]*store.RetainedMessage, visited map[string]struct{}) {
	if n.msg != nil {
		if _, ok := visited[n.msg.Topic]; !ok {
			visited[n.msg.Topic] = struct{}{}
			msgCopy := *n.msg
			if n.msg.Payload != nil {
				msgCopy.Payload = make([]byte, len(n.msg.Payload))
				copy(msgCopy.Payload, n.msg.Payload)
			}
			*result = append(*result, &msgCopy)
		}
	}
	for _, child := range n.children {
		child.collectAll(result, visited)
	}
}
