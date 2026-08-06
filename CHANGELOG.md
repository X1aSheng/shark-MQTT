# Changelog

All notable changes for `shark-mqtt` are recorded here.

This project uses semantic versioning. Pre-release tags use the form
`vMAJOR.MINOR.PATCH-rc.N`.

## Unreleased

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
