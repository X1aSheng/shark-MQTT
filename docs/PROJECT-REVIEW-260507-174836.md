# Project Review 260507-174836

Review time: 2026-05-07 17:48:36 CST

## 1. Full Project Review

- Recursive inventory: 148 files after excluding `logs/` and hidden directories.
- Structure overview: `api/` 3, `broker/` 21, `client/` 3, `config/` 3, `deploy/` 29, `docs/` 11, `pkg/` 9, `protocol/` 10, `store/` 16, `tests/` 18, plus root README, Makefile, Go module files.
- Core implementation: standalone MQTT broker with TCP/TLS networking, MQTT 3.1.1 / 5.0 codec, QoS 0/1/2, retained messages, will messages, persistent sessions, auth, stores, metrics, integration tests, defect tests, and benchmarks.

## 2. Architecture Analysis

Architecture document found: `docs/shark-mqtt architecture.md`. No root `ARCHITECTURE.md` / `DESIGN.md` entry exists; add a root entry or link for discoverability.

Implementation alignment and risks:

- `api.Broker` composes `broker.MQTTServer` and `broker.Broker`; `protocol/` owns MQTT packet encoding/decoding; `store/` provides memory, Badger, and Redis adapters. This matches the documented layered design.
- MQTTServer lifecycle had a restart/Stop race: Stop closed the listener while the accept goroutine could still dereference mutable listener state.
- Windows benchmark runs could exhaust ephemeral TCP ports when connection-churn benchmarks ran before E2E benchmarks in the same package process.
- `pkg/metrics` and Redis store coverage remain low, so distributed-cache and observability paths need stronger tests before production rollout.

Actionable architecture suggestions:

1. Add root `ARCHITECTURE.md` and ADR index for MQTT lifecycle, storage, QoS, and benchmark platform decisions.
2. Keep `MQTTServer` lifecycle state private and immutable per Start call; pass listener snapshots into goroutines.
3. Split benchmark suites by intent: connection churn, broker publish, E2E correctness, and microbenchmarks.
4. Add Redis Cluster / distributed session design docs before introducing cluster routing or retained-message replication.
5. Introduce a protocol compliance matrix mapped to OASIS MQTT 3.1.1 / 5.0 normative clauses.

## 3. Tests and Benchmarks

| Type | Command | Result | Log |
| --- | --- | --- | --- |
| Unit tests | `go test ./... -coverprofile=coverage.out` | Pass, total coverage 38.1% | `logs/unit_test.log` |
| Automated tests | `go test ./tests/... -v` | Pass | `logs/auto_test.log` |
| Benchmark | `go test "-bench=." -benchmem "-run=^$" ./tests/...` | Pass after fixes | `logs/benchmark.log` |
| go vet | `go vet ./...` | Pass | `logs/go_vet.log` |
| golangci-lint | `golangci-lint run ./...` | Pass, 0 issues | `logs/golangci-lint.log` |
| staticcheck | `staticcheck ./...` | Not installed in PATH; recorded | `logs/staticcheck.log` |

Benchmark summary:

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkPublishQos0` | 23582 | 1769 | 27 |
| `BenchmarkPublishQos1` | 94645 | 1948 | 37 |
| `BenchmarkPublishQos2` | 186238 | 2545 | 52 |
| `BenchmarkConcurrentPublish` | 34724 | 1736 | 27 |
| `BenchmarkE2E_QoS0_DataVerify` | 49438 | 904 | 36 |
| `BenchmarkE2E_QoS1_DataVerify` | 131513 | 1355 | 54 |
| `BenchmarkE2E_QoS2_DataVerify` | 244865 | 2414 | 84 |
| `BenchmarkE2E_Payload_64KB` | 368812 | 427214 | 38 |
| `BenchmarkTopicTree_Match_Exact` | 322.2 | 88 | 2 |
| `BenchmarkCodec_RoundTripPublish` | 777.3 | 1168 | 16 |
| `BenchmarkQoSEngine_TrackQoS1` | 20.05 | 0 | 0 |
| `BenchmarkMemoryStore_SessionGet` | 5.055 | 0 | 0 |

On Windows, `BenchmarkConnectionEstablish` and `BenchmarkMQTTConnect` are skipped because they intentionally churn thousands of TCP connections and can poison the same test process for later E2E benchmarks. Broker publish, fanout, E2E data verification, codec, QoS engine, buffer pool, and store benchmarks still run.

## 4. Defect List

| ID | Type | Severity | Reproduction test | Impact | Status |
| --- | --- | --- | --- | --- | --- |
| MQTT-DEF-003 | Concurrency / lifecycle | High | `go test ./tests/defects -run TestDefectMQTTServerRestartAfterStopAcceptsConnections -v` | `MQTTServer` could not be safely restarted after Stop; accept loop could observe mutable listener state during shutdown | Fixed |
| MQTT-DEF-004 | Performance / benchmark stability | Medium | `go test ./tests/defects -run TestDefectWindowsConnectionChurnBenchmarksDoNotPoisonSuite -v` | Full Windows benchmark suite could fail with TCP port exhaustion before E2E MQTT benchmarks | Fixed |

Repair details:

- `broker/server.go`: reset canceled context on Start; pass a listener snapshot into `acceptLoop`; close listener during Stop, wait for goroutines, then clear listener state.
- `tests/bench/broker_bench_test.go`: use loopback random listen/metrics addresses; skip connection-churn benchmarks on Windows.
- `tests/bench/data_delivery_bench_test.go`: use loopback random listen/metrics addresses for E2E benchmark brokers.
- `tests/defects/broker_lifecycle_test.go`: added lifecycle and Windows benchmark stability regressions.

## 5. Protocol Compliance References

- MQTT 3.1.1 defines the network connection as an ordered, lossless, bidirectional byte stream and specifies topic filters, QoS flows, retained messages, and will messages: https://docs.oasis-open.org/mqtt/mqtt/v3.1.1/mqtt-v3.1.1.html
- MQTT 5.0 extends session, properties, reason codes, and flow control semantics: https://docs.oasis-open.org/mqtt/mqtt/v5.0/mqtt-v5.0.html
- TCP/TLS compliance remains bounded to Go `net` / `crypto/tls`; this repo does not implement UDP, HTTP/2, WebSocket, CoAP, QUIC, or gRPC-Web directly.

## 6. Fix Order and Verification

Fix order:

1. High: MQTTServer lifecycle restart / Stop race.
2. Medium: Windows benchmark TCP port exhaustion.

Verification:

- `go test ./tests/defects -v`: pass.
- `go test "-bench=." -benchmem "-run=^$" ./tests/...`: pass.
- `go test ./... -coverprofile=coverage.out`: pass.
- `go test ./tests/... -v`: pass.
- `go vet ./...`: pass.
- `golangci-lint run ./...`: pass, 0 issues.

## 7. Documentation Sync

- Added this review report.
- Updated benchmark behavior notes in performance/testing documentation.
- No new runtime configuration keys were added.
