# Shark-MQTT API Reference

This document provides detailed API documentation for Shark-MQTT.

---

## Table of Contents

- [Quick Start](#quick-start)
- [api.Broker](#apibroker)
- [Configuration](#configuration)
- [Authentication](#authentication)
- [Storage](#storage)
- [Customization](#customization)

---

## Quick Start

```go
package main

import (
    "context"
    "log"
    "os"
    "os/signal"

    "github.com/X1aSheng/shark-mqtt/api"
    "github.com/X1aSheng/shark-mqtt/config"
)

func main() {
    cfg := config.DefaultConfig()
    cfg.ListenAddr = ":1883"

    broker := api.NewBroker(api.WithConfig(cfg))
    if err := broker.Start(); err != nil {
        log.Fatal(err)
    }

    log.Printf("Broker started on %s", broker.Addr())

    sig := make(chan os.Signal, 1)
    signal.Notify(sig, os.Interrupt)
    <-sig

    broker.Stop()
}
```

---

## api.Broker

The main entry point for Shark-MQTT broker.

### Creation

```go
broker := api.NewBroker(opts ...Option)
```

Creates a new broker instance with optional configuration.

### Options

| Option | Type | Description |
|--------|------|-------------|
| `WithConfig(cfg)` | `*config.Config` | Set custom configuration |
| `WithAuth(a)` | `broker.Authenticator` | Set authenticator |
| `WithAuthorizer(a)` | `broker.Authorizer` | Set authorizer |
| `WithSessionStore(s)` | `store.SessionStore` | Set session storage |
| `WithMessageStore(s)` | `store.MessageStore` | Set message storage |
| `WithRetainedStore(s)` | `store.RetainedStore` | Set retained message storage |
| `WithLogger(l)` | `logger.Logger` | Set custom logger |
| `WithMetrics(m)` | `metrics.Metrics` | Set metrics collector |
| `WithPluginManager(m)` | `*plugin.Manager` | Set plugin manager |

### Methods

#### Start()

```go
func (b *Broker) Start() error
```

Starts both the network server and broker core.

**Returns:**
- `nil` on success
- `error` if server or broker fails to start

**Example:**
```go
if err := broker.Start(); err != nil {
    log.Fatalf("Failed to start broker: %v", err)
}
```

#### Stop()

```go
func (b *Broker) Stop()
```

Gracefully shuts down both the server and broker.

**Example:**
```go
broker.Stop()
```

#### Addr()

```go
func (b *Broker) Addr() string
```

Returns the listening address as a string.

**Returns:**
- IP:port string (e.g., "0.0.0.0:1883")
- Empty string if not started

#### ConnCount()

```go
func (b *Broker) ConnCount() int64
```

Returns the current number of active connections.

#### Broker()

```go
func (b *Broker) Broker() *broker.Broker
```

Returns the underlying broker core for advanced usage.

**Use cases:**
- Direct access to topic tree
- Session management
- Custom message handling

---

## Configuration

### Default Configuration

```go
cfg := config.DefaultConfig()
```

### Custom Configuration

```go
cfg := config.DefaultConfig()
cfg.ListenAddr = ":1883"
cfg.KeepAlive = 60
cfg.MaxPacketSize = 262144
cfg.MaxConnections = 10000
cfg.TLSEnabled = false
cfg.LogLevel = "info"
```

### From YAML

```go
cfg, err := config.LoadFromFile("config.yaml")
```

### From Environment

Environment variables with `MQTT_` prefix:
- `MQTT_LISTEN_ADDR`
- `MQTT_KEEP_ALIVE`
- `MQTT_MAX_PACKET_SIZE`
- `MQTT_LOG_LEVEL`

See [configuration.md](configuration.md) for the full list.

---

## Authentication

### Built-in Authenticators

#### AllowAllAuth

Permits all connections (development only).

```go
import "github.com/X1aSheng/shark-mqtt/broker"

broker := api.NewBroker(
    api.WithAuth(broker.AllowAllAuth{}),
)
```

#### DenyAllAuth

Denies all connections.

```go
broker := api.NewBroker(
    api.WithAuth(broker.DenyAllAuth{}),
)
```

#### StaticAuth

Static username/password authentication with optional ACL support.

```go
auth := broker.NewStaticAuth()
auth.AddCredentials("admin", "secret")
auth.AddCredentials("user1", "pass123")

// Optional: Add ACL for a client
auth.AddACL("client1", &broker.ACL{
    PublishTopics:   []string{"data/#"},
    SubscribeTopics: []string{"status/+"},
})

broker := api.NewBroker(
    api.WithAuth(auth),
)
```

#### ChainAuth

Try multiple authenticators in order.

```go
chain := broker.NewChainAuth(fileAuth, staticAuth)
broker := api.NewBroker(
    api.WithAuth(chain),
)
```

#### FileAuth

Load credentials from a YAML or JSON file.

```go
fileAuth, err := broker.NewFileAuth("credentials.yaml")
if err != nil {
    log.Fatal(err)
}
broker := api.NewBroker(
    api.WithAuth(fileAuth),
)
```

### Custom Authenticator

Implement the `broker.Authenticator` interface:

```go
type Authenticator interface {
    Authenticate(ctx context.Context, clientID, username, password string) error
}
```

**Example:**
```go
type MyAuth struct{}

func (a *MyAuth) Authenticate(ctx context.Context, clientID, username, password string) error {
    if username == "admin" && password == "secret" {
        return nil
    }
    return broker.ErrAuthFailed
}

broker := api.NewBroker(
    api.WithAuth(&MyAuth{}),
)
```

### Custom Authorizer

Implement the `broker.Authorizer` interface for topic-level access control:

```go
type Authorizer interface {
    CanPublish(ctx context.Context, clientID, topic string) bool
    CanSubscribe(ctx context.Context, clientID, topic string) bool
}
```

---

## Storage

### Memory Store (Default)

```go
// No configuration needed - used by default
broker := api.NewBroker()
```

### Redis Store

```go
import (
    redisstore "github.com/X1aSheng/shark-mqtt/store/redis"
    "github.com/redis/go-redis/v9"
)

client := redis.NewClient(&redis.Options{
    Addr: "localhost:6379",
})

ss := redisstore.NewSessionStore(redisstore.SessionStoreConfig{
    Client:    client,
    KeyPrefix: "mqtt:session:",
    TTL:       time.Hour,
})

ms := redisstore.NewMessageStore(redisstore.MessageStoreConfig{
    Client:    client,
    KeyPrefix: "mqtt:message:",
})

rs := redisstore.NewRetainedStore(redisstore.RetainedStoreConfig{
    Client:    client,
    KeyPrefix: "mqtt:retained:",
})

broker := api.NewBroker(
    api.WithSessionStore(ss),
    api.WithMessageStore(ms),
    api.WithRetainedStore(rs),
)
```

### Badger Store

```go
import badgerstore "github.com/X1aSheng/shark-mqtt/store/badger"

ss, _ := badgerstore.NewSessionStore("/path/to/db")
ms, _ := badgerstore.NewMessageStore("/path/to/db")
rs, _ := badgerstore.NewRetainedStore("/path/to/db")

broker := api.NewBroker(
    api.WithSessionStore(ss),
    api.WithMessageStore(ms),
    api.WithRetainedStore(rs),
)
```

---

## Customization

### Custom Logger

Implement `logger.Logger` interface:

```go
type Logger interface {
    Debug(msg string, args ...interface{})
    Info(msg string, args ...interface{})
    Warn(msg string, args ...interface{})
    Error(msg string, args ...interface{})
}
```

**Example:**
```go
broker := api.NewBroker(
    api.WithLogger(myLogger),
)
```

### Custom Metrics

Implement `metrics.Metrics` interface (17 methods):

```go
type Metrics interface {
    IncConnections()
    DecConnections()
    IncRejections(reason string)
    IncAuthFailures()
    IncMessagesPublished(topic string, qos uint8)
    IncMessagesDelivered(clientID string, qos uint8)
    IncMessagesDropped(reason string)
    IncInflight(clientID string)
    DecInflight(clientID string)
    DecInflightBatch(clientID string, count int)
    IncInflightDropped(clientID string)
    IncRetries(clientID string)
    SetOnlineSessions(count int)
    SetOfflineSessions(count int)
    SetRetainedMessages(count int)
    SetSubscriptions(count int)
    IncErrors(component string)
}
```

### Plugin System

```go
import "github.com/X1aSheng/shark-mqtt/plugin"

type MyPlugin struct{}

func (p *MyPlugin) Name() string { return "my-plugin" }
func (p *MyPlugin) Hooks() []plugin.Hook {
    return []plugin.Hook{plugin.OnMessage, plugin.OnClose}
}
func (p *MyPlugin) Execute(ctx context.Context, hook plugin.Hook, data *plugin.Context) error {
    if hook == plugin.OnMessage {
        log.Printf("Message from %s on %s", data.ClientID, data.Topic)
    }
    return nil
}

mgr := plugin.NewManager()
mgr.Register(&MyPlugin{})

broker := api.NewBroker(
    api.WithPluginManager(mgr),
)
```

**Available Hooks:**
- `OnAccept` - Connection accepted
- `OnConnected` - Client connected
- `OnMessage` - Message published
- `OnClose` - Connection closed

---

## Complete Example

```go
package main

import (
    "context"
    "log"
    "os"
    "os/signal"

    "github.com/X1aSheng/shark-mqtt/api"
    "github.com/X1aSheng/shark-mqtt/broker"
    "github.com/X1aSheng/shark-mqtt/config"
)

func main() {
    // Load configuration
    cfg := config.DefaultConfig()
    cfg.ListenAddr = ":1883"
    cfg.MaxConnections = 1000

    // Set up authentication
    auth := broker.NewStaticAuth()
    auth.AddCredentials("admin", "secret")

    // Create broker
    b := api.NewBroker(
        api.WithConfig(cfg),
        api.WithAuth(auth),
    )

    // Handle shutdown
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, os.Interrupt)

    // Start broker
    if err := b.Start(); err != nil {
        log.Fatal(err)
    }

    log.Printf("Broker started on %s", b.Addr())

    // Wait for shutdown signal
    <-sigCh
    b.Stop()
}
```

---

## See Also

- [Configuration Guide](configuration.md)
- [Testing Guide](testing.md)
- [Development Guide](development.md)
- [Examples](../examples/)
