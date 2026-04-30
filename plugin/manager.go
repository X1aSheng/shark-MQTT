// Package plugin provides a plugin system with hooks for MQTT broker events.
package plugin

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
)

// Hook represents an event hook in the plugin system.
type Hook string

const (
	OnAccept    Hook = "on_accept"
	OnConnected Hook = "on_connected"
	OnMessage   Hook = "on_message"
	OnClose     Hook = "on_close"
)

// Context provides context for hook calls.
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
}

// NewManager creates a new plugin manager.
func NewManager() *Manager {
	return &Manager{
		plugins: make(map[Hook][]Plugin),
	}
}

// Register registers a plugin with the manager.
func (pm *Manager) Register(p Plugin) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for _, hook := range p.Hooks() {
		pm.plugins[hook] = append(pm.plugins[hook], p)
	}
}

// Dispatch dispatches a hook event to all registered plugins.
// It recovers from panics in plugins and continues to remaining plugins.
// Returns a combined error if any plugin fails.
func (pm *Manager) Dispatch(ctx context.Context, hook Hook, data *Context) error {
	pm.mu.RLock()
	plugins := pm.plugins[hook]
	pm.mu.RUnlock()

	var errs []error
	for _, p := range plugins {
		select {
		case <-ctx.Done():
			errs = append(errs, fmt.Errorf("plugin dispatch cancelled: %w", ctx.Err()))
			goto done
		default:
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					errs = append(errs, fmt.Errorf("plugin %s panic: %v\n%s", p.Name(), r, debug.Stack()))
				}
			}()
			if e := p.Execute(ctx, hook, data); e != nil {
				errs = append(errs, fmt.Errorf("plugin %s: %w", p.Name(), e))
			}
		}()
	}
done:
	if len(errs) > 0 {
		return fmt.Errorf("plugin dispatch: %v", errs)
	}
	return nil
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
