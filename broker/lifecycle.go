package broker

// Broker lifecycle: start, the internal cleanup loops, drain and stop.
//
// The ordering here is deliberate and load-bearing. Plugins start before
// EventBrokerStarted is announced, because a plugin's Start may depend on the
// broker being fully initialised; they stop after EventBrokerStopped, because a
// plugin's Stop may still need to read session state.

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/X1aSheng/shark-mqtt/plugin"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

func (b *Broker) Start() error {
	if b.started.Swap(true) {
		return fmt.Errorf("broker already started")
	}
	// Rebuild the context so cleanup loops actually run after a Stop->Start
	// cycle (Stop cancels the previous context).
	b.ctx, b.cancel = context.WithCancel(context.Background())
	b.shuttingDown.Store(false) // a Stop->Start cycle is not a shutdown any more
	b.drained.Store(false)      // the next shutdown gets its own drain window
	b.startedAt = time.Now()
	b.qos.Start()
	b.startLoop(b.sessionCleanupLoop)
	if b.retainedStore != nil {
		// Rebuild the in-memory expiry map from persisted retained messages so
		// TTL cleanup survives a restart (P3-5), and so a retained message
		// carrying its own Message Expiry Interval is reaped even when no
		// operator TTL is configured.
		b.rebuildRetainedExpirations()
		b.startLoop(b.retainedCleanupLoop)
	}
	if b.opts.sysInterval > 0 {
		b.startLoop(b.sysStatusLoop)
	}

	// Bring plugins up before announcing readiness: a plugin whose Start
	// depends on the broker being fully initialized must not observe
	// EventBrokerStarted first.
	if err := b.pluginMgr.StartPlugins(b.ctx); err != nil {
		// A failing plugin is reported but does not abort startup -- the broker
		// itself is healthy and refusing to serve because an extension is
		// broken would turn a small fault into an outage.
		b.logger.Warn("some plugins failed to start", "error", err)
		b.metrics.IncErrors("plugin_start")
	}
	b.publishEvent(b.ctx, plugin.EventBrokerStarted, plugin.EventData{}, "", nil)
	return nil
}

func (b *Broker) startLoop(fn func()) {
	b.loops.Add(1)
	go func() {
		defer b.loops.Done()
		fn()
	}()
}

func (b *Broker) rebuildRetainedExpirations() {
	if b.retainedStore == nil {
		return
	}
	msgs, err := b.retainedStore.MatchRetained(b.ctx, "#")
	if err != nil {
		b.logger.Warn("failed to list retained messages on start", "error", err)
		return
	}
	b.retainedMu.Lock()
	b.retainedExpirations = make(map[string]time.Time)
	b.retainedCount.Store(0)
	for _, m := range msgs {
		base := m.Timestamp
		if base.IsZero() {
			base = time.Now()
		}
		// Two independent bounds: the operator's retained TTL, and the
		// publisher's own Message Expiry Interval, which is stored as an
		// absolute deadline. Whichever comes first wins, so a restart cannot
		// extend a message's life past the deadline the publisher asked for.
		var deadline time.Time
		if b.opts.retainedExpiry > 0 {
			deadline = base.Add(b.opts.retainedExpiry)
		}
		if !m.ExpiresAt.IsZero() && (deadline.IsZero() || m.ExpiresAt.Before(deadline)) {
			deadline = m.ExpiresAt
		}
		if !deadline.IsZero() {
			b.retainedExpirations[m.Topic] = deadline
		}
		b.retainedCount.Add(1)
	}
	b.retainedMu.Unlock()
	b.metrics.SetRetainedMessages(int(b.retainedCount.Load()))
}

func (b *Broker) sessionCleanupLoop() {
	ticker := time.NewTicker(b.opts.sessionCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			b.cleanupExpiredSessions()
		}
	}
}
func (b *Broker) cleanupExpiredSessions() {
	if b.sessionStore == nil {
		return
	}

	clientIDs, err := b.sessionStore.ListSessions(b.ctx)
	if err != nil {
		b.logger.Warn("failed to list sessions for cleanup", "error", err)
		return
	}

	now := time.Now()
	for _, clientID := range clientIDs {
		// Connected clients: keep their storage key alive on TTL backends
		// (Redis) so a live session can never expire under the broker and
		// then silently disappear after a crash (audit).
		b.mu.RLock()
		_, connected := b.connections[clientID]
		b.mu.RUnlock()
		if connected {
			if err := b.sessionStore.RefreshSession(b.ctx, clientID, b.opts.sessionExpiry); err != nil {
				b.logger.Debug("failed to refresh session TTL", "clientID", clientID, "error", err)
			}
			continue
		}

		data, err := b.sessionStore.GetSession(b.ctx, clientID)
		if err != nil {
			continue
		}

		if data.ExpiryInterval > 0 && !data.ExpiryTime.IsZero() && now.After(data.ExpiryTime) {
			if err := b.sessionStore.DeleteSession(b.ctx, clientID); err != nil {
				b.logger.Warn("failed to delete expired session", "clientID", clientID, "error", err)
			} else {
				// Cascade: release the expired session's offline message queue
				// as well, otherwise messages outlive their session (memory
				// leak / stale replay into a same-named future session) —
				// audit H2/H3.
				if b.messageStore != nil {
					if err := b.messageStore.ClearMessages(b.ctx, clientID); err != nil {
						b.logger.Warn("failed to clear messages of expired session", "clientID", clientID, "error", err)
						b.metrics.IncErrors("message_store")
					} else {
						b.offlineMu.Lock()
						delete(b.offlineCount, clientID)
						b.offlineMu.Unlock()
					}
				}
				// Release the expired session's topic-tree subscriptions so
				// they do not leak after the session is gone (P2-13).
				topics := make([]string, 0, len(data.Subscriptions))
				for _, sub := range data.Subscriptions {
					topics = append(topics, sub.Topic)
				}
				b.unsubscribeTopics(clientID, topics)
				b.logger.Debug("expired session cleaned up", "clientID", clientID)
			}
		}
	}
}

func (b *Broker) retainedCleanupLoop() {
	ticker := time.NewTicker(b.opts.retainedCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			b.cleanupExpiredRetained()
		}
	}
}
func (b *Broker) cleanupExpiredRetained() {
	// Only the store matters here. An operator TTL is folded into the recorded
	// deadline when the retained message is written (see handleRetainedMessage),
	// and a retained message carrying its own MQTT 5.0 Message Expiry Interval
	// records a deadline even when no operator TTL is configured. Gating this
	// sweep on retainedExpiry > 0 therefore meant those per-message expiries
	// were never reaped under the default configuration: they accumulated until
	// maxRetainedTopics was full, after which every new retained message was
	// dropped.
	if b.retainedStore == nil {
		return
	}

	// The sweep collects candidates under the lock and deletes outside it: one
	// DeleteRetained is a Redis round trip or a Badger fsync, and holding
	// retainedMu across the whole batch froze every retained publish and every
	// management read for the duration of the slowest delete.
	now := time.Now()
	var expired []string
	b.retainedMu.Lock()
	for topic, expiry := range b.retainedExpirations {
		if now.After(expiry) {
			expired = append(expired, topic)
		}
	}
	b.retainedMu.Unlock()

	removed := 0
	for _, topic := range expired {
		// Same stripe a publisher of this topic takes, so a republish that
		// refreshed the deadline between the scan above and here is not deleted
		// out from under its new owner. Lock order (stripe then retainedMu)
		// matches handleRetainedMessage.
		stripe := &b.retainedStripes[retainedStripeIndex(topic)]
		stripe.Lock()

		b.retainedMu.Lock()
		cur, tracked := b.retainedExpirations[topic]
		b.retainedMu.Unlock()
		if !tracked || !now.After(cur) {
			stripe.Unlock()
			continue // refreshed or already gone
		}

		err := b.retainedStore.DeleteRetained(b.ctx, topic)
		if err == nil {
			b.retainedMu.Lock()
			delete(b.retainedExpirations, topic)
			b.retainedMu.Unlock()
		}
		stripe.Unlock()

		if err != nil {
			b.logger.Warn("failed to delete expired retained message", "topic", topic, "error", err)
			b.metrics.IncErrors("retained_store")
			continue
		}
		b.retainedCount.Add(-1)
		removed++
		b.logger.Debug("expired retained message cleaned up", "topic", topic)
	}
	if removed > 0 {
		b.metrics.SetRetainedMessages(int(b.retainedCount.Load()))
	}
}

// sysStatusLoop publishes the $SYS tree on a ticker until the broker stops.
func (b *Broker) sysStatusLoop() {
	b.publishSystemStatus()
	ticker := time.NewTicker(b.opts.sysInterval)
	defer ticker.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			b.publishSystemStatus()
		}
	}
}

func (b *Broker) publishSystemStatus() {
	b.mu.RLock()
	conns := len(b.connections)
	b.mu.RUnlock()

	b.publishSystem("$SYS/broker/version", []byte(b.opts.version))
	b.publishSystem("$SYS/broker/uptime", []byte(time.Since(b.startedAt).Round(time.Second).String()))
	b.publishSystem("$SYS/broker/connections", []byte(strconv.Itoa(conns)))
	b.publishSystem("$SYS/broker/retained", []byte(strconv.FormatInt(b.retainedCount.Load(), 10)))
	b.publishSystem("$SYS/broker/subscriptions", []byte(strconv.FormatInt(b.topics.SubscriberCount(), 10)))
}

func (b *Broker) publishSystem(topic string, payload []byte) {
	pkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
		Topic:       topic,
		Payload:     payload,
	}
	for _, sub := range b.topics.Match(topic) {
		b.deliverToClient(b.ctx, sub.ClientID, "$SYS", pkt)
	}
}

func (b *Broker) BeginShutdown() {
	b.shuttingDown.Store(true)
	b.notifyShuttingDown()
}

func (b *Broker) notifyShuttingDown() {
	b.mu.RLock()
	conns := make([]*clientState, 0, len(b.connections))
	for _, cs := range b.connections {
		conns = append(conns, cs)
	}
	b.mu.RUnlock()

	notified := false
	for _, cs := range conns {
		if cs.out == nil || !cs.codec.Spec().ReasonCodes {
			continue
		}
		pkt := &protocol.DisconnectPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeDisconnect},
			ReasonCode:  protocol.DisconnectServerShuttingDown,
		}
		select {
		case cs.out <- pkt:
			notified = true
		default:
		}
	}
	// Only wait when something was actually queued: a shutdown with no clients
	// (or no MQTT 5.0 clients) must not pay the delay.
	if notified {
		time.Sleep(shutdownNotifyDelay)
	}
}

func (b *Broker) Stop() {
	b.cancel()

	// 0. Drain the background loops before touching anything else. Start
	// rewrites b.ctx and b.startedAt, so a loop still reading them would race
	// with the next Start of a Stop -> Start cycle. (Server-level connection
	// goroutines are already joined by MQTTServer.Stop's WaitGroup before this
	// runs; these three loops are the only ones the broker owns.)
	b.loops.Wait()

	// 1. Suppress wills and tell clients the server is going away.
	b.BeginShutdown()

	// 2. Drain in-flight messages BEFORE closing the sockets. Acknowledgments
	// (PUBACK/PUBREC) can only arrive on a live connection, so closing first
	// guaranteed the old wait below ran to its deadline no matter how much was
	// in flight -- it was pure dead time. Draining first lets a well-behaved
	// client finish its QoS 1/2 handshake and lets the count reach zero for
	// real.
	//
	// b.shuttingDown is already set (step 1), so readLoops stop taking new work
	// and wills stay suppressed throughout.
	b.drainInflight(b.opts.drainTimeout)

	// 3. Close all client connections to stop the readLoops for good.
	//
	// The registry is deliberately NOT detached first. Every readLoop calls
	// disconnect() when its socket returns, and disconnect() refuses to clean up
	// unless the conn is still the one registered for that client id. Detaching
	// the map here made every one of those identity checks fail, so Stop left
	// the session map, the topic tree and the QoS table populated with entries
	// from the run that had just ended: a following Start reported those clients
	// as online, shared subscriptions routed messages to sessions with no
	// connection, and a non-clean reconnect was told SessionPresent=1 for state
	// nobody maintained.
	//
	// Closing still happens outside b.mu: conn.Close() is a syscall, and doing N
	// of them under the lock serializes every concurrent registration behind the
	// slowest close.
	b.mu.RLock()
	conns := make([]net.Conn, 0, len(b.connections))
	for _, cs := range b.connections {
		conns = append(conns, cs.conn)
	}
	b.mu.RUnlock()

	for _, c := range conns {
		if err := c.Close(); err != nil {
			b.logger.Debug("failed to close client connection", "error", err)
		}
	}

	// Give the readLoops a bounded window to finish their own teardown. The
	// window is short by design: a readLoop notices the closed socket within
	// microseconds, and this is already the second wait of the shutdown
	// (drainInflight was the first). Anything still registered afterwards is
	// taken out below rather than waited on -- a clientState built without a
	// readLoop (unit tests, embedders that drive the broker directly) would
	// otherwise burn the whole window on every Stop.
	stopWait := 200 * time.Millisecond
	deadline := time.Now().Add(stopWait)
	for time.Now().Before(deadline) {
		b.mu.RLock()
		remaining := len(b.connections)
		b.mu.RUnlock()
		if remaining == 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Safety net for connections that did not deregister themselves: remove them
	// from the registry and publish the writer stop signal, so no goroutine is
	// left waiting on a queue nobody drains.
	type pendingClose struct {
		id      string
		cs      *clientState
		hasStop bool
	}
	b.mu.Lock()
	pending := make([]pendingClose, 0, len(b.connections))
	for id, cs := range b.connections {
		pending = append(pending, pendingClose{id: id, cs: cs, hasStop: cs.out != nil})
		delete(b.connections, id)
	}
	b.mu.Unlock()

	for _, p := range pending {
		b.logger.Debug("connection still registered after the shutdown window", "clientID", p.id)
		if p.hasStop {
			p.cs.stopWriter()
		}
	}

	b.qos.Stop()
	b.will.Stop()
	// Stop the flapping cleanup goroutine before plugins, so it cannot outlive
	// the broker or race with plugin teardown.
	if b.flapping != nil {
		b.flapping.Stop()
	}

	// Tear plugins down last, while the broker's subsystems are still usable:
	// a plugin's Stop may need to read session state or talk to the client.
	b.publishEvent(b.ctx, plugin.EventBrokerStopped, plugin.EventData{}, "", nil)
	if err := b.pluginMgr.StopPlugins(); err != nil {
		b.logger.Warn("some plugins failed to stop cleanly", "error", err)
		b.metrics.IncErrors("plugin_stop")
	}

	b.started.Store(false) // allow re-Start after Stop
}

func (b *Broker) DrainInflight() {
	b.drainInflight(b.opts.drainTimeout)
}

func (b *Broker) drainInflight(timeout time.Duration) {
	if b.drained.Swap(true) {
		return // already drained by an embedder-level call
	}
	if timeout < 0 {
		return
	}
	if timeout == 0 {
		// Still report what is being abandoned, then do not wait.
		if n := b.totalInflight(); n > 0 {
			b.logger.Warn("drain skipped, abandoning in-flight messages", "count", n)
			b.metrics.IncErrors("drain_skipped")
		}
		return
	}

	// Poll rather than block on an event: the count is owned by the QoS engine
	// and has no notification channel, and 100ms is far below the latency a
	// shutdown budget is measured in.
	const pollInterval = 100 * time.Millisecond
	deadline := time.Now().Add(timeout)
	for {
		n := b.totalInflight()
		if n == 0 {
			return
		}
		if !time.Now().Before(deadline) {
			b.logger.Warn("drain timeout, abandoning in-flight messages",
				"count", n, "timeout", timeout)
			b.metrics.IncErrors("drain_timeout")
			return
		}
		time.Sleep(pollInterval)
	}
}

func (b *Broker) totalInflight() int {
	total := 0
	for _, clientID := range b.sessions.ListSessions() {
		total += b.qos.InflightCount(clientID)
	}
	return total
}

func (b *Broker) teardownCtx() (context.Context, context.CancelFunc) {
	return context.WithCancel(b.ctx)
}
