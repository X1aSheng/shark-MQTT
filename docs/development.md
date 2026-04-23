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
├── broker/           # Core business logic (TopicTree, QoSEngine, WillHandler)
├── server/           # Network layer (TCP/TLS, connection management)
├── protocol/         # MQTT 3.1.1 & 5.0 codec
├── session/          # Session management (Attach/Detach model)
├── store/            # Storage interfaces + Memory/Redis/BadgerDB implementations
├── auth/             # Authentication (noop, file, chain)
├── plugin/           # Plugin system (ACL, rate limit, audit)
├── infra/            # Infrastructure (logger, metrics, bufferpool)
├── config/           # Configuration loading (YAML/ENV)
├── integration/      # shark-socket adapter
├── client/           # MQTT client implementation
├── test/             # Integration and benchmark tests
├── examples/         # Usage examples
└── cmd/              # Command-line tools
```

---

## Core Concepts

### Broker and Server Separation

Shark-MQTT maintains a clear separation between the network layer and business logic:

```
┌─────────────────────────────────────────────────┐
│                  api.Broker                     │
│         (unified entry point)                   │
└─────────────────────┬───────────────────────────┘
                      │
         ┌────────────┴────────────┐
         ▼                         ▼
┌──────────────────┐    ┌────────────────────┐
│ server.MQTTServer│    │ broker.Broker      │
│                  │    │                    │
│ - TCP/TLS        │    │ - TopicTree        │
│ - Accept loop    │◄──►│ - QoSEngine        │
│ - Connection mgmt│    │ - WillHandler      │
│ - Codec          │    │ - Sessions         │
└──────────────────┘    └────────────────────┘
```

- **server**: Handles network I/O, TLS, connection lifecycle
- **broker**: Handles MQTT semantics (publish/subscribe, QoS, sessions)

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

Subscription: "home/+/temperature" matches ✓
Subscription: "home/living-room/#" matches ✓
Subscription: "home/kitchen/#" does NOT match
```

### QoS Engine Retry Mechanism

```
QoS 0: Fire and forget
        PUBLISH → delivered

QoS 1: At least once
        PUBLISH → PUBACK
        (retry on timeout)

QoS 2: Exactly once (4-way handshake)
        PUBLISH → PUBREC → PUBREL → PUBCOMP
```

---

## Module Guide

### protocol/

MQTT packet encoding/decoding according to spec.

**Key files:**
- `codec.go` - Encoder and decoder
- `packets.go` - Packet type definitions
- `connect.go`, `publish.go`, `subscribe.go` - Specific packet handling

**Adding new packet types:**
1. Define packet struct in `packets.go`
2. Implement `Packet` interface
3. Add encode/decode logic in `codec.go`

### broker/

Core message broker logic.

**Key components:**
- `broker.go` - Main broker orchestration
- `topic_tree.go` - Topic subscription management
- `qos_engine.go` - QoS 1/2 retry and tracking
- `will_handler.go` - Last will message management

**Broker startup flow:**
```go
func (b *Broker) Start() error {
    b.qos.Start()  // Start retry worker
    return nil
}
```

### session/

Client session management.

**Session states:**
- `StateConnected` - Active and handling messages
- `StateDisconnecting` - Graceful disconnect in progress
- `StateDisconnected` - Session ended

**Session data:**
- Client ID, username, protocol version
- Subscriptions (topic → QoS)
- Inflight messages (QoS 1/2)
- Statistics (bytes sent/received, message counts)

### store/

Storage abstraction with multiple backends.

**Interfaces:**
- `SessionStore` - Session persistence
- `MessageStore` - QoS message persistence
- `RetainedStore` - Retained message storage

**Backends:**
- `memory/` - In-memory (default)
- `redis/` - Redis (distributed)
- `badger/` - BadgerDB (embedded, persistent)

### auth/

Authentication and authorization.

**Authenticator interface:**
```go
type Authenticator interface {
    Authenticate(ctx context.Context, clientID, username string, password []byte) error
}
```

**Built-in implementations:**
- `noop/` - Allow all (development only)
- `file/` - File-based credentials
- `chain/` - Chain multiple authenticators

### plugin/

Extensible hooks for broker events.

**Available hooks:**
- `OnAccept` - New connection accepted
- `OnConnect` - CONNECT packet received
- `OnConnected` - Client connected
- `OnPublish` - Message published
- `OnSubscribe` - Subscription requested
- `OnDisconnect` - Client disconnected
- `OnClose` - Connection closed

---

## Adding New Features

### Adding a New MQTT Packet Type

1. **Define the packet** in `protocol/packets.go`:
```go
type AuthPacket struct {
    FixedHeader
    AuthMethod    string
    AuthData      []byte
    Properties    []Property
}
```

2. **Implement encoding** in `protocol/codec.go`:
```go
func (c *Codec) encodeAuth(pkt *AuthPacket, buf *bytes.Buffer) error {
    // Write variable header
    writeString(buf, pkt.AuthMethod)
    writeBinary(buf, pkt.AuthData)
    writeProperties(buf, pkt.Properties)
    return nil
}
```

3. **Add test** in `protocol/codec_test.go`

### Adding a New Storage Backend

1. **Implement interfaces** in `store/interfaces.go`:
```go
type MyDBStore struct {
    // MyDB connection
}

func (s *MyDBStore) SaveSession(ctx context.Context, clientID string, data *SessionData) error {
    // Implementation
}
```

2. **Add tests** in `store/mydb/`

3. **Update examples** in `examples/`

### Adding a New Authentication Method

1. **Implement interface** in `auth/auth.go`:
```go
type LDAPAuthenticator struct {
    Server string
}

func (a *LDAPAuthenticator) Authenticate(ctx context.Context, clientID, username string, password []byte) error {
    // LDAP authentication logic
}
```

2. **Add example** in `examples/custom_auth/`

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
    api.WithLogger(logger.NewDebugLogger()),
)
```

### Prometheus Metrics

Enable metrics endpoint:
```yaml
metrics_enabled: true
metrics_addr: ":9090"
```

Key metrics to monitor:
- `sharkmqtt_connections_total` - Total connections
- `sharkmqtt_messages_published_total` - Messages published
- `sharkmqtt_messages_delivered_total` - Messages delivered
- `sharkmqtt_qos_retries_total` - QoS retries

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
- [API Reference](api-reference.md)
- [CONTRIBUTING.md](../CONTRIBUTING.md)