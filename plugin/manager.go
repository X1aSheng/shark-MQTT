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
type Manager struct {
	mu      sync.RWMutex
	plugins map[Hook][]Plugin
	// hookTimeout bounds a single plugin Execute call. Default 10s; 0 runs
	// plugins synchronously without a timeout (audit: a blocking plugin used
	// to stall the owning connection goroutine indefinitely and could hang
	// broker shutdown).
	hookTimeout time.Duration
}

// NewManager creates a new plugin manager.
func NewManager() *Manager {
	return &Manager{
		plugins:     make(map[Hook][]Plugin),
		hookTimeout: 10 * time.Second,
	}
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

	for _, hook := range p.Hooks() {
		pm.plugins[hook] = append(pm.plugins[hook], p)
	}
}

// Unregister removes every registration of the plugin across all hooks.
func (pm *Manager) Unregister(p Plugin) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

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
	go func() {
		var runErr error
		defer func() {
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
