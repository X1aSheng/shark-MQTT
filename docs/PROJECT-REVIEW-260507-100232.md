# Project Review 260507-100232

Review time: 2026-05-07 10:02:32 Asia/Shanghai

## 1. Full Project Review

- Read/indexed files: 144 existing project files, excluding `logs/` and hidden directories such as `.git`.
- Initial file mix: 96 Go files, 19 Markdown files, 26 YAML/YML files, plus module, scripts, Makefile, and deployment assets.
- Structure overview:
  - `api/`: public broker API and health endpoints.
  - `broker/`: TCP/TLS server, core broker, topic tree, sessions, QoS, will, auth.
  - `protocol/`: MQTT 3.1.1/5.0 packets, codec, properties, topic validation.
  - `store/`: memory, Redis, Badger implementations.
  - `pkg/`: logger, metrics, buffer pool.
  - `tests/`: integration, benchmarks, and new defect reproductions.
  - `deploy/`: Docker, Kubernetes, Helm.
  - `docs/`: API, architecture, deployment, testing, security, configuration.

## 2. Architecture Analysis

Architecture document found: `docs/shark-mqtt architecture.md`.

The implementation largely follows the documented layered design: `api.Broker` composes `broker.MQTTServer` and `broker.Broker`, `protocol/` owns MQTT codec behavior, `store/` is interface-based, and observability is abstracted behind logger/metrics interfaces.

Differences and risks:

- The architecture document says the business broker has "no network dependency", but `broker.Broker` stores `net.Conn` in `clientState`. Risk: harder to introduce WebSocket/QUIC/gRPC-Web transports without wrapping `net.Conn` semantics.
- Distributed architecture is not implemented beyond pluggable Redis/Badger storage. There is no service discovery, node membership, consistent hashing, distributed session ownership, or SAGA/TCC coordination. Risk: horizontal scaling requires external sticky routing or new clustering work.
- Protocol support is intentionally MQTT over TCP/TLS plus HTTP health endpoints. UDP, HTTP/2, WebSocket, CoAP, QUIC, and gRPC-Web are not implemented. Risk: README/architecture should avoid implying those protocol stacks are supported.
- Redis implementation is single-client oriented, not Redis Cluster aware. Risk: production cache/distributed-state guidance is underdocumented.

Actionable architecture improvements:

1. Introduce a transport abstraction (`Conn`, `Listener`, `Transport`) so TCP/TLS, WebSocket, QUIC, and gRPC-Web adapters can share the MQTT broker core.
2. Split `broker/` into `broker/core`, `broker/session`, `broker/qos`, and `broker/transport` once the transport abstraction lands; keep `api/` as the stable facade.
3. Add cluster design docs for session ownership, retained-message distribution, consistent hashing, node discovery, and duplicate-client takeover across nodes.
4. Add an event-driven internal bus for session lifecycle and retained/QoS events so persistence, metrics, and plugin dispatch are decoupled from hot-path packet handling.

## 3. Test Results

Commands executed and logged:

- `go test ./... -coverprofile=coverage.out 2>&1 | tee logs/unit_test.log`
- `go test ./tests/... -v 2>&1 | tee logs/auto_test.log`

Results:

- Unit/package test pass rate: 100%.
- Integration/defect/deploy test pass rate: 100%.
- Total statement coverage: 36.0%.
- Failure details: none after fixes.

Static analysis:

- `go vet ./...`: pass, zero output.
- `staticcheck ./...`: tool not installed in PATH.
- `golangci-lint run ./...`: pass, `0 issues`.

## 4. Defect List

| ID | Type | Severity | Reproduction | Impact |
| --- | --- | --- | --- | --- |
| D-001 | Protocol violation / boundary condition | High | `go test ./tests/defects -run TestDefectTopicFiltersMayContainEmptyLevels -v` failed before fix because `/finance`, `finance/`, and `finance//usd` were rejected. | Valid MQTT topic filters with zero-length levels could not subscribe or match, causing interoperability failures. |
| D-002 | Protocol violation / maintainability | Medium | `go test ./tests/defects -run TestDefectLongMQTT5PropertyStringReturnsEncodeError -v` failed before fix because a property string > 65535 bytes encoded without error. | MQTT 5.0 property encoding could silently produce malformed packets instead of rejecting invalid UTF-8 string length. |

Standard references:

- OASIS MQTT 3.1.1 section 4.7.1.1 says topic separators may appear anywhere and "Adjacent Topic level separators indicate a zero length topic level." Source: https://docs.oasis-open.org/mqtt/mqtt/v3.1.1/csd01/mqtt-v3.1.1-csd01.html
- OASIS MQTT 5.0 section 1.5.4 states a UTF-8 Encoded String has a Two Byte Integer length prefix and a maximum size of 65,535 bytes. Source: https://docs.oasis-open.org/mqtt/mqtt/v5.0/cos02/mqtt-v5.0-cos02.html

## 5. Fix Plan

Priority order:

1. High: D-001 topic filter empty-level handling.
2. Medium: D-002 MQTT 5.0 property encoding error propagation.
3. Medium: Static-analysis hardening for production code and lint configuration.
4. Low: Future transport/cluster documentation and design work.

## 6. Fix Details and Verification

D-001:

- Files: `protocol/topic.go`, `tests/defects/protocol_defects_test.go`.
- Change: allow empty topic levels while preserving `#` and `+` wildcard placement rules.
- Verification: defect test passes; `go test ./protocol ./broker ./tests/defects` passes; full test suite passes.

D-002:

- Files: `protocol/properties.go`, `tests/defects/protocol_defects_test.go`.
- Change: propagate every property write error, including overlength UTF-8 strings and binary payload length checks.
- Verification: defect test passes; full test suite passes; `golangci-lint run ./...` reports `0 issues`.

Static hardening:

- Files: `api/api.go`, `broker/broker.go`, `broker/will_handler.go`, `client/client.go`, `examples/sharksocket/main.go`, `pkg/bufferpool/pool.go`, `protocol/connect.go`, `protocol/publish.go`, `protocol/subscribe.go`, `.golangci.yaml`.
- Change: handle/record important close, deadline, encode, health-server, will-trigger errors; make buffer pool store pointer-like values; configure v2 golangci-lint exclusions for test-only mechanical noise and QF1008 style hints.
- Verification: `go vet ./...` passes; `golangci-lint run ./...` reports `0 issues`; full test suite passes.

## 7. Protocol Stack Compliance Notes

- TCP: implemented via `broker.MQTTServer`.
- TLS: implemented, minimum TLS 1.2 documented.
- HTTP/1.1: health endpoints implemented via Go `net/http`.
- MQTT 3.1.1 and MQTT 5.0: implemented; this review fixed topic filter and property string compliance gaps.
- UDP, HTTP/2-specific behavior, WebSocket, CoAP, QUIC, and gRPC-Web: not implemented in current codebase; require explicit transport adapters and architecture docs before claiming support.

## 8. Documentation Sync Items

- Added this review report.
- Updated testing/static-analysis documentation to describe the defect regression suite and golangci-lint v2 config.
- Updated README status notes for zero-length topic levels and MQTT 5.0 property encoding validation.
