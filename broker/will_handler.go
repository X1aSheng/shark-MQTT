// Package broker provides the core MQTT message broker.
package broker

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// WillMessage represents a last will message.
type WillMessage struct {
	ClientID string
	Username string // owner username, used for publish authorization
	Topic    string
	Payload  []byte
	QoS      uint8
	Retain   bool

	// Properties is the property set carried by the Will Message's PUBLISH.
	//
	// MQTT 5.0 §3.1.3.2 gives the Will Message a property section of its own,
	// and §3.3.2.3 makes the Server a transparent relay for the per-message
	// subset of it: the Payload Format Indicator, Message Expiry Interval,
	// Content Type, Response Topic, Correlation Data and every User Property
	// travel with the Will Message exactly as they would with an ordinary
	// PUBLISH. Will Delay Interval is the one member of that set which is NOT
	// relayed -- it tells the Server *when* to publish (§3.1.3.2.2) and is not
	// a PUBLISH property at all (§3.3.2.3) -- so it is removed before the set
	// is stored here. See relayableWillProperties.
	Properties *protocol.Properties

	// Delay is the effective wait before the Will Message is published: the
	// client's Will Delay Interval as bounded by server policy and by the
	// Session's own lifetime. §3.1.3.2.2 requires publication when "the Will
	// Delay Interval has passed or the Session ends, whichever happens first",
	// so a wait longer than the Session is not a wait at all; the broker folds
	// that into this value when the will is registered. See HandleConnection.
	Delay time.Duration

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
	publishWill func(username string, topic string, payload []byte, qos uint8, retain bool, props *protocol.Properties) error
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
func (wh *WillHandler) SetPublishCallback(fn func(username string, topic string, payload []byte, qos uint8, retain bool, props *protocol.Properties) error) {
	wh.publishWill = fn
}

// RegisterWill registers a will message for a client.
// Cancels any pending delayed will for the same client.
//
// props is the Will Properties section as decoded from the CONNECT, or nil for
// MQTT 3.1.1 and for a CONNECT without properties; Will Delay Interval is
// ignored here because delay already carries the effective wait.
func (wh *WillHandler) RegisterWill(clientID string, username string, topic string, payload []byte, qos uint8, retain bool, props *protocol.Properties, delay time.Duration, conn net.Conn) error {
	wh.mu.Lock()
	defer wh.mu.Unlock()

	// A new CONNECT for this ClientID replaces whatever was registered for it,
	// and any delayed publication still waiting must not outlive that
	// replacement.
	wh.stopTimerLocked(clientID)

	wh.wills[clientID] = &WillMessage{
		ClientID:   clientID,
		Username:   username,
		Topic:      topic,
		Payload:    payload,
		QoS:        qos,
		Retain:     retain,
		Properties: relayableWillProperties(props),
		Delay:      delay,
		Conn:       conn,
	}
	return nil
}

// relayableWillProperties extracts the Will Properties that belong on the Will
// Message's PUBLISH, dropping the ones that belong to the CONNECT.
//
// §3.1.3.2 defines exactly seven Will Properties and §3.3.2.3 accepts six of
// them unchanged on a PUBLISH. Will Delay Interval (0x18) is the odd one out:
// it is an instruction to the Server (§3.1.3.2.2) and is not in the PUBLISH
// property set, so relaying it would be a Protocol Error on the wire. The
// Authentication Method/Data pair is not a Will Property at all, so nothing
// else has to be filtered here.
//
// The returned set is nil when there is nothing to relay, which keeps a v3.1.1
// or property-free will byte-identical to the pre-existing packet instead of
// growing an empty property section.
//
// UserProperties is shared with the decoded CONNECT rather than deep-copied:
// the packet outlives the will, and the slice is only ever read.
func relayableWillProperties(props *protocol.Properties) *protocol.Properties {
	if props == nil {
		return nil
	}
	out := &protocol.Properties{
		PayloadFormat:         props.PayloadFormat,
		MessageExpiryInterval: props.MessageExpiryInterval,
		ContentType:           props.ContentType,
		ResponseTopic:         props.ResponseTopic,
		CorrelationData:       props.CorrelationData,
		UserProperties:        props.UserProperties,
	}
	if out.PayloadFormat == nil && out.MessageExpiryInterval == nil &&
		out.ContentType == "" && out.ResponseTopic == "" && out.CorrelationData == nil &&
		len(out.UserProperties) == 0 {
		return nil
	}
	return out
}

// TriggerWill triggers the will message for a client (on abnormal disconnect).
// When conn is non-nil it must match the connection that registered the will:
// a stale disconnect from a superseded connection must not fire the will of
// the connection that took over the clientID (audit).
//
// A will with a delay is armed rather than published, and stays registered
// while it waits so that FireWill can still publish it if the Session ends
// first. Triggering an already-armed will is a no-op: the takeover path and
// the superseded readLoop's late error both reach here.
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
	if _, armed := wh.cancel[clientID]; armed {
		wh.mu.Unlock()
		return nil
	}
	if will.Delay <= 0 {
		delete(wh.wills, clientID)
		wh.mu.Unlock()
		if err := wh.publishWillMessage(will); err != nil {
			_ = err // will delivery failed (non-critical, broker may already be disconnected)
		}
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	wh.cancel[clientID] = cancel
	wh.wg.Add(1)
	wh.mu.Unlock()

	go func() {
		defer wh.wg.Done()
		select {
		case <-time.After(will.Delay):
			// Re-check ownership under the lock: a replacement registration
			// (or FireWill/CancelWill racing the timer) may have taken the
			// will over, and publishing it then would duplicate a will that
			// another path already sent or deliberately suppressed.
			wh.mu.Lock()
			current, ok := wh.wills[clientID]
			ours := ok && current == will
			if ours {
				delete(wh.wills, clientID)
				delete(wh.cancel, clientID)
			}
			wh.mu.Unlock()
			if !ours {
				return
			}
			if err := wh.publishWillMessage(will); err != nil {
				_ = err // will delivery failed (non-critical, broker may already be disconnected)
			}
		case <-ctx.Done():
			// Will was cancelled (client reconnected before delay elapsed)
		}
	}()
	return nil
}

// FireWill publishes a will that is waiting out its Will Delay Interval, and
// is a no-op when nothing is pending.
//
// §3.1.3.2.2: "The Server delays publishing the Client's Will Message until the
// Will Delay Interval has passed or the Session ends, whichever happens first."
// A new connection with Clean Start set to 1 ends the existing Session
// (§3.1.4), and so does a take-over under it, so a publication still waiting at
// that moment must be sent rather than left to its timer -- the will is part of
// the Session state and cannot outlive it.
//
// Only an *armed* publication is fired. A will that was merely registered for a
// live connection is left alone: this method is called from the CONNECT path,
// where the connection that owns an untriggered will may still be reading.
func (wh *WillHandler) FireWill(clientID string) {
	wh.mu.Lock()
	will, exists := wh.wills[clientID]
	_, armed := wh.cancel[clientID]
	if !exists || !armed {
		wh.mu.Unlock()
		return
	}
	wh.stopTimerLocked(clientID)
	delete(wh.wills, clientID)
	wh.mu.Unlock()

	if err := wh.publishWillMessage(will); err != nil {
		_ = err // will delivery failed (non-critical, broker may already be disconnected)
	}
}

// CancelWill canc a pending delayed will message for a client.
func (wh *WillHandler) CancelWill(clientID string) {
	wh.mu.Lock()
	defer wh.mu.Unlock()

	wh.stopTimerLocked(clientID)
	// Also remove from wills if still registered
	delete(wh.wills, clientID)
}

// RemoveWill removes a will message without triggering it (graceful disconnect).
func (wh *WillHandler) RemoveWill(clientID string) {
	wh.mu.Lock()
	defer wh.mu.Unlock()

	delete(wh.wills, clientID)
	wh.stopTimerLocked(clientID)
}

// stopTimerLocked cancels the armed delayed publication of one client, if any.
// The caller holds wh.mu. Cancelling an already-fired timer is a no-op, so a
// stale entry cannot be told apart from a live one and is removed either way.
func (wh *WillHandler) stopTimerLocked(clientID string) {
	if cancel, exists := wh.cancel[clientID]; exists {
		cancel()
		delete(wh.cancel, clientID)
	}
}

func (wh *WillHandler) publishWillMessage(will *WillMessage) error {
	if wh.publishWill != nil && len(will.Topic) > 0 {
		return wh.publishWill(will.Username, will.Topic, will.Payload, will.QoS, will.Retain, will.Properties)
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
