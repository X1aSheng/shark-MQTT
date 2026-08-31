# Shark-MQTT

> **English** | [简体中文](README.zh-CN.md)

## Project Overview

Shark-MQTT is a high-performance MQTT broker written in Go, implementing the
**MQTT 3.1.1 and 5.0** specifications end to end. It is engineered for
predictable behavior under load:

- **Zero external dependencies to run** — in-memory storage by default;
  Redis and BadgerDB are opt-in backends compiled behind build tags.
- **Bounded concurrency** — every queue and buffer has a hard cap
  (write queue, flow-control buffer, inflight, retained count), and the
  publish hot path is allocation-tuned (pooled codecs, tiered buffer pool).
- **Prompt dead-link detection** — MQTT keep-alive deadlines, configurable
  OS TCP keep-alive, and write-failure reaping keep zombie connections from
  lingering as "online".
- **Pluggable and observable** — auth/authorization chains, hook-based
  plugins, custom storage/metrics, structured logging, and Prometheus
  metrics with health/readiness endpoints.

On an AMD Ryzen 7 8845HS, the end-to-end QoS 0 round trip runs at ~68 µs/message
with 34 allocs; 128 KB payloads encode at ~103 µs with 135 KB allocated per
message.

## Features

**Protocol (MQTT 3.1.1 & 5.0)**
- All 15 packet types with complete property encoding/decoding
- Enhanced authentication (§4.12), Topic Alias, Message/Session Expiry,
  RequestResponseInformation, ReceiveMaximum flow control, shared
  subscriptions, spec-compliant `$SYS` topic protection

**QoS & Delivery**
- QoS 0/1/2 state machines with automatic retry and inflight tracking
- Overlapping subscriptions deliver once at the **maximum** matching QoS (§3.3.5)
- Offline message queue for persistent sessions; Message Expiry enforced
  across queue, flow-control buffer, and inflight retries

**Sessions**
- Persistent sessions with MQTT 5.0 Session Expiry Interval (honored on
  CONNECT and DISCONNECT); safe client-ID takeover with identity-checked
  cleanup

**Messages**
- Retained messages (with TTL), will messages (with delay interval),
  wildcard `+`/`#` matching, MQTT 5.0 subscription options
  (No Local, Retain Handling, Subscription Identifier)

**Reliability**
- Zombie-connection reaping: keep-alive deadlines, configurable OS TCP
  keep-alive (`tcp_keepalive_period`), and immediate socket close on write
  failure
- Connection limits enforced before authentication; CONNECT handshake
  deadline; per-connection bounded write queues

**Security**
- Pluggable auth chains (AllowAll, DenyAll, StaticAuth with ACL, FileAuth,
  ChainAuth) with **fail-closed defaults**; TLS 1.2+ / mTLS; publish/subscribe
  authorization, including will-message authorization

**Storage & Extensibility**
- `store` interfaces with memory (default), Redis, and BadgerDB backends
- Hook-based plugin system (OnAccept/OnConnected/OnMessage/OnClose) with panic
  isolation; custom authenticators, authorizers, stores, and metrics

**Observability**
- Structured logging (`slog`), Prometheus metrics, `/healthz`/`/readyz`
  (readiness requires both listener and broker subsystems)

## Architecture

```
cmd (CLI) → api (public facade + health server)
                 └─ broker: MQTTServer (TCP/TLS/WS) + Broker
                      ├─ TopicTree (wildcard matching, $SYS protection)
                      ├─ QoSEngine (retry + inflight)
                      ├─ WillHandler (delayed wills)
                      └─ Session Manager (persistence, takeover)
                 ↓        ↓        ↓
           protocol/  store/   pkg/
           (codec)    (memory/ (logger/
                      redis/   metrics/
                      badger)  bufferpool)
```

Layers depend one-directionally; the network layer and business logic are
separated, and connections are decoupled from sessions.

### Directory Layout

| Directory | Purpose |
|-----------|---------|
| `cmd/` `api/` | CLI entry; public API/factory and health endpoints |
| `broker/` | Core: server, broker, TopicTree, QoSEngine, WillHandler, sessions, auth |
| `protocol/` | MQTT 3.1.1 & 5.0 codec (15 packet types, properties) |
| `store/` | Storage interfaces + memory (default); redis/badger behind build tags |
| `client/` | MQTT 3.1.1/5.0 client |
| `plugin/` `config/` `errs/` | Plugin system; configuration; error sentinels |
| `pkg/` | logger, metrics, tiered buffer pool |
| `tests/` | integration/, bench/, defects/ + logs & artifacts (gitignored) |
| `examples/` `deploy/` `docs/` | Examples; Docker/K8s/Helm; documentation |

## Quick Start

```bash
# Run the broker
go run ./cmd -addr :18983 -allow-all        # development only

# Or embed it
```

```go
cfg := config.DefaultConfig()
cfg.ListenAddr = ":18983"
b := api.NewBroker(
    api.WithConfig(cfg),
    api.WithAuth(broker.AllowAllAuth{}), // replace with a real authenticator
)
if err := b.Start(); err != nil {
    log.Fatal(err)
}
defer b.Stop()
```

> The broker defaults to **deny-all** authentication; without an explicit
> authenticator (or `-allow-all`), connections are rejected.

More: [Examples](examples/) · [Configuration](docs/guides/CONFIGURATION.md) ·
[Docker & K8s](docs/architecture/DEPLOY.md)

## Performance

Measured on AMD Ryzen 7 8845HS / Windows 11 / Go 1.26.1 (`go test -bench . ./tests/bench/`).

| Benchmark | Time | B/op | allocs/op |
|---|---|---|---|
| Codec Encode Publish | 153 ns | 94 | 5 |
| Codec Decode Publish | 446 ns | 429 | 7 |
| Codec RoundTrip Publish | 565 ns | 528 | 12 |
| E2E QoS 0 (full round trip) | 68 µs | 956 | 34 |
| E2E QoS 1 | 105 µs | 1,705 | 54 |
| E2E QoS 2 | 226 µs | 2,876 | 87 |
| E2E 64 KB payload | 226 µs | 181 KB | 36 |
| Codec 128 KB payload encode | 103 µs | 135 KB | 24 |
| TopicTree match (exact) | 280 ns | 160 | 2 |
| Buffer pool Get/Put | 34 ns | 24 | 1 |

Details: [docs/guides/PERFORMANCE.md](docs/guides/PERFORMANCE.md)

## Testing

| Suite | Count | Status |
|---|---|---|
| Unit tests (incl. defect regressions) | 375 | Pass |
| Integration tests (end-to-end, incl. deploy verification) | 111 | Pass |
| Benchmarks | 65 | Pass |
| Race detector | full suite | Clean |
| Protocol fuzz (2 fuzzers) | 8.4M+ executions | No crashes |

Cross-platform runner: `go run scripts/run_tests.go -mode all` (unit, integration,
benchmark, cover). Logs go to `tests/logs/`, artifacts to `tests/artifacts/`.
Documentation links are checked in CI by `scripts/check_links.go`.

## Project Status

**Production-ready core.** MQTT 3.1.1/5.0 compliance, QoS state machines,
persistent sessions, retained/will messages, plugin system, and observability
are implemented and covered by regression tests. Recent reliability work:
zombie-connection reaping, storage-backend fail-fast validation, and
observable keep-alive timeouts.

Open items (see [docs/reports/PROJECT-REVIEW-260806-143527.md](docs/reports/PROJECT-REVIEW-260806-143527.md)):

| Priority | Item |
|---|---|
| Medium | TopicTree match caching (optional, measured as low value at current scale) |
| Medium | Large-cluster Kubernetes rollout verification |
| Medium | Raise total coverage toward the 60% target |
| Low | Named timeout constants in tests |

## Documentation

| Document | Description |
|---|---|
| [Architecture](docs/architecture/ARCHITECTURE.md) | Layered design and data flow |
| [Concurrency](docs/architecture/CONCURRENCY.md) | Lock inventory, ordering rules, zombie-connection detection |
| [API Reference](docs/guides/API.md) | Public API |
| [Configuration](docs/guides/CONFIGURATION.md) | YAML/ENV/CLI reference |
| [Performance](docs/guides/PERFORMANCE.md) | Benchmarks and profiling |
| [Deployment](docs/architecture/DEPLOY.md) | Docker, K8s, Helm |
| [Security](docs/architecture/SECURITY.md) | Threat model and hardening |
| [Testing](docs/guides/TESTING.md) | Test strategy and tooling |
| [Development](docs/guides/DEVELOPMENT.md) | Workflow and conventions |

## License

MIT License

