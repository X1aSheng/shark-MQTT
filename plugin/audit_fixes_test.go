package plugin

import (
	"context"
	"strings"
	"testing"
	"time"
)

// blockingPlugin never returns from Execute until ctx is done.
type blockingPlugin struct {
	name string
}

func (p blockingPlugin) Name() string      { return p.name }
func (p blockingPlugin) Hooks() []Hook     { return []Hook{OnMessage} }
func (p blockingPlugin) Execute(ctx context.Context, hook Hook, data *Context) error {
	<-ctx.Done()
	return nil
}

// TestDispatchHookTimeout verifies a stuck plugin is cut off by the hook
// timeout instead of blocking the caller forever (audit).
func TestDispatchHookTimeout(t *testing.T) {
	pm := NewManager()
	pm.SetHookTimeout(100 * time.Millisecond)
	pm.Register(blockingPlugin{name: "stuck"})

	start := time.Now()
	err := pm.Dispatch(context.Background(), OnMessage, &Context{ClientID: "c1"})
	if err == nil {
		t.Fatal("expected a timeout error from the stuck plugin")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Dispatch did not honor the hook timeout: %v", elapsed)
	}

	// A later dispatch must not be poisoned by the earlier stuck goroutine.
	if err := pm.Dispatch(context.Background(), OnMessage, &Context{ClientID: "c2"}); err == nil {
		t.Fatal("expected the second dispatch to time out too (plugin is still stuck)")
	}
}

// TestUnregister verifies a plugin can be removed from every hook.
func TestUnregister(t *testing.T) {
	pm := NewManager()
	pm.SetHookTimeout(0)
	p := blockingPlugin{name: "gone"}
	pm.Register(p)
	if len(pm.RegisteredPlugins()) != 1 {
		t.Fatalf("registered plugins = %v, want 1", pm.RegisteredPlugins())
	}
	pm.Unregister(p)
	if len(pm.RegisteredPlugins()) != 0 {
		t.Fatalf("registered plugins after Unregister = %v, want 0", pm.RegisteredPlugins())
	}
}
