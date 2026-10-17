# Concurrency & Locking Order

> This document lists every lock in the broker and the ordering rules that
> keep the system deadlock-free. Additions to the codebase that introduce a
> new lock must update this document.

## Lock inventory

| Lock | Owner | Protects |
| --- | --- | --- |
| `b.mu` (RWMutex) | `Broker` | `connections` map, connection registration/takeover/cleanup |
| `b.receivedQoS2Mu` | `Broker` | per-client inbound QoS 2 duplicate-detection map |
| `b.retainedMu` | `Broker` | retained count + retained-expiry map |
| `b.topics.mu` (RWMutex) | `TopicTree` | trie structure + per-topic subscriber maps |
| `b.topics.sharedSubsMu` | `TopicTree` | shared-subscription membership maps |
| `b.topics.sharedLastMu` | `TopicTree` | round-robin "last selected" per share group |
| `m.mu` (RWMutex) | `Manager` | session map |
| `s.mu` (RWMutex) | `Session` | session fields, subscriptions, inflight, outbound queue |
| `q.mu` (RWMutex) | `QoSEngine` | inflight map + retry state |
| `wh.mu` | `WillHandler` | wills map + delayed-will cancels |
| `cs.wmu` | `clientState` | synchronous socket writes (no write queue) |
| `api.healthMu` | `api.Broker` | `healthSrv` / `healthLn` / `healthAddr` (health + metrics listener lifecycle) |
| client locks | `MQTTClient` | `mu`/`wmu`/`inflightMu`/`pendingMu`/`msgMu` (client package, independent) |

## Ordering rules

1. **`b.mu` is the top-level broker lock.** Connection registration,
   takeover, and cleanup hold `b.mu` and may acquire `b.topics.mu`,
   `sess.mu`, or `b.receivedQoS2Mu` while holding it - never the reverse.

2. **Heavy work happens outside locks.** Session persistence
   (`Session.Save`) and message-store writes are executed before/after the
   critical section, never while holding `b.mu`. This is why `disconnect`
   saves the session *before* taking `b.mu`.

3. **Callbacks run lock-free.** QoS engine and will callbacks
   (`republish`, `sendPubAck`, `publishWill`) are invoked after the engine
   releases its lock; they acquire broker locks in normal order. Never call a
   broker method while holding `q.mu` (would invert `b.mu -> q.mu`).

4. **Session takeover is identity-checked.** `disconnect` verifies
   `cs.conn == conn` under `b.mu` before removing state, so a stale readLoop
   from a taken-over connection can never delete the new connection's entry.

5. **Per-connection write queue.** `writePacket` looks up `connections`
   under `b.mu.RLock` and enqueues without holding the lock; producers select
   on `stopWrites` to avoid sending to a torn-down queue.

6. **Goroutines own the state they were started with.** A background goroutine
   must read only values captured before it was launched, never a mutable field
   that a later lifecycle call can reassign. `api.startHealthServer` is the
   reference case: it serves its own captured `*http.Server` and `net.Listener`
   rather than the `Broker.healthSrv` field, because `Start` -> `Stop` -> `Start`
   reassigns that field while the previous goroutine may still be running. The
   same rule is what keeps `broker.MQTTServer` restartable.

7. **A stop must release what it owns, not only what is already registered.**
   `api.Broker.Stop` closes the health listener directly; `http.Server.Shutdown`
   alone only closes listeners `Serve` has registered, so relying on it leaks
   the port when `Stop` wins the race against the first `Serve`.

8. **A takeover DISCONNECT is written before `b.mu` is taken.** Replacing a
   connection's map entry has to tell the displaced peer why
   ([MQTT-3.1.4-3]), and that write is confirmed rather than enqueued
   (`writeFinal`, bounded by `finalWriteTimeout`) so the close cannot overtake
   it. It therefore runs with no broker lock held. Moving it inside the `b.mu`
   critical section would hold the top-level broker lock across a socket
   write; resolving the connection by ClientID inside that section would
   instead race the entry the section is about to install, and the DISCONNECT
   could land on the incoming connection.

## Deadlock audit (2026-08-31)

- `b.mu -> b.topics.mu` / `b.mu -> sess.mu` / `b.mu -> receivedQoS2Mu`: one-way,
  no cycle.
- `TopicTree` acquires `sharedSubsMu` then `sharedLastMu`; `UnsubscribeShared`
  holds `sharedSubsMu` and takes `sharedLastMu` - consistent order.
- `QoSEngine.doRetry` collects state under `q.mu`, releases, then invokes
  callbacks (rule 3).
- `WillHandler` never takes broker locks while holding `wh.mu`; delayed-will
  goroutines call `publishWill` (-> `publishWill` -> broker) without `wh.mu`.
- `Session.Save` snapshots under `s.mu.RLock`, deep-copies payloads after
  releasing it (no large copy under lock).

## Zombie-connection detection (MQTT link reliability)

An MQTT link can become a "phantom"/zombie connection: the OS socket stays
ESTABLISHED while the peer is dead or unreachable (network cut, powered-off
device, crashed process without a FIN). The broker must not treat such
connections as online. Detection layers:

| Layer | Mechanism | Coverage |
| --- | --- | --- |
| MQTT keep-alive | Read deadline refreshed to 1.5x KeepAlive after every packet; a timeout tears the session down (`abnormalDisconnect`), logged at info and counted as `rejections{reason="keepalive_timeout"}` | Clients with KeepAlive > 0 |
| OS TCP keep-alive | `tcp_keepalive_period` (default 60s) applies `SetKeepAlivePeriod` on accepted connections | Clients with KeepAlive = 0 (deadline disabled) |
| CONNECT deadline | 10s read deadline during the handshake | Half-open connections that never send CONNECT |
| Write-failure reap | A failed encode/write in `writePacket` or `writeLoop` closes the socket immediately, unblocking the reader | Peers that close with RST or become unreachable |

Design rule: **any socket write error closes the connection** - a peer that
cannot receive is considered dead, and lingering state (session, subscriptions,
write queue) is torn down promptly rather than waiting for a keep-alive
deadline.
