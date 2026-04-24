# Development Guide

This guide helps developers understand the Shark-MQTT codebase and get started with contributing.

---

## Table of Contents

- [Project Structure](#project-structure)
- [Core Concepts](#core-concepts)
- [Module Guide](#module-guide)
- [Adding New Features](#adding-new-features)
- [Debugging](#debugging)

---

## Project Structure

```
shark-mqtt/
├── api/              # Unified public API and factory methods
├── broker/           # Core broker: MQTTServer, Broker, TopicTree, QoSEngine,
│                     #   WillHandler, Session (Manager), Auth, Authorizer
├── protocol/         # MQTT 3.1.1 & 5.0 codec (packets, properties)
├── store/            # Storage interfaces + Memory/Redis/BadgerDB implementations
│   ├── memory/       # In-memory store (default)
│   ├── redis/        # Redis store (distributed)
│   └── badger/       # BadgerDB store (embedded)
├── pkg/              # Infrastructure (logger, metrics, bufferpool)
├── config/           # Configuration loading (YAML/ENV)
├── plugin/           # Plugin system (hook-based architecture)
├── client/           # MQTT client implementation
├── errs/             # Centralized error definitions
├── test/             # Integration and benchmark tests
│   ├── integration/  # End-to-end MQTT workflow tests
│   └── bench/        # Performance benchmarks
├── examples/         # Usage examples
├── cmd/              # Command-line tools
├── testutils/        # Test utilities (mock implementations)
└── docs/             # Documentation
```

---

## Core Concepts

### Broker and Server Separation

Shark-MQTT maintains a clear separation between the network layer and business logic, both within the `broker/` package:

```
┌─────────────────────────────────────────────────┐
│                  api.Broker                     │
│         (unified entry point)                   │
└─────────────────────┬───────────────────────────┘
                      │
         ┌────────────┴────────────┐
         ▼                         ▼
┌──────────────────┐    ┌────────────────────┐
│broker.MQTTServer │    │ broker.Broker      │
│                  │    │                    │
│ - TCP/TLS        │    │ - TopicTree        │
│ - Accept loop    │◄──►│ - QoSEngine        │
│ - Connection mgmt│    │ - WillHandler      │
│ - Codec          │    │ - Manager          │
└──────────────────┘    └────────────────────┘
```

- **MQTTServer**: Handles network I/O, TLS, connection lifecycle
- **Broker**: Handles MQTT semantics (publish/subscribe, QoS, sessions)

### Session Attach/Detach Model

MQTT sessions can persist across connections using the Attach/Detach pattern:

```
Client connects (CONNECT, CleanSession=false)
    │
    ▼
Session created and stored
    │
    ▼
Client disconnects abnormally
    │
    ▼
Session remains (waiting for reconnect)
    │
    ▼
Same client reconnects (same ClientID)
    │
    ▼
Session resumed, subscriptions restored
```

### TopicTree Matching

Topic matching uses an efficient tree structure:

```
Topic: "home/living-room/temperature"

Tree:
root
└── home
    └── living-room
        └── temperature

Subscription: "home/+/temperature" matches
Subscription: "home/living-room/#" matches
Subscription: "home/kitchen/#" does NOT match
```

### QoS Engine Retry Mechanism

```
QoS 0: Fire and forget
        PUBLISH -> delivered

QoS 1: At least once
        PUBLISH -> PUBACK
        (retry on timeout)

QoS 2: Exactly once (4-way handshake)
        PUBLISH -> PUBREC -> PUBREL -> PUBCOMP
```

---

## Module Guide

### protocol/

MQTT packet encoding/decoding according to spec.

**Key files:**
- `codec.go` - Encoder and decoder
- `packets.go` - Packet type definitions (15 packet types)
- `connect.go`, `publish.go`, `subscribe.go` - Specific packet handling
- `properties.go` - MQTT 5.0 properties
- `constants.go` - Protocol constants and version info

**Adding new packet types:**
1. Define packet struct in `packets.go`
2. Implement encode/decode logic in `codec.go`
3. Add test in `codec_test.go`

### broker/

Core message broker logic, including network server.

**Key components:**
- `broker.go` - Main broker orchestration (HandleConnection, publish, subscribe)
- `server.go` - MQTTServer (TCP/TLS accept loop, connection management)
- `topic_tree.go` - Topic subscription management (O(log n) matching)
- `qos_engine.go` - QoS 1/2 retry and inflight tracking
- `will_handler.go` - Last will message management
- `session.go` - Session struct with state machine, statistics, Manager
- `auth.go` - Authenticator and Authorizer interfaces + StaticAuth, ACL
- `auth_noop.go` - NoopAuth, AllowAllAuth, DenyAllAuth
- `auth_chain.go` - ChainAuth (multiple authenticators)
- `auth_file.go` - FileAuth (YAML/JSON credentials file)
- `options.go` - Broker option functions
- `options_server.go` - Server option functions

**Broker startup flow:**
```go
broker := api.NewBroker(api.WithConfig(cfg))
broker.Start()  // Starts MQTTServer + QoSEngine retry loop
```

### Session Management (in broker/)

Client session management is handled by `Manager` in `broker/session.go`.

**Session states:**
- `StateConnected` - Active and handling messages
- `StateDisconnecting` - Graceful disconnect in progress

**Session data:**
- Client ID, username, protocol version
- Subscriptions (topic -> QoS)
- Inflight messages (QoS 1/2)
- Statistics (bytes sent/received, message counts)

**Manager methods:**
- `CreateSession(clientID, connectPkt, isResuming)` - Create or resume session
- `GetSession(clientID)` - Retrieve session
- `RemoveSession(clientID)` - Remove session
- `Save/Restore` - Persist to/reload from store

### store/

Storage abstraction with multiple backends.

**Interfaces (store/interfaces.go):**
- `SessionStore` - Session persistence (5 methods)
- `MessageStore` - QoS message persistence (5 methods)
- `RetainedStore` - Retained message storage (4 methods)

**Backends:**
- `memory/` - In-memory (default)
- `redis/` - Redis (distributed)
- `badger/` - BadgerDB (embedded, persistent)

### Authentication (in broker/)

Authentication and authorization are in the `broker/` package.

**Authenticator interface:**
```go
type Authenticator interface {
    Authenticate(ctx context.Context, clientID, username, password string) error
}
```

**Authorizer interface:**
```go
type Authorizer interface {
    CanPublish(ctx context.Context, clientID, topic string) bool
    CanSubscribe(ctx context.Context, clientID, topic string) bool
}
```

**Built-in implementations (all in broker/):**
- `NoopAuth` - No-op authenticator
- `AllowAllAuth` - Allow all (development only)
- `DenyAllAuth` - Deny all
- `StaticAuth` - Static credentials with ACL support
- `ChainAuth` - Chain multiple authenticators
- `FileAuth` - File-based credentials (YAML/JSON)

### plugin/

Extensible hooks for broker events.

**Available hooks:**
- `OnAccept` - New connection accepted
- `OnConnected` - Client connected
- `OnMessage` - Message published
- `OnClose` - Connection closed

---

## Adding New Features

### Adding a New Storage Backend

1. **Implement interfaces** from `store/interfaces.go`:
```go
package mydb

type SessionStore struct { /* ... */ }

func (s *SessionStore) SaveSession(ctx context.Context, clientID string, data *store.SessionData) error {
    // Implementation
}
// ... implement all interface methods
```

2. **Add tests** in `store/mydb/`

3. **Update examples** in `examples/`

### Adding a New Authentication Method

1. **Implement interface** in `broker/`:
```go
type MyAuth struct{}

func (a *MyAuth) Authenticate(ctx context.Context, clientID, username, password string) error {
    // Custom authentication logic
    return nil // or ErrAuthFailed
}
```

2. **Optionally implement Authorizer** for ACL:
```go
func (a *MyAuth) CanPublish(ctx context.Context, clientID, topic string) bool {
    return true
}

func (a *MyAuth) CanSubscribe(ctx context.Context, clientID, topic string) bool {
    return true
}
```

3. **Add example** in `examples/`

---

## Debugging

### Enable Debug Logging

```yaml
log_level: debug
log_format: text
```

Or programmatically:
```go
broker := api.NewBroker(
    api.WithLogger(myDebugLogger),
)
```

### Prometheus Metrics

Enable metrics endpoint:
```yaml
metrics_enabled: true
metrics_addr: ":9090"
```

Key metrics to monitor:
- Connections (active/rejected)
- Messages (published/delivered/dropped)
- QoS (inflight/retries)
- Sessions (online/offline)
- Errors (by component)

Access metrics:
```bash
curl http://localhost:9090/metrics
```

### Common Issues

**Client cannot connect**
1. Check authentication settings
2. Verify TLS configuration
3. Check connection limits (`max_connections`)

**Messages not delivered**
1. Verify subscription exists
2. Check topic matching (wildcards)
3. Review QoS level compatibility

**High memory usage**
1. Check inflight message backlog
2. Review retained message count
3. Verify session cleanup

---

## Performance Tips

1. **Use TLS 1.3** for better performance
2. **Adjust inflight window** for high-throughput scenarios
3. **Select appropriate storage backend** (Redis for distributed, Badger for embedded)
4. **Monitor metrics** to identify bottlenecks

---

## See Also

- [Testing Guide](testing.md)
- [Configuration Guide](configuration.md)
- [API Reference](API.md)
- [CONTRIBUTING.md](../CONTRIBUTING.md)
