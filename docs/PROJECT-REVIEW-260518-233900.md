# Shark-MQTT Project Review - 260518-233900

## Scope

- Project: `G:\shark-mqtt`
- Files reviewed: 164 non-git project files, excluding historical `logs/` and coverage artifacts.
- Local Go: `go1.26.1 windows/amd64`
- Cloud Go: `go1.26.2 linux/amd64`

## Findings and Fix Plan

| ID | Severity | Status | Finding | Fix / Verification |
|----|----------|--------|---------|--------------------|
| SEC-001 | High | Fixed | `api.NewBroker()` passed nil auth options into `broker.New`, overriding broker defaults and allowing unauthenticated clients by default. | Added `TestDefectAPIDefaultBrokerRejectsUnauthenticatedClient`; API now preserves broker defaults unless auth/authorizer/plugin manager are explicitly provided. Commit: `330554f`. |
| CI-001 | High | Fixed | GitHub Actions Docker smoke test mapped old ports `1883/9090` while the image listens on `18983/18999`. | Updated CI ports and smoke addr; added static integration regression. Commit: `3c3e909`. |
| DEP-001 | High | Fixed | Docker/K8s smoke deployments became health-green but MQTT-rejecting after secure default auth was restored. | Demo/smoke paths now pass `-allow-all`; prod Helm values disable it. Added Compose/K8s/Helm regression checks. Commit: `3c3e909`. |
| TEST-001 | Medium | Fixed | `scripts/run_tests.*` did not include `tests/defects/...`, so defect regressions were absent from scripted full runs. | Added defects package to Go, sh, and bat runners. Commit: `3c3e909`. |
| TEST-002 | Medium | Fixed | Retained-message integration fixture relied on the accidental unauthenticated default. | Fixture now uses explicit `AllowAllAuth`. Commit: `7de8c1e`. |
| DEP-002 | Medium | Fixed | Docker Compose test-runner used default `proxy.golang.org`, which timed out on the cloud server. | Added `GOPROXY=https://goproxy.cn,direct` and regression check. Commit: `4d0f96e`. |
| DOC-001 | Medium | Fixed | README/docs referenced stale Docker paths, stale CI Go matrix, stale test counts, and stale review links. | Updated README, testing, deployment, development, deploy, and Helm docs. |

## Verification

| Command / Activity | Result |
|--------------------|--------|
| Full file read pass | PASS, 164 files / 890,968 bytes read. |
| `go test -count=1 ./...` | PASS locally. |
| `go run scripts/run_tests.go -mode all -timeout 10m` | PASS locally, logs `logs/20260518_234147_*`: 416 unit passed / 13 skipped, 89 integration passed, 64 benchmarks / 3 Windows skips. |
| `go test -race -count=1 ./...` | PASS locally with `D:\Programs\w64devkit\bin` in `PATH`. |
| `go vet ./...` | PASS locally. |
| Coverage | PASS, total statement coverage 49.2% (`coverage.out`). |
| Cloud `go test -count=1 ./...` | PASS on Ubuntu 26.04, Go 1.26.2. |
| Cloud scripted tests | PASS on Ubuntu, logs `/tmp/shark-mqtt-review/logs/20260518_234628_*`: 416 unit passed / 13 skipped, 89 integration passed, 67 Linux benchmarks. |
| Cloud Docker image build | PASS, `docker build -f deploy/docker/Dockerfile -t shark-mqtt:review .`. |
| Cloud Docker container smoke | PASS, `scripts/mqtt_smoke.go` against `127.0.0.1:18983`. |
| Local client -> cloud Docker | PASS, `go run scripts/mqtt_smoke.go -addr 120.76.44.233:18983`. |
| Cloud Docker Compose smoke | PASS after adding `GOPROXY`, `TestConnectFlow`, `TestPubSub`, and `TestQoS0` passed in compose test-runner. |
| Helm lint / render | PASS: `helm lint deploy/k8s/helm/shark-mqtt`, `helm template ...`. |
| Kustomize render | PASS: app rendered 250 lines; Prometheus rendered 108 lines. |
| Kubernetes actual deploy | Partial: attempted kind-based deploy on the 2C/2GB host; kind creation/resource pressure caused SSH timeouts before rollout status could be confirmed. |

## Residual Risks

- A real Kubernetes rollout should be repeated on a larger node or managed cluster. The manifests and Helm chart render/lint successfully, but the temporary kind cluster exceeded this server's practical capacity.
- Docker Compose still uses the obsolete top-level `version` key; Docker treats it as a warning only.
- Production authentication is still an integration point: CLI and demo deployments can use `-allow-all`, but production should wire a real authenticator/authorizer before public exposure.

## Follow-Up Backlog

| ID | Priority | Task |
|----|----------|------|
| K8S-001 | High | Re-run `kubectl apply -k deploy/k8s/app/` on a larger Kubernetes cluster and record pod rollout plus MQTT service verification. |
| AUTH-001 | High | Add a production-ready CLI/config path for StaticAuth/FileAuth so container deployments do not need custom embedding for authentication. |
| DOC-002 | Medium | Remove Compose `version` keys to silence Docker Compose warnings. |
| TEST-003 | Medium | Add protocol fuzz tests for CONNECT/PUBLISH/SUBSCRIBE decoders. |
