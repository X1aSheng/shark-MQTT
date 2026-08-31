# Shark-MQTT Multi-Round Test Result

Date: 2026-05-19 20:51 Asia/Shanghai

## Summary

Result: PASS

This run performed multiple local rounds plus constrained cloud validation. Cloud execution respected the 2 vCPU / 2GB / no-swap resource gate and did not run Docker image builds, kind rollout, race, or heavy benchmark groups.

## Local Rounds

| Round | Command | Result | Evidence |
|-------|---------|--------|----------|
| Full package tests | `go test -count=1 -timeout=300s ./...` | PASS | `logs/20260519_204414_multi_round_all.log` |
| Repeat + shuffle | `go test -count=2 -shuffle=on -timeout=300s ./tests/... ./broker/... ./protocol/... ./store/... ./client/... ./config/... ./errs/... ./pkg/... ./plugin/... ./api/...` | PASS | `logs/20260519_204459_multi_round_count2_shuffle.log` |
| Vet | `go vet ./...` | PASS | `logs/*_multi_round_vet.log` |
| Build | `go build ./...` | PASS | `logs/*_multi_round_build.log` |
| Module verify | `go mod verify` | PASS | `logs/20260519_204545_multi_round_mod_verify.log` |
| Coverage | `go test -count=1 -timeout=300s -coverprofile=... ./...` | PASS, total 49.2% | `logs/20260519_204545_multi_round_coverage.log` |
| Race | `CGO_ENABLED=1 go test -race -count=1 -timeout=600s ./...` | PASS | `logs/20260519_204634_multi_round_race.log` |

## Local Benchmarks

| Group | Command Shape | Count | Result | Evidence |
|-------|---------------|-------|--------|----------|
| Micro | `BenchmarkTopicTree|BenchmarkCodec|BenchmarkQoSEngine|BenchmarkManager|BenchmarkBufferPool|BenchmarkMemoryStore` | 3 | PASS | `logs/20260519_204729_multi_round_bench_micro_count3.log` |
| Publish/payload/E2E | `BenchmarkPublishQos*|BenchmarkConcurrentPublish|BenchmarkFanOut_*|BenchmarkPayload_*|BenchmarkE2E_QoS*|BenchmarkE2E_RetainedMessage` | 2 | PASS | `logs/20260519_204753_multi_round_bench_flow_count2.log` |

Representative local benchmark values:

- `BenchmarkTopicTree_Subscribe`: 121.6-360.5 ns/op
- `BenchmarkCodec_EncodePublish`: 412.7-453.6 ns/op
- `BenchmarkQoSEngine_TrackQoS1`: 24.74-29.14 ns/op
- `BenchmarkManager_GetSession`: 9.686-10.01 ns/op
- `BenchmarkBufferPool_GetPut`: 33.02-45.23 ns/op
- `BenchmarkPublishQos0`: 24758-25339 ns/op
- `BenchmarkPublishQos1`: 94965-97635 ns/op
- `BenchmarkPublishQos2`: 141886-161379 ns/op
- `BenchmarkConcurrentPublish`: 29085-29961 ns/op
- `BenchmarkE2E_QoS0_DataVerify`: 43163-43433 ns/op
- `BenchmarkE2E_QoS1_DataVerify`: 132667-134270 ns/op
- `BenchmarkE2E_RetainedMessage`: 45208-45380 ns/op

## Cloud Rounds

Cloud source was transferred from local `HEAD` to `/tmp/shark-mqtt-multi`.

| Round | Command | Result | Resource Notes |
|-------|---------|--------|----------------|
| Full package tests | `go test -count=1 -timeout=300s ./...` | PASS | Max RSS 214240 KB, wall 22.21s |
| Micro benchmark smoke | `go test -run=^$ -bench='BenchmarkTopicTree|BenchmarkCodec|BenchmarkQoSEngine|BenchmarkBufferPool' -benchmem -benchtime=100ms -count=2` | PASS | Max RSS 192976 KB, wall 9.20s |
| Docker Compose config | `docker compose -f deploy/docker/docker-compose.yml config`, `docker compose -f deploy/docker/docker-compose.test.yml config` | PASS with obsolete `version` warning | Static only |
| Helm lint/template | `helm lint`, `helm template` | PASS | Static only |
| Kustomize render | `kubectl kustomize deploy/k8s/app`, `kubectl kustomize deploy/k8s/infra/prometheus` | PASS | Static only |

Cloud benchmark representative values:

- `BenchmarkTopicTree_Subscribe`: 259.1-259.8 ns/op
- `BenchmarkCodec_EncodePublish`: 463.2-465.7 ns/op
- `BenchmarkCodec_DecodePublish`: 739.1-778.1 ns/op
- `BenchmarkQoSEngine_TrackQoS1`: 58.58-59.05 ns/op
- `BenchmarkQoSEngine_MultiClient`: 720.1-725.0 ns/op
- `BenchmarkBufferPool_GetPut`: 52.27-53.16 ns/op

## Resource Decision

The cloud host ended with:

- `MemAvailable`: about 845 MB
- Load average: about `0.88, 0.64, 0.36`
- Existing containers: `shark-review` and `shark-socket-live`, both healthy

Skipped on cloud:

- `go test -race`
- full benchmark suite
- Docker image build
- actual kind/K8s rollout
- heavy publish/fanout/payload/connection benchmark groups

Reason: the host stayed below the 1 GB medium threshold and has no swap.

## Findings

- No functional, race, build, vet, or dependency verification failure was found in this run.
- Deployment static rendering passed.
- Docker Compose v2 reports the top-level `version` field as obsolete in both compose files; this remains a low-risk cleanup item.
