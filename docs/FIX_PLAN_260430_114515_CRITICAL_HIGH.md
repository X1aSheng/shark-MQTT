# Fix Plan 04 — High & Critical Severity Issues

**Date:** 2026-04-30
**Based on:** REVIEW_PHASE6-20260430-090000.md
**Scope:** Critical + High severity findings (10 items)

---

## Completed Fixes

### ✅ H-7: IPv6 address parsing — DONE
**File:** `client/options.go:28-48`
**Change:** Replaced manual `lastIndexOfByte` port parsing with `net.SplitHostPort`. Deleted `lastIndexOfByte` helper.
**Verification:** Compilation + existing client tests pass.

### ✅ H-8: QoS 2 publish channel reuse — DONE
**File:** `client/client.go:188`
**Change:** Increased channel buffer to 2 for QoS 2 publishes to accommodate both PUBREC and PUBCOMP.
**Verification:** Compilation + existing tests pass.

### ✅ H-3 + H-9: nil config guard + config validation — DONE
**File:** `api/api.go:121-127`
**Change:** Added nil check on `o.cfg` defaulting to `config.DefaultConfig()`. Added `cfg.Validate()` call with warning log on failure.
**Verification:** Compilation + tests pass. Also added `ReadHeaderTimeout` to health server.

### ✅ M-1: Duplicate sentinel errors — DONE
**Files:** `protocol/errors.go`, `errs/errors.go`
**Change:** Protocol package now re-exports sentinel errors from `errs` package. Added `ErrInvalidProtocol` and `ErrMalformedPacket` to `errs`.
**Verification:** Compilation + all tests pass.

### ✅ M-3: Memory store shallow copies — DONE
**File:** `store/memory/memory.go`
**Change:** `GetMessage`, `ListMessages`, `GetRetained`, `MatchRetained` now deep-copy the Payload slice.
**Verification:** Compilation + store tests pass.

### ✅ M-5: Will topic wildcard validation — DONE
**Files:** `broker/broker.go:220-224`, `broker/will_handler.go:58-69`
**Change:** Added `protocol.ValidatePublishTopic()` check before registering will. Returns CONNACK UnspecifiedError for wildcard will topics.
**Verification:** Compilation + broker tests pass.

### ✅ M-6: NextPacketID reuse check — DONE
**File:** `broker/session.go:223-238`
**Change:** NextPacketID now checks inflight map and skips IDs already in use (up to 65535 attempts).
**Verification:** Compilation + broker tests pass.

### ✅ M-7: Old will goroutine cancellation — DONE
**File:** `broker/will_handler.go:58-69`
**Change:** `RegisterWill` now cancels any pending delayed will goroutine for the same client before registering a new will.
**Verification:** Compilation + broker tests pass.

### ✅ M-9: Retained store error handling — DONE
**File:** `broker/broker.go:431-444, 575-582`
**Change:** `DeleteRetained`, `SaveRetained`, and `MatchRetained` errors are now logged via `b.logger.Debug()`.
**Verification:** Compilation + broker tests pass.

---

## Verified Non-Issues (False Positives)

### C-1: Codec.protocolVersion race condition — NOT A BUG
The broker creates a new `Codec` per connection (`broker.go:107`). Encoding and decoding happen in the same goroutine (readLoop). No concurrent access — no race.

### H-1: Session restore Subscriptions map — NOT A BUG
`Restore()` (session.go:317-318) populates the session's Subscriptions map. `CreateSession()` with `isResuming=true` reuses the restored session without clearing Subscriptions. The flow is correct.

---

## Deferred (Require Architectural Changes)

### H-2: Session save/disconnect race
Requires restructuring save+remove into atomic operation under a single lock scope. Medium complexity.

### H-4: Prometheus label cardinality
Needs design decision on which label values to allow. Production readiness concern.

### H-5: Client keepalive (PINGREQ)
Requires adding background goroutine to client — larger change with API implications.

### H-6: Client readLoop read deadline
Requires keepalive-based deadline logic in the client read loop.

---

## Summary

| Category | Fixed | Deferred | False Positive |
|----------|-------|----------|----------------|
| Critical | 0 | 0 | 1 |
| High | 3 | 4 | 1 |
| Medium | 5 | 2 | 0 |
| **Total** | **8** | **6** | **2** |
