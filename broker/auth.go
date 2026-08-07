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
	ErrUserNotFound    = errs.ErrUserNotFound
	ErrAuthUnavailable = errors.New("authenticator unavailable")
	ErrUnauthorized    = errs.ErrNotAuthorized
)

// Authenticator handles client authentication.
type Authenticator interface {
	Authenticate(ctx context.Context, clientID, username, password string) error
}

// EnhancedAuthenticator implements MQTT 5.0 enhanced authentication (§4.12).
// It is stateful per connection. The broker calls Initial with the
// AuthenticationData from CONNECT, then Continue for each subsequent AUTH
// packet, until a non-continue reason code is returned.
//
// Reason codes: protocol.AuthContinueAuth (0x18) continues the exchange;
// protocol.AuthSuccess (0x00) completes it. Any other reason code (e.g.
// ReasonCodeNotAuthorized) rejects the connection.
type EnhancedAuthenticator interface {
	// Method returns the AuthenticationMethod name this authenticator handles.
	Method() string
	// Initial processes the CONNECT AuthenticationData.
	Initial(data []byte) (reasonCode byte, responseData []byte, err error)
	// Continue processes a subsequent AUTH packet's AuthenticationData.
	Continue(data []byte) (reasonCode byte, responseData []byte, err error)
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
	bcryptCost  int               // cost for SetHashedPassword; bcrypt.DefaultCost unless overridden
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
		bcryptCost:  bcrypt.DefaultCost,
	}
}

// SetBcryptCost sets the bcrypt work factor used by SetHashedPassword.
// Lower costs are faster (useful at high connection rates) at the expense of
// weaker hash resistance; the cost is stored in the generated hash itself, so
// verification always uses the hash's own cost regardless of this setting.
func (s *StaticAuth) SetBcryptCost(cost int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bcryptCost = cost
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
	s.mu.RLock()
	cost := s.bcryptCost
	s.mu.RUnlock()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
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
		// Distinguish "user not recognized" so an authentication chain can
		// continue to the next authenticator while still failing closed for
		// a recognized user with wrong credentials.
		return ErrUserNotFound
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
		if matchWithSysProtection(t, topic) {
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
		if matchWithSysProtection(t, topic) {
			return true
		}
	}
	return false
}

// matchWithSysProtection wraps protocol.MatchTopic with MQTT §4.7.2
// system topic protection: root-level + and # wildcards must not
// match topics starting with $.
func matchWithSysProtection(pattern, topic string) bool {
	if len(topic) > 0 && topic[0] == '$' {
		firstLevel := pattern
		if i := strings.IndexByte(pattern, '/'); i >= 0 {
			firstLevel = pattern[:i]
		}
		if firstLevel == "#" || firstLevel == "+" {
			return false
		}
	}
	return protocol.MatchTopic(pattern, topic)
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
