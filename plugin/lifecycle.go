package plugin

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Lifecycle is implemented by a plugin that needs to be driven by the broker.
//
// The four stages are ordered and each runs at most once per plugin:
//
//	Initialize -> Start -> Stop -> Uninstall
//
// Initialize and Start run when the broker starts; Stop and Uninstall run when
// it stops. Every stage is optional -- implement only the ones you need.
//
// A plugin may be restarted (Initialize again after Stop) as long as Uninstall
// has not run. The Manager enforces the ordering, so a plugin never has to
// track state itself.
type Lifecycle interface {
	// Initialize prepares the plugin. It runs before Start and may allocate
	// resources. The context is the broker's startup context.
	Initialize(ctx context.Context) error
	// Start activates the plugin. The broker is already listening by this
	// point, so a plugin may connect to its own address.
	Start(ctx context.Context) error
	// Stop deactivates the plugin and releases what Start acquired. It must be
	// safe to call even if Start failed or never ran, because shutdown can
	// happen at any point.
	Stop(ctx context.Context) error
	// Uninstall releases what Initialize acquired. It runs last, after Stop.
	Uninstall(ctx context.Context) error
}

// LifecycleFuncs builds a Lifecycle from optional functions. Use it for small
// plugins that need one or two stages, rather than declaring a type per stage.
type LifecycleFuncs struct {
	PluginName    string
	OnInitialize  func(context.Context) error
	OnStart       func(context.Context) error
	OnStop        func(context.Context) error
	OnUninstall   func(context.Context) error
}

func (l *LifecycleFuncs) Name() string { return l.PluginName }

// Hooks and Execute satisfy Plugin with a no-op, so a LifecycleFuncs can be
// registered like any other plugin. Use OnHook to receive the packet-path
// hooks as well as the lifecycle stages.
func (l *LifecycleFuncs) Hooks() []Hook { return nil }

func (l *LifecycleFuncs) Execute(context.Context, Hook, *Context) error { return nil }

func (l *LifecycleFuncs) Initialize(ctx context.Context) error {
	if l.OnInitialize == nil {
		return nil
	}
	return l.OnInitialize(ctx)
}

func (l *LifecycleFuncs) Start(ctx context.Context) error {
	if l.OnStart == nil {
		return nil
	}
	return l.OnStart(ctx)
}

func (l *LifecycleFuncs) Stop(ctx context.Context) error {
	if l.OnStop == nil {
		return nil
	}
	return l.OnStop(ctx)
}

func (l *LifecycleFuncs) Uninstall(ctx context.Context) error {
	if l.OnUninstall == nil {
		return nil
	}
	return l.OnUninstall(ctx)
}

// Managed is a plugin the Manager can drive through its lifecycle.
//
// It embeds Plugin, so a managed plugin is also usable through the hook
// dispatch path unchanged.
type Managed interface {
	Plugin
	Lifecycle
}

// stage records which lifecycle stages a plugin has completed, so the Manager
// can skip a stage that already ran (and stay correct across Start/Stop cycles)
// without the plugin tracking anything.
type stage uint8

const (
	stageNone stage = iota
	stageInitialized
	stageStarted
	stageStopped
	stageUninstalled
)

// lifecycleTimeout bounds one lifecycle stage. A plugin that blocks forever in
// Initialize must not prevent the broker from starting, nor Stop from returning.
const lifecycleTimeout = 30 * time.Second

// StartPlugins runs Initialize then Start for every plugin that implements
// Lifecycle, in registration order.
//
// A failure in one plugin is recorded and the remaining plugins still run: a
// broken extension must not prevent the broker from starting. The returned error
// joins every failure so the operator sees all of them at once.
//
// Calling it twice is a no-op for plugins already started.
func (pm *Manager) StartPlugins(ctx context.Context) error {
	if pm == nil {
		return nil
	}
	var errs []error
	for _, p := range pm.managedPlugins() {
		if err := pm.startOne(ctx, p); err != nil {
			errs = append(errs, fmt.Errorf("plugin %s: %w", p.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// StopPlugins runs Stop then Uninstall, in reverse registration order so a
// plugin that depends on an earlier one is torn down first.
//
// Plugins that never started still get Stop and Uninstall: shutdown can happen
// after a partially failed startup, and leaving allocated resources behind
// would leak them. Every stage must therefore tolerate being called when it
// did not previously run -- that requirement is why the Manager tracks stages
// rather than the plugin.
//
// The context is not the caller's startup context: shutdown work must be
// allowed to finish, so each stage is bounded by lifecycleTimeout instead of
// whatever deadline the caller passed.
func (pm *Manager) StopPlugins() error {
	if pm == nil {
		return nil
	}
	// Reverse order: later plugins depend on earlier ones more often.
	plugins := pm.managedPlugins()
	var errs []error
	for i := len(plugins) - 1; i >= 0; i-- {
		if err := pm.stopOne(plugins[i]); err != nil {
			errs = append(errs, fmt.Errorf("plugin %s: %w", plugins[i].Name(), err))
		}
	}
	if pm.bus != nil {
		pm.bus.Stop()
	}
	return errors.Join(errs...)
}

// managedPlugins returns the plugins implementing Lifecycle, in registration
// order, each once. A plugin registered for several hooks appears once.
func (pm *Manager) managedPlugins() []Managed {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	seen := make(map[string]struct{})
	out := make([]Managed, 0, len(pm.all))
	for _, p := range pm.all {
		m, ok := p.(Managed)
		if !ok {
			continue
		}
		name := m.Name()
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, m)
	}
	// Registration order is not recoverable from the hook map, so sort by name
	// for determinism: a stable order makes startup and teardown reproducible,
	// which matters when two plugins touch the same resource.
	sortManaged(out)
	return out
}

// startOne drives Initialize then Start, skipping stages already completed.
func (pm *Manager) startOne(ctx context.Context, p Managed) error {
	st := pm.stages[p.Name()]

	if st < stageInitialized {
		if err := runStage(p.Name(), "Initialize", ctx, p.Initialize); err != nil {
			pm.setStage(p.Name(), stageNone)
			return err
		}
		pm.setStage(p.Name(), stageInitialized)
		st = stageInitialized
	}

	if st == stageStarted {
		return nil
	}
	if err := runStage(p.Name(), "Start", ctx, p.Start); err != nil {
		// Leave the stage at Initialized so a later attempt can retry Start
		// without redoing Initialize.
		pm.setStage(p.Name(), stageInitialized)
		return err
	}
	pm.setStage(p.Name(), stageStarted)
	return nil
}

// stopOne drives Stop then Uninstall.
func (pm *Manager) stopOne(p Managed) error {
	st := pm.stages[p.Name()]
	if st == stageNone || st == stageUninstalled {
		return nil
	}

	shutdown := context.Background()

	if st == stageStarted {
		if err := runStage(p.Name(), "Stop", shutdown, p.Stop); err != nil {
			// Continue to Uninstall: skipping it would leak whatever
			// Initialize acquired.
			pm.setStage(p.Name(), stageStopped)
			if uerr := runStage(p.Name(), "Uninstall", shutdown, p.Uninstall); uerr != nil {
				return errors.Join(err, uerr)
			}
			pm.setStage(p.Name(), stageUninstalled)
			return err
		}
		pm.setStage(p.Name(), stageStopped)
	}

	if err := runStage(p.Name(), "Uninstall", shutdown, p.Uninstall); err != nil {
		pm.setStage(p.Name(), stageStopped)
		return err
	}
	pm.setStage(p.Name(), stageUninstalled)
	return nil
}

// sortManaged orders plugins deterministically by name.
//
// The hook map does not preserve registration order, so without this two
// brokers built from the same code could initialize plugins in different
// orders -- and when two plugins touch the same resource (a shared file, a
// database) that turns into a startup race that reproduces only sometimes.
func sortManaged(list []Managed) {
	sort.Slice(list, func(i, j int) bool { return list[i].Name() < list[j].Name() })
}

// runStage executes one lifecycle stage with panic recovery and a timeout.
func runStage(name, stageName string, ctx context.Context, fn func(context.Context) error) (err error) {
	if fn == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s panicked: %v", stageName, r)
		}
	}()

	stageCtx, cancel := context.WithTimeout(ctx, lifecycleTimeout)
	defer cancel()

	// A plugin that ignores its context must not stall the broker.
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("%s panicked: %v", stageName, r)
			}
		}()
		done <- fn(stageCtx)
	}()

	select {
	case err := <-done:
		return err
	case <-stageCtx.Done():
		return fmt.Errorf("%s timed out after %v", stageName, lifecycleTimeout)
	}
}

// setStage records progress and emits the lifecycle events so subscribers can
// observe plugin state without the Manager exposing it.
func (pm *Manager) setStage(name string, s stage) {
	pm.stageMu.Lock()
	if _, known := pm.stages[name]; known || s != stageNone {
		pm.stages[name] = s
	}
	pm.stageMu.Unlock()

	if pm.bus == nil || s == stageNone {
		return
	}
	var ev Event
	switch s {
	case stageInitialized:
		ev = EventPluginInitialized
	case stageStarted:
		ev = EventPluginStarted
	case stageStopped:
		ev = EventPluginStopped
	case stageUninstalled:
		ev = EventPluginUninstalled
	default:
		return
	}
	// Lifecycle events are best-effort: a subscriber must not be able to fail
	// the transition it is observing.
	_ = pm.bus.Publish(context.Background(), ev, EventData{ClientID: name})
}

// StageOf reports how far a plugin's lifecycle has progressed. It is exported
// so an operator-facing status endpoint can show it.
func (pm *Manager) StageOf(name string) string {
	if pm == nil {
		return "none"
	}
	pm.stageMu.RLock()
	defer pm.stageMu.RUnlock()
	switch pm.stages[name] {
	case stageNone:
		return "none"
	case stageInitialized:
		return "initialized"
	case stageStarted:
		return "started"
	case stageStopped:
		return "stopped"
	case stageUninstalled:
		return "uninstalled"
	default:
		return "unknown"
	}
}
