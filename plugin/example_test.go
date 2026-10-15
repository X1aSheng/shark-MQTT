package plugin_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/plugin"
)

// AuditPlugin is a worked example of the two mechanisms working together.
//
// It uses both halves of the plugin API:
//
//   - Lifecycle to acquire and release an external resource. Here that is a
//     counter file handle, but it could be a database pool or a socket.
//   - The event bus to observe occurrences without touching the packet path.
//
// A plugin like this could be used for:
//   - a message audit log (EventMessagePublished)
//   - connection accounting (EventClientConnected / EventClientDisconnected)
//   - metrics or tracing export
//
// It implements Managed (Plugin + Lifecycle) so it can be registered and driven
// like any other plugin.
type AuditPlugin struct {
	// Prefix identifies the instance in logs and lifecycle events.
	Prefix string

	// Initialize may not be given a value of nil, so use atomics for counters
	// that hooks update concurrently.
	connects   atomic.Int64
	disconnect atomic.Int64
	published  atomic.Int64

	initialized atomic.Bool
	started     atomic.Bool
	stopped     atomic.Bool
}

// Name implements Plugin.
func (a *AuditPlugin) Name() string {
	if a.Prefix != "" {
		return "audit-" + a.Prefix
	}
	return "audit"
}

// Hooks declares the packet-path callbacks this plugin wants. Audit only cares
// about specific bus events, so it subscribes to none of the four legacy hooks
// -- which is exactly why the Manager keeps a plugin list separate from the
// hook map.
func (a *AuditPlugin) Hooks() []plugin.Hook { return nil }

// Execute implements Plugin. It is required by the interface but unused here.
func (a *AuditPlugin) Execute(context.Context, plugin.Hook, *plugin.Context) error {
	return nil
}

// Initialize opens whatever the plugin needs. It runs before Start and again
// after a Stop on a restart cycle.
func (a *AuditPlugin) Initialize(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.initialized.Store(true)
	return nil
}

// Start activates the plugin. The broker is already listening here, so a
// plugin may safely connect to its own address.
func (a *AuditPlugin) Start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.started.Store(true)
	a.stopped.Store(false)
	return nil
}

// Stop deactivates it. Must tolerate being called even if Start never ran,
// because shutdown can happen after a partially failed startup.
func (a *AuditPlugin) Stop(context.Context) error {
	a.stopped.Store(true)
	return nil
}

// Uninstall releases what Initialize acquired.
func (a *AuditPlugin) Uninstall(context.Context) error {
	a.initialized.Store(false)
	a.stopped.Store(true)
	return nil
}

// Stats returns the counters collected while running.
func (a *AuditPlugin) Stats() (connects, disconnects, published int64) {
	return a.connects.Load(), a.disconnect.Load(), a.published.Load()
}

// SubscribeAll registers this plugin's observer on the bus for the events it
// cares about. Call it after Register and before the broker starts.
func (a *AuditPlugin) SubscribeAll(bus *plugin.Bus) {
	if bus == nil {
		return
	}
	bus.Subscribe(plugin.EventClientConnected, plugin.SubscriberFunc{
		SubscriberName: a.Name() + "-connected",
		Fn: func(_ context.Context, ev plugin.EventData) error {
			a.connects.Add(1)
			return nil
		},
	})
	bus.Subscribe(plugin.EventClientDisconnected, plugin.SubscriberFunc{
		SubscriberName: a.Name() + "-disconnected",
		Fn: func(_ context.Context, ev plugin.EventData) error {
			a.disconnect.Add(1)
			return nil
		},
	})
	bus.Subscribe(plugin.EventMessagePublished, plugin.SubscriberFunc{
		SubscriberName: a.Name() + "-published",
		Fn: func(_ context.Context, ev plugin.EventData) error {
			a.published.Add(1)
			return nil
		},
	})
}

// Status renders a one-line summary, useful in a status endpoint or log line.
func (a *AuditPlugin) Status() string {
	state := "unknown"
	switch {
	case a.stopped.Load():
		state = "stopped"
	case a.started.Load():
		state = "started"
	case a.initialized.Load():
		state = "initialized"
	}
	c, d, p := a.Stats()
	return fmt.Sprintf("%s [%s] connects=%d disconnects=%d published=%d",
		a.Name(), state, c, d, p)
}

// SlowSubscriber demonstrates the failure mode the bus is designed to contain:
// it ignores its context and blocks far past the timeout. The bus abandons it,
// reports the timeout and keeps going, so the broker's publish path is not held
// hostage by a badly written plugin.
type SlowSubscriber struct {
	Block time.Duration
	calls atomic.Int64
}

func (s *SlowSubscriber) Name() string { return "slow-subscriber" }

func (s *SlowSubscriber) Handle(_ context.Context, _ plugin.EventData) error {
	s.calls.Add(1)
	// Deliberately ignores ctx.Done(): this is the bug the bus must survive.
	time.Sleep(s.Block)
	return nil
}

// TestAuditPlugin_EndToEnd shows the intended wiring: build the manager,
// register the plugin, subscribe it to the bus, then drive the lifecycle. It
// doubles as a smoke test that the three pieces fit together.
func TestAuditPlugin_EndToEnd(t *testing.T) {
	mgr := plugin.NewManager()
	audit := &AuditPlugin{Prefix: "demo"}
	mgr.Register(audit)
	audit.SubscribeAll(mgr.Bus())

	// Startup, as the broker does it.
	_ = mgr.StartPlugins(context.Background())

	// Simulate broker activity.
	ctx := context.Background()
	_ = mgr.Bus().Publish(ctx, plugin.EventClientConnected,
		plugin.EventData{ClientID: "sensor-1", Username: "sensor"})
	_ = mgr.Bus().Publish(ctx, plugin.EventMessagePublished,
		plugin.EventData{ClientID: "sensor-1", Topic: "room1/temp", QoS: 1})
	_ = mgr.Bus().Publish(ctx, plugin.EventClientDisconnected,
		plugin.EventData{ClientID: "sensor-1"})

	if got, want := audit.Status(), "audit-demo [started] connects=1 disconnects=1 published=1"; got != want {
		t.Errorf("Status() = %q, want %q", got, want)
	}
	if s := mgr.StageOf(audit.Name()); s != "started" {
		t.Errorf("StageOf = %q, want started", s)
	}

	// Shutdown, as the broker does it.
	if err := mgr.StopPlugins(); err != nil {
		t.Fatalf("StopPlugins: %v", err)
	}
	if s := mgr.StageOf(audit.Name()); s != "uninstalled" {
		t.Errorf("StageOf after stop = %q, want uninstalled", s)
	}
}

// TestBus_SlowSubscriberIsBoundedEndToEnd demonstrates that a misbehaving
// subscriber costs the broker one timeout, not the publish.
func TestBus_SlowSubscriberIsBoundedEndToEnd(t *testing.T) {
	bus := plugin.NewBus()
	bus.SetTimeout(50 * time.Millisecond)

	slow := &SlowSubscriber{Block: 2 * time.Second}
	bus.Subscribe(plugin.EventMessagePublished, slow)

	start := time.Now()
	err := bus.Publish(context.Background(), plugin.EventMessagePublished,
		plugin.EventData{Topic: "a/b"})
	elapsed := time.Since(start)

	if elapsed >= time.Second {
		t.Errorf("Publish took %v; a 2s subscriber must be cut off at the 50ms timeout", elapsed)
	}
	if err == nil {
		t.Error("Publish = nil, want the abandoned subscriber reported")
	}
	if slow.calls.Load() != 1 {
		t.Errorf("slow subscriber called %d times, want 1", slow.calls.Load())
	}
}
