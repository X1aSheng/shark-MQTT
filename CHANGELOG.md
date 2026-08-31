# Changelog

All notable changes for `shark-mqtt` are recorded here.

This project uses semantic versioning. Pre-release tags use the form
`vMAJOR.MINOR.PATCH-rc.N`.

## Unreleased

### Design / reliability (2026-08-31)

- **Deduped system-topic matcher:** `matchSysProtected` is now the single MQTT
  §4.7.2 system-topic protection implementation shared by the topic tree,
  session matching, and ACL checks (the duplicated
  `matchWithSysProtection` in auth.go was removed), eliminating a correctness
  drift risk.
- **Non-memory backend now fails loudly:** configuring `storage_backend:
  redis|badger` without explicitly wiring stores makes `Start()` fail with a
  clear error instead of silently running without persistence.
- **Storage errors observable:** 13 persistence/cleanup error paths (session
  save, offline queue, retained save/delete, expiry cleanup) upgraded from
  debug to warn-level logs, so store failures are visible in production logs.
- **Removed dead delivery duplication:** `Session.matchesSubscriptions` is the
  shared core of `MatchesSubscription`/`MatchesRetainedSubscription`;
  `Broker.deliverWithQoS` is the shared core of
  `deliverToClient`/`deliverToSharedClient`; CONNACK construction is unified in
  `buildConnAck`; keep-alive deadline refresh is unified in
  `Broker.setKeepAliveDeadline`.
- **Store decoupled from protocol:** `store/memory` now uses `strings.Split`
  instead of `protocol.SplitTopic` (identical semantics for MQTT topic levels),
  removing the storage→protocol dependency.
- **Metrics interface cleaned:** removed six never-called methods
  (`IncInflight`, `DecInflight`, `DecInflightBatch`, `IncInflightDropped`,
  `IncRetries`, `SetOfflineSessions`) and their never-populated Prometheus
  metrics (`inflight_messages`, `inflight_dropped_total`, `retries_total`,
  `offline_sessions`).
- **Readiness probe strengthened:** `/readyz` now requires both the listener
  and the broker subsystems to be running (`Broker.IsStarted()`).
- **Concurrency documentation:** `docs/architecture/CONCURRENCY.md` documents
  the full lock inventory, ordering rules, and a deadlock audit.

### Project structure (2026-08-31)

- **Test artifacts centralized under `tests/`:** test logs now default to
  `tests/logs/` (`scripts/run_tests.go -logdir tests/logs`, run_tests.sh/bat
  updated; historical logs moved from the repo-root `logs/`), and a new
  `tests/artifacts/` directory is the agreed output location for coverage /
  profile / benchmark process files. Both are gitignored; the convention is
  documented in `docs/guides/TESTING.md` (repo root keeps source, config, and
  docs only).

### Client / correctness (2026-08-29)

- **Client Connect TOCTOU fixed (L-005):** the client now uses a
  per-connection "generation" context (`connCtx`/`connCancel`/`connDone`)
  instead of a shared client-wide context and WaitGroup. A stale readLoop or
  keep-alive loop shutting down after a takeover (same client ID) previously
  cancelled the shared context — killing the brand-new connection's pending
  operations. Now each generation's goroutines hold their own context as
  parameters: an old connection's shutdown can never cancel or tear down a
  newer one, and `Disconnect` waits on exactly its own generation's done
  channel. `Publish`/`Subscribe`/`Unsubscribe` wait on the generation context
  snapshot. Regression test:
  `TestClientConnectTOCTOU_StaleReadLoopDoesNotKillNewConnection` (5 takeover
  rounds with QoS 1 round-trips). Race-clean.
- **Session Expiry Interval 0 honored (MQTT 5.0 §3.1.2.11.2):** an explicit
  `SessionExpiryInterval=0` in CONNECT previously fell through to the
  server-configured maximum (24h default), so "end the session when the
  connection closes" was silently ignored. The CONNACK now reports 0 and the
  disconnect path deletes any stored session/messages instead of saving.
  DISCONNECT may now also update the interval (§3.14.2.2.2, capped at the
  server maximum; 0 ends the session immediately). Absent property still
  defaults to the server maximum. Tests:
  `TestSessionExpiryZero_EndsSessionOnDisconnect`,
  `TestSessionExpiryAbsent_UsesServerDefault`,
  `TestDisconnectUpdatesSessionExpiry`.

### Protocol / correctness (2026-08-29)

- **Max QoS on overlapping subscriptions:** a client with multiple matching
  filters is now delivered once at the **maximum** QoS of the matching
  subscriptions (MQTT 3.1.1 §3.3.5 / 5.0 §3.3.5). Previously the delivered
  QoS was whichever filter the randomized map/trie iteration hit first, so a
  QoS 1 publish could arrive as QoS 0 or QoS 1 nondeterministically. Fixed in
  `TopicTree.Match` (dedup map now tracks QoS, `addSubscriber` keeps the max),
  shared-subscription member selection (`matchShared`), and
  `Session.MatchesSubscription` / `MatchesRetainedSubscription` (highest-QoS
  match wins). Tests: `TestTopicTree_QoSMaxOnMultipleMatches`,
  `TestSessionMatchesSubscriptionMaxQoS`,
  `TestOverlappingSubscriptionsMaxQoS`.
- **SubIDAvailable=1 advertised:** the CONNACK now advertises Subscription
  Identifier support (MQTT 5.0 §3.8.2.1.2) — the broker already parsed,
  stored, and echoed the SUBSCRIBE packet-level SubscriptionIdentifier in
  delivered PUBLISH packets, so the previous SubIDAvailable=0 claim was
  inconsistent with its behavior. New end-to-end test
  (`TestSubscriptionIdentifierAdvertisedAndEchoed`).
- **Protocol fuzz tests (L-008):** `FuzzDecodeNeverPanics` (arbitrary bytes
  into the codec decoder) and `FuzzPublishRoundTrip` (structured
  encode→decode round-trip) added to the protocol package. 8.4M executions
  across both fuzzers with zero crashes/panics.

### Performance (2026-08-29)

- **Fixed-header decode without heap allocation:** `decodeFixedHeader` now
  returns a value instead of a pointer, removing one heap allocation per
  decoded packet across all 15 packet types (was 7 MB flat of 85 MB total in
  the QoS 0 publish-path alloc profile). `validateFixedHeaderFlags` takes the
  value as well; decode function signatures were adjusted accordingly.
- **Pooled publish encode buffer:** `encodePublish` assembles the body in a
  pooled 4 KB buffer (the codec's buffer pool) instead of a fresh
  `bytes.Buffer`, removing the per-packet assembly allocation and most buffer
  growth. Measured on Ryzen 7 8845HS: `BenchmarkCodec_EncodePublish`
  433→144 ns/op (422→94 B/op); `DecodePublish` 489→372 ns/op (454→430 B/op,
  8→7 allocs); `RoundTripPublish` 908→665 ns/op (882→528 B/op, 14→12 allocs);
  E2E QoS 0 71.6→62.2 µs/op (36→34 allocs, 1089→959 B/op); E2E QoS 1/2
  −4/−8 allocs per message.
- **TopicTree.Match dedup map pooled:** the per-call subscriber-dedup map is
  now reused via `sync.Pool` (maps that grew past 64 entries are dropped so
  the pool does not pin large heaps), removing one allocation per published
  message on the match path. `BenchmarkTopicTree_Match_Exact` 370→264 ns/op
  (−29%) with unchanged 2 allocs/op.
- **Tiered buffer pool:** `pkg/bufferpool` now uses size tiers
  (512B/1KB/2KB/16KB/32KB/64KB/256KB) instead of a single 4KB bucket.
  `Get(size)` picks the smallest bucket that fits, so small messages no longer
  pin a 4KB buffer and large messages reuse a large buffer instead of growing
  from 4KB. `encodePublish` sizes its request from the body estimate;
  `readString`/`decodePublish` request exactly the byte count they need.
  Measured on Ryzen 7 8845HS: `BenchmarkCodec_EncodeLargePayload` 96 B/op
  (5 allocs); `BenchmarkPayload_128KB` 262→122 µs/op (551→136 KB/op);
  `BenchmarkE2E_Payload_64KB` 469→202 µs/op (431→180 KB/op); `DecodePublish`
  372→357 ns/op; `EncodePublishQos1` 244→183 ns/op.

### Storage (2026-08-29)

- **Redis store TTL aligned with broker expiry semantics (S5):** the session
  store derives the key TTL from `SessionData.ExpiryTime` (absolute) or
  `ExpiryInterval` (relative) instead of a fixed default, so Redis can no
  longer delete a session before the broker's negotiated Session Expiry
  Interval — which silently turned a reconnect into a fresh session
  (SessionPresent=0). The message store likewise keeps a queued message alive
  at least until its Message Expiry deadline, leaving expiry decisions to the
  broker's delivery path. Regression tests
  (`TestSessionStore_TTLFollowsExpiryTime`,
  `TestSessionStore_TTLFollowsExpiryInterval`,
  `TestMessageStore_TTLFollowsExpiry`) run under `-tags=store_redis`.
- **Redis batch fetch (S2):** `MessageStore.ListMessages` and
  `RetainedStore.MatchRetained` now SCAN for keys first and fetch all values
  with a single `MGet` instead of one `GET` per key, so draining a large
  offline queue or matching retained messages no longer costs one round-trip
  per message. Keys that vanish between SCAN and MGet are skipped (nil value).
  `TestMessageStore_ListMessages_Many` covers 120 messages across two SCAN
  pages.
- **Memory store save isolation (S8):** `memory.sessionStore.SaveSession` now
  deep-copies inflight payloads and the subscriptions slice (mirroring
  `GetSession`), so a caller mutating its `SessionData` after `SaveSession`
  can no longer corrupt the stored session. `TestSessionStore_SaveIsolation`
  covers the regression.

### Performance (2026-08-07)

- **Publish latency sampling:** `WithLatencySampling(N)` controls how often
  publish latency is observed for metrics (1 = every message, the default; N =
  1 in N; 0 = off). Prometheus histogram observations are comparatively
  expensive, so sampling cuts per-message overhead when metrics are enabled.
- **Decode buffer pooling:** `bytes.Reader` instances used to parse packet
  bodies are now pooled (they do not escape the decode call), removing one
  allocation per decoded packet across publish/ack/connect/subscribe paths.
  (A QoS 0 "direct write" delivery path was evaluated but rejected: it would
  have forwarded the Topic Alias property, which MQTT 5.0 forbids the server
  from sending.)
- **Optional store backends behind build tags:** `store/badger` and
  `store/redis` now build only under `-tags=store_badger,store_redis`, so the
  default build no longer compiles them (faster builds/CI). `make test-stores`
  and a CI step run their tests under the tags.
- **Dependency upgrades:** prometheus 1.23→1.24, badger 4.7→4.9,
  redis 9.7→9.22, x/crypto 0.53→0.54, plus transitive deps. `govulncheck`
  reports the 8 standard-library advisories are fixed by **Go 1.26.2+** (the
  local toolchain is 1.26.1); imported-package advisories are unreachable.
- **Configurable bcrypt cost:** `StaticAuth.SetBcryptCost(N)` sets the work
  factor for `SetHashedPassword` (default 10); lower costs trade weaker hash
  resistance for faster logins at high connection rates. Verification always
  uses the cost embedded in the stored hash.
- **QoS 2 path (evaluated):** the remaining allocations are structural — the
  QoS engine/session inflight records and ack packet bodies are escaping
  references that cannot be safely pooled. Attempting to pool the fixed-header
  write buffer measured *worse* (the compiler already stack-allocates it) and
  was reverted. QoS 2's ~2x latency over QoS 1 is inherent to the mandatory
  4-way handshake. Already-applied decode/match optimizations cover this path.

### Protocol gaps (2026-08-06)

- **Enhanced authentication (MQTT 5.0 §4.12):** `EnhancedAuthenticator` interface
  + `WithEnhancedAuth` option. A CONNECT carrying an `AuthenticationMethod` now
  runs the enhanced auth exchange (AUTH packets: 0x18 continue → 0x00 success)
  instead of being rejected; an unregistered method returns CONNACK 0x8C; a
  failed exchange ends with a DISCONNECT (AUTH packets only carry
  0x00/0x18/0x19). Re-authentication during a session is handled in the read
  loop.
- **Message Expiry Interval fully enforced (MQTT 5.0 §3.3.2.3.2):** an absolute
  expiry deadline is tracked per message and carried through the offline message
  queue, the flow-control buffer, and the inflight retry. Expired queued
  messages are dropped on reconnect instead of being delivered late; inflight
  retries stop once the deadline passes; forwarded PUBLISHes carry the remaining
  interval. (Retained messages are still governed by the retained-expiry TTL.)
- **WSS (WebSocket over TLS):** new `wss_listen_addr` config serves
  MQTT-over-WebSocket over TLS using the broker's TLS certificate (requires
  `tls_enabled`); plain `ws_listen_addr` and `wss_listen_addr` can run
  simultaneously. `WSSAddr()` exposed on the api/server.
- **Request Response Information (MQTT 5.0 §3.2.2.3.8/.9):** the CONNACK now
  honors the client's RequestResponseInformation: when requested it advertises
  RequestResponseInfo=1 and returns the client ID as the Response Information
  base; otherwise neither is sent.

### Review Round V7 (2026-08-06) - reference comparison

Reviewed against smart-mqtt (Java, <4MB), smart-mqtt-4g (Go) and mica-mqtt
(~500KB core). Full baseline green (344 unit / 96 integration / 65 benchmark).
Details in `docs/reports/PROJECT-REVIEW-260806-143527.md`.

- **R6 fixed:** the flow-control outbound buffer is now bounded
  (`maxBufferedOutbound = 1000`); a client that never acknowledges can no
  longer grow the buffer without bound (memory exhaustion).
- **R1 fixed:** per-connection async write queues eliminate head-of-line
  blocking. Each connection gets a bounded outbound queue drained by a single
  writer goroutine; `write_queue_size` (config / `WithWriteQueueSize`) now
  takes effect. QoS 0 publishes are dropped (at-most-once) when the queue is
  full so a stalled subscriber cannot block its publisher; QoS 1/2 deliveries
  and control packets apply backpressure because the protocol requires them to
  reach the client. Regression tests cover drop, backpressure release, and
  tiny-queue delivery.
- **R2 fixed:** `decodePublish` no longer allocates a transient body buffer plus
  a payload copy. MQTT 3.1.1 payloads are read straight into the packet-owned
  buffer (no copy); MQTT 5.0 bodies use the buffer pool for the intermediate
  buffer. `BenchmarkCodec_DecodePublish`: 10→8 allocs/op, 758→455 B/op,
  454→328 ns/op; `RoundTripPublish`: 16→14 allocs.
- **R7 fixed:** shared-subscription round-robin is now deterministic and stays
  fair across membership changes. Members are ordered stably (previously the
  selection ran over a map-iteration-randomized slice with `counter % len`, so
  it was not a real round-robin and skewed when a member left/went offline).
  Selection now tracks the last-selected client per share and picks the next
  member after it, so no member is double-selected or starved by a change.
- **R3 (evaluated):** `TopicTree.Match` allocs reduced 3→2/op by pre-sizing the
  results slice for the common fanout case; pooling the dedup map and replacing
  the trie with a bitmap/bloom index were measured to give no further benefit at
  this scale (the report already judged the trie sufficient for normal load).
- **R4 fixed:** a `nometrics` build tag drops the Prometheus metrics backend
  from the binary — **11.5 MB → 7.0 MB (-37%)** (`make build-minimal`). The
  default build is unchanged and still serves `/metrics`; the minimal build uses
  a no-op metrics backend. Note: badger/redis were *not* actually linked into
  the broker binary (only go.mod modules for the opt-in store packages); the
  measured binary bloat was Prometheus, which this addresses.
- **R8 fixed:** the broker publishes periodic $SYS status topics
  (`$SYS/broker/version`, `uptime`, `connections`, `retained`, `subscriptions`)
  for ops, configurable via `sys_interval` (default 30s, 0 disables). MQTT $SYS
  topic protection applies, so only explicit $SYS subscriptions receive them.
- **R5 fixed:** MQTT-over-WebSocket transport via gorilla/websocket. Setting
  `ws_listen_addr` (default off) starts a WebSocket listener; upgraded
  connections run through the normal broker read loop. Each MQTT packet is sent
  as one binary WS message (the broker's write loop flushes per packet), with
  the `mqtt` subprotocol negotiated. End-to-end WS test plus a TCP regression
  test. Binary impact: +~0.2MB over the minimal `nometrics` build.

### V6 Fix Round (2026-08-06)

Implemented the prioritized improvement plan from the 2026-08-06 review. Each
fix is committed separately with regression tests; full suite now 344 unit PASS
(13 Redis skips) + 96 integration PASS, build/vet/gofmt clean, race-clean.

#### Client
- **Keepalive (P1-6):** a PINGREQ keep-alive loop runs at KeepAlive/2 and drops
  a connection with no traffic within 1.5x KeepAlive, so an idle client is not
  disconnected by the broker.
- **Reconnect (P2-11):** `Connect` rebuilds the context, pending, inflight and
  QoS 2 dedup maps, so `Disconnect` followed by `Connect` works.
- **Packet ID allocation (P2-12):** `nextPacketID` skips IDs still in flight or
  awaiting a response, preventing ACK mis-correlation after wrap-around.
- **Concurrent connect guard (NEW-16):** a `connecting` flag rejects a second
  in-flight `Connect`.
- **State cleanup (NEW-17):** `receivedQoS2`/inflight are cleared on disconnect
  and on readLoop error.
- Writes are serialized with a per-client write mutex.

#### Broker: outbound QoS & persistence
- **Outbound retry (NEW-1):** deliveries to subscribers are tracked in the QoS
  engine; the retry loop re-sends PUBLISH with DUP (or PUBREL once the QoS 2
  handshake reaches the PUBREC phase) instead of dropping.
- **Inflight persistence (P2-3):** `doDeliver` adds to `Session.Inflight` and
  PUBACK/PUBCOMP remove it, so inflight state persists across disconnect;
  `api.NewBroker` now wires default in-memory session/message/retained stores.
- **Flow-control floor (P2-16):** the outbound-unacked counter never goes
  negative and is only decremented when a matching inflight entry is removed.
- **Offline queue (P1-5):** QoS 1/2 messages for offline persistent sessions are
  queued in the message store and delivered on reconnect; a clean-session
  connect discards stored session state.

#### Broker: will
- **Delayed will fires (P2-5b):** `disconnect` no longer cancels a just-armed
  delayed will, so a WillDelayInterval is honored.
- **Will-delay cap (P2-5a):** `maxWillDelay` of 0 disables will delay entirely
  (matching `WithMaxWillDelay` docs) instead of leaving it uncapped.
- **Takeover semantics (P2-10):** session takeover triggers the previous
  connection's will before the new will registers; reconnecting to an existing
  session cancels a pending delayed will (P2-5c).

#### Broker: routing & flow control
- **Subscription leak (P2-13, NEW-3):** clean sessions release their topic-tree
  entries (regular and `$share`) on disconnect; expired persistent sessions
  release theirs too.
- **Shared subscriptions (P2-9):** `MatchSharedOnline` never selects an offline
  member, so a shared message is not handed to a dead member.
- **ReceiveMaximum (P2-14):** when a client's receive window is full, QoS 1/2
  deliveries are buffered and flushed on PUBACK/PUBCOMP instead of dropped;
  persistent sessions persist buffered deliveries on disconnect.
- **QoS 2 dedup bound (P2-15):** the duplicate-tracking map only records
  accepted messages, bounding it by maxInflight.
- **Retained flow control (NEW-4):** retained deliveries route through
  `doDeliver`, so they count toward ReceiveMaximum; live forwards always carry
  Retain=0 per spec.

#### Protocol
- **Header size (P3-1):** `decodeFixedHeader` records `HeaderSize` so
  maxPacketSize checks are exact.
- **Minimal remaining length (P3-2):** non-minimal variable-length encodings are
  rejected (MQTT-1.5.5-1).
- **v3.1.1 CONNACK (P3-3):** requires exactly two payload bytes.
- **Trailing bytes (NEW-18):** CONNACK/DISCONNECT/AUTH reject unconsumed bytes
  after properties.
- **v3.1.1 SUBSCRIBE (NEW-19):** rejects MQTT 5.0 option bits.

#### Retained
- **System-topic protection (P3-4):** retained delivery uses a sys-topic-aware
  matcher, so a `$SYS` retained message is never delivered to a bare `#`/`+`
  subscription.
- **TTL across restart (P3-5):** `Start` rebuilds retained-expiry state from the
  store.
- **Limit TOCTOU (NEW-5):** the retained-count limit and store write share one
  lock.

#### Config / cmd / metrics
- **Version injection (P3-6):** `cmd` declares `Version`; `build.sh`'s
  `-X main.Version` now takes effect.
- **Config validation (NEW-7/8):** empty `listen_addr` and unknown `log_level`
  are rejected.
- **Env int parsing (NEW-9):** uses `strconv`, rejecting trailing garbage.
- **`-config` flag (NEW-11):** `cmd` can load a YAML config file.
- **Default `/metrics` (NEW-20):** `api.NewBroker` defaults to Prometheus
  metrics, so the Prometheus endpoint is served out of the box.
- **Badger scan (NEW-13):** retained `MatchRetained` disables value prefetch.
- **Publish-rate race (NEW-2):** `publishRateTracker` is now mutex-guarded, so
  a session taken over by a new connection cannot race the old read loop.

#### Not changed (documented design decisions)
- NEW-10 (returning a non-nil broker whose `Start()` reports config validation)
  and NEW-12 (metrics registration panic for a genuinely misconfigured custom
  registry) are kept as intentional fail-fast/validation-gate behavior.

### Review Round (2026-08-06)
- Full project review + test run: 326 unit PASS (13 Redis skips), 92 integration
  PASS, 65 benchmarks PASS; build/vet/gofmt clean. Details in
  `docs/reports/PROJECT-REVIEW-260806-121651.md`.
- Re-confirmed 11 prior fixes; 19 defects still open (incl. P1-5 offline
  persistent-session queue, P1-6 client keepalive, P2-3 inflight persistence
  wiring, P2-5 delayed-will never fires, P2-9 shared-sub offline member,
  P2-10 takeover will race, P2-13 subscription leak, P2-16 flow-control
  counter). 20 new findings recorded (notably NEW-1 outbound QoS has no retry,
  NEW-2 publish-rate race on takeover, NEW-20 default deployment /metrics 404).
- Cloud server 120.76.44.233 fully cleaned (shark-socket residue, all docker
  containers/images/volumes, 7.3GB reclaimed); native build/vet/test PASS;
  docker image build + healthz + MQTT smoke + QoS1/QoS2 round-trip PASS; k8s
  manifests + helm chart render PASS. See
  `docs/reports/DEPLOYMENT-VALIDATION-260806-124906.md`.

### V5 Audit Fixes (2026-08-06)

#### QoS / Protocol Correctness
- **QoS 1 duplicate delivery eliminated:** Incoming QoS 1 messages are no longer
  tracked in the QoS engine (there is no client acknowledgment for them), so the
  retry loop can no longer re-route the message to subscribers and duplicate
  delivery up to `maxRetries` extra times.
- **QoS 2 exactly-once preserved:** The QoS engine retry callback now re-sends
  PUBREC to the client for an incomplete handshake instead of re-routing to
  subscribers, so messages are never delivered before PUBREL.
- **MQTT 5.0 Topic Alias:** PUBLISH with an empty Topic Name is accepted when a
  non-zero Topic Alias property is present (both encode and decode), making the
  broker's advertised TopicAliasMaximum usable.

#### Broker
- **Clean session SessionPresent:** A clean-session reconnect of an existing
  client returns SessionPresent=0 (MQTT 5.0 3.2.2.2).
- **Restart support:** `Broker.Start` and `QoSEngine.Start` rebuild their context,
  so cleanup and retry loops work after a Stop->Start cycle.
- **SUBSCRIBE limit:** A request exceeding `maxTopicFiltersPerSub` returns
  `SubAckFailure` for every filter instead of falsely granting QoS 0.
- **Shared-subscription retained:** `Session.MatchesSubscription` strips the
  `$share/` prefix, so retained messages are delivered to shared subscribers.
- **Will authorization:** Will messages now respect the authorizer; a client
  cannot set a will on a topic it has no permission to publish.
- **Authentication chain fail-closed:** New `ErrUserNotFound` sentinel; the chain
  continues only for unknown users and aborts (fails closed) when a recognized
  user is rejected, so a permissive fallback cannot bypass the decision.
- **Prompt shutdown:** `MQTTServer.Stop` closes client connections before
  `wg.Wait()`, so shutdown no longer stalls up to 1.5x keep-alive per idle
  connection.

#### Client
- **Inbound QoS 2:** `readLoop` now handles PUBREL by sending PUBCOMP and clearing
  the duplicate-tracking entry, completing the inbound QoS 2 handshake.

#### CI & Test
- All regression tests added pass under `go test -race`.
- Known environment note: the full Windows test suite can hit transient
  WSAEADDRINUSE dial errors when the OS ephemeral port pool is exhausted by
  accumulated TIME_WAIT sockets (not a code defect). Serializing packages
  (`-p=1`) or allowing TIME_WAIT to drain resolves it.
