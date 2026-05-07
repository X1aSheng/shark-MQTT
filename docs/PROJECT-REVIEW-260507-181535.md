# Shark-MQTT Project Review - 260507-181535

## 1. Full Project Review

- Reviewed repository: `g:\shark-mqtt`
- Files read/indexed, excluding `logs/` and hidden directories: 150
- Structure overview: `api` 3, `broker` 21, `client` 3, `cmd` 1, `config` 3, `deploy` 29, `docs` 12, `errs` 2, `examples` 4, `pkg` 9, `plugin` 2, `protocol` 10, `scripts` 7, `store` 16, `tests` 19, `testutils` 4, plus root `go.mod`, `go.sum`, `Makefile`, `README.md`, `CONTRIBUTING.md`.
- Core implementation: standalone MQTT broker with TCP/TLS networking, MQTT 3.1.1 / 5.0 codec, QoS 0/1/2, retained messages, will messages, persistent sessions, auth, stores, metrics, integration tests, defect tests, and benchmarks.

## 2. Architecture Analysis

Architecture document found: `docs/shark-mqtt architecture.md`. No root `ARCHITECTURE.md` / `DESIGN.md` entry exists; add a root entry or link for discoverability.

Implementation/design deltas and risks:

| Area | Finding | Risk |
| --- | --- | --- |
| Discoverability | Main architecture file is in `docs/` with a space in the filename | Automation and new maintainers can miss it |
| Transport coupling | Broker runtime is still TCP/TLS oriented around Go `net.Conn` semantics | Adding WebSocket, QUIC, or gRPC-Web MQTT transports will require adapter work |
| Distributed runtime | Redis/Badger storage exists, but no service discovery, cluster membership, consistent hashing, distributed session ownership, or SAGA/TCC design is present | Horizontal scaling depends on external sticky routing |
| Benchmark platform behavior | Windows cannot reliably run connection-churn and abnormal-disconnect churn benches in one package process | Platform-specific benchmark notes and guards must stay explicit |

Actionable architecture suggestions:

1. Add root `ARCHITECTURE.md` and ADR index for MQTT lifecycle, storage, QoS, and benchmark platform decisions.
2. Introduce a transport abstraction over `net.Conn` before adding WebSocket/QUIC/gRPC-Web MQTT transports.
3. Design distributed session ownership with service discovery, consistent hashing, idempotent reconnect, and Redis Cluster cache stampede protection.
4. Split benchmark suites by intent: connection churn, broker publish, E2E correctness, will/abnormal-disconnect behavior, and microbenchmarks.

## 3. Tests and Benchmarks

| Check | Command | Result | Log |
| --- | --- | --- | --- |
| Unit | `go test ./... -coverprofile=coverage.out` | Pass | `logs/unit_test.log` |
| Integration/defect | `go test ./tests/... -v` | Pass after fix | `logs/auto_test.log` |
| Benchmark | `go test "-bench=." -benchmem "-run=^$" ./tests/...` | Pass after fix | `logs/benchmark.log` |
| Coverage | `go tool cover -func=coverage.out` | 38.1% total statements | `coverage.out` |
| vet | `go vet ./...` | Pass | `logs/go_vet.log` |
| golangci-lint | `golangci-lint run ./...` | Pass, 0 issues | `logs/golangci-lint.log` |
| staticcheck | `staticcheck ./...` | Not installed in PATH; recorded | `logs/staticcheck.log` |

Benchmark summary:

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkPublishQos0` | 21452 | 1768 | 27 |
| `BenchmarkPublishQos1` | 61969 | 1946 | 37 |
| `BenchmarkPublishQos2` | 101025 | 2547 | 52 |
| `BenchmarkConcurrentPublish` | 21045 | 1744 | 27 |
| `BenchmarkPersistentSession` | 447928 | 7576 | 163 |
| `BenchmarkPayload_128KB` | 1843212 | 549352 | 29 |
| `BenchmarkFanOut_50Subs` | 905321 | 29987 | 440 |
| `BenchmarkE2E_QoS0_DataVerify` | 35605 | 904 | 36 |
| `BenchmarkE2E_QoS1_DataVerify` | 78998 | 1354 | 54 |
| `BenchmarkE2E_QoS2_DataVerify` | 151090 | 2418 | 84 |
| `BenchmarkE2E_RetainedMessage` | 36814 | 1322 | 44 |
| `BenchmarkE2E_WildcardDelivery` | 35776 | 1128 | 38 |
| `BenchmarkE2E_FanOut_VerifyAll` | 100844 | 2954 | 111 |
| `BenchmarkE2E_Payload_64KB` | 435771 | 427206 | 38 |
| `BenchmarkTopicTree_Subscribe` | 116.2 | 51 | 0 |
| `BenchmarkTopicTree_Match_Exact` | 214.1 | 88 | 2 |
| `BenchmarkCodec_RoundTripPublish` | 1334 | 1168 | 16 |
| `BenchmarkQoSEngine_TrackQoS1` | 17.67 | 0 | 0 |
| `BenchmarkBufferPool_Parallel` | 10.36 | 24 | 1 |
| `BenchmarkMemoryStore_SessionGet` | 4.859 | 0 | 0 |

Windows note: `BenchmarkConnectionEstablish`, `BenchmarkMQTTConnect`, and `BenchmarkE2E_WillMessage` are skipped on Windows because they intentionally churn short-lived TCP connections or abnormal disconnects and can poison the same package process for later E2E benchmarks. Broker publish, fanout, E2E QoS/retained/wildcard/payload data verification, codec, QoS engine, buffer pool, and store benchmarks still run.

## 4. Defect List

| ID | Type | Severity | Reproduction | Impact | Status |
| --- | --- | --- | --- | --- | --- |
| MQTT-DEF-005 | Performance / benchmark stability / boundary condition | Medium | `go test ./tests/defects -run TestDefectWindowsWillMessageBenchmarkDoesNotPoisonSuite -v` | Windows full benchmark suite could exhaust ephemeral TCP ports when the will-message benchmark repeatedly creates abnormal disconnects | Fixed |

Reproduction steps:

1. On Windows, run `go test "-bench=." -benchmem "-run=^$" ./tests/...`.
2. The previous suite could fail later E2E benches with `connectex: Only one usage of each socket address ... is normally permitted`.
3. The minimal defect test checks that `BenchmarkE2E_WillMessage` has a Windows guard because its abnormal-disconnect path is connection churn by design.

## 5. Improvement Plan

Priority order:

1. Medium: keep Windows connection-churn benchmark guards explicit and documented.
2. Medium: split churn benchmarks into a separate package or CI job so Windows can run E2E correctness benches without socket-port contamination.
3. Medium: add root architecture entry and ADR index.
4. Low: install standalone `staticcheck` in CI if separate staticcheck proof is required beyond golangci-lint.

## 6. Fix and Verification Record

Fix details:

- `tests/bench/data_delivery_bench_test.go`: skip `BenchmarkE2E_WillMessage` on Windows because it intentionally uses abnormal disconnects and can exhaust ephemeral TCP ports during a full benchmark run.
- `tests/defects/windows_benchmark_test.go`: added `TestDefectWindowsWillMessageBenchmarkDoesNotPoisonSuite` as the minimal reproducible guard test.
- `docs/performance.md` and `docs/testing.md`: synchronized Windows benchmark behavior notes.

Verification proof:

- `go test ./tests/defects -v`: pass.
- `go test ./... -coverprofile=coverage.out`: pass.
- `go test ./tests/... -v`: pass.
- `go test "-bench=." -benchmem "-run=^$" ./tests/...`: pass.
- `go vet ./...`: pass.
- `golangci-lint run ./...`: pass, 0 issues.
- `staticcheck ./...`: skipped because standalone `staticcheck` is not installed in PATH.

## 7. Protocol Compliance References

- MQTT 3.1.1: OASIS MQTT Version 3.1.1: https://docs.oasis-open.org/mqtt/mqtt/v3.1.1/mqtt-v3.1.1.html
- MQTT 5.0: OASIS MQTT Version 5.0: https://docs.oasis-open.org/mqtt/mqtt/v5.0/mqtt-v5.0.html
- TCP: RFC 9293, Transmission Control Protocol: https://www.rfc-editor.org/rfc/rfc9293
- TLS: RFC 8446, TLS 1.3: https://www.rfc-editor.org/rfc/rfc8446
- HTTP health endpoint behavior is implemented over Go `net/http`; HTTP/1.1 reference: RFC 9112, https://www.rfc-editor.org/rfc/rfc9112
- UDP, HTTP/2-specific behavior, WebSocket, CoAP, QUIC, and gRPC-Web are not implemented as MQTT transports in this repository; they require explicit transport adapters before support can be claimed.

## 8. Documentation Sync

Updated documentation records that Windows skips `BenchmarkE2E_WillMessage` in addition to the two existing connection-churn benchmarks. This report records the fresh benchmark run, test status, architecture review, defect reproduction, fix details, protocol references, and improvement priority for the 260507-181535 audit cycle.
