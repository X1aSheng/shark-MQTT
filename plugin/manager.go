// Package plugin provides a plugin system with hooks for MQTT broker events.
package plugin

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"
)

// Hook represents an event hook in the plugin system.
type Hook string

const (
	// OnAccept fires once per accepted connection, before CONNECT is read.
	OnAccept Hook = "on_accept"
	// OnConnected fires once a CONNECT has been accepted, just before the
	// CONNACK is sent.
	OnConnected Hook = "on_connected"
	// OnMessage fires for every decoded packet, not only PUBLISH.
	OnMessage Hook = "on_message"
	// OnClose fires once when a connection is torn down.
	OnClose Hook = "on_close"
)

// Context provides context for hook calls.
//
// Each hook populates a different subset of these fields; anything not listed
// for a hook is left at its zero value. Read the table before relying on a
// field -- assuming a field is set because it exists here is how the documented
// plugin examples ended up reading an always-empty Topic on OnMessage.
//
//	Hook         | populated
//	-------------+---------------------------------------------------------
//	OnAccept     | RemoteAddr
//	OnConnected  | ClientID, Username
//	OnMessage    | ClientID, and Topic/Payload/QoS/Retain when the packet is
//	             | a PUBLISH (OnMessage covers every packet type)
//	OnClose      | ClientID
//
// Err is reserved for a hook that needs to report a failure; the broker does
// not set it on any hook today.
type Context struct {
	ClientID   string
	Username   string
	Topic      string
	Payload    []byte
	QoS        uint8
	Retain     bool
	RemoteAddr string
	Err        error
}

// Plugin is the interface for MQTT broker plugins.
type Plugin interface {
	Name() string
	Hooks() []Hook
	Execute(ctx context.Context, hook Hook, data *Context) error
}

// Manager manages plugins and dispatches hook events.
// maxConcurrentHooks caps concurrent plugin hook executions. It is generous
// enough for a broker at full rate with a responsive plugin, and small enough
// that a plugin ignoring its context cannot grow goroutines without bound.
const maxConcurrentHooks = 256

type Manager struct {
	mu      sync.RWMutex
	plugins map[Hook][]Plugin
	// all holds every registered plugin regardless of which hooks it declared.
	//
	// It exists because a plugin may legitimately declare NO hooks and only
	// implement Lifecycle (or only subscribe to the event bus). Deriving the
	// plugin set from the hook map would silently drop those, and their
	// Initialize would never run.
	all []Plugin
	// hookTimeout bounds a single plugin Execute call. Default 10s; 0 runs
	// plugins synchronously without a timeout (audit: a blocking plugin used
	// to stall the owning connection goroutine indefinitely and could hang
	// broker shutdown).
	hookTimeout time.Duration

	// bus carries broker events to subscribers. It is separate from the hook
	// dispatch above: hooks are ordered callbacks on the packet path, while bus
	// events are unordered observations. See the package doc.
	bus *Bus

	// stages tracks per-plugin lifecycle progress so a stage is never run twice
	// and teardown is correct after a partially failed startup. Guarded by
	// stageMu rather than mu, so observing a stage never contends with hook
	// dispatch.
	stageMu sync.RWMutex
	stages  map[string]stage

	// execSlots bounds how many plugin hook executions may be in flight at
	// once. A plugin that ignores its context keeps its goroutine alive after
	// the hook timeout, so without a cap a packet flood against a stuck plugin
	// leaks one goroutine per packet until the process runs out of memory. At
	// the cap the hook is refused, which surfaces through the broker's error
	// metric instead of showing up as an invisible leak. Nil means uncapped
	// (zero-value Manager).
	execSlots chan struct{}
}

// NewManager creates a new plugin manager.
func NewManager() *Manager {
	return &Manager{
		plugins:     make(map[Hook][]Plugin),
		hookTimeout: 10 * time.Second,
		bus:         NewBus(),
		stages:      make(map[string]stage),
		execSlots:   make(chan struct{}, maxConcurrentHooks),
	}
}

// Bus returns the event bus for subscribing to broker occurrences.
//
// Never nil after NewManager.
func (pm *Manager) Bus() *Bus {
	if pm == nil {
		return nil
	}
	if pm.bus == nil {
		// Defensive: a Manager built as a zero value rather than by
		// NewManager would otherwise panic on Subscribe.
		pm.bus = NewBus()
	}
	return pm.bus
}

// SetHookTimeout sets the per-hook execution timeout. 0 disables it.
func (pm *Manager) SetHookTimeout(d time.Duration) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.hookTimeout = d
}

// Register registers a plugin with the manager.
func (pm *Manager) Register(p Plugin) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.all = append(pm.all, p)
	for _, hook := range p.Hooks() {
		pm.plugins[hook] = append(pm.plugins[hook], p)
	}
}

// Unregister removes every registration of the plugin across all hooks.
func (pm *Manager) Unregister(p Plugin) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	keptAll := pm.all[:0]
	for _, registered := range pm.all {
		if registered != p {
			keptAll = append(keptAll, registered)
		}
	}
	pm.all = keptAll

	for hook, list := range pm.plugins {
		kept := list[:0]
		for _, registered := range list {
			if registered == p {
				continue
			}
			kept = append(kept, registered)
		}
		if len(kept) == 0 {
			delete(pm.plugins, hook)
		} else {
			pm.plugins[hook] = kept
		}
	}
}

// Dispatch dispatches a hook event to all registered plugins.
// It recovers from panics in plugins and continues to remaining plugins, and
// bounds each plugin's execution by the manager's hook timeout so a stuck
// plugin cannot stall the broker's connection goroutines or shutdown (audit).
// Returns a combined error if any plugin fails.
func (pm *Manager) Dispatch(ctx context.Context, hook Hook, data *Context) error {
	pm.mu.RLock()
	plugins := pm.plugins[hook]
	timeout := pm.hookTimeout
	pm.mu.RUnlock()

	var errs []error
	for _, p := range plugins {
		if err := pm.run(ctx, p, hook, data, timeout); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("plugin dispatch: %v", errs)
	}
	return nil
}

// run executes one plugin hook, recovering panics and enforcing the hook
// timeout. The plugin runs on its own goroutine only when a timeout is set,
// so a plugin-free broker keeps zero extra goroutine overhead.
func (pm *Manager) run(ctx context.Context, p Plugin, hook Hook, data *Context, timeout time.Duration) error {
	if timeout <= 0 {
		var runErr error
		func() {
			defer func() {
				if r := recover(); r != nil {
					runErr = fmt.Errorf("plugin %s panic: %v\n%s", p.Name(), r, debug.Stack())
				}
			}()
			if e := p.Execute(ctx, hook, data); e != nil {
				runErr = fmt.Errorf("plugin %s: %w", p.Name(), e)
			}
		}()
		return runErr
	}

	hookCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result := make(chan error, 1)

	// Bound the number of concurrent executions before starting another
	// goroutine. A plugin that ignores hookCtx survives the timeout, so without
	// this cap a stuck plugin under load leaks one goroutine per packet.
	if pm.execSlots != nil {
		select {
		case pm.execSlots <- struct{}{}:
		default:
			return fmt.Errorf("plugin %s: hook dropped, %d executions already in flight", p.Name(), maxConcurrentHooks)
		}
	}

	go func() {
		var runErr error
		defer func() {
			if pm.execSlots != nil {
				<-pm.execSlots
			}
			if r := recover(); r != nil {
				runErr = fmt.Errorf("plugin %s panic: %v\n%s", p.Name(), r, debug.Stack())
			}
			result <- runErr
		}()
		if e := p.Execute(hookCtx, hook, data); e != nil {
			runErr = fmt.Errorf("plugin %s: %w", p.Name(), e)
		}
	}()
	select {
	case err := <-result:
		return err
	case <-hookCtx.Done():
		return fmt.Errorf("plugin %s: hook timed out after %v", p.Name(), timeout)
	}
}

// RegisteredPlugins returns the list of registered plugins.
func (pm *Manager) RegisteredPlugins() []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	seen := make(map[string]struct{})
	var names []string
	for _, plugins := range pm.plugins {
		for _, p := range plugins {
			if _, ok := seen[p.Name()]; !ok {
				seen[p.Name()] = struct{}{}
				names = append(names, p.Name())
			}
		}
	}
	return names
}
