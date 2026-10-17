// Package session manages MQTT client sessions.
package broker

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store"
)

// ErrTopicAliasOutOfRange is returned by RegisterTopicAlias when the alias is
// outside the range the server advertised in the CONNACK. It is a Protocol
// Error (MQTT 5.0 §3.3.2.3.4), so the caller answers it with a DISCONNECT
// carrying 0x94 (Topic Alias invalid) instead of ignoring the packet.
var ErrTopicAliasOutOfRange = errors.New("broker: topic alias exceeds negotiated maximum")

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
	SubOptions    map[string]SubscriptionOptions
	Inflight      map[uint16]*InflightMsg
	// inboundQoS2 tracks QoS 2 publishes accepted from this client that are
	// still awaiting PUBREL (see InboundQoS2).
	inboundQoS2 map[uint16]*InboundQoS2
	packetIDSeq uint16

	// ReceiveMax is the server's INBOUND limit for this connection: the
	// largest number of QoS 1/2 PUBLISH packets the server is willing to have
	// unacknowledged from this client at once. It is the value the CONNACK
	// advertises (MQTT 5.0 §3.2.2.3.3) and the threshold inbound enforcement
	// applies (§3.3.4).
	//
	// It carries the server's own policy only. Property 0x21 arrives in both
	// CONNECT and CONNACK meaning opposite things, and this field used to hold
	// min(client, server), which made the CONNACK advertise the client's own
	// declaration back at it as if it were the server's limit.
	ReceiveMax uint16

	// TopicAliasMax is the largest Topic Alias the server accepts FROM the
	// client: the value advertised in the CONNACK (MQTT 5.0 §3.2.2.3.8) and
	// the limit RegisterTopicAlias enforces. 0 means the server accepts no
	// aliases, which is the case for MQTT 3.1.1 (no alias mechanism at all).
	TopicAliasMax uint16

	// ClientTopicAliasMax is the largest Topic Alias the CLIENT accepts FROM
	// the server, as declared in the CONNECT property (§3.1.2.11.5).
	//
	// The two properties share identifier 0x22 but point in opposite
	// directions, so this value must never be written into the CONNACK: that
	// would advertise the client's own acceptance limit as the server's, and
	// reading it back as the server's limit is exactly the defect this field
	// exists to keep apart. It is recorded so a future server-side alias
	// sender knows its ceiling.
	ClientTopicAliasMax uint16

	// OutboundWindow is the largest number of QoS 1/2 PUBLISH packets the
	// server may leave unacknowledged towards this client: the client's
	// declared Receive Maximum (MQTT 5.0 §3.1.2.11.6), capped by the server's
	// own send-side policy.
	//
	// The cap is explicitly allowed -- "The Server might choose to send fewer
	// than Receive Maximum messages to the Client" -- so this is deliberately
	// not the same number as ReceiveMax, and it is never advertised: a CONNACK
	// states the server's inbound limit, not the window the server grants
	// itself on the outbound side.
	OutboundWindow uint16

	mu sync.RWMutex

	// outboundUnacked tracks QoS 1/2 PUBLISH packets sent TO this client
	// that have not yet been acknowledged (PUBACK for QoS 1, PUBCOMP for QoS 2).
	// Combined with ReceiveMax, this enforces MQTT 5.0 flow control (§4.9).
	outboundUnacked int32

	// outboundQueue buffers QoS 1/2 deliveries when the client's receive
	// window is full, so they are not silently dropped (P2-14). Flushed when
	// the client acknowledges earlier deliveries.
	outboundQueue []outboundMsg

	// publishRate tracks the rate of PUBLISH packets from this client for
	// server-enforced rate limiting.
	publishRate *publishRateTracker

	// ExpiryInterval is the session expiry interval in seconds (MQTT 5.0 §3.1.2.11.2).
	// Server caps this at its configured max. 0 = session expires immediately on disconnect.
	ExpiryInterval uint32

	// AssignedClientID is set when the server generates a client ID for a client
	// that connected with an empty ClientID (MQTT 5.0 §3.1.3.6).
	AssignedClientID string

	// RequestResponseInfo records that the client requested the server to return
	// Response Information in the CONNACK (MQTT 5.0 §3.2.2.3.8).
	RequestResponseInfo bool

	// ServerKeepAlive is set when the server enforces a shorter keep-alive than
	// the client requested (MQTT 5.0 §3.1.2.11.4).
	ServerKeepAlive *uint16

	// topicAliases stores the client→server topic alias mappings for this session
	// (MQTT 5.0 §3.3.2.3.4). Key is the alias value, value is the resolved topic.
	topicAliases map[uint16]string

	// State management
	state       State
	closeReason CloseReason

	// Stats tracking
	stats Stats

	// sentCount and sentBytes are the two counters on the delivery hot path.
	// They are atomic rather than part of stats because TrackSent runs once per
	// message per subscriber: with them under s.mu a single published message
	// took the session write lock as many times as it had subscribers, and
	// every subscriber of that message serialised on the same mutex. They are
	// cumulative, and Stats merges them into the snapshot.
	sentCount atomic.Uint64
	sentBytes atomic.Uint64
}

// Spec returns the capability table for the session's negotiated protocol
// version, so the broker core can query capabilities instead of comparing raw
// version values. Unknown/unset versions fall back to the 3.1.1 capabilities
// (the historical default).
func (s *Session) Spec() protocol.Spec {
	if s == nil {
		return protocol.Spec{}
	}
	if sp, ok := protocol.SpecOf(s.ProtocolVer); ok {
		return sp
	}
	sp, _ := protocol.SpecOf(protocol.Version311)
	return sp
}

// SubscriptionOptions captures MQTT subscription options that affect delivery.
type SubscriptionOptions struct {
	QoS               uint8
	NoLocal           bool
	RetainAsPublished bool
	RetainHandling    uint8
	// SubscriptionIdentifier is the identifier THIS subscription carries, so it
	// stays a single value. A message that matched several subscriptions must
	// be delivered once carrying all of their identifiers ([MQTT-3.3.4-4]),
	// which is why the aggregate travels separately -- see matchSubscriptions.
	SubscriptionIdentifier *uint32
}

// outboundMsg is a QoS 1/2 delivery buffered when the client's receive window
// is full.
type outboundMsg struct {
	pkt        *protocol.PublishPacket
	deliverQoS uint8
	subOpts    SubscriptionOptions
	// subIDs is the aggregate identifier set of every subscription that matched
	// this message, carried alongside subOpts so a buffered delivery carries
	// the same identifiers it would have carried if the window had been open.
	subIDs    []uint32
	expiresAt time.Time // absolute Message Expiry Interval deadline; zero = none (MQTT 5.0 §3.3.2.3.3)
}

// maxBufferedOutbound caps the number of QoS 1/2 deliveries buffered per
// session while the client's receive window is full, so a client that never
// acknowledges cannot exhaust broker memory (R6).
const maxBufferedOutbound = 1000

// InflightMsg tracks an in-flight QoS message.
type InflightMsg struct {
	PacketID  uint16
	QoS       uint8
	Topic     string
	Payload   []byte
	Retain    bool
	SentAt    time.Time
	ExpiresAt time.Time // absolute Message Expiry Interval deadline; zero = none (MQTT 5.0 §3.3.2.3.3)
	AckType   byte      // PUBACK, PUBREC, PUBREL, PUBCOMP

	// Properties is the MQTT 5.0 property set that was put on the wire, so a
	// retransmission reproduces the same PUBLISH: §3.3.2.3 requires the
	// forwarded Response Topic / Correlation Data / User Properties / Content
	// Type / Payload Format Indicator to reach the subscriber even when the
	// first attempt needs a retry. nil for MQTT 3.1.1 subscribers.
	//
	// Persisted with the session (as store.MessageProperties, through
	// storeProperties/protocolProperties) so a retransmission after a broker
	// restart still carries it; ExpiresAt is persisted alongside it, which keeps
	// §3.3.2.3.3 enforceable for a restored message.
	Properties *protocol.Properties

	// PendingPubRel marks an outbound QoS 2 delivery that already reached the
	// PUBREC phase: the PUBREL has been sent (or is owed) and we are waiting
	// for the peer's PUBCOMP. Set by MarkInflightPubRel when the PUBREC is
	// accepted.
	//
	// It is persisted so a resumed session resends the right packet: MQTT 5.0
	// §4.4 / 3.1.1 §4.4 require the unacknowledged PUBLISH *and* PUBREL packets
	// to be resent on reconnect, and [MQTT-4.3.3-6] forbids re-sending a
	// PUBLISH once its PUBREL has gone out.
	PendingPubRel bool
}

// InboundQoS2 is a QoS 2 message accepted from the client (PUBLISH received,
// PUBREC sent) whose handshake has not yet completed (PUBREL pending). Entries
// live in the session — separate from the outbound QoS engine table, whose
// shared (clientID, packetID) key let an outbound delivery overwrite the
// inbound entry (audit H4) — and are persisted with the session so a
// reconnect can still route the message instead of acknowledging a PUBREL
// with nothing delivered.
type InboundQoS2 struct {
	PacketID   uint16
	QoS        uint8
	Topic      string
	Payload    []byte
	Retain     bool
	ReceivedAt time.Time

	// Properties holds the publisher's MQTT 5 per-message properties while the
	// message waits for PUBREL. A QoS 2 publish is not routed on arrival, and
	// the routed packet is rebuilt from this staging record, so without them
	// every QoS 2 delivery would lose the relay data §3.3.2.3 requires -- the
	// Response Topic, Correlation Data, User Properties, Content Type and
	// Payload Format Indicator. nil for MQTT 3.1/3.1.1 publishers.
	Properties *protocol.Properties
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
			if existing.SubOptions == nil {
				existing.SubOptions = make(map[string]SubscriptionOptions, len(existing.Subscriptions))
				for topic, qos := range existing.Subscriptions {
					existing.SubOptions[topic] = SubscriptionOptions{QoS: qos}
				}
			}
			// [MQTT-3.3.2-7]: a receiver must not carry Topic Alias mappings
			// over from one Network Connection to the next. Resuming keeps the
			// subscriptions and inflight messages, but the alias table belongs
			// to the network connection, so it starts empty again.
			existing.topicAliases = make(map[uint16]string)
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
		ClientID:       clientID,
		Username:       connectPkt.Username,
		IsClean:        connectPkt.Flags.CleanSession,
		ProtocolVer:    connectPkt.ProtocolVersion,
		KeepAlive:      connectPkt.KeepAlive,
		ConnectedAt:    time.Now(),
		LastActivity:   time.Now(),
		Subscriptions:  make(map[string]uint8),
		SubOptions:     make(map[string]SubscriptionOptions),
		Inflight:       make(map[uint16]*InflightMsg),
		inboundQoS2:    make(map[uint16]*InboundQoS2),
		packetIDSeq:    1,
		ReceiveMax:     65535,
		OutboundWindow: 65535,
		topicAliases:   make(map[uint16]string),
		state:          StateConnected,
	}

	sess.stats.ConnectCount++
	sess.stats.LastConnectedAt = sess.ConnectedAt
	sess.stats.Subscriptions = 0
	sess.stats.InflightCount = 0

	// Initialize publish rate tracker with a 1-second window
	sess.publishRate = newPublishRateTracker(time.Second)

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
	s.AddSubscriptionFilter(protocol.TopicFilter{Topic: topic, QoS: qos})
}

// AddSubscriptionFilter adds or updates a subscription with MQTT 5.0 options.
func (s *Session) AddSubscriptionFilter(filter protocol.TopicFilter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.SubOptions == nil {
		s.SubOptions = make(map[string]SubscriptionOptions, len(s.Subscriptions))
		for topic, qos := range s.Subscriptions {
			s.SubOptions[topic] = SubscriptionOptions{QoS: qos}
		}
	}
	s.Subscriptions[filter.Topic] = filter.QoS
	s.SubOptions[filter.Topic] = SubscriptionOptions{
		QoS:                    filter.QoS,
		NoLocal:                filter.NoLocal,
		RetainAsPublished:      filter.RetainAsPublished,
		RetainHandling:         filter.RetainHandling,
		SubscriptionIdentifier: filter.SubscriptionIdentifier,
	}
}

// RemoveSubscription removes a subscription.
func (s *Session) RemoveSubscription(topic string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Subscriptions, topic)
	delete(s.SubOptions, topic)
}

// HasSubscription reports whether the exact topic filter is already present.
func (s *Session) HasSubscription(topic string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.Subscriptions[topic]
	return ok
}

// matchSubscriptions is the shared core of MatchesSubscription and
// MatchesRetainedSubscription. It returns matched status, the maximum QoS
// across all matching subscriptions (MQTT 3.1.1 §3.3.5 / 5.0 §3.3.5: deliver
// once at the max QoS of matching filters), the subscription options of the
// highest-QoS match, and the Subscription Identifiers of *every* matching
// subscription. When sysProtect is set, MQTT §4.7.2 system-topic protection is
// applied (a retained $SYS message is only delivered to a filter whose first
// level begins with $).
//
// The identifier set is a separate return value rather than part of
// SubscriptionOptions because it is an aggregate over the matches, not a
// property of one subscription. MQTT 5.0 [MQTT-3.3.4-4] requires it: the Server
// delivers one copy of a message that matched several subscriptions, and that
// single copy MUST carry the identifiers of all of them.
//
// Identifiers are de-duplicated and sorted. De-duplicating is what keeps a
// client from turning its own delivery into a large packet -- N overlapping
// filters carrying the same identifier would otherwise become N identical
// property instances on every message -- and sorting makes the encoded bytes
// reproducible for tests. §3.3.2.3.8 leaves the order open.
func (s *Session) matchSubscriptions(topic string, sysProtect bool) (bool, uint8, SubscriptionOptions, []uint32) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	matched := false
	var bestQoS uint8
	var bestOpts SubscriptionOptions
	// bestPattern breaks ties between matching filters at the same QoS. Without
	// it, which one supplies RetainAsPublished / RetainHandling / Retain depends
	// on map iteration order, so two identical publishes could deliver with
	// different retain semantics.
	//
	// The rule is lexicographic order on the filter. MQTT says nothing about
	// which of two equal-QoS matches supplies the options, so the requirement is
	// stability, not a particular winner: any total order removes the
	// nondeterminism, and picking the "most specific" would need a rule for
	// comparing literal levels that the spec does not define.
	var bestPattern string
	// Allocated lazily: the common case is a session that set no identifiers at
	// all, and it should not pay for an allocation on every delivery.
	var identifiers []uint32
	for pattern, qos := range s.Subscriptions {
		// Shared subscription filters are stored as "$share/{group}/{filter}";
		// match against the real filter so retained messages are delivered to
		// shared subscribers.
		realPattern := pattern
		if IsSharedSubscription(pattern) {
			_, realFilter, ok := ParseSharedFilter(pattern)
			if !ok {
				continue
			}
			realPattern = realFilter
		}
		if sysProtect && !matchSysProtected(realPattern, topic) {
			continue
		}
		if protocol.MatchTopic(realPattern, topic) {
			// Record the options of the first match too: a QoS 0 match never
			// satisfies `qos > bestQoS` (both are 0), so the previous form left
			// bestOpts at its zero value for QoS 0 subscriptions and silently
			// dropped RetainAsPublished and SubscriptionIdentifier.
			opts := s.SubOptions[pattern]
			if !matched || qos > bestQoS || (qos == bestQoS && moreSpecific(pattern, bestPattern)) {
				bestQoS = qos
				bestOpts = opts
				bestPattern = pattern
			}
			if opts.SubscriptionIdentifier != nil {
				identifiers = append(identifiers, *opts.SubscriptionIdentifier)
			}
			matched = true
		}
	}
	if len(identifiers) > 1 {
		slices.Sort(identifiers)
		identifiers = slices.Compact(identifiers)
	}
	return matched, bestQoS, bestOpts, identifiers
}

// moreSpecific reports whether candidate should win a tie against current.
// Plain lexicographic order: stable, total, and independent of map iteration.
func moreSpecific(candidate, current string) bool {
	return candidate < current
}

// MatchesSubscription checks if a topic matches any of the session's
// subscriptions, returning the maximum QoS of the matching filters, the
// subscription options of the highest-QoS match, and the Subscription
// Identifiers of every match (MQTT 5.0 [MQTT-3.3.4-4]: the single delivered
// copy must carry them all). Previously the first map-order-random match won,
// making the delivered QoS nondeterministic when filters overlapped.
func (s *Session) MatchesSubscription(topic string) (bool, uint8, SubscriptionOptions, []uint32) {
	return s.matchSubscriptions(topic, false)
}

// MatchesRetainedSubscription is MatchesSubscription with MQTT §4.7.2
// system-topic protection (P3-4). Live routing applies this protection in the
// topic tree; retained delivery goes through the store, so it needs the check
// here.
func (s *Session) MatchesRetainedSubscription(topic string) (bool, uint8, SubscriptionOptions, []uint32) {
	return s.matchSubscriptions(topic, true)
}

// AllowsLocalPublish reports whether a matching subscription accepts messages
// published by the same client. MQTT delivers local publishes by default;
// MQTT 5.0 No Local suppresses them per subscription.
func (s *Session) AllowsLocalPublish(topic string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	matched := false
	for pattern := range s.Subscriptions {
		if !protocol.MatchTopic(pattern, topic) {
			continue
		}
		matched = true
		opts, ok := s.SubOptions[pattern]
		if !ok || !opts.NoLocal {
			return true
		}
	}
	return !matched
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
	// All IDs are in use; return the next one anyway
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

// RemoveInflight removes an in-flight message and reports whether an entry was
// actually present. Callers use the result to avoid decrementing the outbound
// flow-control counter for spurious acknowledgments (P2-16).
func (s *Session) RemoveInflight(packetID uint16) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Inflight[packetID]; !ok {
		return false
	}
	delete(s.Inflight, packetID)
	return true
}

// AddInboundQoS2 stages an accepted inbound QoS 2 publish (PUBREC sent).
//
// maxPending is the Receive Maximum this connection was told about, so a client
// that never completes its half of the handshake cannot grow the table without
// bound, and the bound it hits is the one it was promised rather than an
// unrelated internal constant (MQTT 5.0 §3.3.4 / §4.9). It is a per-Network-
// Connection number, not session state: a reconnect renegotiates it. Returns
// false when the budget is exhausted.
func (s *Session) AddInboundQoS2(msg *InboundQoS2, maxPending int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inboundQoS2 == nil {
		s.inboundQoS2 = make(map[uint16]*InboundQoS2)
	}
	if maxPending > 0 && len(s.inboundQoS2) >= maxPending {
		return false
	}
	s.inboundQoS2[msg.PacketID] = msg
	return true
}

// HasInboundQoS2 reports whether a packet id is still awaiting PUBREL.
func (s *Session) HasInboundQoS2(packetID uint16) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.inboundQoS2[packetID]
	return ok
}

// TakeInboundQoS2 removes and returns the staged inbound QoS 2 message for a
// packet id; ok is false when none is pending (e.g. an unknown PUBREL).
func (s *Session) TakeInboundQoS2(packetID uint16) (*InboundQoS2, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, ok := s.inboundQoS2[packetID]
	if !ok {
		return nil, false
	}
	delete(s.inboundQoS2, packetID)
	return msg, true
}

// GetInflight returns an in-flight message.
func (s *Session) GetInflight(packetID uint16) (*InflightMsg, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	msg, ok := s.Inflight[packetID]
	return msg, ok
}

// MarkInflightPubRel records that an outbound QoS 2 delivery has reached the
// PUBREL-sent stage, i.e. the peer answered the PUBLISH with a PUBREC.
//
// The stage has to be remembered because it selects the packet a later
// retransmission owes: the PUBREL, not the PUBLISH ([MQTT-4.3.3-6], MQTT 5.0
// §4.4). Returns whether an outbound QoS 2 entry for that packet id was found;
// a QoS 1 delivery and an unknown packet id are left untouched, so a spurious
// PUBREC cannot turn an unrelated entry into a PUBREL-pending one.
func (s *Session) MarkInflightPubRel(packetID uint16) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	msg, ok := s.Inflight[packetID]
	if !ok || msg.QoS != 2 {
		return false
	}
	msg.PendingPubRel = true
	return true
}

// InflightSnapshot returns copies of the session's outbound in-flight entries,
// so a caller can walk them without holding the session lock. The Payload and
// Properties of each copy are the live ones and must be treated as read-only.
func (s *Session) InflightSnapshot() []InflightMsg {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]InflightMsg, 0, len(s.Inflight))
	for _, msg := range s.Inflight {
		out = append(out, *msg)
	}
	return out
}

// Save persists the session to the store.
// Snapshot under the read lock, then deep-copy payloads outside the lock
// so that concurrent readers (e.g. deliverToClient) are not blocked by
// large payload copies.
func (s *Session) Save(ctx context.Context, sessionStore store.SessionStore) error {
	s.mu.RLock()

	data := &store.SessionData{
		ClientID:       s.ClientID,
		Username:       s.Username,
		IsClean:        s.IsClean,
		KeepAlive:      s.KeepAlive,
		ProtocolVer:    s.ProtocolVer,
		ExpiryInterval: s.ExpiryInterval,
	}

	if s.ExpiryInterval > 0 {
		data.ExpiryTime = time.Now().Add(time.Duration(s.ExpiryInterval) * time.Second)
	}

	subscriptions := make([]store.Subscription, 0, len(s.Subscriptions))
	for topic, qos := range s.Subscriptions {
		opts := s.SubOptions[topic]
		subscriptions = append(subscriptions, store.Subscription{
			Topic:                  topic,
			QoS:                    qos,
			NoLocal:                opts.NoLocal,
			RetainAsPublished:      opts.RetainAsPublished,
			RetainHandling:         opts.RetainHandling,
			SubscriptionIdentifier: opts.SubscriptionIdentifier,
		})
	}
	data.Subscriptions = subscriptions

	// Snapshot inflight metadata — payload deep-copy happens outside the lock.
	type snapEntry struct {
		packetID uint16
		qos      uint8
		topic    string
		payload  []byte
		retain   bool
		// properties and expiresAt are the MQTT 5 relay data a retransmission
		// after a restart has to reproduce (§3.3.2.3) and keep enforcing
		// (§3.3.2.3.3). They are copied by value; storeProperties rebuilds the
		// user-property slice below.
		properties *protocol.Properties
		expiresAt  time.Time
		// pendingPubRel is the QoS 2 handshake stage a resume has to continue
		// from, see InflightMsg.PendingPubRel.
		pendingPubRel bool
	}
	snapshots := make([]snapEntry, 0, len(s.Inflight))
	for _, msg := range s.Inflight {
		snapshots = append(snapshots, snapEntry{
			packetID:      msg.PacketID,
			qos:           msg.QoS,
			topic:         msg.Topic,
			payload:       msg.Payload,
			retain:        msg.Retain,
			properties:    msg.Properties,
			expiresAt:     msg.ExpiresAt,
			pendingPubRel: msg.PendingPubRel,
		})
	}

	// Snapshot inbound QoS 2 stages (PUBREC sent, PUBREL pending) the same
	// way; payloads are deep-copied below (audit H4).
	inboundSnapshots := make([]snapEntry, 0, len(s.inboundQoS2))
	for _, msg := range s.inboundQoS2 {
		inboundSnapshots = append(inboundSnapshots, snapEntry{
			packetID:   msg.PacketID,
			qos:        msg.QoS,
			topic:      msg.Topic,
			payload:    msg.Payload,
			retain:     msg.Retain,
			properties: msg.Properties,
		})
	}

	s.mu.RUnlock()

	// Deep-copy payloads outside the lock.
	inflight := make(map[uint16]*store.InflightMessage, len(snapshots))
	for _, snap := range snapshots {
		payloadCopy := make([]byte, len(snap.payload))
		copy(payloadCopy, snap.payload)
		inflight[snap.packetID] = &store.InflightMessage{
			PacketID:      snap.packetID,
			QoS:           snap.qos,
			Topic:         snap.topic,
			Payload:       payloadCopy,
			Retain:        snap.retain,
			Properties:    storeProperties(snap.properties),
			ExpiresAt:     snap.expiresAt,
			PendingPubRel: snap.pendingPubRel,
		}
	}
	data.Inflight = inflight

	inbound := make(map[uint16]*store.InflightMessage, len(inboundSnapshots))
	for _, snap := range inboundSnapshots {
		payloadCopy := make([]byte, len(snap.payload))
		copy(payloadCopy, snap.payload)
		inbound[snap.packetID] = &store.InflightMessage{
			PacketID:   snap.packetID,
			QoS:        snap.qos,
			Topic:      snap.topic,
			Payload:    payloadCopy,
			Retain:     snap.retain,
			Properties: storeProperties(snap.properties),
		}
	}
	data.InboundQoS2 = inbound

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
		SubOptions:     make(map[string]SubscriptionOptions),
		Inflight:       make(map[uint16]*InflightMsg),
		inboundQoS2:    make(map[uint16]*InboundQoS2),
		packetIDSeq:    1,
		ReceiveMax:     65535,
		OutboundWindow: 65535,
		topicAliases:   make(map[uint16]string),
	}

	for _, sub := range data.Subscriptions {
		sess.Subscriptions[sub.Topic] = sub.QoS
		sess.SubOptions[sub.Topic] = SubscriptionOptions{
			QoS:                    sub.QoS,
			NoLocal:                sub.NoLocal,
			RetainAsPublished:      sub.RetainAsPublished,
			RetainHandling:         sub.RetainHandling,
			SubscriptionIdentifier: sub.SubscriptionIdentifier,
		}
	}

	for id, msg := range data.Inflight {
		payloadCopy := make([]byte, len(msg.Payload))
		copy(payloadCopy, msg.Payload)
		sess.Inflight[id] = &InflightMsg{
			PacketID:      msg.PacketID,
			QoS:           msg.QoS,
			Topic:         msg.Topic,
			Payload:       payloadCopy,
			Retain:        msg.Retain,
			ExpiresAt:     msg.ExpiresAt,
			Properties:    protocolProperties(msg.Properties),
			PendingPubRel: msg.PendingPubRel,
		}
	}

	// Restore inbound QoS 2 stages (PUBREC sent, PUBREL pending) so a
	// reconnecting client that retransmits PUBREL still gets its message
	// routed (audit H4).
	for id, msg := range data.InboundQoS2 {
		payloadCopy := make([]byte, len(msg.Payload))
		copy(payloadCopy, msg.Payload)
		sess.inboundQoS2[id] = &InboundQoS2{
			PacketID:   msg.PacketID,
			QoS:        msg.QoS,
			Topic:      msg.Topic,
			Payload:    payloadCopy,
			Retain:     msg.Retain,
			ReceivedAt: time.Now(),
			Properties: protocolProperties(msg.Properties),
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
// Stats returns a snapshot of the session's counters.
//
// sentCount and sentBytes are read outside the session lock and merged into
// the snapshot. They are the two counters on the fan-out hot path: one call per
// delivered message, per subscriber, and taking the session write lock there
// serialised every subscriber of the same message against each other. They are
// cumulative for the lifetime of the session, so adding them to the (no longer
// written) fields in s.stats keeps the snapshot monotonic.
func (s *Session) Stats() Stats {
	s.mu.RLock()
	stats := s.stats
	stats.Subscriptions = len(s.Subscriptions)
	stats.InflightCount = len(s.Inflight)
	s.mu.RUnlock()

	stats.MessagesSent += s.sentCount.Load()
	stats.BytesSent += s.sentBytes.Load()
	return stats
}

// TrackReceived increments the received message and byte counters.
func (s *Session) TrackReceived(msgSize int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stats.MessagesReceived++
	s.stats.BytesReceived += uint64(msgSize)
}

// RegisterTopicAlias registers a new topic alias mapping for this session.
// Returns ErrTopicAliasOutOfRange if the alias is outside the range the server
// advertised in the CONNACK (or if the server accepts no aliases at all).
func (s *Session) RegisterTopicAlias(alias uint16, topic string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.TopicAliasMax == 0 || alias > s.TopicAliasMax {
		return ErrTopicAliasOutOfRange
	}
	s.topicAliases[alias] = topic
	return nil
}

// ResolveTopicAlias resolves a topic alias to the mapped topic name.
// Returns false if the alias is not registered.
func (s *Session) ResolveTopicAlias(alias uint16) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	topic, ok := s.topicAliases[alias]
	return topic, ok
}

// TrackSent increments the sent message and byte counters.
func (s *Session) TrackSent(msgSize int) {
	s.sentCount.Add(1)
	s.sentBytes.Add(uint64(msgSize))
}

// CanSendOutbound reports whether another QoS 1/2 message can be sent to this
// client without exceeding the client's Receive Maximum (MQTT 5.0 §4.9). It is
// the outbound window, not the server's inbound limit, that gates this; see
// OutboundWindow.
//
// The window is read under the session lock rather than as a bare field: a
// reconnect re-negotiates it on the existing session object (the socket may be
// new, the session is not), while deliveries keep consulting it from the
// routing goroutine.
func (s *Session) CanSendOutbound() bool {
	s.mu.RLock()
	window := s.OutboundWindow
	s.mu.RUnlock()
	return int(atomic.LoadInt32(&s.outboundUnacked)) < int(window)
}

// IncOutboundUnacked increments the outbound unacknowledged counter.
// Called when a QoS 1/2 PUBLISH is sent to this client.
func (s *Session) IncOutboundUnacked() {
	atomic.AddInt32(&s.outboundUnacked, 1)
}

// DecOutboundUnacked decrements the outbound unacknowledged counter.
// Called when a PUBACK (QoS 1) or PUBCOMP (QoS 2) is received from this client.
// The counter never goes below zero, so spurious acknowledgments cannot bypass
// flow control by driving it negative (P2-16).
func (s *Session) DecOutboundUnacked() {
	for {
		cur := atomic.LoadInt32(&s.outboundUnacked)
		if cur <= 0 {
			return
		}
		if atomic.CompareAndSwapInt32(&s.outboundUnacked, cur, cur-1) {
			return
		}
	}
}

// ResetOutboundUnacked resets the outbound unacknowledged counter to zero.
// Called on disconnect.
func (s *Session) ResetOutboundUnacked() {
	atomic.StoreInt32(&s.outboundUnacked, 0)
}

// BufferOutbound queues a QoS 1/2 delivery for when the client's receive
// window opens (P2-14). It reports whether the message was accepted; the queue
// is bounded by maxBufferedOutbound so a client that never acknowledges cannot
// exhaust memory (R6).
func (s *Session) BufferOutbound(pkt *protocol.PublishPacket, deliverQoS uint8, subOpts SubscriptionOptions, subIDs []uint32, expiresAt time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.outboundQueue) >= maxBufferedOutbound {
		return false
	}
	s.outboundQueue = append(s.outboundQueue, outboundMsg{
		pkt:        pkt,
		deliverQoS: deliverQoS,
		subOpts:    subOpts,
		subIDs:     subIDs,
		expiresAt:  expiresAt,
	})
	return true
}

// PopOutbound returns and removes the oldest buffered delivery.
func (s *Session) PopOutbound() (outboundMsg, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.outboundQueue) == 0 {
		return outboundMsg{}, false
	}
	msg := s.outboundQueue[0]
	s.outboundQueue = s.outboundQueue[1:]
	return msg, true
}

// OutboundQueueLen returns the number of buffered deliveries.
func (s *Session) OutboundQueueLen() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.outboundQueue)
}
