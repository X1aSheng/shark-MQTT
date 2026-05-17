# Shark-MQTT Project Review 260517-115107

## Scope

- Review target: `G:\shark-mqtt`
- Baseline branch: `main`
- Review role: server-side MQTT broker, distributed persistence, cache/message-processing perspective
- File coverage: full repository inventory via `rg --files`, with focused reads across `api/`, `broker/`, `client/`, `config/`, `protocol/`, `store/`, `pkg/`, `plugin/`, `tests/`, `scripts/`, `deploy/`, `docs/`, and examples.

## Verification Run

| Command | Result |
|---|---|
| `go test -count=1 ./...` | PASS |
| `go vet ./...` | PASS |
| `go run scripts/run_tests.go -mode all` | PASS, latest logs: `logs/20260517_123532_*` |
| `cmd /c scripts\run_tests.bat --unit` | PASS |
| Runner failure propagation with temporary failing module | PASS, Go runner and BAT return non-zero |
| `PATH=D:\Programs\w64devkit\bin;%PATH% CGO_ENABLED=1 go test -race -count=1 ./...` | PASS |

Latest scripted run summary:

- Unit: 406 passed, 13 Redis tests skipped without `MQTT_REDIS_ADDR`
- Integration: 83 passed
- Benchmark: 64 executed, 3 Windows connection-churn benchmarks skipped

## Iteration Verification 260517-122841

| Command | Result |
|---|---|
| `go test -count=1 ./...` | PASS |
| `go vet ./...` | PASS |
| `PATH=D:\Programs\w64devkit\bin;%PATH% CGO_ENABLED=1 go test -race -count=1 ./...` | PASS |
| `go run scripts/run_tests.go -mode all` | PASS, logs: `logs/20260517_122841_*` |

## Iteration Verification 260517-123532

| Command | Result |
|---|---|
| `go vet ./...` | PASS |
| `go test -count=1 ./...` | PASS |
| `PATH=D:\Programs\w64devkit\bin;%PATH% CGO_ENABLED=1 go test -race -count=1 ./...` | PASS |
| `go run scripts/run_tests.go -mode all` | PASS, logs: `logs/20260517_123532_*` |

## Fixed Defects

| ID | Severity | Finding | Confirmation | Fix | Commit |
|---|---|---|---|---|---|
| DEF-001 | Low | `go vet ./...` failed on `protocol/boundary_test.go` because of `append` with no values. | `go vet ./protocol` failed before fix. | Replaced no-op `append` with a byte slice literal. | `c122ff5` |
| DEF-002 | High | Test runners could report success to CI even when `go test` failed; BAT also passed shifted label arguments into `go test`. | Temporary failing module returned success before design review; post-fix temporary failure returns non-zero. | Go, Bash, and BAT runners now preserve reports and propagate command exit codes. | `74b6180` |
| DEF-003 | High | Broker skipped publisher self-delivery unconditionally. MQTT should deliver matching self subscriptions by default; MQTT 5.0 `NoLocal` is the opt-in suppression. | `TestSelfPublishDeliveredByDefault` failed with read timeout before fix. | Persisted subscription options in session state and used `NoLocal` only to suppress local publishes. | `0b29b6f` |
| DEF-004 | High | MQTT 5.0 `RetainHandling` was decoded but ignored; retained messages were always sent after SUBSCRIBE. | `TestRetainHandlingDoNotSend` and `TestRetainHandlingSendOnlyOnNewSubscription` failed before fix. | SUBSCRIBE now honors RetainHandling 0/1/2 and only sends retained messages for successful subscriptions. | `bce7742` |

## Improvements Completed

- Added regression coverage for default self-publish delivery and MQTT 5.0 `NoLocal`.
- Added regression coverage for MQTT 5.0 `RetainHandling=1` and `RetainHandling=2`.
- Persisted subscription options through `store.Subscription` so restored sessions retain MQTT 5.0 subscription behavior.
- Updated README and docs to reflect current test counts, runner behavior, and MQTT 5.0 subscription semantics.

## Remaining Risks

- Bash runner normal-path execution was not repeated in this Windows/WSL shell because that shell does not expose `go`; failure propagation was still validated with a temporary failing module.

## Final Recommendation

The project is in a passing state for normal tests, scripted tests, vet, race tests, integration tests, and benchmarks.
