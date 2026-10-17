// Package broker provides the core MQTT message broker.
package broker

import (
	"context"
	"sync"
	"time"

	"github.com/X1aSheng/shark-mqtt/errs"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// InflightState represents the state of an inflight QoS message.
type InflightState int

const (
	StateSent InflightState = iota
	StateAcked
)

// InflightMessage tracks a QoS 1/2 message in flight.
type InflightMessage struct {
	PacketID   uint16
	ClientID   string
	State      InflightState
	SentAt     time.Time
	Retries    int
	MaxRetries int // inapplicable to a 5.0 session: see QoSEngine.SetClientVersion

	// Payload stored for retry
	Topic   string
	Payload []byte
	QoS     uint8
	Retain  bool

	// IsOutbound marks a broker->client delivery (tracked via
	// TrackOutboundQoS1/2). Retry re-sends the PUBLISH (or PUBREL once the
	// QoS 2 handshake reached the PUBREC/PUBCOMP phase) instead of the
	// inbound PUBREC behavior.
	IsOutbound bool
}

// QoSMessage represents a message to be delivered with QoS.
type QoSMessage struct {
	ClientID string
	PacketID uint16
	Topic    string
	Payload  []byte
	QoS      uint8
	Retain   bool
}

// QoSEngine manages QoS 1/2 message state machines.
// It handles retry logic, inflight tracking, and state transitions.
type QoSEngine struct {
	mu            sync.RWMutex
	inflight      map[string]map[uint16]*InflightMessage // clientID -> packetID -> msg
	maxInflight   int
	retryInterval time.Duration
	maxRetries    int

	// sessionVersions records the negotiated protocol version of each connected
	// session so the retry loop can consult its retransmission policy. It is
	// written by SetClientVersion and has the same lifecycle as the inflight
	// table (RemoveClient drops both), never a longer one.
	sessionVersions map[string]protocol.Version

	// Callback to deliver PUBACK/PUBREC/PUBREL/PUBCOMP to clients
	sendPubAck  func(clientID string, packetID uint16) error
	sendPubRel  func(clientID string, packetID uint16) error
	sendPubComp func(clientID string, packetID uint16) error

	// Callback to resend PUBLISH payload on retry
	republish func(clientID string, packetID uint16, topic string, payload []byte, qos uint8, retain bool) error

	onError func(clientID string, packetID uint16, err error)

	// onGiveUp is invoked when a delivery has exhausted its retries and the
	// engine has dropped its own entry. It exists because the engine is not the
	// only owner of that delivery: the session holds the same packet id in
	// Inflight and counts it against the outbound window. Dropping only the
	// engine's copy left the session's entry and the unacked counter behind
	// forever, so each abandoned delivery permanently shrank the window until
	// new deliveries stopped fitting -- with no log and no metric, because the
	// error callback was never registered either.
	onGiveUp func(clientID string, packetID uint16)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewQoSEngine creates a new QoSEngine.
func NewQoSEngine(opts ...QoSOption) *QoSEngine {
	o := defaultQoSOptions()
	for _, opt := range opts {
		opt(&o)
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &QoSEngine{
		inflight:        make(map[string]map[uint16]*InflightMessage),
		sessionVersions: make(map[string]protocol.Version),
		maxInflight:     o.maxInflight,
		retryInterval:   o.retryInterval,
		maxRetries:      o.maxRetries,
		ctx:             ctx,
		cancel:          cancel,
	}
}

// QoSOption configures QoSEngine.
type QoSOption func(*qosOptions)

type qosOptions struct {
	maxInflight   int
	retryInterval time.Duration
	maxRetries    int
}

func defaultQoSOptions() qosOptions {
	return qosOptions{
		maxInflight:   100,
		retryInterval: 10 * time.Second,
		maxRetries:    3,
	}
}

// WithMaxInflight sets the maximum inflight messages.
func WithMaxInflight(n int) QoSOption {
	return func(o *qosOptions) {
		o.maxInflight = n
	}
}

// WithRetryInterval sets the retry interval.
func WithRetryInterval(d time.Duration) QoSOption {
	return func(o *qosOptions) {
		o.retryInterval = d
	}
}

// WithMaxRetries sets the max retries.
func WithMaxRetries(n int) QoSOption {
	return func(o *qosOptions) {
		o.maxRetries = n
	}
}

// SetClientVersion records the protocol version negotiated for clientID's
// session, which decides whether the retry loop may drive that session's
// handshakes forward at all.
//
// The broker calls this once per CONNECT, before any delivery for the session
// is tracked. MQTT 5.0 §4.4 forbids resending outside session resumption
// ([MQTT-4.4.0-1]) while 3.1/3.1.1 §4.4 does not, so for a 5.0 client the retry
// loop performs no work: maxRetries and retryInterval are inapplicable to it,
// its outstanding packets are re-sent by broker.resendSessionInflight when the
// session resumes, and the inflight entry is released by the peer's
// acknowledgment or by RemoveClient on disconnect.
//
// A client with no recorded version (a caller that drives the engine directly)
// keeps the historical 3.1.1 behavior.
func (q *QoSEngine) SetClientVersion(clientID string, ver protocol.Version) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.sessionVersions == nil {
		q.sessionVersions = make(map[string]protocol.Version)
	}
	q.sessionVersions[clientID] = ver
}

// retransmitPermitted reports whether an unacknowledged delivery for clientID
// may be re-sent on the live connection.
//
// q.mu must be held: the version table is read under the same lock that guards
// the inflight table it belongs to. Reading it here rather than asking the
// session manager is deliberate -- doRetry already holds q.mu, and the broker
// takes its session locks before q.mu, so calling back out would close an
// ABBA cycle (the same hazard the lock discipline in doRetry exists to avoid).
func (q *QoSEngine) retransmitPermitted(clientID string) bool {
	spec, ok := protocol.SpecOf(q.sessionVersions[clientID])
	if !ok {
		return true
	}
	return spec.RetransmitUnacked
}

// SetCallbacks sets the delivery callbacks.
func (q *QoSEngine) SetCallbacks(
	sendPubAck func(clientID string, packetID uint16) error,
	sendPubRel func(clientID string, packetID uint16) error,
	sendPubComp func(clientID string, packetID uint16) error,
	republish func(clientID string, packetID uint16, topic string, payload []byte, qos uint8, retain bool) error,
) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sendPubAck = sendPubAck
	q.sendPubRel = sendPubRel
	q.sendPubComp = sendPubComp
	q.republish = republish
}

// SetErrorCallback sets a callback invoked when a QoS send fails.
func (q *QoSEngine) SetErrorCallback(fn func(clientID string, packetID uint16, err error)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.onError = fn
}

// SetGiveUpCallback sets the callback invoked when a delivery exhausts
// maxRetries. The engine has already removed its own tracking entry by then; the
// callback is where the session clears the matching Inflight record and the
// outbound-unacked count. It must not block, or it delays the retry loop for
// every client.
func (q *QoSEngine) SetGiveUpCallback(fn func(clientID string, packetID uint16)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.onGiveUp = fn
}

func (q *QoSEngine) reportError(clientID string, packetID uint16, err error) {
	if q.onError != nil {
		q.onError(clientID, packetID, err)
	}
}

// Start begins the retry loop.
func (q *QoSEngine) Start() {
	// Rebuild the context so the retry loop works after a Stop->Start cycle
	// (Stop cancels the previous context).
	q.mu.Lock()
	q.ctx, q.cancel = context.WithCancel(context.Background())
	q.mu.Unlock()
	q.wg.Add(1)
	go q.retryLoop()
}

// Stop stops the retry loop and cleans up.
func (q *QoSEngine) Stop() {
	q.cancel()
	q.wg.Wait()
}

// TrackQoS1 tracks an inbound QoS 1 publish for acknowledgment.
// Returns an error if the client has exceeded maxInflight.
func (q *QoSEngine) TrackQoS1(clientID string, packetID uint16, topic string, payload []byte, retain bool) error {
	return q.track(clientID, packetID, topic, payload, retain, 1, false)
}

// TrackQoS2 tracks an inbound QoS 2 publish for PUBREC/PUBREL/PUBCOMP sequence.
// Returns an error if the client has exceeded maxInflight.
func (q *QoSEngine) TrackQoS2(clientID string, packetID uint16, topic string, payload []byte, retain bool) error {
	return q.track(clientID, packetID, topic, payload, retain, 2, false)
}

// TrackOutboundQoS1 tracks a QoS 1 message delivered to a client so the retry
// loop can re-send it (with DUP) until the client acknowledges it.
func (q *QoSEngine) TrackOutboundQoS1(clientID string, packetID uint16, topic string, payload []byte, retain bool) error {
	return q.track(clientID, packetID, topic, payload, retain, 1, true)
}

// TrackOutboundQoS2 tracks a QoS 2 message delivered to a client so the retry
// loop can re-send the PUBLISH (or PUBREL after PUBREC) until it is complete.
func (q *QoSEngine) TrackOutboundQoS2(clientID string, packetID uint16, topic string, payload []byte, retain bool) error {
	return q.track(clientID, packetID, topic, payload, retain, 2, true)
}

// TrackOutboundQoS2AwaitingPubComp re-registers an outbound QoS 2 delivery
// whose PUBREC has already been received, so it is tracked in the same state
// AckPubRec would have left it in: the retransmission it owes is the PUBREL,
// never the PUBLISH ([MQTT-4.3.3-6]).
//
// A session restored from the store starts with an empty engine table, so this
// is what keeps a resumed PUBREL-pending delivery on the PUBREL branch of
// doRetry instead of restarting the handshake with a PUBLISH (MQTT 5.0 §4.4).
func (q *QoSEngine) TrackOutboundQoS2AwaitingPubComp(clientID string, packetID uint16, topic string, payload []byte, retain bool) error {
	if err := q.TrackOutboundQoS2(clientID, packetID, topic, payload, retain); err != nil {
		return err
	}
	q.mu.Lock()
	if clientInflight, ok := q.inflight[clientID]; ok {
		if msg, exists := clientInflight[packetID]; exists {
			msg.State = StateAcked
		}
	}
	q.mu.Unlock()
	return nil
}

func (q *QoSEngine) track(clientID string, packetID uint16, topic string, payload []byte, retain bool, qos uint8, outbound bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, ok := q.inflight[clientID]; !ok {
		q.inflight[clientID] = make(map[uint16]*InflightMessage)
	}

	if q.maxInflight > 0 && len(q.inflight[clientID]) >= q.maxInflight {
		return errs.ErrInflightFull
	}

	q.inflight[clientID][packetID] = &InflightMessage{
		PacketID:   packetID,
		ClientID:   clientID,
		State:      StateSent,
		SentAt:     time.Now(),
		MaxRetries: q.maxRetries,
		Topic:      topic,
		Payload:    payload,
		QoS:        qos,
		Retain:     retain,
		IsOutbound: outbound,
	}
	return nil
}

// AckQoS1 acknowledges a QoS 1 message (PUBACK received).
func (q *QoSEngine) AckQoS1(clientID string, packetID uint16) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if clientInflight, ok := q.inflight[clientID]; ok {
		delete(clientInflight, packetID)
	}
}

// AckPubRec acknowledges a QoS 2 PUBREC and sends PUBREL.
func (q *QoSEngine) AckPubRec(clientID string, packetID uint16) {
	q.mu.RLock()
	sendPubRel := q.sendPubRel
	q.mu.RUnlock()

	q.mu.Lock()
	found := false
	if clientInflight, ok := q.inflight[clientID]; ok {
		if msg, exists := clientInflight[packetID]; exists {
			msg.State = StateAcked
			found = true
		}
	}
	q.mu.Unlock()

	// Send PUBREL only for a tracked message so a spurious PUBREC does not
	// produce an unsolicited PUBREL.
	if found && sendPubRel != nil {
		if err := sendPubRel(clientID, packetID); err != nil {
			q.reportError(clientID, packetID, err)
		}
	}
}

// ReleaseInflight stops tracking a delivery without sending anything, because
// the peer declared the exchange over in a way that owes no packet.
//
// A PUBREC with a Reason Code of 0x80 or greater is the case this exists for:
// [MQTT-4.4.0-2] makes it an acknowledgment that the corresponding PUBLISH must
// not be retransmitted, and §4.3.3 frees the Packet Identifier at that point.
// The PUBREL the QoS 2 handshake would otherwise owe must therefore never be
// sent, which is exactly why this is not AckPubRec -- AckPubRec is the
// accepting path and emits the PUBREL ([MQTT-4.3.3-4]).
func (q *QoSEngine) ReleaseInflight(clientID string, packetID uint16) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if clientInflight, ok := q.inflight[clientID]; ok {
		delete(clientInflight, packetID)
	}
}

// AckPubRel acknowledges a QoS 2 PUBREL and sends PUBCOMP.
func (q *QoSEngine) AckPubRel(clientID string, packetID uint16) {
	q.mu.RLock()
	sendPubComp := q.sendPubComp
	q.mu.RUnlock()

	// Send PUBCOMP
	if sendPubComp != nil {
		if err := sendPubComp(clientID, packetID); err != nil {
			q.reportError(clientID, packetID, err)
		}
	}

	q.AckQoS1(clientID, packetID) // Remove from inflight
}

// AckPubComp acknowledges a QoS 2 PUBCOMP (from client).
func (q *QoSEngine) AckPubComp(clientID string, packetID uint16) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if clientInflight, ok := q.inflight[clientID]; ok {
		delete(clientInflight, packetID)
	}
}

// RemoveClient removes all inflight messages for a client, along with the
// protocol version recorded for it. The two are dropped together because the
// recorded version only has meaning for a session that can still be retried.
func (q *QoSEngine) RemoveClient(clientID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.inflight, clientID)
	delete(q.sessionVersions, clientID)
}

// InflightCount returns the number of inflight messages for a client.
func (q *QoSEngine) InflightCount(clientID string) int {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if clientInflight, ok := q.inflight[clientID]; ok {
		return len(clientInflight)
	}
	return 0
}

// GetInflight returns the inflight message for a client and packet ID.
func (q *QoSEngine) GetInflight(clientID string, packetID uint16) (*InflightMessage, bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if clientInflight, ok := q.inflight[clientID]; ok {
		msg, exists := clientInflight[packetID]
		return msg, exists
	}
	return nil, false
}

// retryLoop periodically retries unacknowledged QoS messages.
func (q *QoSEngine) retryLoop() {
	defer q.wg.Done()
	interval := q.retryInterval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-q.ctx.Done():
			return
		case <-ticker.C:
			q.doRetry()
		}
	}
}

func (q *QoSEngine) doRetry() {
	// Collect messages to retry under the lock, then release before
	// calling republish to avoid lock-ordering deadlock with broker's
	// connection/session locks (q.mu → b.mu vs b.mu → q.mu).
	type retryMsg struct {
		clientID string
		msg      InflightMessage
	}
	type retryError struct {
		clientID string
		packetID uint16
		err      error
	}
	var toRetry []retryMsg
	var toReport []retryError

	q.mu.Lock()
	for clientID, clientInflight := range q.inflight {
		// MQTT 5.0 §4.4 [MQTT-4.4.0-1]: "Clients and Servers MUST NOT resend
		// messages at any other time." A 5.0 session therefore has no retry at
		// all -- not the re-send, and not the give-up either: dropping the
		// entry after maxRetries would discard a delivery the session still
		// owns, when the only moment §4.4 does allow it back on the wire is the
		// resumption. Skipping the session wholesale keeps its window bounded
		// by maxInflight instead, and nothing is lost because the session
		// record, not this table, is what resendSessionInflight replays.
		if !q.retransmitPermitted(clientID) {
			continue
		}
		for packetID, msg := range clientInflight {
			if time.Since(msg.SentAt) < q.retryInterval {
				continue
			}
			if msg.Retries >= msg.MaxRetries {
				delete(clientInflight, packetID)
				toReport = append(toReport, retryError{
					clientID: clientID,
					packetID: packetID,
					err:      errs.ErrMaxRetriesExceeded,
				})
				continue
			}

			// Increment retry count on the copy, not the original,
			// to avoid overcounting if republish fails.
			copy := *msg
			copy.Retries++
			copy.SentAt = time.Now()
			toRetry = append(toRetry, retryMsg{
				clientID: clientID,
				msg:      copy,
			})
			// Update original so next retry cycle uses the new count
			msg.Retries = copy.Retries
			msg.SentAt = copy.SentAt
		}
	}
	republish := q.republish
	sendPubRel := q.sendPubRel
	onError := q.onError
	onGiveUp := q.onGiveUp
	q.mu.Unlock()

	for _, item := range toReport {
		if onGiveUp != nil {
			onGiveUp(item.clientID, item.packetID)
		}
		if onError != nil {
			onError(item.clientID, item.packetID, item.err)
		}
	}

	for _, item := range toRetry {
		msg := item.msg
		// An outbound QoS 2 message that already reached the PUBREC phase must
		// re-send PUBREL, not the original PUBLISH.
		if msg.IsOutbound && msg.QoS == 2 && msg.State == StateAcked {
			if sendPubRel != nil {
				if err := sendPubRel(item.clientID, msg.PacketID); err != nil && onError != nil {
					onError(item.clientID, msg.PacketID, err)
				}
			}
			continue
		}
		if republish != nil {
			if err := republish(item.clientID, msg.PacketID, msg.Topic, msg.Payload, msg.QoS, msg.Retain); err != nil {
				if onError != nil {
					onError(item.clientID, msg.PacketID, err)
				}
			}
		}
	}
}
