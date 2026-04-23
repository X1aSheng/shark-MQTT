// Package withsharksocket demonstrates integrating shark-mqtt with shark-socket Gateway.
//
// Run: go run .
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/X1aSheng/shark-mqtt/config"
	mqttadapter "github.com/X1aSheng/shark-mqtt/integration/sharksocket"
)

func main() {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":1883"

	// Create MQTT adapter
	adapter := mqttadapter.New(cfg)

	// In production, this would be added to a shark-socket Gateway:
	// gw := ss.NewGateway().AddServer(adapter).Build()

	log.Println("MQTT Adapter ready for shark-socket integration")

	// Start the adapter
	if err := adapter.Start(); err != nil {
		log.Fatalf("Failed to start adapter: %v", err)
	}
	log.Println("Adapter started on :1883")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig

	log.Println("Shutting down adapter...")
	adapter.Stop(nil)
	log.Println("Adapter stopped")
}
