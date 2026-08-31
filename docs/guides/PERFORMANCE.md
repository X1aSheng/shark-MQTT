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

## Latest Reference Run

Latest full benchmark run: `tests/logs/20260506_123128_benchmark.log` on AMD Ryzen 7 8845HS / Windows 11 / Go 1.26.1.

| Benchmark | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| `BenchmarkConnectionEstablish` | 305,069 | 4,079 | 65 |
| `BenchmarkMQTTConnect` | 407,551 | 6,227 | 123 |
| `BenchmarkPublishQos0` | 23,973 | 1,760 | 27 |
| `BenchmarkPublishQos1` | 74,106 | 1,948 | 37 |
| `BenchmarkPublishQos2` | 200,507 | 2,548 | 52 |
| `BenchmarkConcurrentPublish` | 43,460 | 1,717 | 26 |
| `BenchmarkE2E_QoS0_DataVerify` | 40,963 | 904 | 36 |
| `BenchmarkE2E_QoS1_DataVerify` | 124,036 | 1,357 | 54 |
| `BenchmarkE2E_QoS2_DataVerify` | 230,388 | 2,415 | 84 |
| `BenchmarkTopicTree_Subscribe` | 131.7 | 51 | 0 |
| `BenchmarkTopicTree_Match_Exact` | 243.9 | 88 | 2 |
| `BenchmarkCodec_EncodePublish` | 336.0 | 422 | 6 |
| `BenchmarkCodec_DecodePublish` | 536.2 | 736 | 10 |
| `BenchmarkQoSEngine_TrackQoS1` | 19.2 | 0 | 0 |
| `BenchmarkBufferPool_GetPut` | 29.8 | 24 | 1 |

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
| `BenchmarkPayload_*` | Publish with varying payload sizes (64B-128KB) |
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

Windows note: `BenchmarkConnectionEstablish`, `BenchmarkMQTTConnect`, and `BenchmarkE2E_WillMessage` are skipped on Windows because they intentionally churn short-lived TCP connections or abnormal disconnects and can exhaust ephemeral ports for later benchmarks in the same package process. Publish, fanout, E2E QoS/retained/wildcard/payload data verification, codec, QoS engine, buffer pool, and store benchmarks still run.

---

## Profiling

### CPU Profiling

```bash
make bench-cpu
go tool pprof cpu.prof
```

Common pprof commands:
- `top10` - Show top 10 functions by CPU time
- `web` - Generate call graph visualization
- `list FuncName` - Show per-line CPU usage for a function
- `png` - Export call graph as PNG

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

These are indicative values from the latest Ryzen 7 8845HS run, not targets:

**End-to-End:**
- QoS 0 publish: ~24 us/op, 27 allocs
- QoS 1 publish: ~74 us/op, 37 allocs
- QoS 2 publish: ~201 us/op, 52 allocs
- MQTT CONNECT: ~408 us/op, 123 allocs
- Persistent session round-trip: ~820 us/op, 179 allocs

**Micro-components:**
- TopicTree.Subscribe: ~132 ns/op, 0 allocs
- TopicTree.Match (exact): ~244 ns/op, 2 allocs
- TopicTree.Match (wildcard +): ~354 ns/op, 3 allocs
- Codec.Encode (PUBLISH): ~336 ns/op, 6 allocs
- Codec.Decode (PUBLISH): ~536 ns/op, 10 allocs
- QoSEngine.TrackQoS1: ~19 ns/op, 0 allocs
- Manager.GetSession: ~9 ns/op, 0 allocs
- BufferPool.Get/Put: ~30 ns/op, 1 alloc

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

## Deployment Runtime Tuning

Recommendations for tuning the broker for production workloads. All knobs are
optional; defaults are sensible for general use.

### Go runtime

- **`GOMEMLIMIT`** — set a soft memory limit so the GC stays ahead of the heap
  instead of reacting to it. Recommended: 80% of the container/instance RAM,
  e.g. `GOMEMLIMIT=8GiB` for a 10GiB heap budget. Improves latency
  predictability under load.
- **`GOGC`** — the default `100` (GC when heap doubles) is a good throughput
  choice. For lower tail latency, raise it (e.g. `200`) to GC less often and
  let `GOMEMLIMIT` bound the heap.
- **`GOMAXPROCS`** — leave unset; Go uses all CPUs by default. For noisy
  neighbours in shared VMs you may cap it, but measure first.

### Broker options

| Knob | Default | Guidance |
| --- | --- | --- |
| `write_queue_size` | `256` | Larger absorbs bursts to slow subscribers but uses more memory per connection; smaller increases backpressure (drops for QoS 0). |
| `qos_max_inflight` | `100` | Per-client QoS 1/2 in-flight cap. Raise for clients that expect deep pipelining; lower to bound memory. |
| `latency_sampling` (`WithLatencySampling`) | `1` | 0 disables latency histogram observation (cheapest); N observes 1 in N. Reduces per-message overhead under Prometheus metrics. |
| `sys_interval` | `30s` | `$SYS` status publish interval; 0 disables. |
| `retained_expiry` | `0` | TTL for retained messages; set for bounded retained storage. |

### Authentication

- **bcrypt cost** — `StaticAuth.SetBcryptCost(N)` controls the work factor for
  `SetHashedPassword`. The default `10` (~50–100ms CPU per login) is fine for
  moderate connection rates; lower it (e.g. `4`) for very high connect rates,
  accepting weaker hash resistance. Verification cost is embedded in the hash.

### Measuring

Benchmark with `make bench` and compare changes with `benchstat`. The
`tests/bench` suite covers E2E QoS 0/1/2, fan-out, payload sizes, and
micro-benchmarks for the codec, topic tree, QoS engine, and session manager.

---

## See Also

- [Testing Guide](TESTING.md)
- [Development Guide](DEVELOPMENT.md)
- [Configuration Guide](CONFIGURATION.md)




