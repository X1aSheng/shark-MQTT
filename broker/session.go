// Package session manages MQTT client sessions.
package broker

import (
	"context"
	"sync"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store"
)

// Session represents an active MQTT client session.
type Session struct {
	ClientID      string
	Username      string
	IsClean       bool
	ProtocolVer   uint8
	KeepAlive     uint16
	ConnectedAt   time.Time
	LastActivity  time.Time
	Subscriptions map[string]uint8 // topic -> qos
	Inflight      map[uint16]*InflightMsg
	packetIDSeq   uint16
	ReceiveMax    uint16
	TopicAliasMax uint16
	mu            sync.RWMutex

	// ExpiryInterval is the session expiry interval in seconds (MQTT 5.0 §3.1.2.11.2).
	// Server caps this at its configured max. 0 = session expires immediately on disconnect.
	ExpiryInterval uint32

	// State management
	state       State
	closeReason CloseReason

	// Stats tracking
	stats Stats
}

// InflightMsg tracks an in-flight QoS message.
type InflightMsg struct {
	PacketID uint16
	QoS      uint8
	Topic    string
	Payload  []byte
	Retain   bool
	SentAt   time.Time
	AckType  byte // PUBACK, PUBREC, PUBREL, PUBCOMP
}

// Manager manages all client sessions.
type Manager struct {
	sessions map[string]*Session
	store    store.SessionStore
	mu       sync.RWMutex
	kickCB   KickCallback
}

// KickCallback is called when an existing session is kicked by a new connection
// with the same ClientID.
type KickCallback func(oldSession *Session)

// ManagerOpt configures a Manager.
type ManagerOpt func(*Manager)

// WithKickCallback sets the callback for when a session is kicked.
func WithKickCallback(cb KickCallback) ManagerOpt {
	return func(m *Manager) {
		m.kickCB = cb
	}
}

// NewManager creates a new session manager.
func NewManager(sessionStore store.SessionStore, opts ...ManagerOpt) *Manager {
	m := &Manager{
		sessions: make(map[string]*Session),
		store:    sessionStore,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// CreateSession creates a new session for a client.
func (m *Manager) CreateSession(clientID string, connectPkt *protocol.ConnectPacket, isResuming bool) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for existing session - kick if same ClientID reconnects
	existing, exists := m.sessions[clientID]
	if exists {
		if !connectPkt.Flags.CleanSession && isResuming {
			existing.mu.Lock()
			existing.KeepAlive = connectPkt.KeepAlive
			existing.Username = connectPkt.Username
			existing.ProtocolVer = connectPkt.ProtocolVersion
			existing.LastActivity = time.Now()
			existing.ConnectedAt = time.Now()
			existing.mu.Unlock()
			return existing
		}
		// Kick old session (duplicate ClientID)
		existing.SetCloseReason(ReasonReplacedByNewConnection)
		if m.kickCB != nil {
			m.kickCB(existing)
		}
		delete(m.sessions, clientID)
	}

	sess := &Session{
		ClientID:      clientID,
		Username:      connectPkt.Username,
		IsClean:       connectPkt.Flags.CleanSession,
		ProtocolVer:   connectPkt.ProtocolVersion,
		KeepAlive:     connectPkt.KeepAlive,
		ConnectedAt:   time.Now(),
		LastActivity:  time.Now(),
		Subscriptions: make(map[string]uint8),
		Inflight:      make(map[uint16]*InflightMsg),
		packetIDSeq:   1,
		ReceiveMax:    65535,
		state:         StateConnected,
	}

	sess.stats.ConnectCount++
	sess.stats.LastConnectedAt = sess.ConnectedAt
	sess.stats.Subscriptions = 0
	sess.stats.InflightCount = 0

	m.sessions[clientID] = sess
	return sess
}

// GetSession returns a session by client ID.
func (m *Manager) GetSession(clientID string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sess, ok := m.sessions[clientID]
	return sess, ok
}

// RemoveSession removes and optionally persists session cleanup.
func (m *Manager) RemoveSession(clientID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[clientID]
	if ok {
		if sess.State() == StateConnected {
			sess.SetState(StateDisconnecting)
		}
	}
	delete(m.sessions, clientID)
}

// ListSessions returns all active session client IDs.
func (m *Manager) ListSessions() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	return ids
}

// SessionExists checks if a session exists in memory.
func (m *Manager) SessionExists(clientID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.sessions[clientID]
	return ok
}

// --- Session methods ---

// UpdateActivity updates the last activity timestamp.
func (s *Session) UpdateActivity() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.LastActivity = time.Now()
}

// IsExpired checks if the session has exceeded its keep-alive timeout.
func (s *Session) IsExpired() bool {
	if s.KeepAlive == 0 {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	timeout := time.Duration(s.KeepAlive) * time.Second * 3 / 2
	return time.Since(s.LastActivity) > timeout
}

// AddSubscription adds or updates a subscription.
func (s *Session) AddSubscription(topic string, qos uint8) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Subscriptions[topic] = qos
}

// RemoveSubscription removes a subscription.
func (s *Session) RemoveSubscription(topic string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Subscriptions, topic)
}

// MatchesSubscription checks if a topic matches any of the session's subscriptions.
func (s *Session) MatchesSubscription(topic string) (bool, uint8) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for pattern, qos := range s.Subscriptions {
		if protocol.MatchTopic(pattern, topic) {
			return true, qos
		}
	}
	return false, 0
}

// NextPacketID generates the next packet ID, skipping IDs already in use.
func (s *Session) NextPacketID() uint16 {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Try up to 65535 IDs to find an unused one
	for attempts := 0; attempts < 65535; attempts++ {
		id := s.packetIDSeq
		s.packetIDSeq++
		if s.packetIDSeq == 0 {
			s.packetIDSeq = 1
		}
		if _, inUse := s.Inflight[id]; !inUse {
			return id
		}
	}
	// All IDs are in use (extremely unlikely); return the next one anyway
	id := s.packetIDSeq
	s.packetIDSeq++
	if s.packetIDSeq == 0 {
		s.packetIDSeq = 1
	}
	return id
}

// AddInflight adds an in-flight message.
func (s *Session) AddInflight(msg *InflightMsg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Inflight[msg.PacketID] = msg
}

// RemoveInflight removes an in-flight message.
func (s *Session) RemoveInflight(packetID uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Inflight, packetID)
}

// GetInflight returns an in-flight message.
func (s *Session) GetInflight(packetID uint16) (*InflightMsg, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msg, ok := s.Inflight[packetID]
	return msg, ok
}

// Save persists the session to the store.
func (s *Session) Save(ctx context.Context, sessionStore store.SessionStore) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data := &store.SessionData{
		ClientID:       s.ClientID,
		Username:       s.Username,
		IsClean:        s.IsClean,
		KeepAlive:      s.KeepAlive,
		ProtocolVer:    s.ProtocolVer,
		ExpiryInterval: s.ExpiryInterval,
	}

	subscriptions := make([]store.Subscription, 0, len(s.Subscriptions))
	for topic, qos := range s.Subscriptions {
		subscriptions = append(subscriptions, store.Subscription{Topic: topic, QoS: qos})
	}
	data.Subscriptions = subscriptions

	inflight := make(map[uint16]*store.InflightMessage)
	for id, msg := range s.Inflight {
		payloadCopy := make([]byte, len(msg.Payload))
		copy(payloadCopy, msg.Payload)
		inflight[id] = &store.InflightMessage{
			PacketID: msg.PacketID,
			QoS:      msg.QoS,
			Topic:    msg.Topic,
			Payload:  payloadCopy,
			Retain:   msg.Retain,
		}
	}
	data.Inflight = inflight

	return sessionStore.SaveSession(ctx, s.ClientID, data)
}

// Restore restores a session from the store.
func (m *Manager) Restore(ctx context.Context, clientID string) (*Session, error) {
	if m.store == nil {
		return nil, store.ErrStoreUnavailable
	}

	data, err := m.store.GetSession(ctx, clientID)
	if err != nil {
		return nil, err
	}

	sess := &Session{
		ClientID:       data.ClientID,
		Username:       data.Username,
		IsClean:        data.IsClean,
		KeepAlive:      data.KeepAlive,
		ProtocolVer:    data.ProtocolVer,
		ExpiryInterval: data.ExpiryInterval,
		Subscriptions:  make(map[string]uint8),
		Inflight:       make(map[uint16]*InflightMsg),
		packetIDSeq:    1,
		ReceiveMax:     65535,
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

	m.mu.Lock()
	m.sessions[clientID] = sess
	m.mu.Unlock()

	return sess, nil
}

// --- State management ---

// State returns the current session state.
func (s *Session) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

// SetState transitions the session to a new state.
func (s *Session) SetState(state State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
}

// CloseReason returns the reason the session was closed.
func (s *Session) CloseReason() CloseReason {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closeReason
}

// SetCloseReason sets why the session was closed.
func (s *Session) SetCloseReason(reason CloseReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeReason = reason
	s.state = StateDisconnecting
}

// IsConnected returns true if the session is in connected state.
func (s *Session) IsConnected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state == StateConnected
}

// --- Stats tracking ---

// Stats returns a snapshot of session statistics.
func (s *Session) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stats := s.stats
	stats.Subscriptions = len(s.Subscriptions)
	stats.InflightCount = len(s.Inflight)
	return stats
}

// TrackReceived increments the received message and byte counters.
func (s *Session) TrackReceived(msgSize int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.MessagesReceived++
	s.stats.BytesReceived += uint64(msgSize)
}

// TrackSent increments the sent message and byte counters.
func (s *Session) TrackSent(msgSize int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.MessagesSent++
	s.stats.BytesSent += uint64(msgSize)
}
