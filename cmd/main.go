// Package main is the entry point for the Shark-MQTT broker.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/pkg/logger"
)

// Version is the broker version, injected at build time via
// -ldflags "-X main.Version=..." (see scripts/build.sh).
var Version = "dev"

func main() {
	// Parse command-line flags
	var configPath string
	flag.StringVar(&configPath, "config", "", "path to a YAML configuration file")

	cfg := config.DefaultConfig()

	var allowAllAuth bool
	var authFile string
	flag.StringVar(&cfg.ListenAddr, "addr", cfg.ListenAddr, "listen address (host:port)")
	flag.IntVar(&cfg.MaxConnections, "max-conn", cfg.MaxConnections, "maximum number of connections (0 = unlimited)")
	flag.BoolVar(&cfg.TLSEnabled, "tls", cfg.TLSEnabled, "enable TLS")
	flag.StringVar(&cfg.TLSCertFile, "tls-cert", cfg.TLSCertFile, "TLS certificate file path")
	flag.StringVar(&cfg.TLSKeyFile, "tls-key", cfg.TLSKeyFile, "TLS private key file path")
	flag.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "log level (debug/info/warn/error)")
	flag.BoolVar(&allowAllAuth, "allow-all", false, "allow all connections without authentication (DEVELOPMENT ONLY)")
	flag.StringVar(&authFile, "auth-file", "", "path to a YAML/JSON user credentials file (bcrypt hashes recommended)")
	flag.Parse()

	// Load order (audit): defaults <- environment (MQTT_*) <- config file
	// (when -config is given) <- explicit command-line flags. Previously the
	// environment was only read when a config file was passed, the loaded
	// file silently discarded flag values, and log/metrics/keep-alive config
	// had no runtime effect.
	if err := config.ApplyEnv(cfg); err != nil {
		log.Fatalf("Failed to apply environment configuration: %v", err)
	}

	// Load a YAML configuration file if requested (NEW-11).
	if configPath != "" {
		loaded, err := config.NewLoader(configPath).Load()
		if err != nil {
			log.Fatalf("Failed to load config %s: %v", configPath, err)
		}
		cfg = loaded
	}

	// Re-apply explicitly-set flags so they win over file/env values.
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "addr":
			cfg.ListenAddr = f.Value.String()
		case "max-conn":
			if n, err := strconv.Atoi(f.Value.String()); err == nil {
				cfg.MaxConnections = n
			}
		case "tls":
			if b, err := strconv.ParseBool(f.Value.String()); err == nil {
				cfg.TLSEnabled = b
			}
		case "tls-cert":
			cfg.TLSCertFile = f.Value.String()
		case "tls-key":
			cfg.TLSKeyFile = f.Value.String()
		case "log-level":
			cfg.LogLevel = f.Value.String()
		}
	})
	if authFile != "" {
		cfg.AuthFile = authFile
	}

	addrSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "addr" {
			addrSet = true
		}
	})
	if cfg.TLSEnabled && !addrSet {
		cfg.ListenAddr = config.DefaultTLSListenAddr
	}

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
	fmt.Printf("Shark-MQTT Broker v%s - listening on %s\n\n", Version, cfg.ListenAddr)

	var brokerOpts []api.Option
	brokerOpts = append(brokerOpts, api.WithConfig(cfg))
	brokerOpts = append(brokerOpts, api.WithVersion(Version))

	// Wire a real logger from config (audit: broker logs were previously
	// discarded because no logger was attached and log_level/log_format had
	// no effect).
	brokerOpts = append(brokerOpts, api.WithLogger(logger.NewSlogLogger(cfg.LogLevel, cfg.LogFormat)))

	switch {
	case allowAllAuth:
		fmt.Fprintln(os.Stderr, "WARNING: --allow-all enabled — all connections accepted without authentication. Do NOT use in production.")
		brokerOpts = append(brokerOpts, api.WithAuth(broker.AllowAllAuth{}))
	case cfg.AuthFile != "":
		// Real authentication from a credential file (audit: the CLI could
		// previously only run deny-all or allow-all, so production
		// deployments either rejected every client or accepted everyone).
		fa, err := broker.NewFileAuth(cfg.AuthFile)
		if err != nil {
			log.Fatalf("Failed to load auth file %s: %v", cfg.AuthFile, err)
		}
		fmt.Printf("Authentication enabled via %s\n", cfg.AuthFile)
		brokerOpts = append(brokerOpts, api.WithAuth(fa))
	default:
		fmt.Println("Authentication required. Use --allow-all (development) or -auth-file <users.yaml> for production.")
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
