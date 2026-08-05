package broker

import (
	"context"
	"errors"

	"github.com/X1aSheng/shark-mqtt/errs"
)

// ChainAuth tries multiple Authenticators in order.
// The first successful authentication returns success.
// If all authenticators fail, the last error is returned.
type ChainAuth struct {
	authenticators []Authenticator
}

var _ Authenticator = (*ChainAuth)(nil)

// NewChainAuth creates a new chain authenticator.
func NewChainAuth(auths ...Authenticator) *ChainAuth {
	return &ChainAuth{
		authenticators: auths,
	}
}

// AddAuthenticator appends an authenticator to the chain.
func (c *ChainAuth) AddAuthenticator(auth Authenticator) {
	c.authenticators = append(c.authenticators, auth)
}

// Authenticate iterates through authenticators until one succeeds.
// Returns nil on first success, or the last error if all fail.
//
// The chain is fail-closed: an authenticator that recognizes the user but
// rejects the credentials (ErrAuthFailed / ErrUnauthorized, or any other
// error) aborts the chain immediately so a later permissive authenticator
// (e.g. an anonymous fallback) cannot bypass the decision. Only an
// ErrUserNotFound result — the user is not known to this authenticator —
// lets the chain continue to the next authenticator.
func (c *ChainAuth) Authenticate(ctx context.Context, clientID, username, password string) error {
	if len(c.authenticators) == 0 {
		return ErrAuthUnavailable
	}

	var lastErr error
	for _, auth := range c.authenticators {
		err := auth.Authenticate(ctx, clientID, username, password)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errs.ErrUserNotFound) {
			// Recognized user rejected (or transient failure): fail closed.
			return err
		}
		lastErr = err
	}

	return lastErr
}
