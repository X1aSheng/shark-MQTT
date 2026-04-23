# Shark-MQTT

A high-performance, standalone MQTT Broker written in Go, supporting both MQTT 3.1.1 and 5.0 protocols.

## Features

- **Protocol Support**: Full MQTT 3.1.1 and 5.0 support
- **QoS Levels**: QoS 0, 1, 2 with automatic retry and inflight tracking
- **Persistent Sessions**: Cross-connection session persistence (CleanSession=false)
- **Topic Wildcards**: Full `+` and `#` wildcard support in subscriptions
- **Will Messages**: Automatic last-will message delivery on abnormal disconnect
- **Pluggable Auth**: Chain authentication (noop, file-based, custom)
- **Plugin System**: Extensible hooks for OnAccept, OnConnect, OnPublish, OnSubscribe, OnDisconnect
- **Multiple Storage Backends**: Memory, Redis, BadgerDB for sessions, messages, and retained messages
- **TLS Support**: Secure connections with configurable TLS
- **Observability**: Structured logging (slog) + Prometheus metrics
- **shark-socket Integration**: Can run standalone or integrated with shark-socket framework

## Architecture

Shark-MQTT follows a clean separation of concerns:

```
┌─────────────────────────────────────────────────────────┐
│                      api/api.go                         │
│              Unified Public API & Factory               │
└─────────────────────┬───────────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────┐
│                       broker/                           │
│              Network + Business Logic                   │
│  ┌────────────────┐  ┌──────────────────────────────┐  │
│  │ MQTTServer     │  │ Broker                       │  │
│  │ TCP/TLS/Accept │◄─┤ TopicTree                    │  │
│  │ Connection Mgmt│  │ QoSEngine                    │  │
│  └────────────────┘  │ WillHandler                  │  │
│                        │ Session + Registry + Manager   │  │
│                        │ Authenticator + Authorizer   │  │
│                        └──────────────────────────────┘  │
└─────────────────────────────────────────────────────────┘
                      │
        ┌─────────────┼─────────────┐
        ▼             ▼             ▼
┌──────────────┐ ┌─────────────┐ ┌─────────────┐
│   protocol/  │ │  store/     │ │   pkg/      │
│  MQTT Codec  │ │  Memory     │ │  Logger     │
│  Packets     │ │  Redis      │ │  Metrics    │
│  Properties  │ │  BadgerDB   │ │  BufferPool │
└──────────────┘ └─────────────┘ └─────────────┘
```

### Directory Structure

| Directory | Description |
|-----------|-------------|
| `api/` | Unified public API & factories |
| `broker/` | Core broker: MQTTServer, TopicTree, QoSEngine, WillHandler, Session, Auth |
| `protocol/` | MQTT 3.1.1 & 5.0 codec (packets, properties) |
| `store/` | Storage interfaces + memory/redis/badger implementations |
| `pkg/` | Infrastructure: logger, metrics, bufferpool |
| `config/` | Configuration loading (YAML/ENV) |
| `test/` | Integration and benchmark tests |
| `client/` | MQTT client implementation |
| `plugin/` | Plugin system (ACL, rate limit, audit) |
| `examples/` | Example usage |
| `docs/` | Documentation |
| `cmd/` | Command line tools |

### Key Design Decisions

- **Network/Business Separation**: `server.MQTTServer` handles networking, `broker.Broker` handles MQTT business logic
- **Session/Connection Decoupling**: Attach/Detach model allows persistent sessions across connections
- **Storage Interface**: Default memory implementation, swappable with Redis or BadgerDB
- **TopicTree**: O(log n) topic matching with wildcard support

## Quick Start

### Standalone Broker

```go
import "github.com/X1aSheng/shark-mqtt/api"

broker := api.NewBroker(
    api.WithAddr(":1883"),
)
if err := broker.Start(); err != nil {
    log.Fatal(err)
}
```

### With TLS

```go
broker := api.NewBroker(
    api.WithTLSFromFiles("cert.pem", "key.pem"),
)
```

### With Custom Auth

```go
auth := api.NewFuncAuth(func(req *api.AuthRequest) bool {
    return req.Username == "admin" && string(req.Password) == "secret"
})
broker := api.NewBroker(
    api.WithAuthenticator(auth),
)
```

### With Redis Storage

```go
import redisstore "github.com/X1aSheng/shark-mqtt/store/redis"

ss, ms, rs := redisstore.New(redisClient)
broker := api.NewBroker(
    api.WithSessionStore(ss),
    api.WithMessageStore(ms),
    api.WithRetainedStore(rs),
)
```

## Configuration

The broker can be configured programmatically via options or via YAML configuration file.

### YAML Example

```yaml
listen_addr: ":1883"
keep_alive: 60
max_packet_size: 262144
max_connections: 10000
storage_backend: "memory"
log_level: "info"
qos_retry_interval: "10s"
qos_max_retries: 3
qos_max_inflight: 100
```

### Environment Variables

All config options can be set via environment variables with the `MQTT_` prefix (e.g., `MQTT_LISTEN_ADDR`, `MQTT_KEEP_ALIVE`).

## Project Status

For detailed project status, completed features, and roadmap, see [PROJECT_STATUS.md](PROJECT_STATUS.md).

### Completed Features ✅
- Full MQTT 3.1.1 & 5.0 protocol support (15 packet types)
- QoS 0, 1, 2 with automatic retry and inflight tracking
- Topic wildcard subscriptions (`+` and `#`)
- Retained messages and Will message handling
- Persistent session management
- Pluggable authentication and authorization
- Plugin system with extensible hooks
- Multiple storage backends (Memory, Redis, BadgerDB)
- TLS support
- Structured logging + Prometheus metrics
- Comprehensive test suite (unit + integration tests)

### In Progress 🚧
- Session registry and state machine
- Session statistics tracking
- Authorization enforcement in publish/subscribe
- Additional unit and integration tests
- Benchmark tests for performance validation

## Testing

```bash
# All tests (with race detection)
go test -race ./...

# Unit tests
go test -race ./broker/... ./session/... ./protocol/...

# Integration tests
go test -race -tags=integration ./test/integration/...

# Benchmarks
go test -bench=. -benchmem -benchtime=10s ./test/bench/...

# Coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

## License

MIT License
