package broker

import "context"

// NoopAuth always allows authentication (development only).
type NoopAuth struct{}

// Authenticate always returns nil (allow all).
func (NoopAuth) Authenticate(ctx context.Context, clientID, username, password string) error {
	return nil
}
