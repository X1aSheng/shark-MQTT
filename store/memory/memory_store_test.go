package memory

import (
	"context"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/store"
)

func TestSessionStore_SaveAndGet(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStore()

	data := &store.SessionData{
		ClientID: "test-client",
		IsClean:  false,
	}
	if err := s.SaveSession(ctx, "test-client", data); err != nil {
		t.Fatalf("save error: %v", err)
	}

	got, err := s.GetSession(ctx, "test-client")
	if err != nil {
		t.Fatalf("get error: %v", err)
	}
	if got.ClientID != "test-client" {
		t.Errorf("client ID: got %q, want test-client", got.ClientID)
	}

	// Test session not found
	_, err = s.GetSession(ctx, "nonexistent")
	if err != store.ErrSessionNotFound {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}

	// Test is session exists
	exists, err := s.IsSessionExists(ctx, "test-client")
	if err != nil || !exists {
		t.Errorf("expected session to exist")
	}

	exists, err = s.IsSessionExists(ctx, "nonexistent")
	if err != nil || exists {
		t.Errorf("expected session to not exist")
	}

	// Test delete
	if err := s.DeleteSession(ctx, "test-client"); err != nil {
		t.Fatalf("delete error: %v", err)
	}
	exists, err = s.IsSessionExists(ctx, "test-client")
	if err != nil || exists {
		t.Errorf("expected session to be deleted")
	}
}

func TestSessionStore_ListSessions(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStore()

	s.SaveSession(ctx, "client1", &store.SessionData{ClientID: "client1"})
	s.SaveSession(ctx, "client2", &store.SessionData{ClientID: "client2"})

	sessions, err := s.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestSessionStore_UpdateSession(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStore()

	// Save initial session
	data := &store.SessionData{
		ClientID: "client-update",
		IsClean:  true,
	}
	if err := s.SaveSession(ctx, "client-update", data); err != nil {
		t.Fatalf("save error: %v", err)
	}

	// Update session
	data.IsClean = false
	if err := s.SaveSession(ctx, "client-update", data); err != nil {
		t.Fatalf("update error: %v", err)
	}

	// Verify updated
	got, err := s.GetSession(ctx, "client-update")
	if err != nil {
		t.Fatalf("get error: %v", err)
	}
	if got.IsClean {
		t.Errorf("expected IsClean=false, got true")
	}
}

func TestSessionStore_EmptyList(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStore()

	sessions, err := s.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestSessionStore_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStore()

	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func(id int) {
			clientID := "client" + string(rune('0'+id))
			s.SaveSession(ctx, clientID, &store.SessionData{ClientID: clientID})
			s.GetSession(ctx, clientID)
			s.ListSessions(ctx)
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestMessageStore_SaveAndGet(t *testing.T) {
	ctx := context.Background()
	m := NewMessageStore()

	msg := &store.StoredMessage{
		ID:      "msg1",
		Topic:   "test/topic",
		QoS:     1,
		Payload: []byte("hello"),
	}

	if err := m.SaveMessage(ctx, "client1", msg); err != nil {
		t.Fatalf("save error: %v", err)
	}

	got, err := m.GetMessage(ctx, "client1", "msg1")
	if err != nil {
		t.Fatalf("get error: %v", err)
	}
	if got.Topic != "test/topic" {
		t.Errorf("topic: got %q, want test/topic", got.Topic)
	}
	if string(got.Payload) != "hello" {
		t.Errorf("payload: got %q, want hello", got.Payload)
	}

	// Test message not found
	_, err = m.GetMessage(ctx, "client1", "nonexistent")
	if err != store.ErrMessageNotFound {
		t.Errorf("expected ErrMessageNotFound, got %v", err)
	}

	// Test list
	msgs, err := m.ListMessages(ctx, "client1")
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}

	// Test clear
	if err := m.ClearMessages(ctx, "client1"); err != nil {
		t.Fatalf("clear error: %v", err)
	}
	msgs, err = m.ListMessages(ctx, "client1")
	if err != nil || len(msgs) != 0 {
		t.Errorf("expected messages to be cleared")
	}
}

func TestMessageStore_DeleteMessage(t *testing.T) {
	ctx := context.Background()
	m := NewMessageStore()

	msg := &store.StoredMessage{ID: "msg1", Topic: "t", QoS: 1, Payload: []byte("x")}
	m.SaveMessage(ctx, "client1", msg)
	m.SaveMessage(ctx, "client1", &store.StoredMessage{ID: "msg2", Topic: "t", QoS: 1, Payload: []byte("y")})

	if err := m.DeleteMessage(ctx, "client1", "msg1"); err != nil {
		t.Fatalf("delete error: %v", err)
	}

	// Verify msg1 is gone, msg2 remains
	_, err := m.GetMessage(ctx, "client1", "msg1")
	if err != store.ErrMessageNotFound {
		t.Errorf("expected msg1 to be deleted")
	}

	_, err = m.GetMessage(ctx, "client1", "msg2")
	if err != nil {
		t.Errorf("expected msg2 to still exist")
	}

	msgs, _ := m.ListMessages(ctx, "client1")
	if len(msgs) != 1 {
		t.Errorf("expected 1 message, got %d", len(msgs))
	}
}

func TestMessageStore_MultipleClients(t *testing.T) {
	ctx := context.Background()
	m := NewMessageStore()

	m.SaveMessage(ctx, "client1", &store.StoredMessage{ID: "msg1", Topic: "t", QoS: 1, Payload: []byte("a")})
	m.SaveMessage(ctx, "client2", &store.StoredMessage{ID: "msg1", Topic: "t", QoS: 1, Payload: []byte("b")})

	msgs1, _ := m.ListMessages(ctx, "client1")
	msgs2, _ := m.ListMessages(ctx, "client2")

	if len(msgs1) != 1 {
		t.Errorf("expected 1 message for client1, got %d", len(msgs1))
	}
	if len(msgs2) != 1 {
		t.Errorf("expected 1 message for client2, got %d", len(msgs2))
	}
	if string(msgs1[0].Payload) != "a" {
		t.Errorf("expected payload a, got %s", msgs1[0].Payload)
	}
	if string(msgs2[0].Payload) != "b" {
		t.Errorf("expected payload b, got %s", msgs2[0].Payload)
	}
}

func TestRetainedStore_SaveAndGet(t *testing.T) {
	ctx := context.Background()
	r := NewRetainedStore()

	// Test save and get
	if err := r.SaveRetained(ctx, "sensor/temp", 1, []byte("25.5")); err != nil {
		t.Fatalf("save error: %v", err)
	}

	got, err := r.GetRetained(ctx, "sensor/temp")
	if err != nil {
		t.Fatalf("get error: %v", err)
	}
	if got.Topic != "sensor/temp" {
		t.Errorf("topic: got %q, want sensor/temp", got.Topic)
	}
	if string(got.Payload) != "25.5" {
		t.Errorf("payload: got %q, want 25.5", got.Payload)
	}

	// Test not found
	_, err = r.GetRetained(ctx, "nonexistent")
	if err != store.ErrRetainedNotFound {
		t.Errorf("expected ErrRetainedNotFound, got %v", err)
	}

	// Test delete
	if err := r.DeleteRetained(ctx, "sensor/temp"); err != nil {
		t.Fatalf("delete error: %v", err)
	}
	_, err = r.GetRetained(ctx, "sensor/temp")
	if err != store.ErrRetainedNotFound {
		t.Errorf("expected not found after delete")
	}
}

func TestRetainedStore_Match(t *testing.T) {
	ctx := context.Background()
	r := NewRetainedStore()

	r.SaveRetained(ctx, "sensor/room1/temp", 1, []byte("20"))
	r.SaveRetained(ctx, "sensor/room2/temp", 1, []byte("22"))
	r.SaveRetained(ctx, "sensor/room1/humidity", 1, []byte("60"))

	// Test wildcard +
	matched, err := r.MatchRetained(ctx, "sensor/+/temp")
	if err != nil {
		t.Fatalf("match error: %v", err)
	}
	if len(matched) != 2 {
		t.Errorf("expected 2 matches, got %d", len(matched))
	}

	// Test wildcard #
	matched, err = r.MatchRetained(ctx, "sensor/#")
	if err != nil {
		t.Fatalf("match error: %v", err)
	}
	if len(matched) != 3 {
		t.Errorf("expected 3 matches, got %d", len(matched))
	}

	// Test exact match
	matched, err = r.MatchRetained(ctx, "sensor/room1/temp")
	if err != nil {
		t.Fatalf("match error: %v", err)
	}
	if len(matched) != 1 {
		t.Errorf("expected 1 match, got %d", len(matched))
	}
}

func TestRetainedStore_EmptyPayload(t *testing.T) {
	ctx := context.Background()
	r := NewRetainedStore()

	// Save with payload, then save empty to clear
	r.SaveRetained(ctx, "test/topic", 1, []byte("data"))
	r.SaveRetained(ctx, "test/topic", 1, []byte{})

	_, err := r.GetRetained(ctx, "test/topic")
	if err != store.ErrRetainedNotFound {
		t.Errorf("expected not found after empty payload save")
	}
}

func TestRetainedStore_UpdateRetained(t *testing.T) {
	ctx := context.Background()
	r := NewRetainedStore()

	r.SaveRetained(ctx, "test/topic", 1, []byte("v1"))
	r.SaveRetained(ctx, "test/topic", 2, []byte("v2"))

	got, err := r.GetRetained(ctx, "test/topic")
	if err != nil {
		t.Fatalf("get error: %v", err)
	}
	if got.QoS != 2 {
		t.Errorf("expected QoS 2, got %d", got.QoS)
	}
	if string(got.Payload) != "v2" {
		t.Errorf("expected payload v2, got %s", got.Payload)
	}
}

func TestRetainedStore_NoMatches(t *testing.T) {
	ctx := context.Background()
	r := NewRetainedStore()

	matched, err := r.MatchRetained(ctx, "#")
	if err != nil {
		t.Fatalf("match error: %v", err)
	}
	if len(matched) != 0 {
		t.Errorf("expected 0 matches for empty store, got %d", len(matched))
	}
}

func TestRetainedStore_ConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	r := NewRetainedStore()

	done := make(chan bool, 10)

	for i := 0; i < 10; i++ {
		go func(id int) {
			topic := "test/" + string(rune('0'+id))
			r.SaveRetained(ctx, topic, 1, []byte("data"))
			r.GetRetained(ctx, topic)
			r.MatchRetained(ctx, "test/#")
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestSessionStore_SaveIsolation(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStore()

	payload := []byte("original-payload")
	data := &store.SessionData{
		ClientID: "isolate-client",
		Subscriptions: []store.Subscription{
			{Topic: "a/b", QoS: 1},
		},
		Inflight: map[uint16]*store.InflightMessage{
			1: {PacketID: 1, QoS: 1, Topic: "a/b", Payload: payload},
		},
	}
	if err := s.SaveSession(ctx, data.ClientID, data); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// Mutating the caller's data after Save must not affect the store.
	payload[0] = 'X'
	data.Subscriptions[0].Topic = "mutated"
	data.Inflight[1].Payload[0] = 'Y'

	got, err := s.GetSession(ctx, data.ClientID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	if got.Subscriptions[0].Topic != "a/b" {
		t.Errorf("subscription leaked after save: got %q", got.Subscriptions[0].Topic)
	}
	if string(got.Inflight[1].Payload) != "original-payload" {
		t.Errorf("inflight payload leaked after save: got %q", string(got.Inflight[1].Payload))
	}
}

// TestSessionStore_InflightPropertiesRoundTrip covers the MQTT 5 relay data a
// restored session needs: §3.3.2.3 requires a retransmission after a restart to
// carry the same Response Topic, Correlation Data, User Properties, Content
// Type and Payload Format Indicator, and §3.3.2.3.2 requires the Message Expiry
// deadline to still be enforceable. It also proves the copies are deep in both
// directions, so neither the caller's slice nor the returned one can reach into
// the stored message.
func TestSessionStore_InflightPropertiesRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStore()

	pfi := byte(1)
	subID := uint32(9)
	deadline := time.Now().Add(5 * time.Minute).Truncate(time.Millisecond)
	payload := []byte("body")
	correlation := []byte{0xDE, 0xAD}
	userProps := []store.UserProperty{
		{Key: "trace", Value: "1"},
		{Key: "tenant", Value: "acme"},
		{Key: "trace", Value: "2"}, // same key twice: order must survive
	}
	data := &store.SessionData{
		ClientID: "props-client",
		Inflight: map[uint16]*store.InflightMessage{
			7: {
				PacketID: 7, QoS: 1, Topic: "props/round-trip", Payload: payload, Retain: true,
				ExpiresAt: deadline,
				Properties: store.MessageProperties{
					PayloadFormat:          &pfi,
					ContentType:            "application/json",
					ResponseTopic:          "props/reply",
					CorrelationData:        correlation,
					UserProperties:         userProps,
					SubscriptionIdentifier: &subID,
				},
			},
		},
		InboundQoS2: map[uint16]*store.InflightMessage{
			9: {
				PacketID: 9, QoS: 2, Topic: "props/inbound", Payload: []byte("staged"),
				Properties: store.MessageProperties{ContentType: "text/plain"},
			},
		},
	}
	if err := s.SaveSession(ctx, data.ClientID, data); err != nil {
		t.Fatalf("SaveSession failed: %v", err)
	}

	// Mutating what was handed in must not reach the store, including the new
	// reference-typed fields.
	payload[0] = 'X'
	correlation[0] = 'Y'
	userProps[0].Key = "mutated"
	pfi = 0
	subID = 0

	got, err := s.GetSession(ctx, data.ClientID)
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}
	msg, ok := got.Inflight[7]
	if !ok {
		t.Fatal("inflight message 7 missing after round trip")
	}
	if !msg.ExpiresAt.Equal(deadline) {
		t.Errorf("ExpiresAt = %v, want %v (retransmission must still see the deadline)",
			msg.ExpiresAt, deadline)
	}
	if msg.Properties.PayloadFormat == nil || *msg.Properties.PayloadFormat != 1 {
		t.Errorf("PayloadFormat = %v, want 1", msg.Properties.PayloadFormat)
	}
	if msg.Properties.ContentType != "application/json" {
		t.Errorf("ContentType = %q, want application/json", msg.Properties.ContentType)
	}
	if msg.Properties.ResponseTopic != "props/reply" {
		t.Errorf("ResponseTopic = %q, want props/reply", msg.Properties.ResponseTopic)
	}
	if string(msg.Properties.CorrelationData) != string([]byte{0xDE, 0xAD}) {
		t.Errorf("CorrelationData = %x, want dead", msg.Properties.CorrelationData)
	}
	if msg.Properties.SubscriptionIdentifier == nil || *msg.Properties.SubscriptionIdentifier != 9 {
		t.Errorf("SubscriptionIdentifier = %v, want 9", msg.Properties.SubscriptionIdentifier)
	}
	want := []store.UserProperty{{Key: "trace", Value: "1"}, {Key: "tenant", Value: "acme"}, {Key: "trace", Value: "2"}}
	if len(msg.Properties.UserProperties) != len(want) {
		t.Fatalf("UserProperties = %+v, want %+v", msg.Properties.UserProperties, want)
	}
	for i := range want {
		if msg.Properties.UserProperties[i] != want[i] {
			t.Errorf("UserProperties[%d] = %+v, want %+v (order must survive the round trip)",
				i, msg.Properties.UserProperties[i], want[i])
		}
	}
	if string(msg.Payload) != "body" {
		t.Errorf("Payload = %q, want body", string(msg.Payload))
	}

	if inbound := got.InboundQoS2[9]; inbound == nil || inbound.Properties.ContentType != "text/plain" {
		t.Errorf("inbound QoS 2 properties lost: %+v", inbound)
	}

	// The copy handed out by GetSession must not be the store's own, either.
	msg.Properties.ContentType = "mutated"
	msg.Properties.UserProperties[0].Value = "mutated"
	msg.Properties.CorrelationData[0] = 'Z'

	again, err := s.GetSession(ctx, data.ClientID)
	if err != nil {
		t.Fatalf("second GetSession failed: %v", err)
	}
	if again.Inflight[7].Properties.ContentType != "application/json" {
		t.Errorf("store mutated through the returned copy: ContentType = %q",
			again.Inflight[7].Properties.ContentType)
	}
	if again.Inflight[7].Properties.UserProperties[0].Value != "1" {
		t.Errorf("store mutated through the returned user properties: %+v",
			again.Inflight[7].Properties.UserProperties)
	}
	if again.Inflight[7].Properties.CorrelationData[0] != 0xDE {
		t.Errorf("store mutated through the returned correlation data: %x",
			again.Inflight[7].Properties.CorrelationData)
	}
}
