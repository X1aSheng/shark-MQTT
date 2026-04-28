package plugin

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

// MockPlugin is a test plugin implementation
type MockPlugin struct {
	name        string
	hooks       []Hook
	executeFunc func(ctx context.Context, hook Hook, data *Context) error
	callCount   atomic.Int32
	lastData    *Context
}

func (p *MockPlugin) Name() string {
	return p.name
}

func (p *MockPlugin) Hooks() []Hook {
	return p.hooks
}

func (p *MockPlugin) Execute(ctx context.Context, hook Hook, data *Context) error {
	p.callCount.Add(1)
	p.lastData = data
	if p.executeFunc != nil {
		return p.executeFunc(ctx, hook, data)
	}
	return nil
}

func (p *MockPlugin) GetCallCount() int {
	return int(p.callCount.Load())
}

func TestManager_NewManager(t *testing.T) {
	pm := NewManager()
	if pm == nil {
		t.Fatal("expected non-nil manager")
	}
	if pm.plugins == nil {
		t.Error("expected plugins map to be initialized")
	}
}

func TestManager_Register(t *testing.T) {
	pm := NewManager()

	p1 := &MockPlugin{
		name:  "test-plugin-1",
		hooks: []Hook{OnAccept, OnConnected},
	}

	pm.Register(p1)

	// Verify plugin is registered
	plugins := pm.RegisteredPlugins()
	if len(plugins) != 1 {
		t.Errorf("expected 1 registered plugin, got %d", len(plugins))
	}
	if plugins[0] != "test-plugin-1" {
		t.Errorf("expected plugin name 'test-plugin-1', got '%s'", plugins[0])
	}
}

func TestManager_Register_MultiplePlugins(t *testing.T) {
	pm := NewManager()

	p1 := &MockPlugin{
		name:  "plugin-1",
		hooks: []Hook{OnAccept},
	}
	p2 := &MockPlugin{
		name:  "plugin-2",
		hooks: []Hook{OnConnected, OnMessage},
	}
	p3 := &MockPlugin{
		name:  "plugin-3",
		hooks: []Hook{OnAccept, OnClose},
	}

	pm.Register(p1)
	pm.Register(p2)
	pm.Register(p3)

	plugins := pm.RegisteredPlugins()
	if len(plugins) != 3 {
		t.Errorf("expected 3 registered plugins, got %d", len(plugins))
	}

	// Check all plugin names are present
	nameMap := make(map[string]bool)
	for _, name := range plugins {
		nameMap[name] = true
	}

	expected := []string{"plugin-1", "plugin-2", "plugin-3"}
	for _, exp := range expected {
		if !nameMap[exp] {
			t.Errorf("expected plugin '%s' not found in registered plugins", exp)
		}
	}
}

func TestManager_Dispatch_SingleHook(t *testing.T) {
	pm := NewManager()

	p1 := &MockPlugin{
		name:  "accept-plugin",
		hooks: []Hook{OnAccept},
	}

	pm.Register(p1)

	ctx := context.Background()
	data := &Context{
		ClientID:   "test-client",
		RemoteAddr: "127.0.0.1:12345",
	}

	err := pm.Dispatch(ctx, OnAccept, data)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if p1.GetCallCount() != 1 {
		t.Errorf("expected 1 call, got %d", p1.GetCallCount())
	}

	if p1.lastData.ClientID != "test-client" {
		t.Errorf("expected ClientID 'test-client', got '%s'", p1.lastData.ClientID)
	}
}

func TestManager_Dispatch_MultiplePluginsSameHook(t *testing.T) {
	pm := NewManager()

	p1 := &MockPlugin{
		name:  "plugin-a",
		hooks: []Hook{OnAccept},
	}
	p2 := &MockPlugin{
		name:  "plugin-b",
		hooks: []Hook{OnAccept},
	}

	pm.Register(p1)
	pm.Register(p2)

	ctx := context.Background()
	data := &Context{ClientID: "test-client"}

	err := pm.Dispatch(ctx, OnAccept, data)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if p1.GetCallCount() != 1 {
		t.Errorf("expected plugin-a called 1 time, got %d", p1.GetCallCount())
	}
	if p2.GetCallCount() != 1 {
		t.Errorf("expected plugin-b called 1 time, got %d", p2.GetCallCount())
	}
}

func TestManager_Dispatch_NoRegisteredPlugins(t *testing.T) {
	pm := NewManager()

	ctx := context.Background()
	data := &Context{ClientID: "test-client"}

	err := pm.Dispatch(ctx, OnAccept, data)
	if err != nil {
		t.Errorf("expected no error with no plugins, got: %v", err)
	}
}

func TestManager_Dispatch_NoMatchingHook(t *testing.T) {
	pm := NewManager()

	p1 := &MockPlugin{
		name:  "accept-only",
		hooks: []Hook{OnAccept},
	}

	pm.Register(p1)

	ctx := context.Background()
	data := &Context{ClientID: "test-client"}

	// Dispatch to a hook that p1 is not registered for
	err := pm.Dispatch(ctx, OnConnected, data)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if p1.GetCallCount() != 0 {
		t.Errorf("expected plugin not to be called, got %d calls", p1.GetCallCount())
	}
}

func TestManager_Dispatch_ErrorPropagation(t *testing.T) {
	pm := NewManager()

	expectedErr := errors.New("plugin error")

	p1 := &MockPlugin{
		name:  "error-plugin",
		hooks: []Hook{OnAccept},
		executeFunc: func(ctx context.Context, hook Hook, data *Context) error {
			return expectedErr
		},
	}

	pm.Register(p1)

	ctx := context.Background()
	data := &Context{ClientID: "test-client"}

	err := pm.Dispatch(ctx, OnAccept, data)
	if err != expectedErr {
		t.Errorf("expected error '%v', got '%v'", expectedErr, err)
	}
}

func TestManager_Dispatch_MultipleHooks(t *testing.T) {
	pm := NewManager()

	p1 := &MockPlugin{
		name:  "multi-hook",
		hooks: []Hook{OnAccept, OnConnected, OnMessage, OnClose},
	}

	pm.Register(p1)

	ctx := context.Background()

	// Dispatch to different hooks
	pm.Dispatch(ctx, OnAccept, &Context{ClientID: "c1"})
	pm.Dispatch(ctx, OnConnected, &Context{ClientID: "c2"})
	pm.Dispatch(ctx, OnMessage, &Context{ClientID: "c3"})
	pm.Dispatch(ctx, OnClose, &Context{ClientID: "c4"})

	if p1.GetCallCount() != 4 {
		t.Errorf("expected 4 calls, got %d", p1.GetCallCount())
	}
}

func TestManager_Dispatch_ContextPassing(t *testing.T) {
	pm := NewManager()

	var receivedCtx context.Context
	p1 := &MockPlugin{
		name:  "ctx-plugin",
		hooks: []Hook{OnAccept},
		executeFunc: func(ctx context.Context, hook Hook, data *Context) error {
			receivedCtx = ctx
			return nil
		},
	}

	pm.Register(p1)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1000)
	defer cancel()

	data := &Context{ClientID: "test"}
	pm.Dispatch(ctx, OnAccept, data)

	if receivedCtx == nil {
		t.Error("expected context to be passed to plugin")
	}
}

func TestManager_Dispatch_DataPassing(t *testing.T) {
	pm := NewManager()

	var receivedData *Context
	p1 := &MockPlugin{
		name:  "data-plugin",
		hooks: []Hook{OnAccept},
		executeFunc: func(ctx context.Context, hook Hook, data *Context) error {
			receivedData = data
			return nil
		},
	}

	pm.Register(p1)

	ctx := context.Background()
	data := &Context{
		ClientID:   "client-123",
		Username:   "testuser",
		Topic:      "test/topic",
		Payload:    []byte("hello"),
		QoS:        1,
		Retain:     true,
		RemoteAddr: "192.168.1.1:1234",
		Err:        errors.New("test error"),
	}

	pm.Dispatch(ctx, OnAccept, data)

	if receivedData.ClientID != "client-123" {
		t.Errorf("expected ClientID 'client-123', got '%s'", receivedData.ClientID)
	}
	if receivedData.Username != "testuser" {
		t.Errorf("expected Username 'testuser', got '%s'", receivedData.Username)
	}
	if receivedData.Topic != "test/topic" {
		t.Errorf("expected Topic 'test/topic', got '%s'", receivedData.Topic)
	}
	if string(receivedData.Payload) != "hello" {
		t.Errorf("expected Payload 'hello', got '%s'", string(receivedData.Payload))
	}
	if receivedData.QoS != 1 {
		t.Errorf("expected QoS 1, got %d", receivedData.QoS)
	}
	if !receivedData.Retain {
		t.Error("expected Retain to be true")
	}
	if receivedData.RemoteAddr != "192.168.1.1:1234" {
		t.Errorf("expected RemoteAddr '192.168.1.1:1234', got '%s'", receivedData.RemoteAddr)
	}
	if receivedData.Err == nil || receivedData.Err.Error() != "test error" {
		t.Errorf("expected error 'test error', got '%v'", receivedData.Err)
	}
}

func TestManager_Dispatch_EarlyTermination(t *testing.T) {
	pm := NewManager()

	expectedErr := errors.New("stop here")

	p1 := &MockPlugin{
		name:  "first",
		hooks: []Hook{OnAccept},
		executeFunc: func(ctx context.Context, hook Hook, data *Context) error {
			return expectedErr
		},
	}
	p2 := &MockPlugin{
		name:  "second",
		hooks: []Hook{OnAccept},
	}

	pm.Register(p1)
	pm.Register(p2)

	ctx := context.Background()
	data := &Context{ClientID: "test"}

	err := pm.Dispatch(ctx, OnAccept, data)
	if err != expectedErr {
		t.Errorf("expected error from first plugin, got: %v", err)
	}

	// Second plugin should not have been called
	if p2.GetCallCount() != 0 {
		t.Errorf("expected second plugin not called, got %d calls", p2.GetCallCount())
	}
}

func TestManager_ConcurrentRegistration(t *testing.T) {
	pm := NewManager()

	done := make(chan bool, 3)

	// Register plugins concurrently
	go func() {
		pm.Register(&MockPlugin{name: "concurrent-1", hooks: []Hook{OnAccept}})
		done <- true
	}()
	go func() {
		pm.Register(&MockPlugin{name: "concurrent-2", hooks: []Hook{OnConnected}})
		done <- true
	}()
	go func() {
		pm.Register(&MockPlugin{name: "concurrent-3", hooks: []Hook{OnMessage}})
		done <- true
	}()

	// Wait for all registrations
	for i := 0; i < 3; i++ {
		<-done
	}

	plugins := pm.RegisteredPlugins()
	if len(plugins) != 3 {
		t.Errorf("expected 3 registered plugins, got %d", len(plugins))
	}
}

func TestManager_ConcurrentDispatch(t *testing.T) {
	pm := NewManager()

	counter := &atomic.Int32{}

	p1 := &MockPlugin{
		name:  "counter",
		hooks: []Hook{OnAccept},
		executeFunc: func(ctx context.Context, hook Hook, data *Context) error {
			counter.Add(1)
			return nil
		},
	}

	pm.Register(p1)

	// Dispatch concurrently
	done := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		go func() {
			ctx := context.Background()
			pm.Dispatch(ctx, OnAccept, &Context{ClientID: "test"})
			done <- true
		}()
	}

	// Wait for all dispatches
	for i := 0; i < 100; i++ {
		<-done
	}

	if counter.Load() != 100 {
		t.Errorf("expected 100 calls, got %d", counter.Load())
	}
}

func BenchmarkManager_Register(b *testing.B) {
	pm := NewManager()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pm.Register(&MockPlugin{
			name:  string(rune(i)),
			hooks: []Hook{OnAccept},
		})
	}
}

func BenchmarkManager_Dispatch(b *testing.B) {
	pm := NewManager()

	p1 := &MockPlugin{
		name:  "benchmark-plugin",
		hooks: []Hook{OnAccept},
	}
	pm.Register(p1)

	ctx := context.Background()
	data := &Context{ClientID: "benchmark-client"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pm.Dispatch(ctx, OnAccept, data)
	}
}

func BenchmarkManager_Dispatch_MultiplePlugins(b *testing.B) {
	pm := NewManager()

	for i := 0; i < 10; i++ {
		pm.Register(&MockPlugin{
			name:  string(rune(i)),
			hooks: []Hook{OnAccept},
		})
	}

	ctx := context.Background()
	data := &Context{ClientID: "benchmark-client"}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		pm.Dispatch(ctx, OnAccept, data)
	}
}
