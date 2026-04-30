# Fix Plan 03 — Medium & Low Severity Fixes (2026-04-29)

## Summary

Applied fixes for 4 medium and 2 low severity defects identified in `REVIEW_PHASE5-20260429-093000.md`.
This continues from `FIX_PLAN_02_CRITICAL_MEDIUM.md`.

**Test Results**: 46/46 tests passing

---

## Completed Fixes

### FIX-M01: MQTT 5.0 Session Expiry Interval (Medium)
**Files**: `broker/session.go`, `broker/broker.go`, `broker/options.go`, `api/api.go`, `store/types.go`

**Changes**:
- Added `ExpiryInterval uint32` field to `Session` struct
- Added `ExpiryInterval` to `store.SessionData`
- `HandleConnection` now extracts `SessionExpiryInterval` from MQTT 5.0 CONNECT properties
- Server caps the value at its configured `SessionExpiryInterval` max
- `CleanSession` forces `ExpiryInterval = 0`
- `Save`/`Restore` persist and restore the expiry interval
- Added `WithSessionExpiry` broker option wired through api layer

### FIX-M04: CONNACK MQTT 5.0 Capability Properties (Medium)
**Files**: `broker/broker.go`

**Changes**:
- `sendConnAck` now includes MQTT 5.0 capability properties:
  - `SessionExpiryInterval` — actual expiry value used
  - `ReceiveMaximum` — from QoS max inflight config
  - `MaximumQoS` — 2
  - `RetainAvailable` — 1 if retained store configured
  - `MaximumPacketSize` — from broker config
  - `WildcardSubAvailable` — 1
  - `SubIDAvailable` — 0 (not yet implemented)
  - `SharedSubAvailable` — 0 (not yet implemented)
- Added `buildConnAckProperties` helper

### FIX-M07: Plugin Dispatch Continues After Error (Medium)
**Files**: `plugin/manager.go`, `plugin/manager_test.go`

**Changes**:
- `Dispatch` now continues to all registered plugins even if one fails
- Errors from all plugins are collected and returned as a combined error
- Panics are recovered and included in the error collection
- Updated `TestManager_Dispatch_EarlyTermination` → `TestManager_Dispatch_ContinuesAfterError` to verify new behavior

### FIX-L01/L02: errs Package Integration (Low)
**Files**: `errs/errors.go`, `broker/auth.go`, `store/errors.go`

**Changes**:
- Added `ErrMessageNotFound` and `ErrRetainedNotFound` to `errs` package
- `broker/auth.go`: `ErrAuthFailed` and `ErrUnauthorized` now alias `errs.ErrAuthFailed` and `errs.ErrNotAuthorized`
- `store/errors.go`: All error sentinels now alias their `errs` counterparts
- Removed duplicate `errors.New()` calls, single source of truth in `errs` package

### FIX-L04: RemainingLength Upper Bound Validation (Low)
**Files**: `protocol/codec.go`

**Changes**:
- Added sanity check: `RemainingLength` must be between 0 and 256 MiB - 1 (MQTT 5.0 max variable-length encoding)
- Prevents negative values and integer overflow in `RemainingLength + 5` arithmetic

---

## Remaining Work (Phase 7)

| ID | Priority | Description |
|----|----------|-------------|
| M-002 | Medium | Implement offline message queueing |
| M-005 | Medium | Document StaticAuth ACL behavior |
| M-006 | Medium | TopicTree match caching |
| L-003 | Low | Improve writePacket error propagation |
| L-005 | Low | Fix client Connect TOCTOU |
| L-007 | Low | Use named timeout constants in tests |
| L-008 | Low | Add protocol fuzz tests |

---

*Fix Date: 2026-04-29 10:00 UTC+8*
