# Shark-MQTT Project Review - 260520-233509

Date: 2026-05-20 23:35 Asia/Shanghai

## Scope

Reviewed the full `shark-mqtt` repository as a server-side MQTT broker project:

- Inventory read: 157 project files, 26816 lines, excluding logs and generated coverage artifacts.
- Focused review: `api/`, `broker/`, `client/`, `config/`, `protocol/`, `store/`, `pkg/`, `plugin/`, `tests/`, `scripts/`, `.github/`, `deploy/`, `docs/`, examples.
- Standards focus: MQTT 3.1.1/5.0 packet validation, connection lifecycle, session persistence, QoS state, retained/will handling, CI, Docker and Kubernetes deployability.

## Findings And Fixes

| ID | Severity | Status | Finding | Confirmation | Fix |
|----|----------|--------|---------|--------------|-----|
| REV-260520-001 | High | Fixed | A CONNECT with a wildcard Will Topic was rejected after session creation and connection registration. With `max_connections=1`, the rejected client consumed the broker connection slot and blocked a later valid client. | Added `TestDefectInvalidWillTopicDoesNotConsumeBrokerConnectionSlot`; it failed before the fix because the next valid client was rejected with max connections reached. | Validate Will Topic before session creation and connection registration. Commit `4cfdd82`. |
| REV-260520-002 | Medium | Fixed | GitHub Actions did not run the repository's scripted test runner, so `scripts/run_tests.go` could drift even when raw `go test` passed. | `rg` showed CI only ran raw Go tests and Docker smoke. Local `go run scripts/run_tests.go -mode unit|integration` passed. | Added a `test-scripts` CI job for scripted unit and integration entrypoints. Commit `17e7b19`. |
| REV-260520-003 | Low | Fixed | Docker Compose v2 emitted obsolete top-level `version` warnings for both compose files. | Cloud `docker compose config` previously warned. | Removed top-level `version` from compose files. Commit `17e7b19`. |
| REV-260520-004 | Medium | Open | Total coverage is 49.2%, below the aspirational 60% documented target, although it is above the current CI gate of 30%. | `go test -coverprofile=... ./...`; `go tool cover -func` total 49.2%. | Raise coverage in `client`, `pkg/metrics`, `store/redis` with focused tests, or align docs/CI thresholds deliberately. |
| REV-260520-005 | Medium | Open | Real Kubernetes rollout was not executed on the 2 vCPU / 2GB no-swap host because no Kubernetes cluster is configured and prior kind attempts on this host caused resource pressure. | `kubectl cluster-info` refused `localhost:8080`; `kind get clusters` returned none. | Use a larger host or managed cluster for actual rollout; this run completed Helm/Kustomize static deployment validation. |

## Local Verification

| Check | Result | Notes |
|-------|--------|-------|
| Full tests | PASS | `go test -count=1 -timeout=300s ./...` |
| Script unit | PASS | `go run scripts/run_tests.go -mode unit -timeout=300s`; 417 passed, 13 Redis skips |
| Script integration | PASS | `go run scripts/run_tests.go -mode integration -timeout=300s`; 90 passed |
| Script all | PASS | `go run scripts/run_tests.go -mode all -timeout=600s`; includes 64 benchmarks, 3 Windows benchmark skips |
| Defect regression | PASS | `go test ./broker ./tests/defects -count=1 -v` |
| Race | PASS | `CGO_ENABLED=1 go test -race -count=1 -timeout=600s ./...` with `D:\Programs\w64devkit\bin` |
| Vet | PASS | `go vet ./...` |
| Build | PASS | `go build ./...` |
| Format | PASS | `go fmt ./...` and clean Go diff |
| Module verify | PASS | `go mod verify` |
| Coverage | PASS | Total 49.2%; current CI threshold is 30% |

## Cloud Verification

Cloud host: Ubuntu 26.04 class, 2 vCPU / 2GB, no swap, Go 1.26.2.

| Check | Result | Notes |
|-------|--------|-------|
| Source transfer | PASS | Local `HEAD` archived to `/tmp/shark-mqtt-review-260520` |
| Cloud full tests | PASS | `go test -count=1 -timeout=300s ./...`; max RSS 273224 KB, wall 21.80s |
| Docker Compose config | PASS | Main and test compose files render without obsolete `version` warnings |
| Helm lint/template | PASS | `helm lint` and `helm template` for `deploy/k8s/helm/shark-mqtt` |
| Kustomize render | PASS | `kubectl kustomize deploy/k8s/app` and `deploy/k8s/infra/prometheus` |
| Docker build | PASS | `docker build -f deploy/docker/Dockerfile -t shark-mqtt:review-260520 .` |
| Docker run/health | PASS | `shark-mqtt-review-260520` exposed `18983` and `18999`, `/healthz` passed |
| Cloud MQTT smoke | PASS | `go run scripts/mqtt_smoke.go -addr 127.0.0.1:18983` |
| Local client to cloud | PASS | `go run scripts/mqtt_smoke.go -addr 120.76.44.233:18983`; connect, subscribe, publish, disconnect |
| Kubernetes actual rollout | Blocked | No configured cluster; kind not started due resource gate |

## Notes

- The review Docker container was used for validation only.
- The server remained responsive after Docker and smoke tests; final observed `MemAvailable` was about 1.2 GB before K8s decision.
- Kubernetes manifests and Helm chart are deployable at the rendering/lint layer, but actual pod rollout still needs a real cluster or larger validation host.

## Follow-Up Plan

| Priority | Task |
|----------|------|
| P1 | Run actual Kubernetes rollout on a larger node or managed cluster and verify MQTT smoke through Service or port-forward. |
| P2 | Raise coverage around `client`, `pkg/metrics`, and Redis store behavior; then revisit the documented 60% total target. |
| P2 | Add a CI deploy-static job for `docker compose config`, `helm lint/template`, and `kubectl kustomize` if runner tools are available or installable. |
| P3 | Add protocol fuzz tests for CONNECT/PUBLISH/SUBSCRIBE property and Remaining Length boundaries. |
