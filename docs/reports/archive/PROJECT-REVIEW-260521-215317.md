# Shark-MQTT Project Review Follow-up

**Date**: 2026-05-21 21:53:17 CST  
**Scope**: Verification of external v1.0.0 review checklist and targeted remediation  
**Baseline commits**: `98c77c3`, `7fbaf77`

## Summary

本轮把外部审查中的 12 项作为待确认清单逐项核对。结论是：2 项为已确认缺陷并完成修复；5 项属于当前实现已覆盖或描述不准确；5 项属于架构/性能/安全增强项，建议进入后续版本计划，不在本轮以猜测方式改动核心链路。

## Fixed Items

| ID | Result | Evidence |
|----|--------|----------|
| #10 Client read loop can block Disconnect | Fixed in `7fbaf77` | `client.MQTTClient.Disconnect` now closes the connection before waiting for `readLoop`; regression test `TestDisconnectClosesConnectionBeforeWaitingForReadLoop` |
| #11 QoSEngine drops max-retry messages without reporting | Fixed in `98c77c3` | New sentinel `errs.ErrMaxRetriesExceeded`; regression test `TestQoSEngine_Retry_MaxRetriesExceededReportsError` |

## False Positives / Already Covered

| ID | Finding |
|----|---------|
| #1 Accept goroutine leak | `MQTTServer.acceptLoop` uses `defer s.wg.Done()`, removes connection from the map, decrements counters, and closes the connection on every handler exit path. |
| #2 QoSEngine retry goroutine shutdown leak | `QoSEngine.Stop()` cancels context and waits on the retry loop `WaitGroup`. Persisting inflight state on shutdown remains a product enhancement, not a goroutine leak. |
| #4 Nil `SubOptions` during restore | `Manager.Restore()` and session creation initialize `SubOptions`; current restore path does not dereference a nil map. |
| #5 PUBLISH decode silently swallowing payload errors | `decodePublish` propagates `io.ReadFull` and property/topic validation errors; broker read loop disconnects on decode errors. |
| #9 Redundant lock in subscription update | `Session.AddSubscriptionFilter` uses a single lock scope in current code. |

## Enhancement Backlog

| ID | Priority | Recommended Direction |
|----|----------|-----------------------|
| #3 Atomic session/message persistence | Add optional transaction-capable store interfaces before changing broker flow. Redis/Badger/memory need consistent semantics. |
| #6 Topic tree lock contention | Add a high-subscription benchmark with publish/subscribe churn, then evaluate sharded trie or copy-on-write read path. |
| #7 Subscription QoS grant policy | Current broker supports QoS 2 and omits MQTT 5 `MaximumQoS` accordingly. If a configurable max QoS is introduced, SUBACK grant capping must be added with tests. |
| #8 Write backpressure | Current implementation performs synchronous guarded writes. Add configurable write deadlines or per-client bounded queues after measuring slow-client behavior. |
| #12 MQTT 5 topic alias support | Broker does not advertise `TopicAliasMaximum`; full alias mapping can be added as an explicit MQTT 5 feature. Until then clients should not send aliases. |

## Verification

Local full verification completed on Windows host:

| Command | Result |
|---------|--------|
| `go test ./...` | PASS |
| `go run scripts/run_tests.go -mode all -timeout=300s` | PASS |
| `go vet ./...` | PASS |
| `go build ./...` | PASS |
| `go mod verify` | PASS |

Script logs:

| Log | Result |
|-----|--------|
| `logs/20260521_215054_unit.log` | 419 passed, 0 failed, 13 skipped |
| `logs/20260521_215054_integration.log` | 90 passed, 0 failed, 0 skipped |
| `logs/20260521_215054_benchmark.log` | 64 benchmarks, 3 skipped, total 115.00s |

## Next Steps

1. Add a resource-limited slow-client/write-backpressure test plan before changing the write path.
2. Design a transaction extension for persistent store backends instead of forcing atomicity into existing non-transactional interfaces.
3. Add explicit MQTT 5 topic alias support or documented rejection behavior in a dedicated feature branch.
