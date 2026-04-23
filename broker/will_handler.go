// Package broker provides the core MQTT message broker.
package broker

import (
	"context"
	"sync"
	"time"
)

// WillMessage represents a last will message.
type WillMessage struct {
	ClientID string
	Topic    string
	Payload  []byte
	QoS      uint8
	Retain   bool
	Delay    time.Duration // Delayed will message delivery
}

// WillHandler manages last will messages for clients that disconnect abnormally.
type WillHandler struct {
	mu     sync.Mutex
	wills  map[string]*WillMessage
	cancel map[string]context.CancelFunc

	// Callback to publish will message
	publishWill func(topic string, payload []byte, qos uint8, retain bool) error
}

// NewWillHandler creates a new WillHandler.
func NewWillHandler() *WillHandler {
	return &WillHandler{
		wills:  make(map[string]*WillMessage),
		cancel: make(map[string]context.CancelFunc),
	}
}

// Stop cancels all pending delayed will messages.
func (wh *WillHandler) Stop() {
	wh.mu.Lock()
	defer wh.mu.Unlock()

	for _, cancel := range wh.cancel {
		cancel()
	}
	wh.wills = make(map[string]*WillMessage)
	wh.cancel = make(map[string]context.CancelFunc)
}

// SetPublishCallback sets the callback for publishing will messages.
func (wh *WillHandler) SetPublishCallback(fn func(topic string, payload []byte, qos uint8, retain bool) error) {
	wh.publishWill = fn
}

// RegisterWill registers a will message for a client.
func (wh *WillHandler) RegisterWill(clientID string, topic string, payload []byte, qos uint8, retain bool, delay time.Duration) {
	wh.mu.Lock()
	defer wh.mu.Unlock()

	wh.wills[clientID] = &WillMessage{
		ClientID: clientID,
		Topic:    topic,
		Payload:  payload,
		QoS:      qos,
		Retain:   retain,
		Delay:    delay,
	}
}

// TriggerWill triggers the will message for a client (on abnormal disconnect).
func (wh *WillHandler) TriggerWill(clientID string) error {
	wh.mu.Lock()
	will, exists := wh.wills[clientID]
	if !exists {
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

		go func() {
			select {
			case <-time.After(will.Delay):
				wh.publishWillMessage(will)
			case <-ctx.Done():
				// Will was cancelled (client reconnected before delay elapsed)
			}
		}()
	} else {
		return wh.publishWillMessage(will)
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
		return wh.publishWill(will.Topic, will.Payload, will.QoS, will.Retain)
	}
	return nil
}

// WillMessage represents the public info about a registered will.
type WillInfo struct {
	Topic   string
	QoS     uint8
	Retain  bool
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
		Topic:        will.Topic,
		QoS:          will.QoS,
		Retain:       will.Retain,
		HasPayload: len(will.Payload) > 0,
	}, true
}
