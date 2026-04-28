// Package broker provides the core MQTT broker.
package broker

import (
	"context"
	"crypto/subtle"
	"errors"
	"sync"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

var (
	ErrAuthFailed      = errors.New("authentication failed")
	ErrAuthUnavailable = errors.New("authenticator unavailable")
	ErrUnauthorized    = errors.New("unauthorized")
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

// AddCredentials adds username/password pair.
func (s *StaticAuth) AddCredentials(username, password string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.credentials[username] = password
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

	if subtle.ConstantTimeCompare([]byte(password), []byte(expected)) == 0 {
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
