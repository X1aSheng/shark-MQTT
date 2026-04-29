# Fix Plan 02 — Critical & Medium Defects (2026-04-29)

## Summary

Applied fixes for 3 critical and 2 medium defects identified in `REVIEW_PHASE5-20260429-093000.md`.

**Test Results**: 46/46 tests passing (was 45/46, 1 fixed)

---

## Completed Fixes

### FIX-C01: Session Takeover Race Condition (Critical)
**File**: `broker/broker.go`  
**Root Cause**: When a new connection with the same ClientID arrived, the broker closed the old connection and registered the new one. The old connection's `readLoop` goroutine then called `disconnect()` which deleted the **new** connection's entry from `b.connections`, `b.sessions`, and `b.qos` maps.

**Changes**:
- `disconnect(clientID, conn)` now accepts a `net.Conn` parameter and checks identity before removing entries from shared maps
- `abnormalDisconnect(clientID, conn)` and `gracefulDisconnect(clientID, conn)` pass `conn` through
- `readLoop` passes `conn` to both disconnect functions
- `HandleConnection` no longer calls `delete(b.connections, clientID)` directly when kicking old connection — the old readLoop's cleanup is now safe due to the identity check

**Verification**: `TestPersistentSession_KickPrevious` now passes consistently.

### FIX-C02: Memory Store GetSession Deep Copy (Critical)
**File**: `store/memory/memory.go:46-62`  
**Changes**: `GetSession` now performs a full deep copy of both `Inflight` (map of pointers to InflightMessage, including Payload bytes) and `Subscriptions` slice, preventing callers from modifying internal store state.

### FIX-C03: Auth Configuration Safety (Critical)
**File**: `cmd/main.go`  
**Changes**:
- Removed hardcoded `AllowAllAuth` — broker now defaults to `DenyAllAuth` (safe default)
- Added `--allow-all` flag for explicit development mode opt-in
- Clear stderr warning printed when `--allow-all` is used

### FIX-M03: Unsubscribe Topic Validation (Medium)
**File**: `broker/broker.go:478-490`  
**Changes**: `handleUnsubscribe` now validates topic filters via `protocol.ValidateTopicFilter()` before processing, rejecting invalid topic filters.

---

## Remaining Work (Phase 6)

| ID | Priority | Description |
|----|----------|-------------|
| M-001 | Medium | Implement MQTT 5.0 Session Expiry Interval |
| M-002 | Medium | Implement offline message queueing |
| M-004 | Medium | CONNACK MQTT 5.0 capability properties |
| M-005 | Medium | StaticAuth ACL behavior documentation |
| M-006 | Medium | TopicTree match caching |
| M-007 | Medium | Plugin Dispatch error handling |
| L-001 | Low | Integrate or remove errs package |
| L-002 | Low | Consolidate duplicate error definitions |
| L-003 | Low | Improve writePacket error propagation |
| L-004 | Low | Validate RemainingLength upper bound |
| L-005 | Low | Fix client Connect TOCTOU |
| L-007 | Low | Use named timeout constants in tests |
| L-008 | Low | Add protocol fuzz tests |

---

*Fix Date: 2026-04-29 09:40 UTC+8*
