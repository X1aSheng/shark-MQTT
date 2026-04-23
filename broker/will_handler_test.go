package broker

import (
	"sync"
	"testing"
	"time"
)

// --- RegisterWill tests ---

func TestWillHandler_RegisterWill(t *testing.T) {
	wh := NewWillHandler()

	tests := []struct {
		name     string
		clientID string
		topic    string
		payload  []byte
		qos      uint8
		retain   bool
		delay    time.Duration
	}{
		{"basic will", "client1", "client1/status", []byte("offline"), 0, false, 0},
		{"with delay", "client2", "client2/status", []byte("gone"), 1, true, 5 * time.Second},
		{"empty payload", "client3", "client3/status", nil, 0, false, 0},
		{"QoS 2", "client4", "client4/status", []byte("data"), 2, true, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wh.RegisterWill(tc.clientID, tc.topic, tc.payload, tc.qos, tc.retain, tc.delay)

			info, ok := wh.GetWillInfo(tc.clientID)
			if !ok {
				t.Fatalf("expected will info for %q", tc.clientID)
			}
			if info.Topic != tc.topic {
				t.Errorf("topic: expected %q, got %q", tc.topic, info.Topic)
			}
			if info.QoS != tc.qos {
				t.Errorf("QoS: expected %d, got %d", tc.qos, info.QoS)
			}
			if info.Retain != tc.retain {
				t.Errorf("retain: expected %v, got %v", tc.retain, info.Retain)
			}
			if len(tc.payload) > 0 && !info.HasPayload {
				t.Error("expected HasPayload to be true")
			}
			if len(tc.payload) == 0 && info.HasPayload {
				t.Error("expected HasPayload to be false")
			}
		})
	}
}

func TestWillHandler_RegisterWill_Overwrites(t *testing.T) {
	wh := NewWillHandler()

	wh.RegisterWill("client1", "old/topic", []byte("old"), 0, false, 0)
	wh.RegisterWill("client1", "new/topic", []byte("new"), 1, true, 0)

	info, ok := wh.GetWillInfo("client1")
	if !ok {
		t.Fatal("expected will info")
	}
	if info.Topic != "new/topic" {
		t.Errorf("expected topic new/topic, got %q", info.Topic)
	}
	if info.QoS != 1 {
		t.Errorf("expected QoS 1, got %d", info.QoS)
	}
}

// --- GetWillInfo tests ---

func TestWillHandler_GetWillInfo_NonExistent(t *testing.T) {
	wh := NewWillHandler()

	info, ok := wh.GetWillInfo("nonexistent")
	if ok {
		t.Error("expected false for non-existent client")
	}
	if info != nil {
		t.Error("expected nil info for non-existent client")
	}
}

// --- TriggerWill tests (immediate) ---

func TestWillHandler_TriggerWill_Immediate(t *testing.T) {
	var mu sync.Mutex
	var publishedTopic string
	var publishedPayload []byte
	var publishedQoS uint8
	var publishedRetain bool

	wh := NewWillHandler()
	wh.SetPublishCallback(func(topic string, payload []byte, qos uint8, retain bool) error {
		mu.Lock()
		publishedTopic = topic
		publishedPayload = payload
		publishedQoS = qos
		publishedRetain = retain
		mu.Unlock()
		return nil
	})

	wh.RegisterWill("client1", "client1/status", []byte("offline"), 1, true, 0)
	err := wh.TriggerWill("client1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	if publishedTopic != "client1/status" {
		t.Errorf("topic: expected client1/status, got %q", publishedTopic)
	}
	if string(publishedPayload) != "offline" {
		t.Errorf("payload: expected offline, got %q", publishedPayload)
	}
	if publishedQoS != 1 {
		t.Errorf("QoS: expected 1, got %d", publishedQoS)
	}
	if !publishedRetain {
		t.Error("expected retain to be true")
	}
	mu.Unlock()

	// Will should be removed after triggering
	_, ok := wh.GetWillInfo("client1")
	if ok {
		t.Error("expected will to be removed after triggering")
	}
}

func TestWillHandler_TriggerWill_NonExistent(t *testing.T) {
	wh := NewWillHandler()

	// Should not error for non-existent client
	err := wh.TriggerWill("nonexistent")
	if err != nil {
		t.Errorf("unexpected error for non-existent will: %v", err)
	}
}

func TestWillHandler_TriggerWill_NoCallback(t *testing.T) {
	wh := NewWillHandler()
	wh.RegisterWill("client1", "topic", []byte("data"), 0, false, 0)

	// No publish callback set - should not panic
	err := wh.TriggerWill("client1")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWillHandler_TriggerWill_EmptyTopic(t *testing.T) {
	var published bool
	wh := NewWillHandler()
	wh.SetPublishCallback(func(topic string, payload []byte, qos uint8, retain bool) error {
		published = true
		return nil
	})

	wh.RegisterWill("client1", "", []byte("data"), 0, false, 0)
	wh.TriggerWill("client1")

	if published {
		t.Error("should not publish will with empty topic")
	}
}

// --- TriggerWill tests (delayed) ---

func TestWillHandler_TriggerWill_Delayed(t *testing.T) {
	var mu sync.Mutex
	var published bool

	wh := NewWillHandler()
	wh.SetPublishCallback(func(topic string, payload []byte, qos uint8, retain bool) error {
		mu.Lock()
		published = true
		mu.Unlock()
		return nil
	})

	delay := 100 * time.Millisecond
	wh.RegisterWill("client1", "client1/status", []byte("delayed"), 0, false, delay)
	wh.TriggerWill("client1")

	// Should not be published immediately
	mu.Lock()
	if published {
		t.Error("should not be published immediately for delayed will")
	}
	mu.Unlock()

	// Wait for delay
	time.Sleep(delay * 2)

	mu.Lock()
	if !published {
		t.Error("expected will to be published after delay")
	}
	mu.Unlock()
}

func TestWillHandler_TriggerWill_Delayed_CancelBeforeFiring(t *testing.T) {
	var mu sync.Mutex
	var published bool

	wh := NewWillHandler()
	wh.SetPublishCallback(func(topic string, payload []byte, qos uint8, retain bool) error {
		mu.Lock()
		published = true
		mu.Unlock()
		return nil
	})

	delay := 200 * time.Millisecond
	wh.RegisterWill("client1", "client1/status", []byte("data"), 0, false, delay)
	wh.TriggerWill("client1")

	// Cancel before the delay elapses
	wh.CancelWill("client1")

	// Wait past the original delay
	time.Sleep(delay * 2)

	mu.Lock()
	if published {
		t.Error("should not publish cancelled will")
	}
	mu.Unlock()
}

// --- RemoveWill tests ---

func TestWillHandler_RemoveWill(t *testing.T) {
	wh := NewWillHandler()
	wh.RegisterWill("client1", "client1/status", []byte("offline"), 0, false, 0)

	wh.RemoveWill("client1")

	_, ok := wh.GetWillInfo("client1")
	if ok {
		t.Error("expected will to be removed")
	}
}

func TestWillHandler_RemoveWill_NonExistent(t *testing.T) {
	wh := NewWillHandler()

	// Should not panic
	wh.RemoveWill("nonexistent")
}

func TestWillHandler_RemoveWill_Delayed(t *testing.T) {
	var mu sync.Mutex
	var published bool

	wh := NewWillHandler()
	wh.SetPublishCallback(func(topic string, payload []byte, qos uint8, retain bool) error {
		mu.Lock()
		published = true
		mu.Unlock()
		return nil
	})

	delay := 100 * time.Millisecond
	wh.RegisterWill("client1", "client1/status", []byte("data"), 0, false, delay)

	// Graceful disconnect: remove will (cancels delayed will)
	wh.RemoveWill("client1")

	time.Sleep(delay * 2)

	mu.Lock()
	if published {
		t.Error("should not publish will removed via RemoveWill")
	}
	mu.Unlock()
}

// --- CancelWill tests ---

func TestWillHandler_CancelWill(t *testing.T) {
	wh := NewWillHandler()
	wh.RegisterWill("client1", "client1/status", []byte("data"), 0, false, 0)

	wh.CancelWill("client1")

	_, ok := wh.GetWillInfo("client1")
	if ok {
		t.Error("expected will to be removed after CancelWill")
	}
}

func TestWillHandler_CancelWill_NonExistent(t *testing.T) {
	wh := NewWillHandler()

	// Should not panic
	wh.CancelWill("nonexistent")
}

// --- Stop tests ---

func TestWillHandler_Stop(t *testing.T) {
	wh := NewWillHandler()
	wh.RegisterWill("client1", "client1/status", []byte("data"), 0, false, 0)
	wh.RegisterWill("client2", "client2/status", []byte("data"), 0, false, 0)

	wh.Stop()

	_, ok1 := wh.GetWillInfo("client1")
	_, ok2 := wh.GetWillInfo("client2")
	if ok1 || ok2 {
		t.Error("expected all wills to be removed after Stop")
	}
}

func TestWillHandler_Stop_CancelsDelayed(t *testing.T) {
	var mu sync.Mutex
	var publishedCount int

	wh := NewWillHandler()
	wh.SetPublishCallback(func(topic string, payload []byte, qos uint8, retain bool) error {
		mu.Lock()
		publishedCount++
		mu.Unlock()
		return nil
	})

	delay := 100 * time.Millisecond
	wh.RegisterWill("client1", "t1", []byte("data"), 0, false, delay)
	wh.RegisterWill("client2", "t2", []byte("data"), 0, false, delay)

	wh.Stop()

	// Wait past the delay
	time.Sleep(delay * 2)

	mu.Lock()
	if publishedCount != 0 {
		t.Errorf("expected 0 published after Stop, got %d", publishedCount)
	}
	mu.Unlock()
}

// --- SetPublishCallback tests ---

func TestWillHandler_SetPublishCallback(t *testing.T) {
	wh := NewWillHandler()

	if wh.publishWill != nil {
		t.Error("expected publishWill to be nil initially")
	}

	cb := func(topic string, payload []byte, qos uint8, retain bool) error { return nil }
	wh.SetPublishCallback(cb)

	if wh.publishWill == nil {
		t.Error("expected publishWill callback to be set")
	}
}

// --- Integration: full lifecycle ---

func TestWillHandler_FullLifecycle(t *testing.T) {
	var mu sync.Mutex
	var publishedMessages []string

	wh := NewWillHandler()
	wh.SetPublishCallback(func(topic string, payload []byte, qos uint8, retain bool) error {
		mu.Lock()
		publishedMessages = append(publishedMessages, topic)
		mu.Unlock()
		return nil
	})

	// Register wills
	wh.RegisterWill("client1", "client1/status", []byte("offline"), 0, false, 0)
	wh.RegisterWill("client2", "client2/status", []byte("offline"), 1, true, 0)

	// Graceful disconnect for client2 (no will published)
	wh.RemoveWill("client2")

	// Abnormal disconnect for client1 (will published)
	wh.TriggerWill("client1")

	mu.Lock()
	if len(publishedMessages) != 1 {
		t.Errorf("expected 1 published message, got %d", len(publishedMessages))
	}
	if len(publishedMessages) > 0 && publishedMessages[0] != "client1/status" {
		t.Errorf("expected client1/status, got %q", publishedMessages[0])
	}
	mu.Unlock()

	// client2's will should be gone
	_, ok := wh.GetWillInfo("client2")
	if ok {
		t.Error("client2 will should be removed")
	}
}
