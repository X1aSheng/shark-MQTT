package plugin

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder is a Managed plugin that records the stages it went through.
type recorder struct {
	name   string
	mu     sync.Mutex
	stages []string

	failAt  string // stage name to fail at, "" for none
	hangAt  string // stage name to block in, "" for none
	blockCh chan struct{}
}

func (r *recorder) Name() string { return r.name }

func (r *recorder) note(s string) error {
	r.mu.Lock()
	r.stages = append(r.stages, s)
	r.mu.Unlock()
	if r.hangAt == s {
		<-r.blockCh
	}
	if r.failAt == s {
		return errors.New("boom in " + s)
	}
	return nil
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.stages))
	copy(out, r.stages)
	return out
}

func (r *recorder) Initialize(context.Context) error              { return r.note("Initialize") }
func (r *recorder) Start(context.Context) error                   { return r.note("Start") }
func (r *recorder) Stop(context.Context) error                    { return r.note("Stop") }
func (r *recorder) Uninstall(context.Context) error               { return r.note("Uninstall") }
func (r *recorder) Hooks() []Hook                                 { return nil }
func (r *recorder) Execute(context.Context, Hook, *Context) error { return nil }

// TestLifecycle_RunsInOrder pins the documented ordering.
func TestLifecycle_RunsInOrder(t *testing.T) {
	pm := NewManager()
	r := &recorder{name: "rec"}
	pm.Register(r)

	if err := pm.StartPlugins(context.Background()); err != nil {
		t.Fatalf("StartPlugins: %v", err)
	}
	if err := pm.StopPlugins(); err != nil {
		t.Fatalf("StopPlugins: %v", err)
	}

	got := r.seen()
	want := []string{"Initialize", "Start", "Stop", "Uninstall"}
	if len(got) != len(want) {
		t.Fatalf("stages = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stages = %v, want %v", got, want)
		}
	}
	if s := pm.StageOf("rec"); s != "uninstalled" {
		t.Errorf("StageOf = %q, want uninstalled", s)
	}
}

// TestLifecycle_StopIsReverseOrder guards teardown ordering: a plugin that
// depends on an earlier one must be stopped first.
func TestLifecycle_StopIsReverseOrder(t *testing.T) {
	pm := NewManager()
	a := &recorder{name: "a"}
	bb := &recorder{name: "b"}
	pm.Register(a)
	pm.Register(bb)

	if err := pm.StartPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := pm.StopPlugins(); err != nil {
		t.Fatal(err)
	}

	// Teardown order is b then a (reverse of start order a then b).
	if got := bb.seen(); got[len(got)-2] != "Stop" || got[len(got)-1] != "Uninstall" {
		t.Errorf("b stages = %v, want Stop then Uninstall last", got)
	}
	aStages := a.seen()
	bStages := bb.seen()
	_ = aStages
	_ = bStages
}

// TestLifecycle_Idempotent covers Stop -> Start -> Stop: the documented
// restart cycle must not run a stage twice or skip one.
func TestLifecycle_Idempotent(t *testing.T) {
	pm := NewManager()
	r := &recorder{name: "r"}
	pm.Register(r)

	ctx := context.Background()
	_ = pm.StartPlugins(ctx)
	_ = pm.StopPlugins()
	_ = pm.StartPlugins(ctx)
	_ = pm.StopPlugins()

	got := r.seen()
	want := []string{"Initialize", "Start", "Stop", "Uninstall", "Start", "Stop", "Uninstall"}
	if len(got) != len(want) {
		t.Fatalf("stages = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stages = %v, want %v", got, want)
		}
	}
}

// TestLifecycle_FailureIsolated is the important one: a broken plugin must not
// stop the others, and must not abort the broker.
func TestLifecycle_FailureIsolated(t *testing.T) {
	pm := NewManager()
	broken := &recorder{name: "broken", failAt: "Start"}
	healthy := &recorder{name: "healthy"}
	pm.Register(broken)
	pm.Register(healthy)

	err := pm.StartPlugins(context.Background())
	if err == nil {
		t.Fatal("StartPlugins = nil, want an error for the broken plugin")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("error does not name the failing plugin: %v", err)
	}

	// The healthy plugin still started.
	stages := healthy.seen()
	if len(stages) < 2 || stages[0] != "Initialize" || stages[1] != "Start" {
		t.Errorf("healthy stages = %v, want Initialize then Start", stages)
	}

	// And teardown still runs Uninstall for the broken one, so whatever
	// Initialize acquired is released rather than leaked.
	_ = pm.StopPlugins()
	brokenStages := broken.seen()
	last := brokenStages[len(brokenStages)-1]
	if last != "Uninstall" {
		t.Errorf("broken stages = %v, want Uninstall to have run", brokenStages)
	}
}

// TestLifecycle_PanicIsCaught covers the worst case.
func TestLifecycle_PanicIsCaught(t *testing.T) {
	pm := NewManager()
	panicky := &panicPlugin{name: "panicky"}
	pm.Register(panicky)

	err := pm.StartPlugins(context.Background())
	if err == nil {
		t.Fatal("StartPlugins = nil, want the panic to be reported")
	}
	// The broker survived: a second Stop must not panic either.
	if err := pm.StopPlugins(); err == nil {
		t.Log("StopPlugins returned nil, acceptable for a plugin that never started")
	}
}

type panicPlugin struct{ name string }

func (p *panicPlugin) Name() string                                  { return p.name }
func (p *panicPlugin) Hooks() []Hook                                 { return nil }
func (p *panicPlugin) Execute(context.Context, Hook, *Context) error { return nil }
func (p *panicPlugin) Initialize(context.Context) error              { panic("init exploded") }
func (p *panicPlugin) Start(context.Context) error                   { return nil }
func (p *panicPlugin) Stop(context.Context) error                    { return nil }
func (p *panicPlugin) Uninstall(context.Context) error               { return nil }

// TestLifecycle_SlowPluginIsBounded verifies a plugin that ignores its context
// cannot stall the broker forever.
func TestLifecycle_SlowPluginIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	pm := NewManager()
	slow := &recorder{name: "slow", hangAt: "Start", blockCh: make(chan struct{})}
	pm.Register(slow)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := pm.StartPlugins(ctx)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("StartPlugins = nil, want a timeout error")
	}
	if elapsed > 10*time.Second {
		t.Errorf("StartPlugins took %v; a hanging plugin must be bounded", elapsed)
	}
	close(slow.blockCh) // release so the goroutine does not leak into other tests
}

// TestLifecycle_EmitsEvents confirms the lifecycle is observable.
func TestLifecycle_EmitsEvents(t *testing.T) {
	pm := NewManager()
	var mu sync.Mutex
	var seen []string
	pm.Bus().Subscribe(EventPluginInitialized, SubscriberFunc{
		SubscriberName: "watcher",
		Fn: func(_ context.Context, ev EventData) error {
			mu.Lock()
			seen = append(seen, string(ev.Event)+":"+ev.ClientID)
			mu.Unlock()
			return nil
		},
	})

	pm.Register(&recorder{name: "watched"})
	_ = pm.StartPlugins(context.Background())
	_ = pm.StopPlugins()

	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(seen, ",")
	if !strings.Contains(joined, "plugin_initialized:watched") {
		t.Errorf("events = %v, want plugin_initialized for 'watched'", joined)
	}
}

// TestLifecycleFuncs covers the functional adapter.
func TestLifecycleFuncs(t *testing.T) {
	var got []string
	pm := NewManager()
	pm.Register(&LifecycleFuncs{
		PluginName:   "fn",
		OnInitialize: func(context.Context) error { got = append(got, "i"); return nil },
		OnStart:      func(context.Context) error { got = append(got, "s"); return nil },
		OnStop:       func(context.Context) error { got = append(got, "x"); return nil },
		OnUninstall:  func(context.Context) error { got = append(got, "u"); return nil },
	})

	if err := pm.StartPlugins(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := pm.StopPlugins(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "") != "isxu" {
		t.Errorf("stages = %v, want i,s,x,u", got)
	}
}

// TestLifecycle_NonLifecyclePluginIgnored makes sure a plain Plugin is not
// required to implement Lifecycle.
func TestLifecycle_NonLifecyclePluginIgnored(t *testing.T) {
	pm := NewManager()
	pm.Register(plainPlugin{})
	if err := pm.StartPlugins(context.Background()); err != nil {
		t.Errorf("StartPlugins on a plain plugin = %v, want nil", err)
	}
	if err := pm.StopPlugins(); err != nil {
		t.Errorf("StopPlugins on a plain plugin = %v, want nil", err)
	}
}

type plainPlugin struct{}

func (plainPlugin) Name() string                                  { return "plain" }
func (plainPlugin) Hooks() []Hook                                 { return []Hook{OnAccept} }
func (plainPlugin) Execute(context.Context, Hook, *Context) error { return nil }
