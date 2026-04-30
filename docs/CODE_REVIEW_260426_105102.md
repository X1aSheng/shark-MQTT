# Shark-MQTT Improvement Roadmap

> Generated: 2026-04-26
> Reviewer: Go Expert Audit
> Status: Active

---

## Overview

This document tracks all identified defects, shortcomings, and planned improvements for the shark-mqtt project, organized by severity and priority.

---

## Severity Definitions

| Level | Meaning |
|-------|---------|
| **Critical** | Data loss, data corruption, or protocol violation under normal operation |
| **High** | Incorrect behavior in common scenarios, potential message loss |
| **Medium** | Usability or reliability gap, may cause issues in production |
| **Low** | Nice-to-have improvement, code quality or DevOps gap |

---

## Critical Issues

### C1. Badger Store ID Generation Race Condition

- **File**: `store/badger/message_store.go:145-147`
- **Impact**: Duplicate message IDs under concurrent writes, causing silent data overwrite
- **Current Code**:
  ```go
  func atomicAddUint64(addr *uint64, delta uint64) uint64 {
      return *addr + delta // NOT atomic!
  }
  ```
- **Fix**: Replace with `sync/atomic.AddUint64`

### C2. MQTT 5.0 PUBLISH Properties Not Decoded

- **File**: `protocol/publish.go:22-29`
- **Impact**: MQTT 5.0 client properties (Message Expiry, Topic Alias, etc.) are silently discarded on incoming PUBLISH packets
- **Current Code**: Properties block is a no-op comment
- **Fix**: Parse properties using `c.decodeProperties(r)` before reading payload, accounting for variable-length header bytes

### C3. MQTT 5.0 SUBSCRIBE Properties Not Parsed

- **File**: `protocol/subscribe.go:14-17`
- **Impact**: Subscription Identifier and other MQTT 5.0 SUBSCRIBE properties are completely skipped
- **Current Code**: `// Simplified: skip properties for now`
- **Fix**: Parse properties using `c.decodeProperties(r)` after reading packet ID, adjusting byte count

---

## High Issues

### H1. QoS Engine Ignores Send Errors

- **File**: `broker/qos_engine.go:219, 231, 308, 314, 318`
- **Impact**: Failed PUBREL/PUBACK/PUBCOMP sends are silently ignored. Inflight messages marked as acked but never delivered, causing message loss
- **Fix**: Log errors and keep messages in inflight for retry when send fails

### H2. No Topic Filter Validation

- **File**: `broker/topic_tree.go:32` (Subscribe entry point)
- **Impact**: Invalid patterns like `a/#/b`, `#abc`, `+foo` are accepted, causing unpredictable matching behavior. Violates MQTT spec.
- **Fix**: Add `ValidateTopicFilter()` function checking:
  - `#` must be last character and preceded by `/` or be the first character
  - `+` must occupy an entire level (bounded by `/` or start/end)
  - No null characters

### H3. Concurrent Write to Same Connection

- **File**: `broker/broker.go:459-469` (`writePacketTo`)
- **Impact**: Multiple goroutines (publish delivery + QoS retry) can write to the same `net.Conn` simultaneously, corrupting MQTT frames
- **Fix**: Add `sync.Mutex` to `clientState` for write serialization

### H4. No Graceful Shutdown for In-flight Messages

- **File**: `broker/broker.go:185-189` (`Stop`)
- **Impact**: Broker stop immediately kills QoS engine, losing all in-flight QoS 1/2 messages
- **Fix**: Drain inflight messages with timeout before stopping QoS engine

### H5. No Connection Limit Enforcement

- **File**: `broker/broker.go:102` (`HandleConnection`)
- **Impact**: No upper bound on concurrent connections; a single malicious client can exhaust file descriptors
- **Fix**: Check active connection count against `MaxConnections` before accepting

---

## Medium Issues

### M1. go.mod Version Invalid

- **File**: `go.mod:3`
- **Current**: `go 1.26.1` (does not exist)
- **Fix**: Set to actual Go toolchain version (e.g., `go 1.23`)

### M2. Config Validation Missing

- **File**: `config/config.go`
- **Impact**: Invalid port numbers, negative timeouts, empty paths accepted without error
- **Fix**: Add `Validate() error` method to `Config` struct

### M3. Will Delay Interval Not Implemented

- **File**: `broker/will_handler.go`
- **Impact**: MQTT 5.0 Will Delay Interval property is parsed but never applied; will messages always fire immediately
- **Fix**: Use timer-based delayed trigger in WillHandler

---

## Low Issues

### L1. No Health Check Endpoint

- **Impact**: No `/healthz` endpoint for Kubernetes probes or load balancer checks
- **Fix**: Add simple HTTP health endpoint to `api/` package

### L2. No Signal Handling for Graceful Shutdown

- **Impact**: Process killed without draining connections
- **Fix**: Capture SIGTERM/SIGINT in main, call `broker.Stop()`

### L3. No CI Pipeline

- **Impact**: No automated testing, linting, or security scanning
- **Fix**: Add `.github/workflows/ci.yml` with `go test`, `golangci-lint`, `go vet`

### L4. Missing Edge-case Integration Tests

- **Impact**: Auth failure, ACL rejection, Keep-Alive timeout, max connections not tested
- **Fix**: Add integration tests for these scenarios

---

## Implementation Priority

| Phase | Items | Description |
|-------|-------|-------------|
| **Phase 1** | C1, C2, C3 | Fix data correctness issues |
| **Phase 2** | H1, H2, H3 | Fix reliability and concurrency issues |
| **Phase 3** | H4, H5, M1, M2 | Fix production readiness gaps |
| **Phase 4** | M3, L1, L2, L3, L4 | Polish and DevOps improvements |

---

## Progress Tracker

| ID | Severity | Description | Status |
|----|----------|-------------|--------|
| C1 | Critical | Badger Store race condition | **Done** |
| C2 | Critical | PUBLISH properties decoding | **Done** |
| C3 | Critical | SUBSCRIBE properties parsing | **Done** |
| H1 | High | QoS send error handling | **Done** |
| H2 | High | Topic filter validation | **Done** |
| H3 | High | Per-client write mutex | **Done** |
| H4 | High | Graceful shutdown drain | **Done** |
| H5 | High | Connection limit | **Done** |
| M1 | Medium | go.mod version | Verified (1.26.1 is correct) |
| M2 | Medium | Config validation | **Done** |
| M3 | Medium | Will delay interval | **Done** |
| L1 | Low | Health check endpoint | **Done** |
| L2 | Low | Signal handling | N/A (handled by caller) |
| L3 | Low | CI pipeline | **Updated** (go version matrix) |
| L4 | Low | Edge-case tests | **Done** (5 new tests) |
