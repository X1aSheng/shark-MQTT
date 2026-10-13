// Package memory provides in-memory implementations of store interfaces.
package memory

import (
	"context"
	"strings"
	"sync"
	"time"

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

// copyInflightMessage deep-copies one stored message. Every reference-typed
// field has to be cloned here, not just the struct: the caller keeps using the
// value it handed in, so sharing a payload, a correlation blob or the
// user-property backing array would let a later mutation reach into the store,
// and the store's contents leak back out through GetSession. Keeping the field
// list in one place is also what stops the next added field from being copied
// by SaveSession and forgotten by GetSession.
func copyInflightMessage(v *store.InflightMessage) *store.InflightMessage {
	msgCopy := *v
	if v.Payload != nil {
		msgCopy.Payload = make([]byte, len(v.Payload))
		copy(msgCopy.Payload, v.Payload)
	}
	msgCopy.Properties = copyMessageProperties(v.Properties)
	return &msgCopy
}

// copyMessageProperties deep-copies the store-neutral MQTT 5 property set.
//
// The pointer and slice fields must never be shared with the caller: the store
// record outlives the decoded packet it was built from, so aliasing them would
// let a later packet mutation rewrite a stored message (or the reverse). Both
// the inflight and the retained paths need this, and having one helper keeps
// them from drifting apart when a property kind is added.
func copyMessageProperties(p store.MessageProperties) store.MessageProperties {
	out := p
	if p.PayloadFormat != nil {
		pf := *p.PayloadFormat
		out.PayloadFormat = &pf
	}
	if p.CorrelationData != nil {
		out.CorrelationData = make([]byte, len(p.CorrelationData))
		copy(out.CorrelationData, p.CorrelationData)
	}
	if p.UserProperties != nil {
		up := make([]store.UserProperty, len(p.UserProperties))
		copy(up, p.UserProperties)
		out.UserProperties = up
	}
	if p.SubscriptionIdentifier != nil {
		sid := *p.SubscriptionIdentifier
		out.SubscriptionIdentifier = &sid
	}
	return out
}

// copyRetainedMessage deep-copies a retained record, payload and MQTT 5 relay
// data included.
func copyRetainedMessage(v *store.RetainedMessage) *store.RetainedMessage {
	out := *v
	if v.Payload != nil {
		out.Payload = make([]byte, len(v.Payload))
		copy(out.Payload, v.Payload)
	}
	out.Properties = copyMessageProperties(v.Properties)
	return &out
}

// copyInflightMap deep-copies a whole inflight map, preserving nil.
func copyInflightMap(m map[uint16]*store.InflightMessage) map[uint16]*store.InflightMessage {
	if m == nil {
		return nil
	}
	out := make(map[uint16]*store.InflightMessage, len(m))
	for k, v := range m {
		out[k] = copyInflightMessage(v)
	}
	return out
}

func (s *sessionStore) SaveSession(ctx context.Context, clientID string, data *store.SessionData) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if data == nil {
		delete(s.sessions, clientID)
		return nil
	}
	// Store a deep copy so callers cannot modify internal state after saving
	// (mirrors GetSession's defensive copying; previously only the Inflight
	// message structs were copied, sharing their payload backing arrays).
	copied := *data
	copied.Inflight = copyInflightMap(data.Inflight)
	copied.InboundQoS2 = copyInflightMap(data.InboundQoS2)
	if data.Subscriptions != nil {
		copied.Subscriptions = make([]store.Subscription, len(data.Subscriptions))
		copy(copied.Subscriptions, data.Subscriptions)
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
	copied.Inflight = copyInflightMap(data.Inflight)
	copied.InboundQoS2 = copyInflightMap(data.InboundQoS2)
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

// RefreshSession is a no-op for the in-memory backend, which has no TTLs.
func (s *sessionStore) RefreshSession(ctx context.Context, clientID string, ttl time.Duration) error {
	return nil
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

func (r *retainedStore) SaveRetained(ctx context.Context, msg *store.RetainedMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// [MQTT-3.3.1-7] / [MQTT-3.3.1-8]: a zero-length payload deletes the
	// retained message for the topic rather than storing an empty one.
	if len(msg.Payload) == 0 {
		delete(r.messages, msg.Topic)
		r.removeFromIndex(msg.Topic)
		return nil
	}
	stored := copyRetainedMessage(msg)
	if stored.Timestamp.IsZero() {
		// Still the store's job when the caller did not stamp it: retained TTLs
		// are measured from the moment the message was stored.
		stored.Timestamp = time.Now()
	}
	r.messages[msg.Topic] = stored
	r.addToIndex(msg.Topic, stored)
	return nil
}

func (r *retainedStore) addToIndex(topic string, msg *store.RetainedMessage) {
	parts := strings.Split(topic, "/")
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
	parts := strings.Split(topic, "/")
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
	return copyRetainedMessage(msg), nil
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

	parts := strings.Split(pattern, "/")
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
				*result = append(*result, copyRetainedMessage(n.msg))
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
			*result = append(*result, copyRetainedMessage(n.msg))
		}
	}
	for _, child := range n.children {
		child.collectAll(result, visited)
	}
}
