# Changelog

All notable changes for `shark-mqtt` are recorded here.

This project uses semantic versioning. Pre-release tags use the form
`vMAJOR.MINOR.PATCH-rc.N`.

## Unreleased

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
