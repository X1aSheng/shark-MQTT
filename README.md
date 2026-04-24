# Shark-MQTT

A high-performance, standalone MQTT Broker written in Go, supporting both MQTT 3.1.1 and 5.0 protocols.

## Features

- **Protocol Support**: Full MQTT 3.1.1 and 5.0 support (15 packet types)
- **QoS Levels**: QoS 0, 1, 2 with automatic retry and inflight tracking
- **Persistent Sessions**: Cross-connection session persistence (CleanSession=false)
- **Topic Wildcards**: Full `+` and `#` wildcard support in subscriptions
- **Will Messages**: Automatic last-will message delivery on abnormal disconnect
- **Pluggable Auth**: Chain authentication (noop, static, file-based, custom)
- **Authorization**: ACL-based publish/subscribe access control
- **Plugin System**: Extensible hooks for OnAccept, OnConnected, OnMessage, OnClose
- **Multiple Storage Backends**: Memory, Redis, BadgerDB for sessions, messages, and retained messages
- **TLS Support**: Secure connections with configurable TLS
- **Observability**: Structured logging (slog) + Prometheus metrics (17+ methods)
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
│                        │ Manager (Sessions)           │  │
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
| `broker/` | Core broker: MQTTServer, Broker, TopicTree, QoSEngine, WillHandler, Session, Auth |
| `protocol/` | MQTT 3.1.1 & 5.0 codec (packets, properties) |
| `store/` | Storage interfaces + memory/redis/badger implementations |
| `pkg/` | Infrastructure: logger, metrics, bufferpool |
| `config/` | Configuration loading (YAML/ENV) |
| `plugin/` | Plugin system with hook-based architecture |
| `client/` | MQTT client implementation |
| `test/` | Integration and benchmark tests |
| `examples/` | Example usage |
| `docs/` | Documentation |
| `cmd/` | Command line tools |
| `errs/` | Centralized error definitions |

### Key Design Decisions

- **Network/Business Separation**: `broker.MQTTServer` handles networking, `broker.Broker` handles MQTT business logic
- **Session/Connection Decoupling**: Attach/Detach model allows persistent sessions across connections
- **Storage Interface**: Default memory implementation, swappable with Redis or BadgerDB
- **TopicTree**: O(log n) topic matching with wildcard support

## Quick Start

### Standalone Broker

```go
import (
    "log"
    "github.com/X1aSheng/shark-mqtt/api"
    "github.com/X1aSheng/shark-mqtt/config"
)

cfg := config.DefaultConfig()
cfg.ListenAddr = ":1883"

broker := api.NewBroker(api.WithConfig(cfg))
if err := broker.Start(); err != nil {
    log.Fatal(err)
}
```

### With Authentication

```go
import "github.com/X1aSheng/shark-mqtt/broker"

auth := broker.NewStaticAuth()
auth.AddCredentials("admin", "secret")

broker := api.NewBroker(
    api.WithAuth(auth),
)
```

### With TLS

```go
cfg := config.DefaultConfig()
cfg.TLSEnabled = true
cfg.TLSCertFile = "cert.pem"
cfg.TLSKeyFile = "key.pem"

broker := api.NewBroker(api.WithConfig(cfg))
```

### With Redis Storage

```go
import (
    redisstore "github.com/X1aSheng/shark-mqtt/store/redis"
    "github.com/redis/go-redis/v9"
)

client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

ss := redisstore.NewSessionStore(redisstore.SessionStoreConfig{
    Client:    client,
    KeyPrefix: "mqtt:session:",
})
// Similarly for MessageStore and RetainedStore

broker := api.NewBroker(
    api.WithSessionStore(ss),
)
```

## Performance

For detailed performance testing and profiling, see [docs/performance.md](docs/performance.md).

```bash
# Quick benchmark
make bench-quick

# Full suite
make bench

# CPU/Memory profiling
make bench-cpu
make bench-mem
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

For detailed project status, completed features, and roadmap, see [docs/PROJECT_STATUS.md](docs/PROJECT_STATUS.md).

**Overall completion: ~85-90%**

### Completed Features
- Full MQTT 3.1.1 & 5.0 protocol support (15 packet types)
- QoS 0, 1, 2 with automatic retry and inflight tracking
- Topic wildcard subscriptions (`+` and `#`)
- Retained messages and Will message handling
- Persistent session management with state machine and statistics
- Pluggable authentication and authorization (integrated into broker)
- Plugin system with extensible hooks
- Multiple storage backends (Memory, Redis, BadgerDB)
- TLS support
- Structured logging + Prometheus metrics
- Comprehensive test suite (unit, integration, benchmark, boundary)

## Testing

```bash
# All unit tests
go test ./api/... ./broker/... ./client/... ./config/... ./errs/... ./pkg/... ./plugin/... ./protocol/... ./store/...

# Integration tests (requires running broker)
go test -tags=integration ./test/integration/...

# Benchmarks
go test -bench=. -benchmem -benchtime=10s ./test/bench/...

# Coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html

# Redis store tests (requires Redis)
MQTT_REDIS_ADDR=localhost:6379 go test ./store/redis/...
```

## License

MIT License
