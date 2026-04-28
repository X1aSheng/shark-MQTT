package broker

import (
	"sync"
	"testing"
	"time"
)

// --- TrackQoS1 tests ---

func TestQoSEngine_TrackQoS1(t *testing.T) {
	tests := []struct {
		name     string
		clientID string
		packetID uint16
		topic    string
		payload  []byte
		retain   bool
	}{
		{"basic", "client1", 1, "home/temp", []byte("hello"), false},
		{"retain", "client1", 2, "home/temp", []byte("data"), true},
		{"empty payload", "client2", 3, "sensors/humid", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := NewQoSEngine()
			q.TrackQoS1(tc.clientID, tc.packetID, tc.topic, tc.payload, tc.retain)

			count := q.InflightCount(tc.clientID)
			if count != 1 {
				t.Errorf("expected 1 inflight message, got %d", count)
			}
		})
	}
}

func TestQoSEngine_TrackQoS1_SamePacketIDOverwrites(t *testing.T) {
	q := NewQoSEngine()

	q.TrackQoS1("client1", 1, "topic1", []byte("first"), false)
	q.TrackQoS1("client1", 1, "topic2", []byte("second"), false)

	count := q.InflightCount("client1")
	if count != 1 {
		t.Errorf("expected 1 inflight message (overwrite), got %d", count)
	}
}

// --- TrackQoS2 tests ---

func TestQoSEngine_TrackQoS2(t *testing.T) {
	q := NewQoSEngine()

	q.TrackQoS2("client1", 1, "home/temp", []byte("qos2data"), true)

	count := q.InflightCount("client1")
	if count != 1 {
		t.Errorf("expected 1 inflight message, got %d", count)
	}
}

// --- Multiple clients ---

func TestQoSEngine_MultipleClients(t *testing.T) {
	q := NewQoSEngine()

	q.TrackQoS1("client1", 1, "t1", nil, false)
	q.TrackQoS1("client1", 2, "t2", nil, false)
	q.TrackQoS1("client2", 1, "t3", nil, false)
	q.TrackQoS2("client3", 1, "t4", nil, false)

	if q.InflightCount("client1") != 2 {
		t.Errorf("client1: expected 2, got %d", q.InflightCount("client1"))
	}
	if q.InflightCount("client2") != 1 {
		t.Errorf("client2: expected 1, got %d", q.InflightCount("client2"))
	}
	if q.InflightCount("client3") != 1 {
		t.Errorf("client3: expected 1, got %d", q.InflightCount("client3"))
	}
	if q.InflightCount("nonexistent") != 0 {
		t.Errorf("nonexistent: expected 0, got %d", q.InflightCount("nonexistent"))
	}
}

// --- AckQoS1 tests ---

func TestQoSEngine_AckQoS1(t *testing.T) {
	q := NewQoSEngine()

	q.TrackQoS1("client1", 1, "home/temp", []byte("data"), false)
	q.AckQoS1("client1", 1)

	count := q.InflightCount("client1")
	if count != 0 {
		t.Errorf("expected 0 inflight after ack, got %d", count)
	}
}

func TestQoSEngine_AckQoS1_NonExistent(t *testing.T) {
	q := NewQoSEngine()

	// Should not panic
	q.AckQoS1("nonexistent", 1)
	q.AckQoS1("client1", 999)
}

// --- AckPubRec tests ---

func TestQoSEngine_AckPubRec(t *testing.T) {
	var pubRelCalled bool
	var pubRelClientID string
	var pubRelPacketID uint16

	q := NewQoSEngine()
	q.SetCallbacks(
		nil,
		func(clientID string, packetID uint16) error {
			pubRelCalled = true
			pubRelClientID = clientID
			pubRelPacketID = packetID
			return nil
		},
		nil,
		nil,
	)

	q.TrackQoS2("client1", 5, "home/temp", []byte("data"), false)
	q.AckPubRec("client1", 5)

	if !pubRelCalled {
		t.Error("expected sendPubRel callback to be called")
	}
	if pubRelClientID != "client1" {
		t.Errorf("expected clientID client1, got %q", pubRelClientID)
	}
	if pubRelPacketID != 5 {
		t.Errorf("expected packetID 5, got %d", pubRelPacketID)
	}

	// Message should still be inflight but in StateAcked
	count := q.InflightCount("client1")
	if count != 1 {
		t.Errorf("expected 1 inflight after PUBREC, got %d", count)
	}
}

func TestQoSEngine_AckPubRec_NoCallback(t *testing.T) {
	q := NewQoSEngine()
	q.TrackQoS2("client1", 1, "home/temp", []byte("data"), false)

	// Should not panic even without callbacks
	q.AckPubRec("client1", 1)
}

func TestQoSEngine_AckPubRec_NonExistentPacket(t *testing.T) {
	q := NewQoSEngine()

	// Should not panic
	q.AckPubRec("client1", 999)
}

// --- AckPubRel tests ---

func TestQoSEngine_AckPubRel(t *testing.T) {
	var pubCompCalled bool
	var pubCompClientID string
	var pubCompPacketID uint16

	q := NewQoSEngine()
	q.SetCallbacks(
		nil,
		nil,
		func(clientID string, packetID uint16) error {
			pubCompCalled = true
			pubCompClientID = clientID
			pubCompPacketID = packetID
			return nil
		},
		nil,
	)

	q.TrackQoS2("client1", 3, "home/temp", []byte("data"), false)
	q.AckPubRel("client1", 3)

	if !pubCompCalled {
		t.Error("expected sendPubComp callback to be called")
	}
	if pubCompClientID != "client1" {
		t.Errorf("expected clientID client1, got %q", pubCompClientID)
	}
	if pubCompPacketID != 3 {
		t.Errorf("expected packetID 3, got %d", pubCompPacketID)
	}

	// Should be removed from inflight
	count := q.InflightCount("client1")
	if count != 0 {
		t.Errorf("expected 0 inflight after PUBREL/PUBCOMP, got %d", count)
	}
}

func TestQoSEngine_AckPubRel_NoCallback(t *testing.T) {
	q := NewQoSEngine()
	q.TrackQoS2("client1", 1, "home/temp", []byte("data"), false)

	// Should not panic
	q.AckPubRel("client1", 1)
}

// --- AckPubComp tests ---

func TestQoSEngine_AckPubComp(t *testing.T) {
	q := NewQoSEngine()
	q.TrackQoS2("client1", 7, "home/temp", []byte("data"), false)
	q.AckPubComp("client1", 7)

	count := q.InflightCount("client1")
	if count != 0 {
		t.Errorf("expected 0 inflight after PUBCOMP, got %d", count)
	}
}

func TestQoSEngine_AckPubComp_NonExistent(t *testing.T) {
	q := NewQoSEngine()

	// Should not panic
	q.AckPubComp("nonexistent", 1)
	q.AckPubComp("client1", 999)
}

// --- RemoveClient tests ---

func TestQoSEngine_RemoveClient(t *testing.T) {
	q := NewQoSEngine()

	q.TrackQoS1("client1", 1, "t1", nil, false)
	q.TrackQoS1("client1", 2, "t2", nil, false)
	q.TrackQoS2("client2", 1, "t3", nil, false)

	q.RemoveClient("client1")

	if q.InflightCount("client1") != 0 {
		t.Errorf("expected 0 inflight for client1 after removal, got %d", q.InflightCount("client1"))
	}
	if q.InflightCount("client2") != 1 {
		t.Errorf("expected 1 inflight for client2 (should be unaffected), got %d", q.InflightCount("client2"))
	}
}

func TestQoSEngine_RemoveClient_NonExistent(t *testing.T) {
	q := NewQoSEngine()

	// Should not panic
	q.RemoveClient("nonexistent")
}

// --- InflightCount tests ---

func TestQoSEngine_InflightCount(t *testing.T) {
	q := NewQoSEngine()

	tests := []struct {
		name      string
		setup     func()
		clientID  string
		wantCount int
	}{
		{
			name:      "empty engine",
			setup:     func() {},
			clientID:  "client1",
			wantCount: 0,
		},
		{
			name: "single message",
			setup: func() { q.TrackQoS1("client1", 1, "t", nil, false) },
			clientID:  "client1",
			wantCount: 1,
		},
		{
			name: "multiple messages same client",
			setup: func() {
				q.TrackQoS1("client2", 1, "t1", nil, false)
				q.TrackQoS1("client2", 2, "t2", nil, false)
				q.TrackQoS2("client2", 3, "t3", nil, false)
			},
			clientID:  "client2",
			wantCount: 3,
		},
		{
			name: "different client",
			setup: func() { q.TrackQoS1("client1", 1, "t", nil, false) },
			clientID:  "other",
			wantCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup()
			count := q.InflightCount(tc.clientID)
			if count != tc.wantCount {
				t.Errorf("expected %d, got %d", tc.wantCount, count)
			}
		})
	}
}

// --- Retry loop tests ---

func TestQoSEngine_StartStop(t *testing.T) {
	q := NewQoSEngine()

	q.Start()
	q.Stop()
}

func TestQoSEngine_Retry_Triggered(t *testing.T) {
	var republishCalled bool
	var republishTopic string
	var republishQoS uint8

	interval := 50 * time.Millisecond
	q := NewQoSEngine(
		WithRetryInterval(interval),
		WithMaxRetries(3),
	)

	var mu sync.Mutex
	q.SetCallbacks(
		nil,   // sendPubAck: broker acknowledges publisher directly, not via retry
		nil,   // sendPubRel
		nil,   // sendPubComp
		func(clientID string, packetID uint16, topic string, payload []byte, qos uint8, retain bool) error {
			mu.Lock()
			republishCalled = true
			republishTopic = topic
			republishQoS = qos
			mu.Unlock()
			return nil
		},
	)

	q.Start()
	defer q.Stop()

	q.TrackQoS1("client1", 1, "home/temp", []byte("retry"), false)

	// Wait for retry interval + small buffer
	time.Sleep(interval * 2)

	mu.Lock()
	if !republishCalled {
		t.Error("expected republish callback to be called")
	}
	if republishTopic != "home/temp" {
		t.Errorf("expected topic home/temp, got %q", republishTopic)
	}
	if republishQoS != 1 {
		t.Errorf("expected QoS 1, got %d", republishQoS)
	}
	mu.Unlock()
}

func TestQoSEngine_Retry_MaxRetriesExceeded(t *testing.T) {
	var republishCount int

	interval := 50 * time.Millisecond
	maxRetries := 2
	q := NewQoSEngine(
		WithRetryInterval(interval),
		WithMaxRetries(maxRetries),
	)

	var mu sync.Mutex
	q.SetCallbacks(
		nil,
		nil,
		nil,
		func(clientID string, packetID uint16, topic string, payload []byte, qos uint8, retain bool) error {
			mu.Lock()
			republishCount++
			mu.Unlock()
			return nil
		},
	)

	q.Start()
	defer q.Stop()

	// Small delay to ensure ticker is running
	time.Sleep(10 * time.Millisecond)

	q.TrackQoS1("client1", 1, "home/temp", []byte("data"), false)

	// Wait long enough for all retries to complete and message to be removed
	time.Sleep(interval * time.Duration(maxRetries+6))

	mu.Lock()
	count := republishCount
	mu.Unlock()

	// Should have retried exactly maxRetries times, then removed
	if count != maxRetries {
		t.Errorf("expected %d republish calls, got %d", maxRetries, count)
	}

	// Message should be removed from inflight
	q.mu.RLock()
	count = q.InflightCount("client1")
	q.mu.RUnlock()
	if count != 0 {
		t.Errorf("expected 0 inflight after max retries, got %d", count)
	}
}

func TestQoSEngine_Retry_QoS2_AckedState(t *testing.T) {
	var sendPubRelCalled bool

	interval := 50 * time.Millisecond
	q := NewQoSEngine(
		WithRetryInterval(interval),
		WithMaxRetries(3),
	)

	var mu sync.Mutex
	q.SetCallbacks(
		nil,
		func(clientID string, packetID uint16) error {
			mu.Lock()
			sendPubRelCalled = true
			mu.Unlock()
			return nil
		},
		nil,
		func(clientID string, packetID uint16, topic string, payload []byte, qos uint8, retain bool) error {
			return nil
		},
	)

	q.Start()
	defer q.Stop()

	q.TrackQoS2("client1", 1, "home/temp", []byte("data"), false)
	// Move to StateAcked (simulating PUBREC received)
	q.AckPubRec("client1", 1)

	time.Sleep(interval * 2)

	mu.Lock()
	if !sendPubRelCalled {
		t.Error("expected sendPubRel callback on retry for QoS 2 Acked state")
	}
	mu.Unlock()
}

// --- Options tests ---

func TestQoSEngine_Options(t *testing.T) {
	tests := []struct {
		name        string
		opts        []QoSOption
		wantMax     int
		wantRetries int
	}{
		{
			name:        "defaults",
			opts:        nil,
			wantMax:     100,
			wantRetries: 3,
		},
		{
			name:        "custom max inflight",
			opts:        []QoSOption{WithMaxInflight(50)},
			wantMax:     50,
			wantRetries: 3,
		},
		{
			name:        "custom max retries",
			opts:        []QoSOption{WithMaxRetries(10)},
			wantMax:     100,
			wantRetries: 10,
		},
		{
			name:        "multiple options",
			opts:        []QoSOption{WithMaxInflight(200), WithMaxRetries(5)},
			wantMax:     200,
			wantRetries: 5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := NewQoSEngine(tc.opts...)
			if q.maxInflight != tc.wantMax {
				t.Errorf("maxInflight: expected %d, got %d", tc.wantMax, q.maxInflight)
			}
			if q.maxRetries != tc.wantRetries {
				t.Errorf("maxRetries: expected %d, got %d", tc.wantRetries, q.maxRetries)
			}
		})
	}
}

// --- Concurrent access tests ---

func TestQoSEngine_ConcurrentAccess(t *testing.T) {
	q := NewQoSEngine()
	done := make(chan bool, 4)

	go func() {
		for i := uint16(0); i < 100; i++ {
			q.TrackQoS1("client1", i, "t", nil, false)
		}
		done <- true
	}()

	go func() {
		for i := uint16(0); i < 100; i++ {
			q.AckQoS1("client1", i)
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			q.InflightCount("client1")
		}
		done <- true
	}()

	go func() {
		for i := uint16(0); i < 100; i++ {
			q.TrackQoS2("client2", i, "t", nil, false)
		}
		done <- true
	}()

	for i := 0; i < 4; i++ {
		<-done
	}
}

func TestQoSEngine_SetCallbacks(t *testing.T) {
	q := NewQoSEngine()

	// Initially no callbacks set
	if q.sendPubAck != nil {
		t.Error("expected sendPubAck to be nil initially")
	}

	cb1 := func(clientID string, packetID uint16) error { return nil }
	cb2 := func(clientID string, packetID uint16) error { return nil }
	cb3 := func(clientID string, packetID uint16) error { return nil }
	cb4 := func(clientID string, packetID uint16, topic string, payload []byte, qos uint8, retain bool) error {
		return nil
	}

	q.SetCallbacks(cb1, cb2, cb3, cb4)

	if q.sendPubAck == nil || q.sendPubRel == nil || q.sendPubComp == nil || q.republish == nil {
		t.Error("expected all callbacks to be set")
	}
}

// --- InflightState constants ---

func TestQoSEngine_InflightStateValues(t *testing.T) {
	if StateSent != 0 {
		t.Errorf("StateSent should be 0, got %d", StateSent)
	}
	if StateAcked != 1 {
		t.Errorf("StateAcked should be 1, got %d", StateAcked)
	}
}
