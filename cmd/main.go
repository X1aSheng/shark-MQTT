// Package main is the entry point for the Shark-MQTT broker.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
)

func main() {
	// Parse command-line flags
	cfg := config.DefaultConfig()

	var allowAllAuth bool
	flag.StringVar(&cfg.ListenAddr, "addr", cfg.ListenAddr, "listen address (host:port)")
	flag.IntVar(&cfg.MaxConnections, "max-conn", cfg.MaxConnections, "maximum number of connections (0 = unlimited)")
	flag.BoolVar(&cfg.TLSEnabled, "tls", cfg.TLSEnabled, "enable TLS")
	flag.StringVar(&cfg.TLSCertFile, "tls-cert", cfg.TLSCertFile, "TLS certificate file path")
	flag.StringVar(&cfg.TLSKeyFile, "tls-key", cfg.TLSKeyFile, "TLS private key file path")
	flag.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "log level (debug/info/warn/error)")
	flag.BoolVar(&allowAllAuth, "allow-all", false, "allow all connections without authentication (DEVELOPMENT ONLY)")
	flag.Parse()

	// Setup signal handling
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	// Create and run the broker
	fmt.Println("  _   _                   _          ___ ")
	fmt.Println(" | | | |_ __  _   _  __ _| | ___    / _ \\")
	fmt.Println(" | | | | '_ \\| | | |/ _` | |/ _ \\  / /_\\/")
	fmt.Println(" | |_| | |_) | |_| | (_| | |  __/ / /_\\\\ ")
	fmt.Println("  \\___/| .__/ \\__,_|\\__,_|_|\\___| \\____/ ")
	fmt.Println("       |_|")
	fmt.Printf("Shark-MQTT Broker v1.0.0 - listening on %s\n\n", cfg.ListenAddr)

	var brokerOpts []api.Option
	brokerOpts = append(brokerOpts, api.WithConfig(cfg))

	if allowAllAuth {
		fmt.Fprintln(os.Stderr, "WARNING: --allow-all enabled — all connections accepted without authentication. Do NOT use in production.")
		brokerOpts = append(brokerOpts, api.WithAuth(broker.AllowAllAuth{}))
	} else {
		fmt.Println("Authentication required. Use --allow-all for development mode (no auth).")
	}

	b := api.NewBroker(brokerOpts...)

	// Start the broker
	if err := b.Start(); err != nil {
		log.Fatalf("Failed to start broker: %v", err)
	}

	fmt.Printf("PID: %d\n", os.Getpid())
	fmt.Println("Press Ctrl+C to stop")

	// Wait for signal
	<-ctx.Done()
	fmt.Println("\nShutting down...")
	b.Stop()
	log.Println("Shutdown complete")
}
