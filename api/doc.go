// Package api provides the unified entry point for Shark-MQTT.
//
// The `api` package is the public API for creating and running an MQTT broker.
// It combines the network server (`server.MQTTServer`) with the broker core
// (`broker.Broker`) into a single, easy-to-use `Broker` type.
//
// # Quick Start
//
// The simplest way to start a broker:
//
//	b := api.NewBroker()
//	if err := b.Start(); err != nil {
//	    log.Fatal(err)
//	}
//	defer b.Stop()
//
// # Configuration
//
// Use options to customize the broker:
//
//	b := api.NewBroker(
//	    api.WithConfig(&config.Config{
//	        ListenAddr: ":1883",
//	        KeepAlive:  60,
//	    }),
//	    api.WithAuth(auth.NewNoopAuth()),
//	    api.WithLogger(logger.Default()),
//	    api.WithMetrics(metrics.Default()),
//	)
//
// # Authentication & Authorization
//
//	b := api.NewBroker(
//	    api.WithAuth(auth.NewFileAuth("users.yaml")),
//	    api.WithAuthorizer(auth.NewACLAuthorizer(&auth.ACLConfig{
//	        PublishTopics:   []string{"data/#"},
//	        SubscribeTopics: []string{"data/#", "commands/#"},
//	    })),
//	)
//
// # Storage Backends
//
// Replace the default in-memory stores:
//
//	b := api.NewBroker(
//	    api.WithSessionStore(store.NewRedisSessionStore(redisClient)),
//	    api.WithMessageStore(store.NewBadgerMessageStore(db)),
//	    api.WithRetainedStore(store.NewMemoryRetainedStore()),
//	)
//
// # Plugin System
//
//	b := api.NewBroker(
//	    api.WithPluginManager(plugin.NewManager()),
//	)
//
// # Blocking Mode
//
// Use `Run` for a blocking call that stops on context cancellation:
//
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//	api.Run(ctx, api.WithConfig(cfg))
package api
