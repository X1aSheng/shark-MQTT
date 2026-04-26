# Performance Testing Guide

This guide covers how to run, analyze, and interpret Shark-MQTT performance benchmarks.

---

## Quick Start

```bash
# Quick benchmark (recommended for development)
make bench-quick

# Full benchmark suite (5s per test, 3 iterations)
make bench

# Run a specific benchmark
go test -bench=BenchmarkPublishQos0 -benchmem ./tests/bench/...
```

---

## Benchmark Categories

### End-to-End Benchmarks

Located in `tests/bench/broker_bench_test.go`. These tests start a real broker on a random port and measure actual network throughput.

| Benchmark | Description |
|-----------|-------------|
| `BenchmarkConnectionEstablish` | Raw TCP connection/disconnect |
| `BenchmarkMQTTConnect` | Full MQTT CONNECT/CONNACK handshake |
| `BenchmarkPublishQos0` | QoS 0 publish with one subscriber |
| `BenchmarkPublishQos1` | QoS 1 publish + PUBACK round-trip |
| `BenchmarkPublishQos2` | QoS 2 full handshake (PUBREC/PUBREL/PUBCOMP) |
| `BenchmarkConcurrentPublish` | Parallel publishers with shared subscriber |
| `BenchmarkTopicWildcardMatch` | Single-level wildcard (+) topic matching |
| `BenchmarkPersistentSession` | CleanSession=false reconnect cycle |
| `BenchmarkPayload_*` | Publish with varying payload sizes (64B–128KB) |
| `BenchmarkFanOut_*` | 1 publisher to N subscribers (1/5/10/50) |

### Micro-Benchmarks

Located in `tests/bench/micro_bench_test.go`. These benchmark individual components in isolation.

| Benchmark | Component | What it measures |
|-----------|-----------|------------------|
| `BenchmarkTopicTree_Subscribe` | broker.TopicTree | Subscription insertion |
| `BenchmarkTopicTree_Match_*` | broker.TopicTree | Topic matching (exact/+/+) |
| `BenchmarkTopicTree_Unsubscribe` | broker.TopicTree | Subscription removal |
| `BenchmarkCodec_Encode*` | protocol.Codec | Packet encoding |
| `BenchmarkCodec_Decode*` | protocol.Codec | Packet decoding |
| `BenchmarkCodec_RoundTrip*` | protocol.Codec | Encode + decode combined |
| `BenchmarkQoSEngine_Track*` | broker.QoSEngine | Inflight message tracking |
| `BenchmarkQoSEngine_TrackAck*` | broker.QoSEngine | Track + ack cycle |
| `BenchmarkManager_*` | broker.Manager | Session CRUD operations |
| `BenchmarkBufferPool_*` | pkg/bufferpool | Pool vs raw allocation |
| `BenchmarkMemoryStore_*` | store/memory | In-memory store operations |

---

## Running Benchmarks

### Basic Commands

```bash
# All benchmarks with memory allocation stats
go test -bench=. -benchmem ./tests/bench/...

# Specific benchmark
go test -bench=BenchmarkPublishQos1 -benchmem ./tests/bench/...

# Longer run for more stable results
go test -bench=. -benchmem -benchtime=10s ./tests/bench/...

# Multiple iterations
go test -bench=. -benchmem -benchtime=5s -count=5 ./tests/bench/...
```

### Makefile Targets

```bash
make bench          # Full suite: 5s x 3 iterations
make bench-quick    # Quick: 1s x 1 iteration
make bench-cpu      # CPU profiling
make bench-mem      # Memory profiling
make bench-profile  # Both CPU + Memory profiling
```

---

## Profiling

### CPU Profiling

```bash
make bench-cpu
go tool pprof cpu.prof
```

Common pprof commands:
- `top10` — Show top 10 functions by CPU time
- `web` — Generate call graph visualization
- `list FuncName` — Show per-line CPU usage for a function
- `png` — Export call graph as PNG

### Memory Profiling

```bash
make bench-mem
go tool pprof mem.prof
```

### Trace

```bash
go test -bench=BenchmarkConcurrentPublish -benchtime=3s \
    -trace=trace.out ./tests/bench/...
go tool trace trace.out
```

---

## Interpreting Results

### Output Format

```
BenchmarkPublishQos0-16    10000    21387 ns/op    1342 B/op    20 allocs/op
```

| Column | Meaning |
|--------|---------|
| `10000` | Iterations completed |
| `21387 ns/op` | Nanoseconds per operation |
| `1342 B/op` | Bytes allocated per operation |
| `20 allocs/op` | Heap allocations per operation |

### Performance Reference (on development machine)

These are indicative values from a Ryzen 7 8845HS, not targets:

**End-to-End:**
- QoS 0 publish: ~21 µs/op, 20 allocs
- QoS 1 publish: ~70 µs/op, 31 allocs
- QoS 2 publish: ~118 µs/op, 46 allocs
- MQTT CONNECT: ~345 µs/op, 107 allocs
- Persistent session round-trip: ~420 µs/op, 156 allocs

**Micro-components:**
- TopicTree.Subscribe: ~100 ns/op, 0 allocs
- TopicTree.Match (exact): ~198 ns/op, 2 allocs
- TopicTree.Match (wildcard +): ~300 ns/op, 3 allocs
- Codec.Encode (PUBLISH): ~189 ns/op, 6 allocs
- Codec.Decode (PUBLISH): ~220 ns/op, 8 allocs
- QoSEngine.TrackQoS1: ~91 ns/op, 1 alloc
- Manager.GetSession: ~8.5 ns/op, 0 allocs
- BufferPool.Get/Put: ~26 ns/op, 1 alloc

---

## Writing New Benchmarks

### Pattern for E2E Benchmarks

```go
func BenchmarkXxx(b *testing.B) {
    brk := setupBroker(b)
    defer brk.Stop()

    // Setup subscribers with drain goroutines
    subConn, subCodec := connectedClient(b, brk, "sub")
    defer subConn.Close()
    subscribeTopic(b, subConn, subCodec, "topic", 0)
    stop := drainConn(subConn)
    defer stop()

    pubConn, pubCodec := connectedClient(b, brk, "pub")
    defer pubConn.Close()

    b.ResetTimer()
    b.ReportAllocs()

    for i := 0; i < b.N; i++ {
        // ... benchmark logic
    }
}
```

### Pattern for Micro-Benchmarks

```go
func BenchmarkComponent(b *testing.B) {
    // Setup component (outside timer)
    comp := NewComponent()

    b.ResetTimer()
    b.ReportAllocs()

    for i := 0; i < b.N; i++ {
        comp.DoSomething()
    }
}
```

### Guidelines

1. Always call `b.ResetTimer()` after setup, before the hot loop
2. Always call `b.ReportAllocs()` to track memory allocations
3. For subscriber benchmarks, use `drainConn()` to prevent buffer overflow
4. Use unique ClientIDs to avoid session conflicts
5. Use `PacketID >= 100` for subscribe packets to avoid collision with publish IDs
6. For parallel benchmarks, use `b.RunParallel()`

---

## Continuous Performance Monitoring

### Comparing Runs

```bash
# Save baseline
go test -bench=. -benchmem -count=5 ./tests/bench/... > old.txt

# After changes
go test -bench=. -benchmem -count=5 ./tests/bench/... > new.txt

# Compare (requires benchstat)
go install golang.org/x/perf/cmd/benchstat@latest
benchstat old.txt new.txt
```

### CI Integration

Add to your CI pipeline:

```yaml
- name: Benchmark
  run: |
    go test -bench=. -benchmem -benchtime=1s -count=3 ./tests/bench/... | tee bench-results.txt
```

---

## See Also

- [Testing Guide](testing.md)
- [Development Guide](development.md)
- [Configuration Guide](configuration.md)
