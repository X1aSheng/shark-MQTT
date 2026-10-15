package plugin

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// collect is a Subscriber that records the events it receives.
type collect struct {
	name string
	mu   sync.Mutex
	got  []EventData
	err  error
	slow time.Duration
}

func (c *collect) Name() string { return c.name }

func (c *collect) Handle(_ context.Context, ev EventData) error {
	if c.slow > 0 {
		time.Sleep(c.slow)
	}
	c.mu.Lock()
	c.got = append(c.got, ev)
	c.mu.Unlock()
	return c.err
}

func (c *collect) events() []EventData {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]EventData, len(c.got))
	copy(out, c.got)
	return out
}

// TestBus_DeliversToSubscriber is the basic contract.
func TestBus_DeliversToSubscriber(t *testing.T) {
	b := NewBus()
	sub := &collect{name: "s1"}
	b.Subscribe(EventClientConnected, sub)

	if err := b.Publish(context.Background(), EventClientConnected, EventData{ClientID: "c1", Username: "u1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := sub.events()
	if len(got) != 1 {
		t.Fatalf("received %d events, want 1", len(got))
	}
	// The bus stamps the event kind so a subscriber cannot receive a payload
	// whose Event does not match the dispatch.
	if got[0].Event != EventClientConnected {
		t.Errorf("Event = %q, want %q", got[0].Event, EventClientConnected)
	}
	if got[0].ClientID != "c1" {
		t.Errorf("ClientID = %q, want c1", got[0].ClientID)
	}
	if b.Dispatched() != 1 {
		t.Errorf("Dispatched = %d, want 1", b.Dispatched())
	}
}

// TestBus_NoSubscribersIsCheapNoOp matters on the publish path: the common case
// is nobody listening.
func TestBus_NoSubscribersIsCheapNoOp(t *testing.T) {
	b := NewBus()
	if err := b.Publish(context.Background(), EventClientConnected, EventData{Topic: "t"}); err != nil {
		t.Errorf("Publish with no subscribers = %v, want nil", err)
	}
}

// TestBus_SubscriberFailureIsReported proves one bad subscriber does not stop
// the others -- the property that makes events safe to put on the publish path.
func TestBus_SubscriberFailureIsReported(t *testing.T) {
	b := NewBus()
	bad := &collect{name: "bad", err: errors.New("subscriber exploded")}
	good := &collect{name: "good"}
	b.Subscribe(EventMessagePublished, bad)
	b.Subscribe(EventMessagePublished, good)

	err := b.Publish(context.Background(), EventMessagePublished, EventData{Topic: "a/b"})
	if err == nil {
		t.Fatal("Publish = nil, want the bad subscriber reported")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("error does not name the failing subscriber: %v", err)
	}
	if len(good.events()) != 1 {
		t.Errorf("healthy subscriber received %d events, want 1", len(good.events()))
	}
	if b.Dropped() != 1 {
		t.Errorf("Dropped = %d, want 1", b.Dropped())
	}
}

// TestBus_PanicIsContained: a panicking subscriber must not take the process
// down -- the broker is in the middle of a publish.
func TestBus_PanicIsContained(t *testing.T) {
	b := NewBus()
	b.Subscribe(EventClientConnected, SubscriberFunc{
		SubscriberName: "panicky",
		Fn:             func(context.Context, EventData) error { panic("kaboom") },
	})
	good := &collect{name: "good"}
	b.Subscribe(EventClientConnected, good)

	err := b.Publish(context.Background(), EventClientConnected, EventData{ClientID: "c"})
	if err == nil {
		t.Fatal("Publish = nil, want the panic reported")
	}
	if len(good.events()) != 1 {
		t.Errorf("healthy subscriber received %d events, want 1", len(good.events()))
	}
}

// TestBus_SlowSubscriberIsBounded is the guarantee that matters most for a
// broker: an observer cannot hold the publish path hostage.
func TestBus_SlowSubscriberIsBounded(t *testing.T) {
	b := NewBus()
	b.SetTimeout(50 * time.Millisecond)
	slow := &collect{name: "slow", slow: 2 * time.Second}
	fast := &collect{name: "fast"}
	b.Subscribe(EventMessagePublished, slow)
	b.Subscribe(EventMessagePublished, fast)

	start := time.Now()
	err := b.Publish(context.Background(), EventMessagePublished, EventData{Topic: "t"})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Publish = nil, want the slow subscriber reported")
	}
	if elapsed > time.Second {
		t.Errorf("Publish took %v; a slow subscriber must be bounded", elapsed)
	}
	if len(fast.events()) != 1 {
		t.Errorf("fast subscriber received %d events, want 1", len(fast.events()))
	}
}

// TestBus_Unsubscribe stops delivery.
func TestBus_Unsubscribe(t *testing.T) {
	b := NewBus()
	sub := &collect{name: "s"}
	b.Subscribe(EventSubscribed, sub)
	b.Unsubscribe(EventSubscribed, sub)

	if err := b.Publish(context.Background(), EventClientConnected, EventData{}); err != nil {
		t.Fatal(err)
	}
	if len(sub.events()) != 0 {
		t.Errorf("received %d events after Unsubscribe, want 0", len(sub.events()))
	}
	if names := b.Subscribers(EventSubscribed); len(names) != 0 {
		t.Errorf("Subscribers = %v, want empty", names)
	}
}

// TestBus_SubscribeAll: one Subscriber can serve several events.
func TestBus_SubscribeAll(t *testing.T) {
	b := NewBus()
	sub := &collect{name: "watcher"}
	b.Subscribe(EventSubscribed, sub)
	b.Subscribe(EventUnsubscribed, sub)

	_ = b.Publish(context.Background(), EventSubscribed, EventData{})
	_ = b.Publish(context.Background(), EventUnsubscribed, EventData{})
	if got := sub.events(); len(got) != 2 {
		t.Errorf("received %d events, want 2", len(got))
	}
}

// TestBus_StopIsIdempotentAndSilencesFurtherPublishes covers shutdown ordering:
// once the bus is stopped, no plugin receives events.
func TestBus_StopIsIdempotentAndSilencesFurtherPublishes(t *testing.T) {
	b := NewBus()
	sub := &collect{name: "s"}
	b.Subscribe(EventClientConnected, sub)

	b.Stop()
	b.Stop() // must not panic

	if err := b.Publish(context.Background(), EventClientConnected, EventData{}); err != nil {
		t.Errorf("Publish after Stop = %v, want nil", err)
	}
	if len(sub.events()) != 0 {
		t.Errorf("received %d events after Stop, want 0", len(sub.events()))
	}
	if b.Started() {
		t.Error("Started() = true after Stop")
	}
}

// TestBus_ConcurrentPublish is the race-detector target: subscriptions are read
// under RLock while a publisher may be iterating a copy.
func TestBus_ConcurrentPublish(t *testing.T) {
	b := NewBus()
	sub := &collect{name: "s"}
	b.Subscribe(EventMessagePublished, sub)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = b.Publish(context.Background(), EventClientConnected, EventData{Topic: "t"})
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				b.Unsubscribe(EventMessagePublished, sub)
				b.Subscribe(EventMessagePublished, sub)
			}
		}()
	}
	wg.Wait()
}

// TestBus_ManagerBusIsNeverNil guards the accessor used by the broker.
func TestBus_ManagerBusIsNeverNil(t *testing.T) {
	pm := NewManager()
	if pm.Bus() == nil {
		t.Fatal("Bus() = nil after NewManager")
	}
	// A Manager built as a zero value must still work.
	var zero Manager
	if zero.Bus() == nil {
		t.Fatal("Bus() = nil on a zero-value Manager")
	}
}
