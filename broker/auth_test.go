package broker

import (
	"context"
	"testing"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

func TestStaticAuthAuthenticate(t *testing.T) {
	a := NewStaticAuth()
	a.AddCredentials("admin", "secret")
	a.AddCredentials("user1", "pass123")

	ctx := context.Background()

	// Test valid auth
	if err := a.Authenticate(ctx, "client1", "admin", "secret"); err != nil {
		t.Errorf("expected success, got %v", err)
	}

	// Test invalid user
	err := a.Authenticate(ctx, "client1", "nonexistent", "secret")
	if err != ErrAuthFailed {
		t.Errorf("expected ErrAuthFailed, got %v", err)
	}

	// Test invalid password
	err = a.Authenticate(ctx, "client1", "admin", "wrong")
	if err != ErrAuthFailed {
		t.Errorf("expected ErrAuthFailed, got %v", err)
	}
}

func TestStaticAuthACL(t *testing.T) {
	a := NewStaticAuth()
	a.AddACL("client1", &ACL{
		PublishTopics:   []string{"data/#"},
		SubscribeTopics: []string{"status/+"},
	})

	ctx := context.Background()

	// Test publish allowed
	if !a.CanPublish(ctx, "client1", "data/room1") {
		t.Error("expected publish to be allowed")
	}

	// Test publish denied
	if a.CanPublish(ctx, "client1", "other/topic") {
		t.Error("expected publish to be denied")
	}

	// Test subscribe allowed
	if !a.CanSubscribe(ctx, "client1", "status/room1") {
		t.Error("expected subscribe to be allowed")
	}

	// Test subscribe denied
	if a.CanSubscribe(ctx, "client1", "other/topic") {
		t.Error("expected subscribe to be denied")
	}
}

func TestAllowAllAuth(t *testing.T) {
	a := AllowAllAuth{}
	ctx := context.Background()

	if err := a.Authenticate(ctx, "client1", "any", "pass"); err != nil {
		t.Errorf("expected success, got %v", err)
	}
	if !a.CanPublish(ctx, "client1", "any/topic") {
		t.Error("expected publish to be allowed")
	}
	if !a.CanSubscribe(ctx, "client1", "any/topic") {
		t.Error("expected subscribe to be allowed")
	}
}

func TestDenyAllAuth(t *testing.T) {
	a := DenyAllAuth{}
	ctx := context.Background()

	err := a.Authenticate(ctx, "client1", "any", "pass")
	if err != ErrAuthFailed {
		t.Errorf("expected ErrAuthFailed, got %v", err)
	}
	if a.CanPublish(ctx, "client1", "any/topic") {
		t.Error("expected publish to be denied")
	}
	if a.CanSubscribe(ctx, "client1", "any/topic") {
		t.Error("expected subscribe to be denied")
	}
}

func TestTopicMatch(t *testing.T) {
	tests := []struct {
		pattern string
		topic   string
		want    bool
	}{
		{"data/#", "data/room1", true},
		{"status/+", "status/room1", true},
		{"status/+", "status/room1/temp", false},
		{"#", "anything/goes", true},
		{"exact/topic", "exact/topic", true},
		{"exact/topic", "exact/other", false},
	}

	for _, tt := range tests {
		if got := protocol.MatchTopic(tt.pattern, tt.topic); got != tt.want {
			t.Errorf("MatchTopic(%q, %q) = %v, want %v", tt.pattern, tt.topic, got, tt.want)
		}
	}
}
