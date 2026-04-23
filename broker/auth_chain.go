package broker

import (
	"context"
)

// ChainAuth tries multiple Authenticators in order.
// The first successful authentication returns success.
// If all authenticators fail, the last error is returned.
type ChainAuth struct {
	authenticators []Authenticator
}

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
		lastErr = err
	}

	return lastErr
}
