# Shark-MQTT Expert Skill

## Overview
Shark-MQTT is a high-performance MQTT Broker implementation supporting **MQTT 3.1.1** and **MQTT 5.0** protocols, extracted from Shark-Socket as an independent project.

**Module**: `github.com/X1aSheng/shark-mqtt`
**Go Version**: 1.26.1

## Architecture
Layered architecture with clear separation of concerns:

```
api/                    → Unified public facade (Broker + Options)
broker/                 → Core: MQTTServer (network), Broker (business), TopicTree,
                           QoSEngine, WillHandler, Manager (sessions), Auth
protocol/               → MQTT 3.1.1 + 5.0 codec (15 packet types, properties)
store/                  → Pluggable storage (memory/redis/badger)
plugin/                 → Plugin system (Manager with hooks)
pkg/                    → Infrastructure (logger, metrics, bufferpool)
config/                 → YAML/ENV configuration loading
errs/                   → Error definitions
client/                 → MQTT client implementation
testutils/              → Testing utilities (mocks, helpers)
test/                   → Integration and benchmark tests
examples/               → Example applications
cmd/                    → CLI entry point
```

## Key Design Principles
- **P1**: Network layer and business logic separation (`broker.MQTTServer` vs `broker.Broker`)
- **P2**: Connection and session separation (Attach/Detach model via `Manager`)
- **P3**: Storage and runtime separation (pluggable `SessionStore`, `MessageStore`, `RetainedStore`)
- **P4**: QoS state machine encapsulation (`QoSEngine`)
- **P5**: Observability first (`Logger`, `Metrics` interfaces)

## MQTT Protocol Implementation
- **15 packet types**: CONNECT, CONNACK, PUBLISH, PUBACK, PUBREC, PUBREL, PUBCOMP, SUBSCRIBE, SUBACK, UNSUBSCRIBE, UNSUBACK, PINGREQ, PINGRESP, DISCONNECT, AUTH
- **QoS levels**: 0 (at most once), 1 (at least once), 2 (exactly once)
- **Topic wildcards**: `+` (single-level), `#` (multi-level)
- **Persistent sessions**: CleanSession=false retains state across disconnects
- **Will messages**: Last-will with delayed delivery support
- **Retained messages**: Last known good value per topic
- **MQTT 5.0 Properties**: Full property encode/decode support

## Storage Layer
Three interfaces with three implementations each:
| Interface | Memory | Redis | BadgerDB |
|-----------|--------|-------|----------|
| `SessionStore` | done | done | done |
| `MessageStore` | done | done | done |
| `RetainedStore` | done | done | done |

## Authentication (in broker/)
- `NoopAuth` - No-op authenticator
- `AllowAllAuth` / `DenyAllAuth` - Simple allow/deny
- `StaticAuth` - Static credentials with ACL support
- `FileAuth` - Credentials from YAML/JSON file
- `ChainAuth` - Multiple authenticators in chain
- `Authorizer` interface - ACL-based `CanPublish`/`CanSubscribe` (integrated in broker)

## Plugin System
Hook-based plugin manager with these hooks:
- `OnAccept` - Before connection established
- `OnConnected` - After CONNECT/CONNACK
- `OnMessage` - On each MQTT packet
- `OnClose` - On connection close

## Infrastructure (in pkg/)
- **Logger**: `pkg/logger/` - slog-based structured logging
- **Metrics**: `pkg/metrics/` - 17-method Metrics interface for Prometheus
- **BufferPool**: `pkg/bufferpool/` - Reusable byte buffer pool

## External Dependencies
```
github.com/dgraph-io/badger/v4     → Persistent KV store (BadgerDB backend)
github.com/prometheus/client_golang → Prometheus metrics
github.com/redis/go-redis/v9       → Redis store backend
gopkg.in/yaml.v3                   → Config YAML loading
```

## Testing Strategy
- **Unit tests**: Per package (`broker`, `protocol`, `store/memory`, `store/badger`, `config`, `api`, `plugin`, `pkg/`, `client`, `errs`)
- **Integration tests**: `test/integration/` (connect, pubsub, QoS 0/1/2, will, persistent sessions)
- **Benchmark tests**: `test/bench/` (E2E benchmarks in `broker_bench_test.go`, micro-benchmarks in `micro_bench_test.go`)
- **Boundary tests**: `broker/qos_engine_boundary_test.go` (PacketID, Topic, ClientID, QoS, Payload, Retain)
- **Mock implementations**: `testutils/`

## Current Limitations

### Protocol
- MQTT 5.0 enhanced auth (AuthPacket exchange) not implemented
- No `$share/` shared subscription support
- Properties parsing minimal - `UserProperty` not fully wired

### Advanced Features
- No HTTP admin API
- No Topic Alias support
- MQTT 5.0 Enhanced Auth incomplete

## API Usage Pattern

```go
// Standalone broker
cfg := config.DefaultConfig()
cfg.ListenAddr = ":1883"

broker := api.NewBroker(
    api.WithConfig(cfg),
    api.WithAuth(broker.NewStaticAuth()),
)
broker.Start()
```
