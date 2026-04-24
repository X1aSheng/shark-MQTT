# Test Design Skill — Shark-MQTT

> This skill encodes the testing strategy and patterns from `doc/shark-mqtt architecture.md v1.0`

## Testing Philosophy

1. **Network/Business Separation**: Broker core is tested without network using mocks
2. **Layered Testing**: Each layer has unit tests before integration tests
3. **Protocol Correctness**: Codec tests verify binary encoding/decoding with real bytes
4. **QoS State Machine**: Table-driven tests for all QoS 1/2 state transitions

## Test Categories

### Unit Tests (per package)
- **errs/**: Error sentinel values, `Is()` / `As()` behavior
- **protocol/**: Round-trip encode/decode for every packet type; malformed input rejection
- **broker/**: Session lifecycle, subscription matching, expiration, auth, authorization
- **store/**: CRUD operations for each backend, consistency, concurrent access
- **broker/**: TopicTree matching, QoSEngine state transitions, will message triggers

### Integration Tests (`test/integration/`)
- `connect_test.go`: CONNECT/CONNACK flow with all reason codes
- `pubsub_test.go`: PUBLISH → SUBSCRIBE → message delivery
- `qos_test.go`: QoS 0/1/2 publish flows with ACK sequences
- `persistent_session_test.go`: Disconnect → reconnect, session restoration
- `will_test.go`: Last will triggered on ungraceful disconnect

### Benchmarks (`test/bench/`)
- `broker_bench_test.go`: End-to-end benchmarks (connect, publish QoS 0/1/2, concurrent, fan-out, payload sizes)
- `micro_bench_test.go`: Component micro-benchmarks (TopicTree, Codec, QoSEngine, Manager, BufferPool, MemoryStore)

## Test Patterns

### Table-Driven Tests (protocol)
```go
func TestCodecRoundTrip(t *testing.T) {
    tests := []struct {
        name string
        pkt  Packet
        want []byte  // expected binary
    }{
        {"CONNECT v3.1.1", connectPacket, []byte{...}},
        {"CONNACK accepted", connackPacket, []byte{...}},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // encode → compare binary
            // decode → deep-equal struct
        })
    }
}
```

### Mock net.Conn (testutils)
```go
type mockConn struct {
    readBuf  *bytes.Buffer
    writeBuf *bytes.Buffer
    closed   bool
}
```

### Integration Test Pattern
```go
func testBroker(t *testing.T) *api.Broker {
    cfg := config.DefaultConfig()
    cfg.ListenAddr = ":0" // random port
    broker := api.NewBroker(api.WithConfig(cfg), api.WithAuth(auth.AllowAllAuth{}))
    if err := broker.Start(); err != nil {
        t.Fatalf(...)
    }
    t.Cleanup(func() { broker.Stop() })
    return broker
}
```

## Coverage Targets
| Layer | Target |
|-------|--------|
| errs/ | 100% |
| protocol/ | 95%+ |
| broker/ | 90%+ |
| store/memory/ | 95%+ |
| pkg/ | 95%+ |
| plugin/ | 100% |
