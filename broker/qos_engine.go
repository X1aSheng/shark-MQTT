// Package broker provides the core MQTT message broker.
package broker

import (
	"context"
	"sync"
	"time"
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
	MaxRetries int

	// Payload stored for retry
	Topic   string
	Payload []byte
	QoS     uint8
	Retain  bool
}

// QoSMessage represents a message to be delivered with QoS.
type QoSMessage struct {
	ClientID  string
	PacketID  uint16
	Topic     string
	Payload   []byte
	QoS       uint8
	Retain    bool
}

// QoSEngine manages QoS 1/2 message state machines.
// It handles retry logic, inflight tracking, and state transitions.
type QoSEngine struct {
	mu           sync.RWMutex
	inflight     map[string]map[uint16]*InflightMessage // clientID -> packetID -> msg
	maxInflight  int
	retryInterval time.Duration
	maxRetries    int

	// Callback to deliver PUBACK/PUBREC/PUBREL/PUBCOMP to clients
	sendPubAck func(clientID string, packetID uint16) error
	sendPubRel func(clientID string, packetID uint16) error
	sendPubComp func(clientID string, packetID uint16) error

	// Callback to resend PUBLISH payload on retry
	republish func(clientID string, packetID uint16, topic string, payload []byte, qos uint8, retain bool) error

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
		inflight:      make(map[string]map[uint16]*InflightMessage),
		maxInflight:   o.maxInflight,
		retryInterval: o.retryInterval,
		maxRetries:    o.maxRetries,
		ctx:           ctx,
		cancel:        cancel,
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

// Start begins the retry loop.
func (q *QoSEngine) Start() {
	q.wg.Add(1)
	go q.retryLoop()
}

// Stop stops the retry loop and cleans up.
func (q *QoSEngine) Stop() {
	q.cancel()
	q.wg.Wait()
}

// TrackQoS1 tracks a QoS 1 publish for acknowledgment.
func (q *QoSEngine) TrackQoS1(clientID string, packetID uint16, topic string, payload []byte, retain bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, ok := q.inflight[clientID]; !ok {
		q.inflight[clientID] = make(map[uint16]*InflightMessage)
	}

	q.inflight[clientID][packetID] = &InflightMessage{
		PacketID:   packetID,
		ClientID:   clientID,
		State:      StateSent,
		SentAt:     time.Now(),
		MaxRetries: q.maxRetries,
		Topic:      topic,
		Payload:    payload,
		QoS:        1,
		Retain:     retain,
	}
}

// TrackQoS2 tracks a QoS 2 publish for PUBREC/PUBREL/PUBCOMP sequence.
func (q *QoSEngine) TrackQoS2(clientID string, packetID uint16, topic string, payload []byte, retain bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, ok := q.inflight[clientID]; !ok {
		q.inflight[clientID] = make(map[uint16]*InflightMessage)
	}

	q.inflight[clientID][packetID] = &InflightMessage{
		PacketID:   packetID,
		ClientID:   clientID,
		State:      StateSent,
		SentAt:     time.Now(),
		MaxRetries: q.maxRetries,
		Topic:      topic,
		Payload:    payload,
		QoS:        2,
		Retain:     retain,
	}
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
	if clientInflight, ok := q.inflight[clientID]; ok {
		if msg, exists := clientInflight[packetID]; exists {
			msg.State = StateAcked
		}
	}
	q.mu.Unlock()

	// Send PUBREL
	if sendPubRel != nil {
		_ = sendPubRel(clientID, packetID)
	}
}

// AckPubRel acknowledges a QoS 2 PUBREL and sends PUBCOMP.
func (q *QoSEngine) AckPubRel(clientID string, packetID uint16) {
	q.mu.RLock()
	sendPubComp := q.sendPubComp
	q.mu.RUnlock()

	// Send PUBCOMP
	if sendPubComp != nil {
		_ = sendPubComp(clientID, packetID)
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

// RemoveClient removes all inflight messages for a client.
func (q *QoSEngine) RemoveClient(clientID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.inflight, clientID)
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
	q.mu.RLock()
	defer q.mu.RUnlock()

	sendPubAck := q.sendPubAck
	sendPubRel := q.sendPubRel
	republish := q.republish

	for clientID, clientInflight := range q.inflight {
		for packetID, msg := range clientInflight {
			if time.Since(msg.SentAt) < q.retryInterval {
				continue
			}
			if msg.Retries >= msg.MaxRetries {
				// Max retries exceeded, remove from inflight
				delete(clientInflight, packetID)
				continue
			}

			msg.Retries++
			msg.SentAt = time.Now()

			// For QoS 1/2, first retry the actual PUBLISH message
			if republish != nil {
				_ = republish(clientID, msg.PacketID, msg.Topic, msg.Payload, msg.QoS, msg.Retain)
			}

			// Also send the acknowledgment packet (PUBACK for QoS 1, PUBREL for QoS 2)
			if msg.State == StateSent {
				if sendPubAck != nil {
					_ = sendPubAck(clientID, packetID)
				}
			} else if msg.State == StateAcked {
				if sendPubRel != nil {
					_ = sendPubRel(clientID, packetID)
				}
			}
		}
	}
}
