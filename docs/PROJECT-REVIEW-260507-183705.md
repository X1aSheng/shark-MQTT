# Shark-MQTT Project Review - 260507-183705

## 1. Full Project Review

- Repository: `g:\shark-mqtt`
- Files read/indexed, excluding `logs/` and hidden directories: 149.
- Structure overview: `api` 3, `broker` 21, `client` 3, `cmd` 1, `config` 3, `deploy` 29, `docs` 13, `errs` 2, `examples` 3, `pkg` 9, `plugin` 2, `protocol` 10, `scripts` 7, `store` 16, `tests` 18, `testutils` 4, plus root files.
- Core implementation: independent MQTT broker with TCP/TLS networking, MQTT 3.1.1 / 5.0 codec, QoS 0/1/2, retained messages, will messages, persistent sessions, auth, storage backends, metrics, tests, defect tests, and benchmarks.

## 2. Architecture Analysis

Architecture document found: `docs/shark-mqtt architecture.md`. No root `ARCHITECTURE.md` / `DESIGN.md` entry exists; add a root entry or link for discoverability.

Implementation/design status:

| Area | Finding | Risk |
| --- | --- | --- |
| Independent runtime | shark-socket adapter/example files have been removed; docs describe independent deployment and data interoperability | Good alignment with current architecture |
| Data interoperability | Architecture now recommends shared Redis/cache, shared database, outbox/CDC/MQ, and batch/CDC modes | Needs schema examples and ownership rules before production multi-service writes |
| Transport scope | Broker remains TCP/TLS MQTT; WebSocket/QUIC/gRPC-Web MQTT transports are not implemented | Docs should keep these as future transport work only |
| Distributed runtime | Redis/Badger storage exists, but no built-in service discovery, cluster membership, consistent hashing, or distributed session ownership | Horizontal scaling still requires external routing and data-contract discipline |

Actionable architecture suggestions:

1. Add root `ARCHITECTURE.md` and ADR index for independent runtime, storage ownership, event contracts, and benchmark platform decisions.
2. Define data interoperability schemas for device state, auth ACLs, retained projections, online state, and message audit/outbox events.
3. Add idempotency and ordering rules for outbox/CDC/MQ consumers, including retry backoff and dead-letter handling.
4. Document Redis key namespaces, TTL policy, anti-penetration, anti-avalanche, and hot-key protection for shared cache usage.

## 3. Tests and Benchmarks

| Check | Command | Result | Log |
| --- | --- | --- | --- |
| Unit | `go test ./... -coverprofile=coverage.out` | Pass | `logs/unit_test.log` |
| Integration/defect | `go test ./tests/... -v` | Pass | `logs/auto_test.log` |
| Benchmark | `go test "-bench=." -benchmem "-run=^$" ./tests/...` | Pass | `logs/benchmark.log` |
| Coverage | `go tool cover -func=coverage.out` | 38.4% total statements | `coverage.out` |
| vet | `go vet ./...` | Pass | `logs/go_vet.log` |
| golangci-lint | `golangci-lint run ./...` | Pass, 0 issues | `logs/golangci-lint.log` |
| staticcheck | `staticcheck ./...` | Not installed in PATH; recorded as skipped | `logs/staticcheck.log` |

Benchmark summary:

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkPublishQos0` | 25530 | 1767 | 27 |
| `BenchmarkPublishQos1` | 86987 | 1947 | 37 |
| `BenchmarkPublishQos2` | 157644 | 2552 | 52 |
| `BenchmarkConcurrentPublish` | 32821 | 1161 | 28 |
| `BenchmarkPersistentSession` | 608695 | 7558 | 163 |
| `BenchmarkPayload_128KB` | 1532498 | 549847 | 29 |
| `BenchmarkFanOut_50Subs` | 1094112 | 30018 | 440 |
| `BenchmarkE2E_QoS0_DataVerify` | 44519 | 904 | 36 |
| `BenchmarkE2E_QoS1_DataVerify` | 124197 | 1355 | 54 |
| `BenchmarkE2E_QoS2_DataVerify` | 202342 | 2412 | 84 |
| `BenchmarkE2E_RetainedMessage` | 50138 | 1322 | 44 |
| `BenchmarkE2E_WildcardDelivery` | 45798 | 1128 | 38 |
| `BenchmarkE2E_FanOut_VerifyAll` | 125476 | 2954 | 111 |
| `BenchmarkE2E_Payload_64KB` | 427145 | 427226 | 38 |
| `BenchmarkTopicTree_Subscribe` | 172.8 | 51 | 0 |
| `BenchmarkTopicTree_Match_Exact` | 283.9 | 88 | 2 |
| `BenchmarkCodec_RoundTripPublish` | 916.8 | 1168 | 16 |
| `BenchmarkQoSEngine_TrackQoS1` | 19.07 | 0 | 0 |
| `BenchmarkBufferPool_Parallel` | 10.79 | 24 | 1 |
| `BenchmarkMemoryStore_SessionGet` | 4.699 | 0 | 0 |

Windows note: `BenchmarkConnectionEstablish`, `BenchmarkMQTTConnect`, and `BenchmarkE2E_WillMessage` are skipped on Windows to avoid ephemeral TCP port exhaustion from short-lived connection churn or abnormal disconnect churn.

## 4. Defect List

No new reproducible defects were found in this run. Existing defect tests pass.

| ID | Type | Severity | Reproduction | Impact | Status |
| --- | --- | --- | --- | --- | --- |
| MQTT-OBS-006 | Architecture follow-up | Low | Review `docs/shark-mqtt architecture.md` data interoperability section | Data contracts are described conceptually but not yet provided as schema files | Observation only |

## 5. Improvement Plan

Priority order:

1. Medium: add concrete shared database/cache/event schema examples.
2. Medium: add outbox consumer idempotency, retry backoff, dead-letter, and ordering guidance.
3. Medium: add Redis namespace and cache protection documentation.
4. Low: add root architecture entry and ADR index.
5. Low: install standalone `staticcheck` in CI if separate staticcheck proof is required beyond golangci-lint.

## 6. Fix and Verification Record

No source fix was required for shark-MQTT in this run.

Verification proof:

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
- UDP, HTTP/2-specific MQTT behavior, WebSocket MQTT, CoAP, QUIC, and gRPC-Web are not implemented as MQTT transports in this repository.

## 8. Documentation Sync

This report records the post-decoupling review, fresh benchmark run, test status, protocol references, and data-interoperability improvement priorities for the 260507-183705 audit cycle.
