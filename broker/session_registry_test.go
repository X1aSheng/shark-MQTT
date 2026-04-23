package broker

import (
	"context"
	"net"
	"testing"

	"github.com/X1aSheng/shark-mqtt/store"
)

type mockSessionStore struct{}

func (m *mockSessionStore) SaveSession(_ context.Context, _ string, _ *store.SessionData) error {
	return nil
}
func (m *mockSessionStore) GetSession(_ context.Context, _ string) (*store.SessionData, error) {
	return nil, context.Canceled
}
func (m *mockSessionStore) DeleteSession(_ context.Context, _ string) error {
	return nil
}
func (m *mockSessionStore) ListSessions(_ context.Context) ([]string, error) {
	return nil, nil
}
func (m *mockSessionStore) IsSessionExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func TestSessionRegistry_Register(t *testing.T) {
	sr := NewSessionRegistry(&mockSessionStore{})
	conn := &net.TCPConn{}
	sess := sr.Register("client1", conn)

	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	if sess.ClientID != "client1" {
		t.Errorf("expected clientID 'client1', got '%s'", sess.ClientID)
	}
	if !sess.IsConnected() {
		t.Error("expected session to be connected")
	}
	if count := sr.Count(); count != 1 {
		t.Errorf("expected count 1, got %d", count)
	}
}

func TestSessionRegistry_Unregister(t *testing.T) {
	sr := NewSessionRegistry(&mockSessionStore{})
	conn := &net.TCPConn{}
	sr.Register("client1", conn)
	sr.Unregister("client1")

	if count := sr.Count(); count != 0 {
		t.Errorf("expected count 0 after unregister, got %d", count)
	}
}

func TestSessionRegistry_Kick(t *testing.T) {
	sr := NewSessionRegistry(&mockSessionStore{})
	conn1 := &net.TCPConn{}
	sr.Register("client1", conn1)

	// Register same clientID again - should kick old session
	conn2 := &net.TCPConn{}
	sess := sr.Register("client1", conn2)

	if sess == nil {
		t.Fatal("expected new session")
	}
	if count := sr.Count(); count != 1 {
		t.Errorf("expected count 1 after kick, got %d", count)
	}
}

func TestSessionRegistry_ReplaceSession(t *testing.T) {
	sr := NewSessionRegistry(&mockSessionStore{})
	conn1 := &net.TCPConn{}
	sess1 := sr.Register("client1", conn1)
	sess1.AddSubscription("test/#", 1)

	conn2 := &net.TCPConn{}
	sess2 := sr.ReplaceSession("client1", conn2)

	if sess2 == nil {
		t.Fatal("expected session")
	}
	if len(sess2.Subscriptions) != len(sess1.Subscriptions) {
		t.Errorf("expected subscriptions preserved: got %d, want %d", len(sess2.Subscriptions), len(sess1.Subscriptions))
	}
}

func TestSessionRegistry_CloseAll(t *testing.T) {
	sr := NewSessionRegistry(&mockSessionStore{})
	sr.Register("client1", &net.TCPConn{})
	sr.Register("client2", &net.TCPConn{})

	sr.CloseAll(ReasonServerShut)

	if count := sr.Count(); count != 0 {
		t.Errorf("expected count 0 after close all, got %d", count)
	}
}
