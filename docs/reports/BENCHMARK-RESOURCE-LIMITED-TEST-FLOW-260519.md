# Shark-MQTT Resource-Limited Benchmark Test Flow

适用目标：在 **2 vCPU / 2GB RAM / no swap** 云服务器上做可控 benchmark，避免一次性全量压测导致 SSH、Docker、K8s 或系统 OOM。该流程也适用于本机先建立基线，再把云端作为受限资源回归环境。

## Goals

- 验证当前提交在受限资源下的性能趋势和稳定性。
- 通过分组、阶梯、资源门控逐步扩大测试强度。
- 发现资源拐点后停止升级，而不是追求极限吞吐。
- 云端只跑轻量和中等强度 benchmark；完整压力测试放到 CI 或更大机器。

## Non-Goals

- 不在 2GB 云机上跑 kind/k3s/K8s 控制面 benchmark。
- 不在云机上跑 `go test -race` benchmark。
- 不同时运行 Docker 容器、K8s 集群、全量 benchmark。
- 不把连接 churn、大 payload、Redis benchmark 混进第一阶段。

## Benchmark Groups

| Group | Regex | Cost | Purpose |
|-------|-------|------|---------|
| `micro` | `BenchmarkTopicTree|BenchmarkCodec|BenchmarkQoSEngine|BenchmarkManager|BenchmarkBufferPool|BenchmarkMemoryStore` | Low | 组件级 CPU/alloc 基线 |
| `publish` | `BenchmarkPublishQos0|BenchmarkPublishQos1|BenchmarkPublishQos2|BenchmarkConcurrentPublish|BenchmarkFanOut_1Sub|BenchmarkFanOut_5Subs|BenchmarkFanOut_10Subs` | Medium | 单 broker 发布/扇出链路 |
| `payload-small` | `BenchmarkPayload_64B|BenchmarkPayload_256B|BenchmarkPayload_1KB|BenchmarkPayload_4KB` | Medium | 小负载稳定性 |
| `payload-large` | `BenchmarkPayload_16KB|BenchmarkPayload_128KB|BenchmarkE2E_Payload_64KB` | High | 大负载内存压力，云端谨慎跑 |
| `e2e-basic` | `BenchmarkE2E_QoS0_DataVerify|BenchmarkE2E_QoS1_DataVerify|BenchmarkE2E_RetainedMessage|BenchmarkE2E_WildcardDelivery|BenchmarkE2E_Payload_String|BenchmarkE2E_Payload_Binary|BenchmarkE2E_Payload_Empty` | Medium | 端到端数据验证 |
| `e2e-heavy` | `BenchmarkE2E_QoS2_DataVerify|BenchmarkE2E_FanOut_VerifyAll|BenchmarkE2E_WillMessage|BenchmarkE2E_Payload_Unicode` | High | QoS2、fanout、will、Unicode |
| `connection` | `BenchmarkConnectionEstablish|BenchmarkMQTTConnect` | High | 连接 churn；Windows 跳过，云端最后单独跑 |
| `redis` | `./store/redis -bench=.` | External | 只在 Redis 明确可用时跑 |

## Stages

| Stage | Benchtime | Count | Cloud? | Stop If |
|-------|-----------|-------|--------|---------|
| `smoke` | `100ms` | 1 | Yes | Any failure |
| `light` | `300ms` | 1 | Yes | MemAvailable < 768MB, load1 > 2.5 |
| `medium` | `1s` | 1 | Yes, selected groups only | MemAvailable < 1GB, load1 > 2.0 |
| `stress` | `3s` | 2 | No on 2GB cloud | Any resource warning |
| `full` | `5s` | 3 | No on 2GB cloud | CI / large host only |

## Resource Gates

Before every cloud benchmark group:

```bash
avail_mb=$(awk '/MemAvailable/ {print int($2/1024)}' /proc/meminfo)
load1=$(awk '{print $1}' /proc/loadavg)
echo "MemAvailable=${avail_mb}MB Load1=${load1}"
```

Stop or do not upgrade when:

- `MemAvailable < 768MB` before smoke/light.
- `MemAvailable < 1024MB` before medium.
- `load1 > 2.5` before smoke/light.
- `load1 > 2.0` before medium.
- SSH latency or command startup becomes slow.
- Any command is killed, times out, or reports OOM.

Use timeouts:

- Smoke/light group timeout: `120s`
- Medium group timeout: `180s`
- Heavy group timeout: `240s`

## Local Execution Plan

Run from project root.

```bash
go test -run=^$ -bench='BenchmarkTopicTree|BenchmarkCodec|BenchmarkQoSEngine|BenchmarkManager|BenchmarkBufferPool|BenchmarkMemoryStore' -benchmem -benchtime=100ms -count=1 -timeout=120s ./tests/bench/...

go test -run=^$ -bench='BenchmarkTopicTree|BenchmarkCodec|BenchmarkQoSEngine|BenchmarkManager|BenchmarkBufferPool|BenchmarkMemoryStore' -benchmem -benchtime=300ms -count=1 -timeout=120s ./tests/bench/...

go test -run=^$ -bench='BenchmarkPublishQos0|BenchmarkPublishQos1|BenchmarkPublishQos2|BenchmarkConcurrentPublish|BenchmarkFanOut_1Sub|BenchmarkFanOut_5Subs|BenchmarkFanOut_10Subs' -benchmem -benchtime=300ms -count=1 -timeout=180s ./tests/bench/...

go test -run=^$ -bench='BenchmarkPayload_64B|BenchmarkPayload_256B|BenchmarkPayload_1KB|BenchmarkPayload_4KB' -benchmem -benchtime=300ms -count=1 -timeout=180s ./tests/bench/...

go test -run=^$ -bench='BenchmarkE2E_QoS0_DataVerify|BenchmarkE2E_QoS1_DataVerify|BenchmarkE2E_RetainedMessage|BenchmarkE2E_WildcardDelivery|BenchmarkE2E_Payload_String|BenchmarkE2E_Payload_Binary|BenchmarkE2E_Payload_Empty' -benchmem -benchtime=100ms -count=1 -timeout=180s ./tests/bench/...
```

If all pass and the machine remains responsive, run medium:

```bash
go test -run=^$ -bench='BenchmarkTopicTree|BenchmarkCodec|BenchmarkQoSEngine|BenchmarkManager|BenchmarkBufferPool|BenchmarkMemoryStore' -benchmem -benchtime=1s -count=1 -timeout=180s ./tests/bench/...

go test -run=^$ -bench='BenchmarkPublishQos0|BenchmarkPublishQos1|BenchmarkConcurrentPublish|BenchmarkFanOut_1Sub|BenchmarkFanOut_5Subs' -benchmem -benchtime=1s -count=1 -timeout=180s ./tests/bench/...
```

## Cloud Execution Plan

Before running cloud benchmarks:

1. Ensure no Docker smoke container is running.
2. Ensure no kind/k3s/minikube cluster is running.
3. Confirm SSH responds quickly.
4. Run one group at a time.

Prepare code:

```bash
rm -rf /tmp/shark-mqtt-bench
mkdir -p /tmp/shark-mqtt-bench
tar -xf /tmp/shark-mqtt-review.tar -C /tmp/shark-mqtt-bench
cd /tmp/shark-mqtt-bench
```

Cloud smoke:

```bash
go test -run=^$ -bench='BenchmarkTopicTree|BenchmarkCodec|BenchmarkQoSEngine|BenchmarkBufferPool' -benchmem -benchtime=100ms -count=1 -timeout=120s ./tests/bench/...
```

Cloud light:

```bash
go test -run=^$ -bench='BenchmarkTopicTree|BenchmarkCodec|BenchmarkQoSEngine|BenchmarkBufferPool' -benchmem -benchtime=300ms -count=1 -timeout=120s ./tests/bench/...

go test -run=^$ -bench='BenchmarkPublishQos0|BenchmarkPublishQos1|BenchmarkConcurrentPublish|BenchmarkFanOut_1Sub' -benchmem -benchtime=300ms -count=1 -timeout=180s ./tests/bench/...

go test -run=^$ -bench='BenchmarkE2E_QoS0_DataVerify|BenchmarkE2E_QoS1_DataVerify|BenchmarkE2E_RetainedMessage' -benchmem -benchtime=100ms -count=1 -timeout=180s ./tests/bench/...
```

Cloud medium, only if resource gates pass:

```bash
go test -run=^$ -bench='BenchmarkTopicTree|BenchmarkCodec|BenchmarkQoSEngine|BenchmarkBufferPool' -benchmem -benchtime=1s -count=1 -timeout=180s ./tests/bench/...

go test -run=^$ -bench='BenchmarkPublishQos0|BenchmarkPublishQos1|BenchmarkFanOut_1Sub' -benchmem -benchtime=1s -count=1 -timeout=180s ./tests/bench/...
```

Do not run `payload-large`, `e2e-heavy`, `connection`, or `redis` on the 2GB cloud host unless explicitly scheduled and the machine is idle.

## Result Recording

Create a timestamped result file:

```bash
docs/BENCHMARK-RESULT-YYMMDD-HHMMSS.md
```

Record:

- Host and Go version.
- Stage and group.
- Command.
- Pass/fail.
- MemAvailable and load before/after.
- Key ns/op, B/op, allocs/op.
- Stop reason if a stage is skipped.

## Current Run Log

This section should be appended during execution.

