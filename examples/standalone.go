// Package standalone demonstrates running the shark-mqtt broker independently.
//
// Run: go run .
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
)

func main() {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":1883"

	// Create broker with default auth (allow all)
	broker := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)

	if err := broker.Start(); err != nil {
		log.Fatalf("Failed to start broker: %v", err)
	}
	log.Println("Broker started on :1883")

	// Wait for interrupt signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	log.Println("Shutting down broker...")
	broker.Stop()
	log.Println("Broker stopped")
}
