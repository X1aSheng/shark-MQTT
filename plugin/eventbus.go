// Package plugin provides a plugin system for the MQTT broker: a lifecycle
// for extension objects and an event bus for observation.
//
// # Two independent mechanisms
//
// They answer different questions and are deliberately not merged:
//
//   - Lifecycle drives objects the broker OWNS. It is ordered, synchronous and
//     exactly-once per transition: Install -> Start -> Stop -> Uninstall. A
//     plugin that opens a database connection needs this.
//   - The event bus reports things that HAPPENED. It is unordered, may be
//     invoked concurrently, and a subscriber must not be able to affect the
//     publish path.
//
// Collapsing them (one hook that does both) is the usual mistake: it forces
// every observer to be ordered and every owner to be fire-and-forget.
//
// # Failure isolation
//
// A misbehaving plugin cannot take the broker down. Panics are recovered,
// calls are bounded by a timeout, and a failure in one plugin does not stop the
// others. A plugin's failure is reported, never swallowed.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Event identifies a kind of broker occurrence. The zero value is not a valid
// event; use the constants below.
type Event string

const (
	// EventBrokerStarted fires once the broker's subsystems are running.
	EventBrokerStarted Event = "broker_started"
	// EventBrokerStopped fires when the broker has fully shut down.
	EventBrokerStopped Event = "broker_stopped"

	// EventClientConnected fires after a CONNECT has been accepted and the
	// session is registered.
	EventClientConnected Event = "client_connected"
	// EventClientDisconnected fires after a connection has been torn down and
	// its session state settled.
	EventClientDisconnected Event = "client_disconnected"

	// EventMessagePublished fires for each inbound PUBLISH, before it is
	// routed to subscribers.
	EventMessagePublished Event = "message_published"
	// EventMessageDelivered fires once per outbound delivery to one client.
	EventMessageDelivered Event = "message_delivered"

	// EventSubscribed fires after a SUBSCRIBE has been applied.
	EventSubscribed Event = "subscribed"
	// EventUnsubscribed fires after an UNSUBSCRIBE has been applied.
	EventUnsubscribed Event = "unsubscribed"

	// EventRetainedChanged fires when a retained message is stored or removed.
	EventRetainedChanged Event = "retained_changed"

	// Lifecycle events. ClientID carries the plugin name.
	EventPluginInitialized Event = "plugin_initialized"
	EventPluginStarted     Event = "plugin_started"
	EventPluginStopped     Event = "plugin_stopped"
	EventPluginUninstalled Event = "plugin_uninstalled"
)

// EventData is the payload delivered with an event.
//
// It is a struct rather than per-event types because subscribers almost always
// want a subset of the same fields, and one shape keeps the bus generic. Fields
// that do not apply to an event are left at their zero value; Event says which
// ones are meaningful.
type EventData struct {
	// Event identifies what happened.
	Event Event
	// ClientID is the session the event concerns, empty for broker-wide events.
	ClientID string
	// Username is the authenticated user, when known.
	Username string
	// RemoteAddr is the peer address for connection events.
	RemoteAddr string
	// Topic and Payload are set for message events.
	Topic   string
	Payload []byte
	// QoS and Retain mirror the MQTT PUBLISH flags.
	QoS    uint8
	Retain bool
	// Filter is the topic filter for subscribe/unsubscribe events.
	Filter string
	// GrantedQoS is the QoS actually granted for a subscription.
	GrantedQoS uint8
	// Count carries an aggregate for events that describe a quantity
	// (for example the number of affected sessions at shutdown).
	Count int
	// Err is set for events reporting a failure.
	Err error
}

// Subscriber receives events from the bus.
//
// Implementations must be safe for concurrent use: the bus may deliver to one
// subscriber from several goroutines.
//
// Handle must return quickly. It runs under the bus's timeout, and a subscriber
// that blocks past it is reported and abandoned for that event -- the broker
// does not wait for it.
type Subscriber interface {
	Name() string
	Handle(ctx context.Context, ev EventData) error
}

// SubscriberFunc adapts a function to Subscriber.
type SubscriberFunc struct {
	// Name identifies the subscriber in errors and diagnostics.
	SubscriberName string
	Fn             func(ctx context.Context, ev EventData) error
}

func (s SubscriberFunc) Name() string { return s.SubscriberName }

func (s SubscriberFunc) Handle(ctx context.Context, ev EventData) error {
	if s.Fn == nil {
		return nil
	}
	return s.Fn(ctx, ev)
}

// Bus delivers events to subscribers.
//
// A Bus is safe for concurrent use. Zero value is not usable; call NewBus.
type Bus struct {
	mu   sync.RWMutex
	subs map[Event][]Subscriber

	// timeout bounds one Handle call. Default 5s; 0 dispatches synchronously
	// with no timeout, which is only appropriate in tests.
	timeout time.Duration
	// dropped counts events whose delivery was abandoned, so a saturated
	// subsystem shows up as a metric rather than as missing data.
	dropped atomic.Int64
	// dispatched counts successful deliveries.
	dispatched atomic.Int64
	// stopped makes Publish a no-op after Stop, so a broker shutting down does
	// not push events at plugins that are already gone.
	stopped atomic.Bool
}

// NewBus creates a running event bus.
func NewBus() *Bus {
	return &Bus{
		subs:    make(map[Event][]Subscriber),
		timeout: 5 * time.Second,
	}
}

// SetTimeout bounds a single Handle call. 0 disables the bound.
func (b *Bus) SetTimeout(d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.timeout = d
}

// Subscribe registers s for ev. Subscribing the same instance twice for the
// same event delivers twice; callers that need exactly-once should subscribe
// once. The same subscriber may serve several events.
func (b *Bus) Subscribe(ev Event, s Subscriber) {
	if s == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[ev] = append(b.subs[ev], s)
}

// Unsubscribe removes every registration of s for ev.
func (b *Bus) Unsubscribe(ev Event, s Subscriber) {
	if s == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	list := b.subs[ev]
	kept := list[:0]
	for _, existing := range list {
		if existing == s {
			continue
		}
		kept = append(kept, existing)
	}
	if len(kept) == 0 {
		delete(b.subs, ev)
	} else {
		b.subs[ev] = kept
	}
}

// Subscribers returns the registered subscriber names for ev.
func (b *Bus) Subscribers(ev Event) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	list := b.subs[ev]
	names := make([]string, 0, len(list))
	for _, s := range list {
		names = append(names, s.Name())
	}
	return names
}

// Publish delivers ev to the subscribers registered for kind and returns a
// joined error describing every failure.
//
// The event kind is a separate argument rather than a field of ev on purpose.
// Reading it from the payload would let a call site omit it, and the bus would
// then look up subscribers for the empty string and silently deliver nothing --
// a no-op indistinguishable from "nobody subscribed", and impossible to notice
// in production. Here the compiler enforces it.
//
// It never panics and never blocks past the bus timeout: a subscriber that
// hangs is abandoned for that event, counted in Dropped, and reported as an
// error. That is deliberate -- the publish path must not be held hostage by an
// observer.
//
// Publishing to an event with no subscribers is a cheap no-op.
func (b *Bus) Publish(ctx context.Context, kind Event, ev EventData) error {
	if b.stopped.Load() {
		return nil
	}
	// Stamp so a subscriber can trust Event matches where it was delivered.
	ev.Event = kind

	b.mu.RLock()
	subs := b.subs[kind]
	timeout := b.timeout
	// Copy so a subscriber subscribing or unsubscribing during delivery does
	// not race with this loop.
	list := make([]Subscriber, len(subs))
	copy(list, subs)
	b.mu.RUnlock()

	if len(list) == 0 {
		return nil
	}

	var errs []error
	for _, s := range list {
		if err := b.deliver(ctx, s, ev, timeout); err != nil {
			// Name the subscriber even when its own error does not. An operator
			// reading a log needs to know WHICH plugin failed; "subscriber
			// exploded" without a name is not actionable.
			errs = append(errs, fmt.Errorf("subscriber %s: %w", s.Name(), err))
		}
	}
	if len(errs) > 0 {
		b.dropped.Add(int64(len(errs)))
		return fmt.Errorf("event bus %s: %w", kind, errors.Join(errs...))
	}
	b.dispatched.Add(int64(len(list)))
	return nil
}

// deliver invokes one subscriber with panic recovery and a timeout.
func (b *Bus) deliver(ctx context.Context, s Subscriber, ev EventData, timeout time.Duration) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panicked on %s: %v", ev.Event, r)
		}
	}()

	if timeout <= 0 {
		return s.Handle(ctx, ev)
	}

	handleCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// The result is written exactly once: the deferred recover assigns it and
	// the normal path assigns it, but never both. A second send would block on a
	// full channel and leak the goroutine.
	var result error
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				result = fmt.Errorf("panicked on %s: %v", ev.Event, r)
			}
		}()
		result = s.Handle(handleCtx, ev)
	}()

	select {
	case <-done:
		return result
	case <-handleCtx.Done():
		return fmt.Errorf("timed out after %v on %s", timeout, ev.Event)
	}
}

// Dropped returns the number of failed or abandoned deliveries.
func (b *Bus) Dropped() int64 { return b.dropped.Load() }

// Dispatched returns the number of successful deliveries.
func (b *Bus) Dispatched() int64 { return b.dispatched.Load() }

// Stop makes further Publish calls no-ops. Subscribers are not notified; a
// plugin that needs teardown implements the lifecycle instead.
func (b *Bus) Stop() { b.stopped.Store(true) }

// Started reports whether the bus still accepts events.
func (b *Bus) Started() bool { return !b.stopped.Load() }
