# Shark-MQTT Development Skill Guide

## Project Overview
Shark-MQTT is a high-performance MQTT v3.1.1/v5.0 broker written in Go, extracted from Shark-Socket. It supports complete QoS 0/1/2 delivery, topic wildcard matching, persistent sessions, last-will messages, and retained messages with pluggable storage backends (memory, Redis, BadgerDB).

## Architecture Layers
The project follows strict separation of concerns:

| Layer | Package | Responsibility |
|-------|---------|----------------|
| **API** | `api/` | Unified public interface — all exports in one package |
| **Network** | `broker/` (MQTTServer) | TCP listening, TLS, per-connection read/write loops, protocol decoding |
| **Protocol** | `protocol/` | MQTT 3.1.1 & 5.0 encode/decode, all 15 packet types |
| **Broker Core** | `broker/` (Broker) | Message routing, subscription matching (TopicTree), QoS state machine (QoSEngine), will handling (WillHandler) |
| **Session** | `broker/` (Manager/Session) | Connection/session lifecycle, persistent session registry, session attach/detach |
| **Auth** | `broker/` (Authenticator/Authorizer) | Connection authentication + topic-level authorization |
| **Storage** | `store/` | Pluggable backends for sessions, offline messages, retained messages |
| **Plugin** | `plugin/` | Lifecycle hooks (OnAccept, OnConnected, OnMessage, OnClose) |
| **Config** | `config/` | YAML/ENV-based configuration loading |
| **Infra** | `pkg/` | Cross-cutting: Logger, Metrics, BufferPool |
| **Errors** | `errs/` | Centralized error definitions |
| **Client** | `client/` | MQTT client implementation |

## Key Design Principles

### P1 — Network/Business Separation
- `broker.MQTTServer` handles TCP accept, TLS, connection read/write loops
- `broker.Broker` handles MQTT business logic, no network dependencies
- Communication via `ConnectionHandler` interface

### P2 — Connection/Session Separation
- Connection lifecycle = TCP connection lifetime
- Session lifecycle >= connection (CleanSession=false persists across reconnects)
- `Manager.CreateSession()` handles creation and kick of old sessions

### P3 — Storage/Runtime Separation
- All persistence via interfaces: `SessionStore`, `MessageStore`, `RetainedStore`
- Default memory implementations for zero-dependency usage
- Production backends: Redis (cluster-ready), BadgerDB (embedded disk)

### P4 — QoS State Machine
- QoS 1: Enqueue -> SENT -> PUBACK -> DONE (with retry)
- QoS 2: Enqueue -> SENT -> PUBREC -> PUBREL_SENT -> PUBCOMP -> DONE
- Max inflight per client, backpressure via configurable limits
- Background retry loop with max retries before message drop

## Key Interfaces

### Store Interfaces (store/interfaces.go)
```go
type SessionStore interface {
    SaveSession(ctx context.Context, clientID string, data *SessionData) error
    GetSession(ctx context.Context, clientID string) (*SessionData, error)
    DeleteSession(ctx context.Context, clientID string) error
    ListSessions(ctx context.Context) ([]string, error)
    IsSessionExists(ctx context.Context, clientID string) (bool, error)
}

type MessageStore interface {
    SaveMessage(ctx context.Context, clientID string, msg *StoredMessage) error
    GetMessage(ctx context.Context, clientID, id string) (*StoredMessage, error)
    DeleteMessage(ctx context.Context, clientID, id string) error
    ListMessages(ctx context.Context, clientID string) ([]*StoredMessage, error)
    ClearMessages(ctx context.Context, clientID string) error
}

type RetainedStore interface {
    SaveRetained(ctx context.Context, topic string, qos uint8, payload []byte) error
    GetRetained(ctx context.Context, topic string) (*RetainedMessage, error)
    DeleteRetained(ctx context.Context, topic string) error
    MatchRetained(ctx context.Context, pattern string) ([]*RetainedMessage, error)
}
```

### Auth Interfaces (broker/auth.go)
```go
type Authenticator interface {
    Authenticate(ctx context.Context, clientID, username, password string) error
}

type Authorizer interface {
    CanPublish(ctx context.Context, clientID, topic string) bool
    CanSubscribe(ctx context.Context, clientID, topic string) bool
}
```

### Plugin Interface (plugin/manager.go)
```go
type Plugin interface {
    Name() string
    Hooks() []Hook
    Execute(ctx context.Context, hook Hook, data *Context) error
}

type Hook string // "OnAccept", "OnConnected", "OnMessage", "OnClose"

type Context struct {
    ClientID   string
    Username   string
    Topic      string
    Payload    []byte
    QoS        uint8
    Retain     bool
    RemoteAddr string
    Err        error
}
```

### Logger (pkg/logger/)
```go
type Logger interface {
    Debug(msg string, args ...interface{})
    Info(msg string, args ...interface{})
    Warn(msg string, args ...interface{})
    Error(msg string, args ...interface{})
}
```

### Metrics (pkg/metrics/)
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

## Coding Standards
- Go 1.22+, standard library conventions
- Use option pattern for configuration: `WithConfig()`, `WithAuth()`, `WithMetrics()`
- Table-driven tests for unit tests
- All interfaces should have noop/default implementations
- Proper error handling using sentinel errors
- Context cancellation throughout for graceful shutdown
- No global state — inject all dependencies

## Implementation Guidelines
When extending or modifying:
1. **New store backend**: Implement all 3 interfaces (SessionStore, MessageStore, RetainedStore) in `store/mybackend/`
2. **New auth provider**: Implement `Authenticator` (+ optionally `Authorizer`) in `broker/`
3. **New plugin**: Implement `Plugin` interface, register with `Manager.Register()`
4. **Broker changes**: Keep TopicTree, QoSEngine, WillHandler as separate components within `broker/`
5. **Error handling**: Add new errors to `errs/` package, use sentinel errors

## Three Usage Modes
1. **Standalone**: `api.NewBroker(opts...)` — default memory store, no external deps
2. **Embedded in Shark-Socket**: `sharksocket.NewMQTTAdapter()` — Gateway integration
3. **Library only**: `broker.New(opts...)` — use broker core, manage own network layer
