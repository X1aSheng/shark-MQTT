package broker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

func TestNoopAuth(t *testing.T) {
	a := NoopAuth{}
	ctx := context.Background()

	if err := a.Authenticate(ctx, "client1", "user", "pass"); err != nil {
		t.Errorf("expected success, got %v", err)
	}
	if err := a.Authenticate(ctx, "client2", "", ""); err != nil {
		t.Errorf("expected success with empty credentials, got %v", err)
	}
}

func TestChainAuth_NoAuthenticators(t *testing.T) {
	chain := NewChainAuth()
	ctx := context.Background()

	err := chain.Authenticate(ctx, "client1", "user", "pass")
	if err != ErrAuthUnavailable {
		t.Errorf("expected ErrAuthUnavailable, got %v", err)
	}
}

func TestChainAuth_SingleSuccess(t *testing.T) {
	chain := NewChainAuth(AllowAllAuth{})
	ctx := context.Background()

	if err := chain.Authenticate(ctx, "client1", "any", "creds"); err != nil {
		t.Errorf("expected success, got %v", err)
	}
}

func TestChainAuth_FirstSuccess(t *testing.T) {
	chain := NewChainAuth(AllowAllAuth{}, DenyAllAuth{})
	ctx := context.Background()

	if err := chain.Authenticate(ctx, "client1", "any", "creds"); err != nil {
		t.Errorf("expected first auth to succeed, got %v", err)
	}
}

func TestChainAuth_FallthroughToSecond(t *testing.T) {
	deny := DenyAllAuth{}
	allow := AllowAllAuth{}
	chain := NewChainAuth(deny, allow)
	ctx := context.Background()

	if err := chain.Authenticate(ctx, "client1", "any", "creds"); err != nil {
		t.Errorf("expected second auth to succeed, got %v", err)
	}
}

func TestChainAuth_AllFail(t *testing.T) {
	chain := NewChainAuth(DenyAllAuth{}, DenyAllAuth{})
	ctx := context.Background()

	err := chain.Authenticate(ctx, "client1", "any", "creds")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestChainAuth_AddAuthenticator(t *testing.T) {
	chain := NewChainAuth()
	chain.AddAuthenticator(AllowAllAuth{})
	ctx := context.Background()

	if err := chain.Authenticate(ctx, "client1", "user", "pass"); err != nil {
		t.Errorf("expected success after adding authenticator, got %v", err)
	}
}

func TestFileAuth_LoadAndAuthenticate(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "auth.yaml")
	content := `
users:
  - username: admin
    password: secret123
    client_ids: ["admin-client"]
  - username: reader
    password: readonly
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write auth file: %v", err)
	}

	fa, err := NewFileAuth(cfgPath)
	if err != nil {
		t.Fatalf("NewFileAuth failed: %v", err)
	}

	ctx := context.Background()

	// Valid auth
	if err := fa.Authenticate(ctx, "admin-client", "admin", "secret123"); err != nil {
		t.Errorf("expected success for valid credentials, got %v", err)
	}

	// Wrong password
	if err := fa.Authenticate(ctx, "admin-client", "admin", "wrong"); err != ErrAuthFailed {
		t.Errorf("expected ErrAuthFailed for wrong password, got %v", err)
	}

	// Unknown user
	if err := fa.Authenticate(ctx, "admin-client", "unknown", "x"); err != ErrAuthFailed {
		t.Errorf("expected ErrAuthFailed for unknown user, got %v", err)
	}
}

func TestFileAuth_ClientIDRestriction(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "auth.yaml")
	content := `
users:
  - username: admin
    password: secret
    client_ids: ["allowed-client"]
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write auth file: %v", err)
	}

	fa, err := NewFileAuth(cfgPath)
	if err != nil {
		t.Fatalf("NewFileAuth failed: %v", err)
	}

	ctx := context.Background()

	// Allowed client ID
	if err := fa.Authenticate(ctx, "allowed-client", "admin", "secret"); err != nil {
		t.Errorf("expected success for allowed client, got %v", err)
	}

	// Disallowed client ID
	if err := fa.Authenticate(ctx, "unauthorized-client", "admin", "secret"); err != ErrUnauthorized {
		t.Errorf("expected ErrUnauthorized for unauthorized client, got %v", err)
	}
}

func TestFileAuth_JSONFormat(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "auth.json")
	content := `{"users":[{"username":"admin","password":"pass"}]}`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write auth file: %v", err)
	}

	fa, err := NewFileAuth(cfgPath)
	if err != nil {
		t.Fatalf("NewFileAuth failed: %v", err)
	}

	ctx := context.Background()
	if err := fa.Authenticate(ctx, "client1", "admin", "pass"); err != nil {
		t.Errorf("expected success for JSON auth, got %v", err)
	}
}

func TestFileAuth_UsersCopy(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "auth.yaml")
	content := `users: [{username: "user1", password: "pass1"}]`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write auth file: %v", err)
	}

	fa, err := NewFileAuth(cfgPath)
	if err != nil {
		t.Fatalf("NewFileAuth failed: %v", err)
	}

	users := fa.Users()
	if len(users) != 1 || users[0].Username != "user1" {
		t.Errorf("expected [user1], got %+v", users)
	}
}

func TestFileAuth_NonExistentFile(t *testing.T) {
	_, err := NewFileAuth("/nonexistent/auth.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestFileAuth_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(cfgPath, []byte("{{invalid yaml}"), 0644); err != nil {
		t.Fatalf("failed to write auth file: %v", err)
	}

	_, err := NewFileAuth(cfgPath)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestFileAuth_LoadFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "auth.yaml")
	content1 := `users: [{username: "user1", password: "pass1"}]`
	if err := os.WriteFile(cfgPath, []byte(content1), 0644); err != nil {
		t.Fatalf("failed to write auth file: %v", err)
	}

	fa, err := NewFileAuth(cfgPath)
	if err != nil {
		t.Fatalf("NewFileAuth failed: %v", err)
	}

	// Reload with different users
	content2 := `users: [{username: "user2", password: "pass2"}]`
	if err := os.WriteFile(cfgPath, []byte(content2), 0644); err != nil {
		t.Fatalf("failed to write auth file: %v", err)
	}

	if err := fa.LoadFile(cfgPath); err != nil {
		t.Fatalf("LoadFile failed: %v", err)
	}

	ctx := context.Background()
	// Old user should no longer work
	if err := fa.Authenticate(ctx, "c1", "user1", "pass1"); err != ErrAuthFailed {
		t.Errorf("expected old user to be gone after reload, got %v", err)
	}
	// New user should work
	if err := fa.Authenticate(ctx, "c1", "user2", "pass2"); err != nil {
		t.Errorf("expected new user to work after reload, got %v", err)
	}
}

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
