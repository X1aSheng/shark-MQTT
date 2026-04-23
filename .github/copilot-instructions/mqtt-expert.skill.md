# Shark-MQTT Expert Skill

## Overview
Shark-MQTT is a high-performance MQTT Broker implementation supporting **MQTT 3.1.1** and **MQTT 5.0** protocols, extracted from Shark-Socket as an independent project.

**Module**: `github.com/X1aSheng/shark-mqtt`
**Go Version**: 1.26.1

## Architecture
Layered architecture with clear separation of concerns:

```
api/                    → Unified public facade (Broker + Option + Run)
integration/sharksocket/ → Shark-Socket Gateway adapter
server/                 → Network layer (MQTTServer, TCP/TLS, connection handling)
protocol/               → MQTT 3.1.1 + 5.0 codec (15 packet types)
broker/                 → Core business (Broker, TopicTree, QoSEngine, WillHandler)
session/                → Session management (Session, Manager)
store/                  → Pluggable storage (memory/redis/badger)
auth/                   → Authentication (Authenticator, Authorizer, chain/file/noop)
plugin/                 → Plugin system (Manager with hooks)
infra/                  → Infrastructure (logger, metrics, bufferpool)
config/                 → YAML configuration loading
errs/                   → Error definitions
client/                 → MQTT client implementation
testutils/              → Testing utilities (mocks, helpers, benchmarks)
examples/               → Example applications
test/                   → Integration and benchmark tests
cmd/                    → CLI entry point
```

## Key Design Principles
- **P1**: Network layer and business logic separation (`server.MQTTServer` vs `broker.Broker`)
- **P2**: Connection and session separation (Attach/Detach model)
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
- **Packet identifiers**: Required for QoS 1/2 message flows

## Storage Layer
Three interfaces with three implementations each:
| Interface | Memory | Redis | BadgerDB |
|-----------|--------|-------|----------|
| `SessionStore` | ✅ | ✅ | ✅ |
| `MessageStore` | ✅ | ✅ | ✅ |
| `RetainedStore` | ✅ | ✅ | ✅ |

## Authentication
- `NoopAuth` - Allow all (testing)
- `StaticAuth` - Static credentials
- `FileAuth` - Credentials from file
- `ChainAuth` - Multiple authenticators in chain
- `AllowAllAuth` / `DenyAllAuth` - Simple implementations
- `Authorizer` interface - ACL-based publish/subscribe checks

## Plugin System
Hook-based plugin manager with these hooks:
- `OnAccept` - Before connection established
- `OnConnected` - After CONNECT/CONNACK
- `OnMessage` - On each MQTT packet
- `OnClose` - On connection close

## Infrastructure
- **Logger**: `infra/logger/logger.go` + `infra/logger/slog.go` (slog implementation)
- **Metrics**: `infra/metrics/metrics.go` (interface) + `prometheus.go` + `noop.go`
- **BufferPool**: `infra/bufferpool/pool.go` (reusable byte buffers)

## External Dependencies
```
github.com/dgraph-io/badger/v4    → Persistent KV store (BadgerDB backend)
github.com/prometheus/client_golang → Prometheus metrics
github.com/redis/go-redis/v9       → Redis store backend
gopkg.in/yaml.v3                   → Config YAML loading
```

## Testing Strategy
- **Unit tests**: Per package (`protocol`, `store/memory`, `session`, `server`, `auth`, `config`, `api`)
- **Integration tests**: `test/integration/` (connect, pubsub, QoS 0/1/2)
- **Benchmark tests**: `testutils/bench.go`
- **Mock implementations**: `testutils/mock_store.go`, `testutils/mock_conn.go`
- **Test helpers**: `testutils/helpers.go` (MustEncode* functions)

## Known Issues & Limitations

### Protocol
- Protocol properties encoding uses simplified implementation
- MQTT 5.0 PUBLISH decode doesn't track bytes read precisely
- MQTT 5.0 enhanced auth (AuthPacket exchange) not implemented
- No `$share/` shared subscription support
- No retained message delivery on subscribe
- No `protocol/registry.go` for packet type registration
- No `protocol/errors.go` with protocol error types
- Properties parsing minimal - `UserProperty` not fully wired into CONNECT, PUBLISH

### Architecture Gaps
- **CRITICAL**: `broker/broker.go` (with TopicTree/QoSEngine/WillHandler) is DEAD CODE - not wired into `api/api.go`
- **CRITICAL**: `infra/broker/broker.go` is a monolithic implementation with no TopicTree, no QoS retry, no Will handling
- **CRITICAL**: Metrics interface (`infra/metrics`) defined with 17+ methods but NEVER called in broker/server/qos
- **CRITICAL**: Plugin system (`plugin/manager.go`) exists but NOT imported/used by server or broker
- `server/server.go` has functional options file but ignores them - uses config directly
- No `SafeConn` wrapper usage for read/write in server
- No `infra/bufferpool/` implementation

### Session Layer
- `session/registry.go` does NOT exist - no session tracking/kick/replace
- `Session.State` field defined but never used - no state machine
- `Session.Stats` defined but never tracked - no message/byte counting
- No session close reason tracking

### Authorization
- `broker/broker.go` doesn't call `CanPublish` in `HandlePublish`
- `broker/broker.go` doesn't call `CanSubscribe` in `HandleSubscribe`
- `api/api.go` has `WithAuth`/`WithAuthorizer` but auth only forwarded to infra/broker

### Missing Tests
- `broker/broker_test.go` - main broker tests
- `test/integration/will_test.go` - Will message integration tests
- `test/bench/` - benchmark tests
- `plugin/manager_test.go`
- `store/badger/*_test.go` and `store/redis/*_test.go`
- `session/registry_test.go` (registry doesn't exist yet)
- `test-errs` - errs/ package tests
- `test-infra` - infra/ layer tests

### API Layer Missing Options
- `WithPlugin` - does not exist
- `WithMetrics` - does not exist
- `WithStore` - does not exist
- No HTTP admin API endpoints

## Implementation Priority
| Priority | Feature | Status |
|----------|---------|--------|
| **P0** | Wire `broker/broker.go` into `api/api.go` | Dead code path |
| **P0** | Wire metrics (`infra/metrics`) into broker/server/qos | 0% integrated |
| **P0** | Wire plugin system into broker and server | 0% integrated |
| **P1** | Implement `session/registry.go` | Missing |
| **P1** | Wire authorization in broker (CanPublish/CanSubscribe) | Missing |
| **P1** | Add missing test coverage | Multiple gaps |
| **P2** | Implement `infra/bufferpool/` | Missing |
| **P2** | Add store tests for badger/redis | Missing |
| **P3** | MQTT 5.0 enhanced auth | Packet exists, no logic |
| **P3** | Session state machine & stats | Defined, unused |
| **P3** | `server/server.go` functional options | File unused |