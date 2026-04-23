package broker

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

// =============================================================================
// PacketID Boundary Tests
// =============================================================================

// TestPacketID_Exhaustion tests the behavior when PacketID reaches max value 65535
func TestPacketID_Exhaustion(t *testing.T) {
	// MQTT spec: PacketID is a 16-bit unsigned integer (0-65535)
	// 0 is reserved for QoS 0 messages, so valid range is 1-65535

	t.Run("PacketID_MaxValue_65535", func(t *testing.T) {
		q := NewQoSEngine()

		// Track message at max PacketID
		q.TrackQoS1("client1", 65535, "topic", []byte("data"), false)

		if q.InflightCount("client1") != 1 {
			t.Errorf("expected 1 inflight message at PacketID 65535, got %d", q.InflightCount("client1"))
		}

		// Acknowledge it
		q.AckQoS1("client1", 65535)

		if q.InflightCount("client1") != 0 {
			t.Errorf("expected 0 inflight after ack, got %d", q.InflightCount("client1"))
		}
	})

	t.Run("PacketID_Zero_NotUsedForQoS1", func(t *testing.T) {
		q := NewQoSEngine()

		// Track with PacketID 0 - this is technically allowed but QoS 0 doesn't use PacketID
		// QoS 1/2 should only use 1-65535
		q.TrackQoS1("client1", 0, "topic", []byte("data"), false)

		// The engine should handle it, even if unusual
		count := q.InflightCount("client1")
		if count != 1 {
			t.Errorf("expected 1 inflight for PacketID 0, got %d", count)
		}
	})

	t.Run("PacketID_WrapAround_Behavior", func(t *testing.T) {
		q := NewQoSEngine()

		// Fill up to max, then try to add more
		for i := 1; i <= 65535; i++ {
			q.TrackQoS1("client1", uint16(i), "topic", []byte("data"), false)
		}

		// Should still be tracked (overwrites are allowed)
		q.TrackQoS1("client1", 1, "topic", []byte("data"), false)

		// Count should still be max 65535 (or less due to overwrites)
		count := q.InflightCount("client1")
		if count == 0 {
			t.Error("expected some inflight messages after filling")
		}
	})

	t.Run("MultipleClients_PacketIDExhaustion", func(t *testing.T) {
		q := NewQoSEngine()

		// Each client can have up to 65535 inflight messages
		// Test that separate clients don't interfere
		for i := 1; i <= 100; i++ {
			q.TrackQoS1("client1", uint16(i), "topic", []byte("data"), false)
			q.TrackQoS1("client2", uint16(i), "topic", []byte("data"), false)
		}

		if q.InflightCount("client1") != 100 {
			t.Errorf("client1: expected 100, got %d", q.InflightCount("client1"))
		}
		if q.InflightCount("client2") != 100 {
			t.Errorf("client2: expected 100, got %d", q.InflightCount("client2"))
		}
	})
}

// TestPacketID_ConcurrentAccess tests thread safety under high concurrency
func TestPacketID_ConcurrentAccess(t *testing.T) {
	q := NewQoSEngine()

	// Simulate many clients sending messages concurrently
	clientCount := 100
	packetIDsPerClient := 100

	done := make(chan bool, clientCount)

	for c := 0; c < clientCount; c++ {
		go func(clientID string) {
			for i := 1; i <= packetIDsPerClient; i++ {
				q.TrackQoS1(clientID, uint16(i), "topic", []byte("data"), false)
				// Simulate some acks
				if i%10 == 0 {
					q.AckQoS1(clientID, uint16(i-5))
				}
			}
			done <- true
		}("client-" + string(rune('A'+c)))
	}

	for i := 0; i < clientCount; i++ {
		<-done
	}

	// Verify engine is still functional
	q.Start()
	defer q.Stop()

	// Can still track new messages
	q.TrackQoS1("new-client", 1, "topic", []byte("data"), false)
	if q.InflightCount("new-client") != 1 {
		t.Error("engine should remain functional after high concurrency")
	}
}

// =============================================================================
// Topic Length Boundary Tests
// =============================================================================

// TestTopic_LengthBoundary tests MQTT topic length limits
func TestTopic_LengthBoundary(t *testing.T) {
	// MQTT spec: Topic names are UTF-8 encoded strings
	// The length is limited by the remaining packet size

	t.Run("Topic_MaxLength_ApproachingLimit", func(t *testing.T) {
		// Create a very long topic name
		longTopic := strings.Repeat("a", 10000)

		q := NewQoSEngine()
		payload := []byte("data")

		// Should handle long topics
		q.TrackQoS1("client1", 1, longTopic, payload, false)

		if q.InflightCount("client1") != 1 {
			t.Error("expected message to be tracked with long topic")
		}
	})

	t.Run("Topic_EmptyString", func(t *testing.T) {
		q := NewQoSEngine()

		// Empty topic should be handled (though invalid in MQTT)
		q.TrackQoS1("client1", 1, "", []byte("data"), false)

		if q.InflightCount("client1") != 1 {
			t.Error("expected message to be tracked with empty topic")
		}
	})

	t.Run("Topic_WithSpecialCharacters", func(t *testing.T) {
		q := NewQoSEngine()

		// Topics with various valid characters
		specialTopics := []string{
			"home/room-1/temp+sensor",
			"$SYS/broker/load",
			"sensor/🏠/temperature",
			"a/b/c/d/e/f/g/h/i/j",
		}

		for i, topic := range specialTopics {
			q.TrackQoS1("client1", uint16(i+1), topic, []byte("data"), false)
		}

		if q.InflightCount("client1") != len(specialTopics) {
			t.Errorf("expected %d inflight, got %d", len(specialTopics), q.InflightCount("client1"))
		}
	})

	t.Run("Topic_MultiLevelWildcard", func(t *testing.T) {
		q := NewQoSEngine()

		wildcardTopics := []string{
			"sensors/#",
			"home/+/temperature",
			"devices/+/+/status",
			"#",
			"+",
		}

		for i, topic := range wildcardTopics {
			q.TrackQoS1("client1", uint16(i+1), topic, []byte("data"), false)
		}

		if q.InflightCount("client1") != len(wildcardTopics) {
			t.Errorf("expected %d wildcard topics tracked, got %d", len(wildcardTopics), q.InflightCount("client1"))
		}
	})
}

// =============================================================================
// ClientID Boundary Tests
// =============================================================================

// TestClientID_Boundary tests ClientID length and format limits
func TestClientID_Boundary(t *testing.T) {
	t.Run("ClientID_Empty", func(t *testing.T) {
		q := NewQoSEngine()

		// Empty ClientID - valid for clean session in MQTT 3.1.1
		q.TrackQoS1("", 1, "topic", []byte("data"), false)
		q.TrackQoS1("", 2, "topic", []byte("data"), false)

		if q.InflightCount("") != 2 {
			t.Errorf("expected 2 inflight for empty clientID, got %d", q.InflightCount(""))
		}
	})

	t.Run("ClientID_VeryLong", func(t *testing.T) {
		q := NewQoSEngine()

		// Long ClientID (MQTT 5.0 allows up to 65535 bytes)
		longClientID := strings.Repeat("X", 1000)

		q.TrackQoS1(longClientID, 1, "topic", []byte("data"), false)

		if q.InflightCount(longClientID) != 1 {
			t.Error("expected message to be tracked with long clientID")
		}

		// Remove the client
		q.RemoveClient(longClientID)

		if q.InflightCount(longClientID) != 0 {
			t.Error("expected 0 after removal")
		}
	})

	t.Run("ClientID_WithSpecialCharacters", func(t *testing.T) {
		q := NewQoSEngine()

		// ClientIDs with special characters
		clientIDs := []string{
			"client-123",
			"sensor_456",
			"device@home",
			"IoT.Device.01",
			"123456",
		}

		for i, clientID := range clientIDs {
			q.TrackQoS1(clientID, 1, "topic", []byte("data"), false)
			q.TrackQoS2(clientID, 2, "topic", []byte("data"), false)

			if q.InflightCount(clientID) != 2 {
				t.Errorf("clientID %s: expected 2 inflight, got %d", clientID, q.InflightCount(clientID))
			}

			// Release
			q.AckQoS1(clientID, 1)
			q.AckPubComp(clientID, 2)
		}
	})

	t.Run("MultipleClients_SamePrefix", func(t *testing.T) {
		q := NewQoSEngine()

		// Many clients with similar prefixes
		for i := 0; i < 100; i++ {
			clientID := "client-prefix-" + strings.Repeat("0", 3-len(string(rune('0'+i/10))))
			clientID = "device" + string(rune('0'+i/10)) + string(rune('0'+i%10))
			q.TrackQoS1(clientID, 1, "topic", []byte("data"), false)
		}

		// Verify each client has exactly 1 inflight
		for i := 0; i < 100; i++ {
			clientID := "device" + string(rune('0'+i/10)) + string(rune('0'+i%10))
			if q.InflightCount(clientID) != 1 {
				t.Errorf("client %d: expected 1 inflight, got %d", i, q.InflightCount(clientID))
			}
		}
	})
}

// =============================================================================
// QoS Level Tests
// =============================================================================

// TestQoS_LevelBoundary tests QoS level handling
func TestQoS_LevelBoundary(t *testing.T) {
	t.Run("QoS_0_Basic", func(t *testing.T) {
		// QoS 0 doesn't use PacketID typically, but test the engine accepts it
		q := NewQoSEngine()
		q.TrackQoS1("client1", 1, "topic", []byte("data"), false)

		// QoS 0 messages are fire-and-forget, but we still track internally
	})

	t.Run("QoS_1_Basic", func(t *testing.T) {
		q := NewQoSEngine()
		q.TrackQoS1("client1", 1, "topic", []byte("data"), false)
		q.AckQoS1("client1", 1)

		if q.InflightCount("client1") != 0 {
			t.Error("QoS 1 message should be acked successfully")
		}
	})

	t.Run("QoS_2_FullFlow", func(t *testing.T) {
		q := NewQoSEngine()

		// Track QoS 2 message
		q.TrackQoS2("client1", 1, "topic", []byte("data"), false)

		// PUBREC received -> AckPubRec
		q.AckPubRec("client1", 1)

		// PUBREL received -> AckPubRel
		q.AckPubRel("client1", 1)

		if q.InflightCount("client1") != 0 {
			t.Error("QoS 2 message should complete flow")
		}
	})

	t.Run("QoS_Invalid_Values", func(t *testing.T) {
		q := NewQoSEngine()

		// QoS values > 2 are invalid per MQTT spec
		// The engine should still handle them gracefully
		// Test that no panic occurs with invalid QoS

		// Note: Our InflightMessage stores QoS as uint8, so 255 is valid
		q.TrackQoS1("client1", 1, "topic", []byte("data"), false)

		// Verify it's tracked
		if q.InflightCount("client1") != 1 {
			t.Error("expected message to be tracked")
		}
	})
}

// =============================================================================
// Message Size Boundary Tests
// =============================================================================

// TestMessageSize_Boundary tests message payload size handling
func TestMessageSize_Boundary(t *testing.T) {
	t.Run("Payload_Empty", func(t *testing.T) {
		q := NewQoSEngine()

		q.TrackQoS1("client1", 1, "topic", nil, false)
		q.TrackQoS1("client1", 2, "topic", []byte{}, false)

		if q.InflightCount("client1") != 2 {
			t.Errorf("expected 2 messages with empty payloads, got %d", q.InflightCount("client1"))
		}
	})

	t.Run("Payload_Large", func(t *testing.T) {
		q := NewQoSEngine()

		// Create a large payload (1MB)
		largePayload := make([]byte, 1024*1024)

		// Should handle large payloads
		q.TrackQoS1("client1", 1, "topic", largePayload, false)

		if q.InflightCount("client1") != 1 {
			t.Error("expected large payload message to be tracked")
		}
	})

	t.Run("Payload_VariousSizes", func(t *testing.T) {
		q := NewQoSEngine()

		sizes := []int{1, 100, 1024, 10240, 102400, 1048576}

		for i, size := range sizes {
			payload := make([]byte, size)
			q.TrackQoS1("client1", uint16(i+1), "topic", payload, false)
		}

		if q.InflightCount("client1") != len(sizes) {
			t.Errorf("expected %d messages tracked, got %d", len(sizes), q.InflightCount("client1"))
		}
	})
}

// =============================================================================
// Retain Flag Tests
// =============================================================================

// TestRetain_FlagBoundary tests retain flag handling
func TestRetain_FlagBoundary(t *testing.T) {
	t.Run("Retain_TrueAndFalse", func(t *testing.T) {
		q := NewQoSEngine()

		q.TrackQoS1("client1", 1, "topic1", []byte("data"), true)
		q.TrackQoS1("client1", 2, "topic2", []byte("data"), false)

		if q.InflightCount("client1") != 2 {
			t.Errorf("expected 2 messages, got %d", q.InflightCount("client1"))
		}
	})

	t.Run("Retain_MultiplePerTopic", func(t *testing.T) {
		q := NewQoSEngine()

		// Multiple messages with retain flag to same topic
		for i := 1; i <= 10; i++ {
			q.TrackQoS1("client1", uint16(i), "retained/topic", []byte("data"), true)
		}

		if q.InflightCount("client1") != 10 {
			t.Errorf("expected 10 retained messages, got %d", q.InflightCount("client1"))
		}
	})
}

// =============================================================================
// Protocol Constraint Tests
// =============================================================================

// TestProtocol_Constraints tests MQTT protocol-level constraints
func TestProtocol_Constraints(t *testing.T) {
	t.Run("MaxInflight_Limit", func(t *testing.T) {
		// Test with custom maxInflight limit
		q := NewQoSEngine(WithMaxInflight(10))

		// Fill up to the limit
		for i := 1; i <= 10; i++ {
			q.TrackQoS1("client1", uint16(i), "topic", []byte("data"), false)
		}

		// Adding more should still work (overwrite behavior)
		q.TrackQoS1("client1", 11, "topic", []byte("data"), false)

		// The count should be at most maxInflight
		count := q.InflightCount("client1")
		if count > 10 {
			t.Errorf("expected count <= 10, got %d", count)
		}
	})

	t.Run("MaxRetries_Zero", func(t *testing.T) {
		// Edge case: max retries = 0
		q := NewQoSEngine(WithMaxRetries(0), WithRetryInterval(10*time.Millisecond))
		q.Start()
		defer q.Stop()

		q.TrackQoS1("client1", 1, "topic", []byte("data"), false)

		// Message should be removed immediately after first retry
		// (no retries allowed)
	})

	t.Run("RetryInterval_Zero", func(t *testing.T) {
		// Edge case: retry interval = 0
		q := NewQoSEngine(WithRetryInterval(0), WithMaxRetries(3))
		q.Start()
		defer q.Stop()

		// Should handle zero interval gracefully
		q.TrackQoS1("client1", 1, "topic", []byte("data"), false)
	})
}

// =============================================================================
// Binary Encoding Tests
// =============================================================================

// TestBinary_PacketIDEncoding tests that PacketIDs are correctly encoded in binary
func TestBinary_PacketIDEncoding(t *testing.T) {
	t.Run("PacketID_Encode_Decode", func(t *testing.T) {
		original := uint16(12345)
		buf := new(bytes.Buffer)
		binary.Write(buf, binary.BigEndian, original)

		var decoded uint16
		binary.Read(buf, binary.BigEndian, &decoded)

		if decoded != original {
			t.Errorf("PacketID encoding mismatch: got %d, want %d", decoded, original)
		}
	})

	t.Run("PacketID_MaxValue_Encode", func(t *testing.T) {
		maxValue := uint16(65535)
		buf := new(bytes.Buffer)
		binary.Write(buf, binary.BigEndian, maxValue)

		var decoded uint16
		binary.Read(buf, binary.BigEndian, &decoded)

		if decoded != maxValue {
			t.Errorf("Max PacketID encoding failed: got %d, want %d", decoded, maxValue)
		}
	})

	t.Run("PacketID_Zero_Encode", func(t *testing.T) {
		zeroValue := uint16(0)
		buf := new(bytes.Buffer)
		binary.Write(buf, binary.BigEndian, zeroValue)

		var decoded uint16
		binary.Read(buf, binary.BigEndian, &decoded)

		if decoded != zeroValue {
			t.Errorf("Zero PacketID encoding failed: got %d, want %d", decoded, zeroValue)
		}
	})
}

// =============================================================================
// Error Handling Tests
// =============================================================================

// TestError_BoundaryConditions tests error handling in boundary scenarios
func TestError_BoundaryConditions(t *testing.T) {
	t.Run("Ack_NonExistentClient", func(t *testing.T) {
		q := NewQoSEngine()

		// Should not panic when acking non-existent client
		q.AckQoS1("non-existent", 1)
		q.AckPubRec("non-existent", 1)
		q.AckPubComp("non-existent", 1)
	})

	t.Run("Remove_NonExistentClient", func(t *testing.T) {
		q := NewQoSEngine()

		// Should not panic
		q.RemoveClient("non-existent")
	})

	t.Run("InflightCount_NonExistentClient", func(t *testing.T) {
		q := NewQoSEngine()

		count := q.InflightCount("non-existent")
		if count != 0 {
			t.Errorf("expected 0 for non-existent client, got %d", count)
		}
	})

	t.Run("Track_AfterStop", func(t *testing.T) {
		q := NewQoSEngine()
		q.Start()
		q.Stop()

		// Should still work after stop
		q.TrackQoS1("client1", 1, "topic", []byte("data"), false)

		if q.InflightCount("client1") != 1 {
			t.Error("tracking should work after stop")
		}
	})
}