# Shark-MQTT

A high-performance MQTT Broker written in Go, supporting both **MQTT 3.1.1** and **MQTT 5.0** protocols.

## Features

- **Protocol Support**: Full MQTT 3.1.1 & 5.0 with 15 packet types, complete property encoding/decoding
- **QoS Levels**: QoS 0, 1, 2 with automatic retry, inflight tracking, and error handling
- **Persistent Sessions**: Cross-connection session persistence (`CleanSession=false`) with graceful shutdown drain
- **Topic Wildcards**: Full `+` and `#` wildcard support with spec-compliant filter validation
- **Retained Messages**: Store-and-forward last message per topic with wildcard delivery
- **Will Messages**: Automatic last-will delivery on abnormal disconnect, with MQTT 5.0 Will Delay Interval support
- **Pluggable Auth**: Chain authentication — `AllowAll`, `DenyAll`, `StaticAuth` (credentials + ACL), or custom `Authenticator`/`Authorizer` interfaces
- **Plugin System**: Extensible hooks for `OnAccept`, `OnConnected`, `OnMessage`, `OnClose`
- **Multiple Storage Backends**: In-memory (default), Redis, BadgerDB for sessions, messages, and retained messages
- **Connection Limit**: Configurable max connections with pre-auth enforcement
- **TLS Support**: Secure connections with configurable TLS
- **Observability**: Structured logging (`slog`) + Prometheus metrics (17+ methods) + `/healthz`/`/readyz` endpoints
- **Safe Concurrency**: Per-connection write mutex, atomic ID generation, thread-safe session management
- **Config Validation**: Built-in `Validate()` for all config fields
- **shark-socket Integration**: Can run standalone or as a shark-socket server adapter

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│                       cmd/main.go                        │
│                   CLI Entry Point                        │
└────────────────────────┬─────────────────────────────────┘
                         │
                         ▼
┌──────────────────────────────────────────────────────────┐
│                      api/api.go                          │
│             Unified Public API & Factory                 │
│            (Start / Stop / Addr / ConnCount)             │
│            + Health Server (/healthz, /readyz)           │
└────────────────────────┬─────────────────────────────────┘
                         │
                         ▼
┌──────────────────────────────────────────────────────────┐
│                        broker/                           │
│               Network + Business Logic                   │
│  ┌─────────────────┐  ┌──────────────────────────────┐  │
│  │  MQTTServer      │  │  Broker                      │  │
│  │  TCP/TLS Accept  │◄─┤  TopicTree (wildcard match)  │  │
│  │  Connection Mgmt │  │  QoSEngine (retry + inflight)│  │
│  │  Per-conn Mutex  │  │  WillHandler (delay support) │  │
│  └─────────────────┘  │  Manager (sessions)           │  │
│                        │  Authenticator + Authorizer   │  │
│                        │  Connection Limiter           │  │
│                        └──────────────────────────────┘  │
└──────────────────────────────────────────────────────────┘
              │              │              │
              ▼              ▼              ▼
┌────────────────┐ ┌──────────────┐ ┌──────────────┐
│   protocol/    │ │   store/     │ │    pkg/      │
│  MQTT Codec    │ │  Memory      │ │  Logger      │
│  15 Packets    │ │  Redis       │ │  Metrics     │
│  MQTT 5.0 Props│ │  BadgerDB    │ │  BufferPool  │
└────────────────┘ └──────────────┘ └──────────────┘
```

### Directory Structure

| Directory | Description |
|-----------|-------------|
| `cmd/` | CLI entry point with signal handling and flag parsing |
| `api/` | Unified public API, broker factory, health endpoints |
| `broker/` | Core: MQTTServer, Broker, TopicTree, QoSEngine, WillHandler, Session, Auth |
| `protocol/` | MQTT 3.1.1 & 5.0 codec — 15 packet types with property support |
| `store/` | Storage interfaces + memory / redis / badger implementations |
| `pkg/` | Infrastructure: logger (slog), metrics (Prometheus), bufferpool |
| `config/` | Configuration loading (YAML / ENV) with validation |
| `plugin/` | Plugin system with hook-based architecture |
| `client/` | MQTT client implementation |
| `errs/` | Centralized error definitions |
| `tests/integration/` | 47 end-to-end integration tests |
| `tests/bench/` | 46 benchmarks (broker + micro) |
| `examples/` | Runnable example programs (standalone, TLS, custom auth, shark-socket) |
| `docs/` | Architecture, deployment, performance, and improvement docs |
| `scripts/` | Test runner scripts (Windows/Linux/macOS) |

## Quick Start

### Installation

```bash
go get github.com/X1aSheng/shark-mqtt
```

### Standalone Broker

```go
package main

import (
    "context"
    "log"
    "os/signal"
    "syscall"

    "github.com/X1aSheng/shark-mqtt/api"
    "github.com/X1aSheng/shark-mqtt/broker"
    "github.com/X1aSheng/shark-mqtt/config"
)

func main() {
    cfg := config.DefaultConfig()
    cfg.ListenAddr = ":1883"

    b := api.NewBroker(
        api.WithConfig(cfg),
        api.WithAuth(broker.AllowAllAuth{}),
    )

    if err := b.Start(); err != nil {
        log.Fatal(err)
    }

    ctx, _ := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    <-ctx.Done()
    b.Stop()
}
```

### CLI

```bash
# Build and run
go build -o shark-mqtt ./cmd/
./shark-mqtt -addr :1883 -log-level info

# With TLS
./shark-mqtt -addr :8883 -tls -tls-cert cert.pem -tls-key key.pem

# With connection limit
./shark-mqtt -addr :1883 -max-conn 10000
```

### Authentication

```go
auth := broker.NewStaticAuth()
auth.AddCredentials("admin", "secret")
auth.AddCredentials("device-1", "token-abc")
auth.AddPublishACL("admin", "sensor/#")   // admin can publish to sensor/*
auth.AddSubscribeACL("admin", "#")        // admin can subscribe to everything

b := api.NewBroker(api.WithAuth(auth))
```

### TLS

```go
cfg := config.DefaultConfig()
cfg.TLSEnabled = true
cfg.TLSCertFile = "cert.pem"
cfg.TLSKeyFile = "key.pem"

b := api.NewBroker(api.WithConfig(cfg))
```

### Redis Storage

```go
import (
    redisstore "github.com/X1aSheng/shark-mqtt/store/redis"
    "github.com/redis/go-redis/v9"
)

client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

b := api.NewBroker(
    api.WithSessionStore(redisstore.NewSessionStore(redisstore.SessionStoreConfig{
        Client:    client,
        KeyPrefix: "mqtt:session:",
    })),
)
```

### Plugin

```go
type LogPlugin struct{}

func (p *LogPlugin) Name() string                          { return "log-plugin" }
func (p *LogPlugin) Hooks() []plugin.Hook                  { return []plugin.Hook{plugin.OnMessage} }
func (p *LogPlugin) Execute(hook plugin.Hook, data any) error {
    if msg, ok := data.(*protocol.PublishPacket); ok {
        log.Printf("message: topic=%s payload=%s", msg.Topic, msg.Payload)
    }
    return nil
}

pm := plugin.NewManager()
pm.Register(&LogPlugin{})
b := api.NewBroker(api.WithPluginManager(pm))
```

## Configuration

### YAML

```yaml
listen_addr: ":1883"
keep_alive: 60
max_packet_size: 262144
max_connections: 10000
storage_backend: "memory"
log_level: "info"
log_format: "json"
qos_retry_interval: "10s"
qos_max_retries: 3
qos_max_inflight: 100
session_expiry_interval: 3600

# TLS
tls_enabled: false
tls_cert_file: ""
tls_key_file: ""

# Redis
redis_addr: "localhost:6379"
redis_password: ""
redis_db: 0

# BadgerDB
badger_path: "./data"

# Metrics
metrics_enabled: true
metrics_addr: ":9090"
```

### Environment Variables

All config options support the `MQTT_` prefix:

```bash
MQTT_LISTEN_ADDR=:1883 MQTT_MAX_CONNECTIONS=5000 ./shark-mqtt
```

### Options API

```go
b := api.NewBroker(
    api.WithConfig(cfg),
    api.WithAuth(myAuth),
    api.WithMaxConnections(5000),
    api.WithSessionStore(ss),
    api.WithMessageStore(ms),
    api.WithRetainedStore(rs),
    api.WithLogger(logger),
    api.WithMetrics(metrics),
    api.WithPluginManager(pm),
)
```

## Performance

Benchmarks run on **AMD Ryzen 7 8845HS / Windows 11 / Go 1.26.1**:

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| Connection Establish | 243k | — | — |
| MQTT Connect | 314k | — | — |
| Publish QoS 0 | 21.6k | — | — |
| Publish QoS 1 | 60.3k | — | — |
| Publish QoS 2 | 96.7k | — | — |
| Concurrent Publish | 21.0k | — | — |
| Payload 128KB | 1.96M | — | — |
| TopicTree Subscribe | 115 | 51 | 0 |
| TopicTree Match (exact) | 209 | 88 | 2 |
| TopicTree Match (wildcard #) | 187 | 88 | 2 |
| TopicTree Match (wildcard +) | 299 | 136 | 3 |
| Codec Encode Publish | 435 | 422 | 6 |
| Codec Decode Publish | 458 | 432 | 8 |
| QoS Engine Track QoS 1 | 89 | 128 | 1 |
| BufferPool Get/Put | 32 | 24 | 1 |
| MemoryStore Session Get | 8.5 | 0 | 0 |

Full results: `make bench` or see `docs/performance.md`.

## Testing

| Type | Count | Status |
|------|-------|--------|
| Unit Tests | 197 | All pass |
| Integration Tests | 47 | All pass |
| Benchmarks | 46 | All pass |
| **Total** | **290** | **0 failures** |

> 13 Redis tests skipped when `MQTT_REDIS_ADDR` is not set.

### Integration Test Coverage

| Category | Tests | Details |
|----------|-------|---------|
| Connect & Session | 5 | CONNECT flow, persistent session, reconnect, kick |
| Pub/Sub | 4 | Basic, QoS 0/1/2 |
| Will Messages | 4 | Abnormal/graceful disconnect, QoS 0/1 |
| Topic Wildcards | 5 | `+`, `#`, root, mixed, multiple subscribers |
| Retained Messages | 5 | New subscriber, update, delete, wildcard, QoS downgrade |
| Multi-subscriber | 12 | Same topic, mixed QoS, ordering, burst, large/binary/empty/unicode payload, overlapping |
| Unsubscribe | 4 | Stop delivery, multi-topic, wildcard, resubscribe |
| QoS Details | 3 | Publisher ACK, full QoS 2 handshake, no-subscriber publish |
| System Topics | 1 | Normal client exclusion |
| Edge Cases | 5 | Auth failure, duplicate clientID, invalid filter, max connections, empty clientID |

All integration tests with subscriptions verify **end-to-end data delivery**: publish a message after subscribe and confirm the subscriber receives the correct topic and payload.

### Running Tests

```bash
# Unit tests
make test

# Integration tests
make test-integration

# With race detector
make test-race

# Benchmarks
make bench-quick          # 1s per test
make bench                # 5s x 3 runs

# Coverage report
make test-coverage

# Redis tests
MQTT_REDIS_ADDR=localhost:6379 make test-redis

# Full CI pipeline
make ci
```

## Examples

Each example is in its own directory and can be run independently:

```bash
# Standalone broker
go run ./examples/standalone

# TLS broker
go run ./examples/tls_broker

# Custom authentication
go run ./examples/custom_auth

# shark-socket integration
go run ./examples/sharksocket
```

## CI

GitHub Actions CI runs on every push/PR:

- **Unit Tests**: Go 1.23 / 1.24 / 1.25 x Ubuntu / macOS / Windows
- **Plugin Tests**: Dedicated plugin manager test job
- **Lint**: `go vet` + `gofmt` formatting check
- **Build**: Cross-platform build verification
- **Coverage**: 50% minimum threshold with Codecov upload

See `.github/workflows/ci.yml` for details.

## Project Status

**Overall: Production-ready core**

All critical and high-severity issues resolved. See [docs/IMPROVEMENT_ROADMAP.md](docs/IMPROVEMENT_ROADMAP.md) for the full tracker.

### Completed

- Full MQTT 3.1.1 & 5.0 protocol support (15 packet types + properties)
- QoS 0/1/2 with automatic retry, inflight tracking, and send error handling
- Spec-compliant topic filter validation
- Retained messages and Will messages (with delay interval)
- Persistent session management with graceful shutdown drain
- Per-connection write mutex (concurrent frame safety)
- Configurable connection limits
- Pluggable auth/authz
- Plugin system
- Memory / Redis / BadgerDB storage
- TLS support
- Health endpoints (`/healthz`, `/readyz`)
- Config validation
- Comprehensive test suite (290 tests)

### Future Improvements

- MQTT 5.0 Enhanced Authentication
- HTTP Admin API
- Topic Alias support
- Protocol fuzzing
- Large-scale connection stress tests

## Documentation

| Document | Description |
|----------|-------------|
| [Architecture](docs/shark-mqtt%20architecture.md) | Detailed architecture design |
| [API Reference](docs/API.md) | Public API documentation |
| [Configuration](docs/configuration.md) | Full configuration guide |
| [Performance](docs/performance.md) | Benchmarking and profiling |
| [Deployment](docs/DEPLOY.md) | Deployment instructions |
| [Security](docs/SECURITY.md) | Security considerations |
| [Testing](docs/testing.md) | Testing guide |
| [Development](docs/development.md) | Development workflow |
| [Improvement Roadmap](docs/IMPROVEMENT_ROADMAP.md) | Defects and planned improvements |

## License

MIT License
