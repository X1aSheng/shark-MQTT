package badger

import (
	"context"
	"os"
	"testing"

	"github.com/X1aSheng/shark-mqtt/store"
	"github.com/dgraph-io/badger/v4"
)

func setupBadger(t *testing.T) *badger.DB {
	t.Helper()
	dir, err := os.MkdirTemp("", "badger-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	opts := badger.DefaultOptions(dir).WithLogger(nil)
	db, err := badger.Open(opts)
	if err != nil {
		t.Fatalf("failed to open badger: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestBadgerSessionStore_SaveAndGet(t *testing.T) {
	ctx := context.Background()
	db := setupBadger(t)
	s := NewSessionStore(SessionStoreConfig{DB: db})

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
	if got.ClientID != "test-client" || got.IsClean {
		t.Errorf("unexpected session data: %+v", got)
	}

	_, err = s.GetSession(ctx, "nonexistent")
	if err != store.ErrSessionNotFound {
		t.Errorf("expected ErrSessionNotFound, got %v", err)
	}
}

func TestBadgerSessionStore_Delete(t *testing.T) {
	ctx := context.Background()
	db := setupBadger(t)
	s := NewSessionStore(SessionStoreConfig{DB: db})

	s.SaveSession(ctx, "c1", &store.SessionData{ClientID: "c1"})
	if err := s.DeleteSession(ctx, "c1"); err != nil {
		t.Fatalf("delete error: %v", err)
	}

	exists, err := s.IsSessionExists(ctx, "c1")
	if err != nil || exists {
		t.Errorf("expected session to be deleted")
	}
}

func TestBadgerSessionStore_List(t *testing.T) {
	ctx := context.Background()
	db := setupBadger(t)
	s := NewSessionStore(SessionStoreConfig{DB: db})

	s.SaveSession(ctx, "c1", &store.SessionData{ClientID: "c1"})
	s.SaveSession(ctx, "c2", &store.SessionData{ClientID: "c2"})

	sessions, err := s.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestBadgerMessageStore_SaveAndGet(t *testing.T) {
	ctx := context.Background()
	db := setupBadger(t)
	m := NewMessageStore(MessageStoreConfig{DB: db})

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
	if got.Topic != "test/topic" || string(got.Payload) != "hello" {
		t.Errorf("unexpected message: %+v", got)
	}

	_, err = m.GetMessage(ctx, "client1", "nonexistent")
	if err != store.ErrMessageNotFound {
		t.Errorf("expected ErrMessageNotFound, got %v", err)
	}
}

func TestBadgerMessageStore_DeleteAndClear(t *testing.T) {
	ctx := context.Background()
	db := setupBadger(t)
	m := NewMessageStore(MessageStoreConfig{DB: db})

	m.SaveMessage(ctx, "c1", &store.StoredMessage{ID: "m1", Topic: "t", QoS: 1, Payload: []byte("a")})
	m.SaveMessage(ctx, "c1", &store.StoredMessage{ID: "m2", Topic: "t", QoS: 1, Payload: []byte("b")})

	if err := m.DeleteMessage(ctx, "c1", "m1"); err != nil {
		t.Fatalf("delete error: %v", err)
	}

	msgs, _ := m.ListMessages(ctx, "c1")
	if len(msgs) != 1 {
		t.Errorf("expected 1 message after delete, got %d", len(msgs))
	}

	if err := m.ClearMessages(ctx, "c1"); err != nil {
		t.Fatalf("clear error: %v", err)
	}

	msgs, _ = m.ListMessages(ctx, "c1")
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after clear, got %d", len(msgs))
	}
}

func TestBadgerRetainedStore_SaveAndGet(t *testing.T) {
	ctx := context.Background()
	db := setupBadger(t)
	r := NewRetainedStore(RetainedStoreConfig{DB: db})

	if err := r.SaveRetained(ctx, "sensor/temp", 1, []byte("25.5")); err != nil {
		t.Fatalf("save error: %v", err)
	}

	got, err := r.GetRetained(ctx, "sensor/temp")
	if err != nil {
		t.Fatalf("get error: %v", err)
	}
	if got.Topic != "sensor/temp" || string(got.Payload) != "25.5" {
		t.Errorf("unexpected retained message: %+v", got)
	}

	_, err = r.GetRetained(ctx, "nonexistent")
	if err != store.ErrRetainedNotFound {
		t.Errorf("expected ErrRetainedNotFound, got %v", err)
	}
}

func TestBadgerRetainedStore_Match(t *testing.T) {
	ctx := context.Background()
	db := setupBadger(t)
	r := NewRetainedStore(RetainedStoreConfig{DB: db})

	r.SaveRetained(ctx, "sensor/room1/temp", 1, []byte("20"))
	r.SaveRetained(ctx, "sensor/room2/temp", 1, []byte("22"))
	r.SaveRetained(ctx, "sensor/room1/humidity", 1, []byte("60"))

	matched, err := r.MatchRetained(ctx, "sensor/+/temp")
	if err != nil {
		t.Fatalf("match error: %v", err)
	}
	if len(matched) != 2 {
		t.Errorf("expected 2 matches, got %d", len(matched))
	}

	matched, err = r.MatchRetained(ctx, "sensor/#")
	if err != nil {
		t.Fatalf("match error: %v", err)
	}
	if len(matched) != 3 {
		t.Errorf("expected 3 matches, got %d", len(matched))
	}
}

func TestBadgerRetainedStore_DeleteRetained(t *testing.T) {
	ctx := context.Background()
	db := setupBadger(t)
	r := NewRetainedStore(RetainedStoreConfig{DB: db})

	r.SaveRetained(ctx, "test/topic", 1, []byte("data"))
	if err := r.DeleteRetained(ctx, "test/topic"); err != nil {
		t.Fatalf("delete error: %v", err)
	}

	_, err := r.GetRetained(ctx, "test/topic")
	if err != store.ErrRetainedNotFound {
		t.Errorf("expected not found after delete")
	}
}

func TestBadgerRetainedStore_EmptyPayload(t *testing.T) {
	ctx := context.Background()
	db := setupBadger(t)
	r := NewRetainedStore(RetainedStoreConfig{DB: db})

	r.SaveRetained(ctx, "test/topic", 1, []byte("data"))
	// Per MQTT spec, a retained message with zero-length payload deletes the existing retained message.
	r.SaveRetained(ctx, "test/topic", 1, []byte{})

	_, err := r.GetRetained(ctx, "test/topic")
	if err == nil {
		t.Fatal("expected ErrRetainedNotFound after deleting with empty payload")
	}
}
