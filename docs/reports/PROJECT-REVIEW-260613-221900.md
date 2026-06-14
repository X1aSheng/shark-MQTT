# Shark-MQTT Project Improvement Summary - 2026-06-13

## Improvements Made

### 1. Metrics Test Coverage (2% → 98%)
- Added comprehensive `prometheusMetrics` tests for all 17+ methods
- Added `registerOrReuse` reuse test (same registry, no panic)
- Added `qosLabel` boundary tests (QoS 0,1,2,3,255 → "unknown")
- Added `ObserveMessageLatency` to existing noop tests (was missing)
- Added Prometheus metrics benchmark

### 2. Client Test Coverage (29% → 85%)
- Integration tests with real in-process broker:
  - Full Connect/Disconnect cycle, duplicate-connect rejection
  - Publish QoS 0 (no ack), QoS 1 (PUBACK), QoS 2 (PUBREC→PUBREL→PUBCOMP)
  - Subscribe/Unsubscribe round-trip
  - Message delivery (publish → subscriber callback)
  - Auto-generated client ID, connection failure
- Internal unit tests:
  - `handlePublish` QoS 0, QoS 1 (PUBACK written), QoS 2 dedup
  - `deliverResponse` with and without pending channel
  - `SetOnError`/`logError` with and without callback
  - `readLoop` decode error → disconnect + drain pending

### 3. Auth Test Coverage (0% → 100%)
- **ChainAuth**: empty chain, single success, first-success fallthrough, all-fail, AddAuthenticator
- **FileAuth**: YAML/JSON auth file, client ID restriction, reload, Users() copy, nonexistent file, invalid YAML
- **NoopAuth**: basic authentication with credentials and empty

### 4. Config Validate Coverage (0% → 100%; config 54% → 79%)
- Test all validation rules: MaxPacketSize, MaxConnections, QoS settings (max retries, max inflight, retry interval), TLS cert/key, storage backend, Redis addr requirement
- Valid config acceptance test

### 5. CI Improvements
- Added Redis service container (`redis:7-alpine`) to test-unit and test-scripts jobs
- Set `MQTT_REDIS_ADDR=localhost:6379` to enable store/redis tests
- Raised coverage threshold: 45% → 55% (current: 57.8%)

## Coverage Summary

| Package | Before | After | Change |
|---------|--------|-------|--------|
| pkg/metrics | 2.0% | 98.0% | **+96%** |
| client | 29.0% | 84.9% | **+56%** |
| config | 54.3% | 79.0% | **+25%** |
| broker (auth) | 0% | 100% | **+100%** |
| **Overall** | **50.1%** | **57.8%** | **+7.7%** |

## Remaining Low-Coverage Areas

| Package | Coverage | Notes |
|---------|----------|-------|
| store/redis | 0% local | Tested by integration/CI with Redis |
| cmd/ | 0% | CLI entry point, not tested |
| protocols (encode/decode) | 59% | Deep broker internals |
| broker (core) | 57% | HandleConnection/readLoop via integration tests |
| API | 63% | |
| pkg/metrics/noop | 0%* | Empty function bodies, no statements |
| examples/ | 0% | Example code, not tested |
