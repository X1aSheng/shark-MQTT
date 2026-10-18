# Shark-MQTT

> **English** | [简体中文](README.zh-CN.md)

## Project Overview

Shark-MQTT is a high-performance MQTT broker written in Go, implementing the
**MQTT 3.1.1 and 5.0** specifications end to end. It is engineered for
predictable behavior under load:

- **Zero external dependencies to run** - in-memory storage by default;
  Redis and BadgerDB are opt-in backends compiled behind build tags.
- **Bounded concurrency** - every queue and buffer has a hard cap
  (write queue, flow-control buffer, inflight, retained count), and the
  publish hot path is allocation-tuned (pooled codecs, tiered buffer pool).
- **Prompt dead-link detection** - MQTT keep-alive deadlines, configurable
  OS TCP keep-alive, and write-failure reaping keep zombie connections from
  lingering as "online".
- **Pluggable and observable** - auth/authorization chains, hook-based
  plugins, custom storage/metrics, structured logging, and Prometheus
  metrics with health/readiness endpoints.

On an AMD Ryzen 7 8845HS, the end-to-end QoS 0 round trip runs at ~68 us/message
with 34 allocs; 128 KB payloads encode at ~103 us with 135 KB allocated per
message.

## Features

**Protocol (MQTT 3.1.1 & 5.0)**
- All 15 packet types with complete property encoding/decoding
- Enhanced authentication (Section 4.12), Topic Alias, Message/Session Expiry,
  RequestResponseInformation, ReceiveMaximum flow control, shared
  subscriptions, spec-compliant `$SYS` topic protection

**QoS & Delivery**
- QoS 0/1/2 state machines with automatic retry and inflight tracking
- Overlapping subscriptions deliver once at the **maximum** matching QoS (Section 3.3.5)
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
cmd (CLI) -> api (public facade + health server)
                  +- broker: MQTTServer (TCP/TLS/WS) + Broker
                  |    +- TopicTree (wildcard matching, $SYS protection)
                  |    +- QoSEngine (retry + inflight)
                  |    +- WillHandler (delayed wills)
                  |    +- Session Manager (persistence, takeover)
                  v          v          v
             protocol/    store/     pkg/
             (codec)      (memory/   (logger/
                           redis/     metrics/
                           badger)    bufferpool)
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
| `client/` | MQTT 3.1.1 client (5.0 support lives in the broker) |
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

More: [Examples](examples/) ,  [Configuration](docs/guides/CONFIGURATION.md) , 
[Docker & K8s](docs/architecture/DEPLOY.md)

## Performance

Measured on AMD Ryzen 7 8845HS / Windows 11 / Go 1.26.1 (`go test -bench . ./tests/bench/`).

| Benchmark | Time | B/op | allocs/op |
|---|---|---|---|
| Codec Encode Publish | 153 ns | 94 | 5 |
| Codec Decode Publish | 446 ns | 429 | 7 |
| Codec RoundTrip Publish | 565 ns | 528 | 12 |
| E2E QoS 0 (full round trip) | 68 us | 956 | 34 |
| E2E QoS 1 | 105 us | 1,705 | 54 |
| E2E QoS 2 | 226 us | 2,876 | 87 |
| E2E 64 KB payload | 226 us | 181 KB | 36 |
| Codec 128 KB payload encode | 103 us | 135 KB | 24 |
| TopicTree match (exact) | 280 ns | 160 | 2 |
| Buffer pool Get/Put | 34 ns | 24 | 1 |

### Server Capacity

The table above measures individual functions. This measures a running broker:
2 vCPU / 4 GB Alibaba Cloud ECS, load generator on a **separate host** over the
private network (0.12 ms RTT).

| Scenario | Result |
|---|---|
| Idle connections | 10,000 at **~25 KB each** (258 MB RSS, 9 threads) |
| Mixed fleet: 10k online, 10 subscribers, 100 publishers | **193,838 msg/s** delivered, 0.82 of 2 cores, 423 MB |
| Saturation, 1 KB payload, 4 subscribers | **353,855 msg/s** delivered |
| 2-hour soak, mixed QoS and payloads | **1.44B messages delivered, 0 errors**, RSS peak 110.1 MB, back to 22.5 MB after load |
| Binary size | 11.2 MiB (7.7 MiB with `-tags nometrics`) |

The soak line is the one that answers "does it leak". Two hours, 1.44 billion
delivered messages, and the RSS peak stayed at the same 110-112 MB level the
30-minute and 9-hour runs showed - then returned to the idle 22.5 MB once every
client disconnected.

Two qualifiers matter more than the numbers. The 353k run is where the
**network** gave out, not the broker - CPU sat at 1.37 of 2 cores while pushing
2.9 Gbps. And 10,000 idle connections is what was measured, not a ceiling: the
limit is `max_connections` (10,000 by default, configurable).

Method, raw data, and the fan-out knee: [LOAD-TEST-PLAN](docs/benchmarks/LOAD-TEST-PLAN.md),
[Aliyun baseline](docs/benchmarks/2026-10-17-aliyun-2c4g.md),
[cross-broker comparison](docs/reports/CROSS-BROKER-COMPARISON-261017-121500.md).


Details: [docs/guides/PERFORMANCE.md](docs/guides/PERFORMANCE.md)

## Testing

| Suite | Count | Status |
|---|---|---|
| Unit tests (incl. defect regressions) | 547 | Pass |
| Integration tests (end-to-end, incl. deploy verification) | 196 | Pass |
| Benchmarks | 72 | Pass |
| Race detector | full suite | Clean |
| Protocol fuzz (2 fuzzers) | 8.4M+ executions | No crashes |

Counts are declared test/benchmark functions and are reproducible with
`go test -list '.*' <packages> | grep -c '^Test'`. Two caveats when reproducing:
three connection-churn benchmarks are skipped on Windows by design (a local run
reports 69), and the `store/redis` / `store/badger` suites only compile under the
`store_redis` / `store_badger` build tags, so they are not part of the default
counts. Per-package detail: [docs/guides/TESTING.md](docs/guides/TESTING.md).

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

