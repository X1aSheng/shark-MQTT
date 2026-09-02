// Package broker provides the core MQTT message broker.
package broker

import (
	"context"
	"net"
	"sync"
	"time"
)

// WillMessage represents a last will message.
type WillMessage struct {
	ClientID string
	Username string // owner username, used for publish authorization
	Topic    string
	Payload  []byte
	QoS      uint8
	Retain   bool
	Delay    time.Duration // Delayed will message delivery
	// Conn is the network connection that registered this will. TriggerWill
	// only fires a will owned by the given connection: after a session
	// takeover the old connection's late abnormal-disconnect must not trigger
	// the NEW connection's freshly-registered will (audit).
	Conn net.Conn
}

// WillHandler manages last will messages for clients that disconnect abnormally.
type WillHandler struct {
	mu     sync.Mutex
	wills  map[string]*WillMessage
	cancel map[string]context.CancelFunc
	wg     sync.WaitGroup

	// Callback to publish will message (username included for authorization)
	publishWill func(username string, topic string, payload []byte, qos uint8, retain bool) error
}

// NewWillHandler creates a new WillHandler.
func NewWillHandler() *WillHandler {
	return &WillHandler{
		wills:  make(map[string]*WillMessage),
		cancel: make(map[string]context.CancelFunc),
	}
}

// Stop cancels all pending delayed will messages and waits for completion.
func (wh *WillHandler) Stop() {
	wh.mu.Lock()
	for _, cancel := range wh.cancel {
		cancel()
	}
	wh.wills = make(map[string]*WillMessage)
	wh.cancel = make(map[string]context.CancelFunc)
	wh.mu.Unlock()

	wh.wg.Wait()
}

// SetPublishCallback sets the callback for publishing will messages.
func (wh *WillHandler) SetPublishCallback(fn func(username string, topic string, payload []byte, qos uint8, retain bool) error) {
	wh.publishWill = fn
}

// RegisterWill registers a will message for a client.
// Cancels any pending delayed will for the same client.
func (wh *WillHandler) RegisterWill(clientID string, username string, topic string, payload []byte, qos uint8, retain bool, delay time.Duration, conn net.Conn) error {
	wh.mu.Lock()
	defer wh.mu.Unlock()

	// Cancel pending delayed will goroutine for the same client
	if cancel, exists := wh.cancel[clientID]; exists {
		cancel()
		delete(wh.cancel, clientID)
	}

	wh.wills[clientID] = &WillMessage{
		ClientID: clientID,
		Username: username,
		Topic:    topic,
		Payload:  payload,
		QoS:      qos,
		Retain:   retain,
		Delay:    delay,
		Conn:     conn,
	}
	return nil
}

// TriggerWill triggers the will message for a client (on abnormal disconnect).
// When conn is non-nil it must match the connection that registered the will:
// a stale disconnect from a superseded connection must not fire the will of
// the connection that took over the clientID (audit).
func (wh *WillHandler) TriggerWill(clientID string, conn net.Conn) error {
	wh.mu.Lock()
	will, exists := wh.wills[clientID]
	if !exists {
		wh.mu.Unlock()
		return nil
	}
	if conn != nil && will.Conn != nil && will.Conn != conn {
		// Different owner (a newer connection registered its will): not ours
		// to trigger.
		wh.mu.Unlock()
		return nil
	}
	delete(wh.wills, clientID)
	wh.mu.Unlock()
	if will.Delay > 0 {
		// Delayed will message
		ctx, cancel := context.WithCancel(context.Background())
		wh.mu.Lock()
		wh.cancel[clientID] = cancel
		wh.mu.Unlock()
		wh.wg.Add(1)
		go func() {
			defer wh.wg.Done()
			select {
			case <-time.After(will.Delay):
				if err := wh.publishWillMessage(will); err != nil {
					_ = err // will delivery failed (non-critical, broker may already be disconnected)
				}
			case <-ctx.Done():
				// Will was cancelled (client reconnected before delay elapsed)
			}
		}()
	} else {
		if err := wh.publishWillMessage(will); err != nil {
			_ = err // will delivery failed (non-critical, broker may already be disconnected)
		}
	}
	return nil
}

// CancelWill canc a pending delayed will message for a client.
func (wh *WillHandler) CancelWill(clientID string) {
	wh.mu.Lock()
	defer wh.mu.Unlock()

	if cancel, exists := wh.cancel[clientID]; exists {
		cancel()
		delete(wh.cancel, clientID)
	}
	// Also remove from wills if still registered
	delete(wh.wills, clientID)
}

// RemoveWill removes a will message without triggering it (graceful disconnect).
func (wh *WillHandler) RemoveWill(clientID string) {
	wh.mu.Lock()
	defer wh.mu.Unlock()

	delete(wh.wills, clientID)
	if cancel, exists := wh.cancel[clientID]; exists {
		cancel()
		delete(wh.cancel, clientID)
	}
}

func (wh *WillHandler) publishWillMessage(will *WillMessage) error {
	if wh.publishWill != nil && len(will.Topic) > 0 {
		return wh.publishWill(will.Username, will.Topic, will.Payload, will.QoS, will.Retain)
	}
	return nil
}

// WillMessage represents the public info about a registered will.
type WillInfo struct {
	Topic      string
	QoS        uint8
	Retain     bool
	HasPayload bool
}

// GetWillInfo returns will info for a client.
func (wh *WillHandler) GetWillInfo(clientID string) (*WillInfo, bool) {
	wh.mu.Lock()
	defer wh.mu.Unlock()

	will, exists := wh.wills[clientID]
	if !exists {
		return nil, false
	}

	return &WillInfo{
		Topic:      will.Topic,
		QoS:        will.QoS,
		Retain:     will.Retain,
		HasPayload: len(will.Payload) > 0,
	}, true
}
