# Shark-MQTT Resource-Limited Benchmark Result - 260519-182500

## Summary

按照 [BENCHMARK-RESOURCE-LIMITED-TEST-FLOW-260519.md](reports/BENCHMARK-RESOURCE-LIMITED-TEST-FLOW-260519.md) 执行分层 benchmark。本机完成 smoke、light、medium；云服务器完成 smoke/light，并因可用内存不足 1GB 按门控停止升级 medium/heavy。

## Environment

| Host | CPU / RAM | OS / Go | Role |
|------|-----------|---------|------|
| Local Windows | AMD Ryzen 7 8845HS / ~32GB | Windows / Go 1.26.1 | Baseline and medium stages |
| Cloud `120.76.44.233` | 2 vCPU / 2GB no swap | Ubuntu 26.04 / Go 1.26.2 | Resource-limited smoke/light |

## Local Results

Logs:

- `logs/20260519_bench_local_micro_smoke.log`
- `logs/20260519_bench_local_micro_light.log`
- `logs/20260519_bench_local_publish_light.log`
- `logs/20260519_bench_local_payload_small_light.log`
- `logs/20260519_bench_local_e2e_basic_smoke.log`
- `logs/20260519_bench_local_micro_medium.log`
- `logs/20260519_bench_local_publish_medium.log`

| Stage | Group | Result | Representative Results |
|-------|-------|--------|------------------------|
| smoke `100ms` | micro | PASS | `TopicTree_Match_Exact` 242 ns/op, `Codec_EncodePublish` 466 ns/op, `QoSEngine_TrackQoS1` 19.1 ns/op |
| light `300ms` | micro | PASS | `TopicTree_Match_Exact` 227 ns/op, `Codec_EncodePublish` 491 ns/op, `BufferPool_GetPut` 38.7 ns/op |
| light `300ms` | publish | PASS | QoS0 25.9 us/op, QoS1 79.9 us/op, QoS2 145 us/op, fanout 10 subs 186 us/op |
| light `300ms` | payload-small | PASS | 64B 24.0 us/op, 256B 30.0 us/op, 1KB 30.6 us/op, 4KB 34.8 us/op |
| smoke `100ms` | e2e-basic | PASS | QoS0 50.6 us/op, QoS1 123 us/op, retained 47.7 us/op, wildcard 46.4 us/op |
| medium `1s` | micro | PASS | `TopicTree_Match_Exact` 231 ns/op, `Codec_EncodePublish` 550 ns/op, `QoSEngine_TrackQoS1` 20.3 ns/op |
| medium `1s` | publish | PASS | QoS0 24.9 us/op, QoS1 85.3 us/op, concurrent 34.4 us/op, fanout 5 subs 89.0 us/op |

## Cloud Results

Remote logs:

- `/tmp/shark-mqtt-bench-cloud-micro-smoke.log`
- `/tmp/shark-mqtt-bench-cloud-micro-light.log`
- `/tmp/shark-mqtt-bench-cloud-publish-light.log`
- `/tmp/shark-mqtt-bench-cloud-e2e-basic-smoke.log`

| Stage | Group | Before Resource | Result | Max RSS | After Resource | Representative Results |
|-------|-------|-----------------|--------|---------|----------------|------------------------|
| smoke `100ms` | micro | MemAvailable 815MB, load1 0.10 | PASS | 197MB | MemAvailable 798MB, load1 0.53 | `TopicTree_Match_Exact` 486 ns/op, `Codec_EncodePublish` 460 ns/op, `QoSEngine_TrackQoS1` 60.0 ns/op |
| light `300ms` | micro | MemAvailable 813MB, load1 0.57 | PASS | 193MB | MemAvailable 823MB, load1 1.17 | `TopicTree_Match_Exact` 480 ns/op, `Codec_EncodePublish` 467 ns/op, `BufferPool_GetPut` 56.5 ns/op |
| light `300ms` | publish | MemAvailable 827MB, load1 0.99 | PASS | 197MB | MemAvailable 783MB, load1 0.99 | QoS0 19.9 us/op, QoS1 78.7 us/op, concurrent 14.8 us/op, fanout 1 sub 21.3 us/op |
| smoke `100ms` | e2e-basic | MemAvailable 827MB, load1 0.84 | PASS | 202MB | MemAvailable 828MB, load1 0.84 | QoS0 44.0 us/op, QoS1 107 us/op, retained 46.6 us/op |

## Stop Decision

Cloud medium and heavy stages were intentionally skipped:

- Cloud MemAvailable stayed around 0.78-0.83GB.
- The flow requires at least 1GB MemAvailable before cloud medium.
- No swap is configured, so memory pressure can quickly affect SSH and Docker.
- Previous kind/K8s attempts already showed this host can become unresponsive under higher load.

## Conclusions

- The staged benchmark method is feasible and avoided server collapse.
- Cloud smoke/light benchmark groups are safe on this host when run one at a time.
- Cloud medium should remain disabled unless MemAvailable is consistently above 1GB and no Docker/K8s workload is present.
- `payload-large`, `e2e-heavy`, `connection`, Redis benchmark, and K8s actual rollout should run on a larger host or CI.

## Next Recommended Automation

- Add a guarded benchmark runner script that implements the resource gates automatically.
- Save local and cloud benchmark outputs into timestamped `logs/` files consistently.
- Add a `make bench-guarded` target for smoke/light execution.
