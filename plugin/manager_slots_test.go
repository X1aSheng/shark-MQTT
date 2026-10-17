package plugin

import (
	"context"
	"sync"
	"testing"
	"time"
)

// slotBlockingPlugin never returns from Execute and ignores its context, which is
// exactly the plugin the concurrency cap exists for.
type slotBlockingPlugin struct {
	release chan struct{}
	started sync.WaitGroup
}

func (p *slotBlockingPlugin) Name() string      { return "blocking" }
func (p *slotBlockingPlugin) Hooks() []Hook { return []Hook{OnMessage} }
func (p *slotBlockingPlugin) Execute(ctx context.Context, hook Hook, data *Context) error {
	p.started.Done()
	<-p.release
	return nil
}

// TestManagerBoundsConcurrentHookExecutions pins the goroutine bound.
//
// A hook that times out leaves its goroutine running when the plugin ignores
// the context, so an unloaded broker never notices and a loaded one grows
// goroutines by one per packet. The cap converts that unbounded leak into a
// refusal that the broker counts as a plugin error.
func TestManagerBoundsConcurrentHookExecutions(t *testing.T) {
	pm := NewManager()
	pm.SetHookTimeout(time.Millisecond)

	p := &slotBlockingPlugin{release: make(chan struct{})}
	p.started.Add(maxConcurrentHooks)
	pm.Register(p)

	// Fill every slot. Each Dispatch returns on its own timeout while the
	// plugin goroutine stays parked.
	for i := 0; i < maxConcurrentHooks; i++ {
		_ = pm.Dispatch(context.Background(), OnMessage, &Context{})
	}

	// The next one must be refused rather than starting another goroutine.
	err := pm.Dispatch(context.Background(), OnMessage, &Context{})
	if err == nil {
		t.Fatal("Dispatch admitted a hook beyond maxConcurrentHooks")
	}

	close(p.release) // let the parked goroutines drain
}
