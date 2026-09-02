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
		if aclCovers(t, topic) {
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
		if aclCovers(t, topic) {
			return true
		}
	}
	return false
}

// aclCovers reports whether the ACL pattern covers the requested topic or
// topic filter, i.e. every topic matched by the request is also matched by
// the ACL pattern. Matching the request *filter* literally against the ACL
// pattern was wrong: wildcard-to-wildcard comparison let an ACL of "a/+"
// authorize a subscription to "a/#" (a strictly wider set), and vice versa
// (audit H7).
//
// Rules (per level):
//   - an ACL '#' level covers the request and everything below it;
//   - an exact ACL level covers only the same exact request level;
//   - an ACL '+' level covers a literal request level or another '+', but
//     never a request '#' (which is wider) and never a longer request;
//   - a request cannot be wider than the ACL: extra request levels are only
//     allowed under an ACL '#'.
//
// MQTT §4.7.2 system-topic protection applies first: a root-level '+' or '#'
// ACL never covers a request whose first level starts with '$'.
func aclCovers(aclPattern, requested string) bool {
	ap := protocol.SplitTopic(aclPattern)
	rp := protocol.SplitTopic(requested)
	if len(ap) == 0 {
		return false
	}
	if len(rp) > 0 && len(rp[0]) > 0 && rp[0][0] == '$' && (ap[0] == "+" || ap[0] == "#") {
		return false
	}
	for i := 0; i < len(ap); i++ {
		if ap[i] == "#" {
			return true
		}
		if i >= len(rp) {
			return false
		}
		if rp[i] == "#" {
			// Only an ACL '#' level can cover a request '#' level.
			return false
		}
		if rp[i] == "+" {
			// A request '+' level matches any single level; only an ACL '+'
			// covers exactly the same set (an ACL literal is narrower, an
			// ACL '#' was already handled above).
			if ap[i] != "+" {
				return false
			}
			continue
		}
		if ap[i] != "+" && ap[i] != rp[i] {
			return false
		}
	}
	return len(ap) == len(rp)
}

// matchSysProtected is defined in topic_tree.go — it wraps protocol.MatchTopic
// with MQTT §4.7.2 system topic protection (root-level + and # wildcards must
// not match topics starting with $) and is shared by the topic tree, session
// matching, and ACL checks.

// AllowAllAuth allows all authentication (development only).
type AllowAllAuth struct{}

func (AllowAllAuth) Authenticate(ctx context.Context, clientID, username, password string) error {
	return nil
}

func (AllowAllAuth) CanPublish(ctx context.Context, username, topic string) bool {
	// System topics (anything starting with '$', conventionally $SYS/...) are
	// broker-owned: the permissive allow-all authorizer must not let clients
	// forge broker status payloads, including retained ones (audit H5).
	// Authorizers with explicit ACLs (e.g. StaticAuth granting "$SYS/+")
	// keep working unchanged.
	if strings.HasPrefix(topic, "$") {
		return false
	}
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
