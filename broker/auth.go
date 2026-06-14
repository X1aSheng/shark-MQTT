// Package broker provides the core MQTT broker.
package broker

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"sync"

	"github.com/X1aSheng/shark-mqtt/errs"
	"github.com/X1aSheng/shark-mqtt/protocol"
	"golang.org/x/crypto/bcrypt"
)

// Re-export errs sentinels for backward compatibility.
var (
	ErrAuthFailed      = errs.ErrAuthFailed
	ErrAuthUnavailable = errors.New("authenticator unavailable")
	ErrUnauthorized    = errs.ErrNotAuthorized
)

// Authenticator handles client authentication.
type Authenticator interface {
	Authenticate(ctx context.Context, clientID, username, password string) error
}

// Authorizer handles topic-level authorization.
type Authorizer interface {
	CanPublish(ctx context.Context, username, topic string) bool
	CanSubscribe(ctx context.Context, username, topic string) bool
}

// StaticAuth implements both Authenticator and Authorizer with static credentials.
type StaticAuth struct {
	mu          sync.RWMutex
	credentials map[string]string // username -> password
	acls        map[string]*ACL   // username -> ACL
}

var (
	_ Authenticator = (*StaticAuth)(nil)
	_ Authorizer    = (*StaticAuth)(nil)
	_ Authenticator = AllowAllAuth{}
	_ Authorizer    = AllowAllAuth{}
	_ Authenticator = DenyAllAuth{}
	_ Authorizer    = DenyAllAuth{}
)

// ACL defines access control for a user.
type ACL struct {
	PublishTopics   []string
	SubscribeTopics []string
}

// NewStaticAuth creates a new static authenticator.
func NewStaticAuth() *StaticAuth {
	return &StaticAuth{
		credentials: make(map[string]string),
		acls:        make(map[string]*ACL),
	}
}

// AddCredentials adds username/password pair. Passwords are stored as-is
// for backward compatibility. Use SetHashedPassword to store bcrypt hashes.
func (s *StaticAuth) AddCredentials(username, password string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.credentials[username] = password
}

// SetHashedPassword adds a username/password pair where the password is
// automatically bcrypt-hashed before storage. This is the recommended method
// for production use to avoid storing plaintext passwords in memory.
func (s *StaticAuth) SetHashedPassword(username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.credentials[username] = string(hash)
	return nil
}

// IsBcryptHash reports whether a stored password string appears to be a
// bcrypt hash (starts with $2a$, $2b$, or $2y$).
func IsBcryptHash(stored string) bool {
	return strings.HasPrefix(stored, "$2a$") ||
		strings.HasPrefix(stored, "$2b$") ||
		strings.HasPrefix(stored, "$2y$")
}

// HashPassword generates a bcrypt hash of the given plaintext password.
// This can be used to pre-hash passwords for file-based auth configurations.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// AddACL adds access control for a user.
func (s *StaticAuth) AddACL(username string, acl *ACL) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acls[username] = acl
}

func (s *StaticAuth) Authenticate(ctx context.Context, clientID, username, password string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	expected, ok := s.credentials[username]
	if !ok {
		return ErrAuthFailed
	}

	// If the stored password is a bcrypt hash, use bcrypt comparison.
	// Otherwise fall back to constant-time compare for plaintext compat.
	if IsBcryptHash(expected) {
		if err := bcrypt.CompareHashAndPassword([]byte(expected), []byte(password)); err != nil {
			return ErrAuthFailed
		}
	} else if subtle.ConstantTimeCompare([]byte(password), []byte(expected)) == 0 {
		return ErrAuthFailed
	}
	return nil
}

func (s *StaticAuth) CanPublish(ctx context.Context, username, topic string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	acl, ok := s.acls[username]
	if !ok {
		return false
	}
	for _, t := range acl.PublishTopics {
		if protocol.MatchTopic(t, topic) {
			return true
		}
	}
	return false
}

func (s *StaticAuth) CanSubscribe(ctx context.Context, username, topic string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	acl, ok := s.acls[username]
	if !ok {
		return false
	}
	for _, t := range acl.SubscribeTopics {
		if protocol.MatchTopic(t, topic) {
			return true
		}
	}
	return false
}

// AllowAllAuth allows all authentication (development only).
type AllowAllAuth struct{}

func (AllowAllAuth) Authenticate(ctx context.Context, clientID, username, password string) error {
	return nil
}

func (AllowAllAuth) CanPublish(ctx context.Context, username, topic string) bool {
	return true
}

func (AllowAllAuth) CanSubscribe(ctx context.Context, username, topic string) bool {
	return true
}

// DenyAllAuth denies all authentication.
type DenyAllAuth struct{}

func (DenyAllAuth) Authenticate(ctx context.Context, clientID, username, password string) error {
	return ErrAuthFailed
}

func (DenyAllAuth) CanPublish(ctx context.Context, username, topic string) bool {
	return false
}

func (DenyAllAuth) CanSubscribe(ctx context.Context, username, topic string) bool {
	return false
}
