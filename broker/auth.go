// Package broker provides the core MQTT broker.
package broker

import (
	"context"
	"crypto/subtle"
	"errors"
	"sync"
)

var (
	ErrAuthFailed       = errors.New("authentication failed")
	ErrAuthUnavailable  = errors.New("authenticator unavailable")
	ErrUnauthorized     = errors.New("unauthorized")
)

// Authenticator handles client authentication.
type Authenticator interface {
	Authenticate(ctx context.Context, clientID, username, password string) error
}

// Authorizer handles topic-level authorization.
type Authorizer interface {
	CanPublish(ctx context.Context, clientID, topic string) bool
	CanSubscribe(ctx context.Context, clientID, topic string) bool
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

func (s *StaticAuth) CanPublish(ctx context.Context, clientID, topic string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	acl, ok := s.acls[clientID]
	if !ok {
		return false
	}
	for _, t := range acl.PublishTopics {
		if matchTopic(t, topic) {
			return true
		}
	}
	return false
}

func (s *StaticAuth) CanSubscribe(ctx context.Context, clientID, topic string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	acl, ok := s.acls[clientID]
	if !ok {
		return false
	}
	for _, t := range acl.SubscribeTopics {
		if matchTopic(t, topic) {
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

func (AllowAllAuth) CanPublish(ctx context.Context, clientID, topic string) bool {
	return true
}

func (AllowAllAuth) CanSubscribe(ctx context.Context, clientID, topic string) bool {
	return true
}

// DenyAllAuth denies all authentication.
type DenyAllAuth struct{}

func (DenyAllAuth) Authenticate(ctx context.Context, clientID, username, password string) error {
	return ErrAuthFailed
}

func (DenyAllAuth) CanPublish(ctx context.Context, clientID, topic string) bool {
	return false
}

func (DenyAllAuth) CanSubscribe(ctx context.Context, clientID, topic string) bool {
	return false
}

// matchTopic checks if a topic matches a wildcard pattern.
func matchTopic(pattern, topic string) bool {
	if pattern == topic {
		return true
	}
	return topicMatch(pattern, topic)
}

// topicMatch implements MQTT topic matching.
func topicMatch(pattern, topic string) bool {
	patternParts := splitPath(pattern)
	topicParts := splitPath(topic)

	if len(patternParts) > 0 && patternParts[0] == "#" {
		return true
	}

	if len(patternParts) > len(topicParts) {
		return false
	}

	for i, pp := range patternParts {
		if pp == "#" {
			return true
		}
		if pp == "+" {
			continue
		}
		if pp != topicParts[i] {
			return false
		}
	}

	return len(patternParts) == len(topicParts)
}

// splitPath splits a topic path by '/'.
func splitPath(path string) []string {
	parts := make([]string, 0)
	start := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || path[i] == '/' {
			parts = append(parts, path[start:i])
			start = i + 1
		}
	}
	return parts
}
