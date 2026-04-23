# Testing Guide

This guide explains the testing strategy and how to write effective tests for Shark-MQTT.

---

## Table of Contents

- [Testing Architecture](#testing-architecture)
- [Test Types](#test-types)
- [Running Tests](#running-tests)
- [Writing Tests](#writing-tests)
- [Test Utilities](#test-utilities)
- [Coverage Requirements](#coverage-requirements)

---

## Testing Architecture

Shark-MQTT uses a layered testing approach:

```
┌────────────────────────────────────────┐
│           E2E Tests                    │
│  (MQTT compatibility, chaos tests)       │
│  → Rare, expensive                     │
├────────────────────────────────────────┤
│           Integration Tests            │
│  (End-to-end MQTT workflows)           │
│  → test/integration/                   │
│  → Run with: -tags=integration         │
├────────────────────────────────────────┤
│           Unit Tests                   │
│  (Component isolation)                 │
│  → Each package's *_test.go files      │
├────────────────────────────────────────┤
│           Benchmark Tests              │
│  (Performance validation)              │
│  → test/bench/                         │
└────────────────────────────────────────┘
```

---

## Test Types

### Unit Tests

Unit tests isolate individual components and mock their dependencies.

**Location**: Within each package (`*_test.go`)

**Purpose**: Test component logic in isolation

**Example**:
```go
func TestTopicTree_Subscribe(t *testing.T) {
    tree := NewTopicTree()
    tree.Subscribe("home/temp", "client1", 1)
    
    subs := tree.Match("home/temp")
    if len(subs) != 1 {
        t.Fatalf("expected 1 subscription, got %d", len(subs))
    }
}
```

### Integration Tests

Integration tests use real TCP connections and verify end-to-end workflows.

**Location**: `test/integration/`

**Purpose**: Test complete MQTT workflows

**Run**:
```bash
go test -race -tags=integration -v ./test/integration/...
```

**Example**:
```go
// +build integration

func TestPublishSubscribe(t *testing.T) {
    broker := testBroker(t)
    
    conn := dialTestBroker(t, broker)
    connectClient(t, conn, protocol.NewCodec(65536), "pubsub-client")
    
    // Subscribe
    subscribe(t, conn, "test/topic", 1)
    
    // Publish and verify delivery
    publish(t, conn, "test/topic", []byte("hello"), 1)
    
    // Verify message received
    // ... verification logic
}
```

### Benchmark Tests

Benchmark tests measure performance characteristics.

**Location**: `test/bench/`

**Purpose**: Performance validation and regression detection

**Run**:
```bash
go test -bench=. -benchmem -benchtime=10s ./test/bench/...
```

**Example**:
```go
func BenchmarkPublishQos0(b *testing.B) {
    // Setup
    broker := api.NewBroker(api.WithAddr(":0"))
    broker.Start()
    defer broker.Stop()
    
    conn := dialBroker(b, broker)
    codec := protocol.NewCodec(65536)
    
    // Reset timer to exclude setup
    b.ResetTimer()
    
    for i := 0; i < b.N; i++ {
        publish(conn, codec, "bench/topic", payload, 0)
    }
}
```

---

## Running Tests

### Quick Commands

```bash
# All unit tests
go test ./...

# Unit tests with race detection
go test -race ./...

# All tests including integration
go test -race -tags=integration ./...

# Specific package
go test -v ./broker/...

# Coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html

# Benchmarks
go test -bench=. -benchmem -benchtime=10s ./test/bench/...

# Redis store tests (requires Redis running)
MQTT_REDIS_ADDR=localhost:6379 go test ./store/redis/...
```

### Using Make

```bash
# Run all tests (unit + integration)
make test

# Unit tests only
make test-unit

# Integration tests
make test-integration

# With race detection
make test-race

# Benchmarks
make test-bench

# Coverage
make test-coverage
```

---

## Writing Tests

### Unit Test Patterns

**Table-Driven Tests** (preferred):
```go
func TestQoSEngine_TrackQoS1(t *testing.T) {
    tests := []struct {
        name      string
        clientID  string
        packetID  uint16
        topic     string
        payload   []byte
        wantError bool
    }{
        {
            name:     "valid QoS 1 message",
            clientID: "client1",
            packetID: 1,
            topic:    "test/topic",
            payload:  []byte("hello"),
        },
        {
            name:     "duplicate packet ID",
            clientID: "client1",
            packetID: 1,  // Same as above
            topic:    "test/another",
            payload:  []byte("world"),
            // Implementation dependent behavior
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            qe := NewQoSEngine()
            err := qe.TrackQoS1(tt.clientID, tt.packetID, tt.topic, tt.payload, false)
            if (err != nil) != tt.wantError {
                t.Errorf("TrackQoS1() error = %v, wantError %v", err, tt.wantError)
            }
        })
    }
}
```

**Subtests for Setup/Teardown**:
```go
func TestSessionManager(t *testing.T) {
    sm := NewManager(nil)
    
    t.Run("Create", func(t *testing.T) {
        sess := sm.CreateSession("client1", connectPkt, false)
        if sess == nil {
            t.Fatal("expected session, got nil")
        }
    })
    
    t.Run("Get", func(t *testing.T) {
        sess, ok := sm.GetSession("client1")
        if !ok {
            t.Fatal("expected session to exist")
        }
        if sess.ClientID != "client1" {
            t.Errorf("expected client1, got %s", sess.ClientID)
        }
    })
    
    t.Run("Remove", func(t *testing.T) {
        sm.RemoveSession("client1")
        if sm.SessionExists("client1") {
            t.Error("expected session to be removed")
        }
    })
}
```

### Integration Test Patterns

**Use the testBroker helper**:
```go
func testBroker(t *testing.T) *api.Broker {
    t.Helper()
    broker := api.NewBroker(
        api.WithAddr(":0"),
        api.WithAuth(auth.AllowAllAuth{}),
    )
    if err := broker.Start(); err != nil {
        t.Fatalf("failed to start broker: %v", err)
    }
    t.Cleanup(func() {
        broker.Stop()
    })
    return broker
}
```

**Network setup pattern**:
```go
func dialTestBroker(t *testing.T, broker *api.Broker) net.Conn {
    t.Helper()
    
    addr := broker.Addr()
    if addr == "" {
        t.Fatal("broker has no address")
    }
    
    conn, err := net.Dial("tcp", addr)
    if err != nil {
        t.Fatalf("failed to dial broker: %v", err)
    }
    
    t.Cleanup(func() {
        conn.Close()
    })
    
    return conn
}
```

**Full workflow example**:
```go
func TestQoS1MessageDelivery(t *testing.T) {
    // 1. Setup broker
    broker := testBroker(t)
    
    // 2. Create subscriber
    subConn := dialTestBroker(t, broker)
    subCodec := protocol.NewCodec(65536)
    connectClient(t, subConn, subCodec, "subscriber")
    subscribe(t, subConn, subCodec, "test/qos1", 1)
    
    // 3. Create publisher
    pubConn := dialTestBroker(t, broker)
    pubCodec := protocol.NewCodec(65536)
    connectClient(t, pubConn, pubCodec, "publisher")
    
    // 4. Publish QoS 1 message
    packetID := uint16(1)
    publishQoS1(t, pubConn, pubCodec, "test/qos1", []byte("hello"), packetID)
    
    // 5. Verify publisher receives PUBACK
    ack := readPacket(t, pubConn, pubCodec)
    if ack.Type() != PacketTypePubAck {
        t.Fatalf("expected PUBACK, got %v", ack.Type())
    }
    
    // 6. Verify subscriber receives message
    msg := readPacket(t, subConn, subCodec)
    pubMsg, ok := msg.(*PublishPacket)
    if !ok {
        t.Fatalf("expected Publish packet, got %T", msg)
    }
    if string(pubMsg.Payload) != "hello" {
        t.Errorf("expected 'hello', got %s", string(pubMsg.Payload))
    }
    
    // 7. Send PUBACK from subscriber
    sendPubAck(t, subConn, subCodec, pubMsg.PacketID)
}
```

### Benchmark Test Patterns

**Include memory stats**:
```go
func BenchmarkPublishQos1(b *testing.B) {
    // Setup (not measured)
    broker := api.NewBroker(api.WithAddr(":0"))
    broker.Start()
    defer broker.Stop()
    
    conn := dialBroker(b, broker)
    codec := protocol.NewCodec(65536)
    connectClient(b, conn, codec, "bench-client")
    
    payload := make([]byte, 1024)
    
    b.ResetTimer()
    b.ReportAllocs()
    
    for i := 0; i < b.N; i++ {
        packetID := uint16(i%65535 + 1)
        publishQoS1(b, conn, codec, "bench/topic", payload, packetID)
    }
    
    b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "ops/sec")
}
```

**Parallel benchmarks**:
```go
func BenchmarkConcurrentPublish(b *testing.B) {
    broker := api.NewBroker(api.WithAddr(":0"))
    broker.Start()
    defer broker.Stop()
    
    payload := make([]byte, 256)
    
    b.ResetTimer()
    
    b.RunParallel(func(pb *testing.PB) {
        conn := dialBroker(b, broker)
        codec := protocol.NewCodec(65536)
        connectClient(b, conn, codec, fmt.Sprintf("client-%d", rand.Int()))
        
        for pb.Next() {
            publish(conn, codec, "bench/topic", payload, 0)
        }
    })
}
```

---

## Test Utilities

### Mock Objects

`testutils/mock_*.go` provides reusable mocks:

```go
// Mock connection
type MockConn struct {
    ReadBuf  bytes.Buffer
    WriteBuf bytes.Buffer
    Closed   bool
}

// Mock store
type MockSessionStore struct {
    Sessions map[string]*store.SessionData
}

// Mock authenticator
type MockAuth struct {
    Allowed bool
}
```

### Benchmark Utilities

`testutils/bench.go` provides benchmark helpers:

```go
// LatencyRecorder collects latency measurements
recorder := testutils.NewLatencyRecorder()
start := time.Now()
// ... operation ...
recorder.Record(time.Since(start))

// Get results
result := recorder.Result(duration)
fmt.Println(result.String())
// Output: 10000 ops in 1.234s (8103 ops/sec) - avg: 123µs, p50: 100µs, p95: 250µs, p99: 500µs
```

---

## Coverage Requirements

### Minimum Coverage

| Module | Minimum Coverage |
|--------|-----------------|
| protocol/ | 80% |
| broker/ | 75% |
| session/ | 75% |
| store/memory/ | 80% |
| auth/ | 70% |
| Overall | 60% |

### Exclusions

The following are excluded from coverage:
- `test/` - Test code
- `examples/` - Example applications
- `cmd/` - Command-line tools
- `integration/` - Integration tests

### Generating Coverage

```bash
# Generate coverage report
go test -coverprofile=coverage.out -covermode=atomic ./...

# View in browser
go tool cover -html=coverage.out

# Text summary
go tool cover -func=coverage.out

# Check threshold (60%)
./scripts/check-coverage.sh
```

---

## Best Practices

1. **Use t.Helper()** in test helper functions for better error locations
2. **Use t.Cleanup()** for resource cleanup instead of defer in loops
3. **Use t.Parallel()** for independent tests to speed up execution
4. **Table-driven tests** are preferred for multiple test cases
5. **Mock external dependencies** in unit tests (network, storage)
6. **Use real connections** in integration tests
7. **Reset timers** in benchmarks after setup
8. **Report allocations** in benchmarks with b.ReportAllocs()

---

## See Also

- [Development Guide](development.md)
- [Configuration Guide](configuration.md)
- [CONTRIBUTING.md](../CONTRIBUTING.md)