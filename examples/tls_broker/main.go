// Package tlsbroker demonstrates running the broker with TLS.
//
// Generate self-signed certs for testing:
//
//	openssl req -x509 -newkey rsa:4096 -keyout key.pem -out cert.pem -days 365 -nodes
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
	cfg.ListenAddr = ":8883"
	cfg.TLSEnabled = true
	cfg.TLSCertFile = "cert.pem"
	cfg.TLSKeyFile = "key.pem"

	broker := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)

	if err := broker.Start(); err != nil {
		log.Fatalf("Failed to start broker: %v", err)
	}
	log.Println("TLS Broker started on :8883")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	log.Println("Shutting down broker...")
	broker.Stop()
	log.Println("Broker stopped")
}
