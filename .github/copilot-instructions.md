# Shark-MQTT Development Skill Guide

## Project Overview
Shark-MQTT is a high-performance MQTT v3.1.1/v5.0 broker written in Go, extracted from Shark-Socket. It supports complete QoS 0/1/2 delivery, topic wildcard matching, persistent sessions, last-will messages, and retained messages with pluggable storage backends (memory, Redis, BadgerDB).

## Architecture Layers
The project follows strict separation of concerns:

| Layer | Package | Responsibility |
|-------|---------|----------------|
| **Network** | `server/` | TCP listening, TLS, per-connection read/write loops, protocol decoding |
| **Protocol** | `protocol/` | MQTT 3.1.1 & 5.0 encode/decode, all packet types |
| **Broker Core** | `broker/` | Message routing, subscription matching (TopicTree), QoS state machine (QoSEngine), will handling (WillHandler) |
| **Session** | `session/` | Connection/session lifecycle, persistent session registry, session attach/detach |
| **Storage** | `store/` | Pluggable backends for sessions, offline messages, retained messages |
| **Auth** | `auth/` | Connection authentication (noop, file, chain, func) |
| **Plugin** | `plugin/` | Lifecycle hooks (OnAccept, OnConnect, OnPublish, OnSubscribe, OnDisconnect) |
| **API** | `api/` | Unified public interface — all exports in one package |
| **Config** | `config/` | YAML/ENV-based configuration loading |
| **Infra** | `infra/` | Cross-cutting: Logger, Metrics, BufferPool |
| **Errors** | `errs/` | Centralized error definitions |

## Key Design Principles

### P1 — Network/Business Separation
- `server.MQTTServer` handles TCP accept, TLS, connection read/write loops
- `broker.Broker` handles MQTT business logic, no network dependencies
- Communication via interfaces: server calls broker's `HandlePacket(sess, packet)`

### P2 — Connection/Session Separation
- Connection lifecycle = TCP connection lifetime
- Session lifecycle >= connection (CleanSession=false persists across reconnects)
- `Attach(conn)` binds session to connection, `Detach()` unbinds (session stays alive), `Close()` destroys session

### P3 — Storage/Runtime Separation
- All persistence via interfaces: `SessionStore`, `MessageStore`, `RetainedStore`
- Default memory implementations for zero-dependency usage
- Production backends: Redis (cluster-ready), BadgerDB (embedded disk)

### P4 — QoS State Machine
- QoS 1: Enqueue → SENT → PUBACK → DONE (with retry)
- QoS 2: Enqueue → SENT → PUBREC → PUBREL_SENT → PUBCOMP → DONE
- Max inflight per client, backpressure via `ErrInflightFull`
- Background retry loop with DUP flag, max retries before session kill

## Key Interfaces

### Store Interfaces (store/interfaces.go)
```go
type SessionStore interface {
    SaveSession(ctx, clientID string, data *SessionData) error
    GetSession(ctx, clientID string) (*SessionData, error)
    DeleteSession(ctx, clientID string) error
    ListSessions(ctx context.Context) ([]string, error)
    IsSessionExists(ctx, clientID string) (bool, error)
}

type MessageStore interface {
    SaveMessage(ctx, clientID string, msg *StoredMessage) error
    GetMessage(ctx, clientID, id string) (*StoredMessage, error)
    DeleteMessage(ctx, clientID, id string) error
    ListMessages(ctx, clientID string) ([]*StoredMessage, error)
    ClearMessages(ctx, clientID string) error
}

type RetainedStore interface {
    SaveRetained(ctx, topic string, qos uint8, payload []byte) error
    GetRetained(ctx, topic string) (*RetainedMessage, error)
    DeleteRetained(ctx, topic string) error
    MatchRetained(ctx, pattern string) ([]*RetainedMessage, error)
}
```

### Auth Interfaces (auth/auth.go)
```go
type Authenticator interface {
    Authenticate(ctx, clientID, username, password string) (bool, error)
}

type Authorizer interface {
    Authorize(ctx, clientID, topic string, write bool) (bool, error)
}
```

### Plugin Interface (plugin/)
```go
type MQTTPlugin interface {
    Name() string
    Priority() int
    OnAccept(remoteAddr string) bool
    OnConnect(sess *session.MQTTSession)
    OnPublish(sess *session.MQTTSession, pkt *protocol.PublishPacket) bool
    OnSubscribe(sess *session.MQTTSession, filter string, qos uint8) bool
    OnDisconnect(sess *session.MQTTSession, reason session.CloseReason)
}
```

### Observability (infra/)
```go
type Logger interface {
    Debug(msg string, fields map[string]any)
    Info(msg string, fields map[string]any)
    Warn(msg string, fields map[string]any)
    Error(msg string, fields map[string]any)
}

type Metrics interface {
    IncConnections(), DecConnections(), IncRejections(reason string), IncAuthFailures()
    IncMessagesPublished(topic string, qos uint8), IncMessagesDelivered(clientID string, qos uint8), IncMessagesDropped(reason string)
    IncInflight(clientID string), DecInflight(clientID string), DecInflightBatch(clientID string, count int)
    IncInflightDropped(clientID string), IncRetries(clientID string)
    SetOnlineSessions(count int), SetOfflineSessions(count int), SetRetainedMessages(count int), SetSubscriptions(count int)
    IncErrors(component string)
}
```

## Coding Standards
- Go 1.26+, standard library conventions
- Use option pattern for configuration: `WithAddr()`, `WithAuth()`, `WithTLS()`
- Table-driven tests for unit tests
- All interfaces should have noop/default implementations
- Proper error handling using `errs` package
- Context cancellation throughout for graceful shutdown
- No global state — inject all dependencies

## Implementation Guidelines
When extending or modifying:
1. **New store backend**: Implement all 3 interfaces (SessionStore, MessageStore, RetainedStore)
2. **New auth provider**: Implement Authenticator + optionally Authorizer
3. **New plugin**: Embed BaseMQTTPlugin, override needed hooks
4. **Broker changes**: Keep TopicTree, QoSEngine, WillHandler as separate components
5. **Error handling**: Add new errors to errs/errors.go, use sentinel errors

## Three Usage Modes
1. **Standalone**: `api.NewBroker(opts...)` — default memory store, no external deps
2. **Embedded in Shark-Socket**: `sharksocket.NewMQTTAdapter()` — Gateway integration
3. **Library only**: `broker.New(config)` — use broker core, manage own network layer
