package broker

import (
	"net"
	"sync"
	"time"

	"github.com/X1aSheng/shark-mqtt/store"
)

// Registry manages persistent MQTT sessions across connections.
// It tracks sessions by clientID and handles session lifecycle including
// kicks, replacements, and persistence.
type Registry struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	store    store.SessionStore
}

// NewSessionRegistry creates a new session registry.
func NewSessionRegistry(sessionStore store.SessionStore) *Registry {
	return &Registry{
		sessions: make(map[string]*Session),
		store:    sessionStore,
	}
}

// Register creates a new session or replaces an existing one.
func (r *Registry) Register(clientID string, conn net.Conn) *Session {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Kick existing session if present
	if existing, ok := r.sessions[clientID]; ok {
		existing.SetCloseReason(ReasonReplacedByNewConnection)
		delete(r.sessions, clientID)
	}

	sess := &Session{
		ClientID:      clientID,
		ConnectedAt:   nowFunc(),
		LastActivity:  nowFunc(),
		Subscriptions: make(map[string]uint8),
		Inflight:      make(map[uint16]*InflightMsg),
		packetIDSeq:   1,
		state:         StateConnected,
	}

	r.sessions[clientID] = sess
	return sess
}

// Unregister removes a session from the registry.
func (r *Registry) Unregister(clientID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, clientID)
}

// Kick forcefully closes and removes a session.
func (r *Registry) Kick(clientID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sess, ok := r.sessions[clientID]; ok {
		sess.SetCloseReason(ReasonServerKick)
		delete(r.sessions, clientID)
	}
}

// ReplaceSession replaces an existing session while preserving state.
func (r *Registry) ReplaceSession(clientID string, conn net.Conn) *Session {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, ok := r.sessions[clientID]
	if !ok {
		// No existing session, create new one
		sess := &Session{
			ClientID:      clientID,
			ConnectedAt:   nowFunc(),
			LastActivity:  nowFunc(),
			Subscriptions: make(map[string]uint8),
			Inflight:      make(map[uint16]*InflightMsg),
			packetIDSeq:   1,
			state:         StateConnected,
		}
		r.sessions[clientID] = sess
		return sess
	}

	// Create new session preserving subscriptions and inflight
	sess := &Session{
		ClientID:      clientID,
		ConnectedAt:   nowFunc(),
		LastActivity:  nowFunc(),
		Subscriptions: make(map[string]uint8),
		Inflight:      make(map[uint16]*InflightMsg),
		packetIDSeq:   1,
		state:         StateConnected,
	}

	// Copy subscriptions
	for topic, qos := range existing.Subscriptions {
		sess.Subscriptions[topic] = qos
	}

	// Copy inflight messages
	for id, msg := range existing.Inflight {
		payloadCopy := make([]byte, len(msg.Payload))
		copy(payloadCopy, msg.Payload)
		sess.Inflight[id] = &InflightMsg{
			PacketID: msg.PacketID,
			QoS:      msg.QoS,
			Topic:    msg.Topic,
			Payload:  payloadCopy,
			Retain:   msg.Retain,
			AckType:  msg.AckType,
		}
	}

	// Mark old session as replaced
	existing.SetCloseReason(ReasonReplacedByNewConnection)

	r.sessions[clientID] = sess
	return sess
}

// SessionByClientID returns a session by client ID.
func (r *Registry) SessionByClientID(clientID string) (*Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sess, ok := r.sessions[clientID]
	return sess, ok
}

// CloseAll closes all sessions with the given reason.
func (r *Registry) CloseAll(reason CloseReason) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for clientID, sess := range r.sessions {
		sess.SetCloseReason(reason)
		delete(r.sessions, clientID)
	}
}

// Count returns the number of active sessions.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

// ListSessions returns all active session client IDs.
func (r *Registry) ListSessions() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.sessions))
	for id := range r.sessions {
		ids = append(ids, id)
	}
	return ids
}

// Save persists a session to the store.
func (r *Registry) Save(sess *Session) error {
	if r.store == nil {
		return store.ErrStoreUnavailable
	}
	return sess.Save(nil, r.store)
}

// Load restores a session from the store.
func (r *Registry) Load(clientID string) (*Session, error) {
	if r.store == nil {
		return nil, store.ErrStoreUnavailable
	}
	data, err := r.store.GetSession(nil, clientID)
	if err != nil {
		return nil, err
	}

	sess := &Session{
		ClientID:      clientID,
		IsClean:       data.IsClean,
		ConnectedAt:   nowFunc(),
		LastActivity:  nowFunc(),
		Subscriptions: make(map[string]uint8),
		Inflight:      make(map[uint16]*InflightMsg),
		packetIDSeq:   1,
		state:         StateConnected,
	}

	for _, sub := range data.Subscriptions {
		sess.Subscriptions[sub.Topic] = sub.QoS
	}

	for id, msg := range data.Inflight {
		payloadCopy := make([]byte, len(msg.Payload))
		copy(payloadCopy, msg.Payload)
		sess.Inflight[id] = &InflightMsg{
			PacketID: msg.PacketID,
			QoS:      msg.QoS,
			Topic:    msg.Topic,
			Payload:  payloadCopy,
			Retain:   msg.Retain,
		}
	}

	r.mu.Lock()
	r.sessions[clientID] = sess
	r.mu.Unlock()

	return sess, nil
}

var nowFunc = func() time.Time {
	return time.Now()
}
