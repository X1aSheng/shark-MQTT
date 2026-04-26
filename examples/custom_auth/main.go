// Package customauth demonstrates implementing a custom authenticator.
//
// Run: go run .
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/config"
)

// TokenAuthenticator validates connections using a token in the password field.
type TokenAuthenticator struct {
	validTokens map[string]bool
}

func (a *TokenAuthenticator) Authenticate(ctx context.Context, clientID, username, password string) error {
	if !a.validTokens[password] {
		return fmt.Errorf("invalid token")
	}
	return nil
}

// NewTokenAuth creates a new token authenticator with the given valid tokens.
func NewTokenAuth(tokens ...string) *TokenAuthenticator {
	m := make(map[string]bool)
	for _, t := range tokens {
		m[t] = true
	}
	return &TokenAuthenticator{validTokens: m}
}

func main() {
	auth := NewTokenAuth("secret-token-1", "secret-token-2")

	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":1883"

	broker := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(auth),
	)

	if err := broker.Start(); err != nil {
		log.Fatalf("Failed to start broker: %v", err)
	}
	log.Println("Broker started on :1883 (token auth enabled)")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	log.Println("Shutting down broker...")
	broker.Stop()
	log.Println("Broker stopped")
}
