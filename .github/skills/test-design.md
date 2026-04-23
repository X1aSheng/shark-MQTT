---
name: test-design
description: >
  Design and implement comprehensive test strategies for Go projects.
  Use when: writing unit tests, integration tests, table-driven tests,
  mock testing, coverage analysis, benchmark tests, or establishing test patterns.
---

# Test Design for Shark-MQTT

## Overview

Shark-MQTT is a high-performance MQTT broker in Go. The project follows clean
layered architecture with each component being independently testable.

## Test Categories

### 1. Table-Driven Tests (Preferred)

Use table-driven tests for encode/decode, validation, and state transitions.

```go
func TestConnectEncodeDecode(t *testing.T) {
	tests := []struct {
		name   string
		pkt    *ConnectPacket
		want   []byte
	}{
		{"minimal", &ConnectPacket{...}, []byte{...}},
		{"with will", &ConnectPacket{...}, []byte{...}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// test logic
		})
	}
}
```

### 2. Component Test Patterns

**Protocol layer** (`protocol/`):
- Round-trip encode/decode tests for all MQTT packet types
- Edge cases: malformed input, buffer boundaries, max sizes
- Property-based tests for field validation

**Server layer** (`server/`):
- Start/stop lifecycle tests
- Connection acceptance and rejection
- Handler integration tests with mock connections
- TLS handshake tests

**Session layer** (`session/`):
- Session creation and retrieval
- Subscription management with wildcard topics
- Inflight message tracking
- Session expiration

**Auth layer** (`auth/`):
- Static credential verification
- AllowAll/DenyAll test doubles
- ACL topic matching

**Broker** (`infra/broker/`):
- Full CONNECT handshake
- Client registration and deregistration
- Session persistence

### 3. Test Organization

- Tests live alongside source files as `*_test.go`
- Integration tests go in `integration/` directory
- Use build tags for slow tests: `//go:build integration`

### 4. Coverage Goals

- Protocol layer: 100% line coverage (critical path)
- Server layer: 90%+ line coverage
- Session/Broker: 80%+ line coverage
- Store implementations: 90%+ line coverage

### 5. Testing Utilities

Common patterns in the codebase:
- Temporary file helpers for persistence tests
- Mock connections using `net.Pipe()`
- Test timeout enforcement (`-timeout` flag)
- `-race` flag for data race detection
- `-count=1` to disable test caching during development

### 6. MQTT-Specific Test Cases

- CONNECT/CONNACK flow (all return codes)
- PUBLISH QoS 0, 1, 2 round-trips
- SUBSCRIBE/UNSUBSCRIBE with all topic wildcards (#, +)
- Retained message delivery
- Will message on disconnect
- Keep-alive timeout
- Multiple clients, publish/subscribe fan-out

## Running Tests

```bash
# All packages
go test ./... -v -count=1 -timeout 60s

# With race detection
go test ./... -race -count=1 -timeout 120s

# Coverage
go test ./... -coverprofile=cover.out
go tool cover -html=cover.out

# Single package
go test ./protocol/... -v -run TestConnect
```
