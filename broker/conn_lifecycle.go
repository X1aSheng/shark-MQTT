package broker

// Connection establishment: the handshake from accept through CONNECT to a
// registered session, the rejection paths, and the disconnect teardown.
//
// Everything here runs on the connection's own goroutine.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/X1aSheng/shark-mqtt/plugin"
	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store"
)

// reserveConnectionSlot reserves one connection slot against maxConnections.
//
// A limit of zero or less means unlimited: nothing is reserved, and
// releaseConnectionSlot is then a no-op as well.
func (b *Broker) reserveConnectionSlot() bool {
	if b.opts.maxConnections <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.connections)+b.pendingConns >= b.opts.maxConnections {
		return false
	}
	b.pendingConns++
	return true
}

// releaseConnectionSlot gives back an unused reservation. It is safe to call
// after the connection was registered: registration consumes the reservation.
func (b *Broker) releaseConnectionSlot() {
	if b.opts.maxConnections <= 0 {
		return
	}
	b.mu.Lock()
	if b.pendingConns > 0 {
		b.pendingConns--
	}
	b.mu.Unlock()
}

// connAckForConnectRejection maps a ValidateConnect failure to the CONNACK
// return/reason code. MQTT 5.0 uses 0x82 Protocol Error; MQTT 3.1.1 uses the
// 0x01 Unacceptable Protocol Version code; MQTT 3.1 reports a ClientID rule
// violation with 0x02 Identifier Rejected.
func connAckForConnectRejection(sp protocol.Spec, connectPkt *protocol.ConnectPacket) byte {
	if sp.ConnAckHasReasonCode {
		return protocol.ConnAckProtocolError
	}
	emptyID := len(connectPkt.ClientID) == 0 && !sp.AllowEmptyID
	tooLongID := sp.MaxClientIDLen > 0 && len(connectPkt.ClientID) > sp.MaxClientIDLen
	if emptyID || tooLongID {
		return protocol.ConnAckIdentifierRejected
	}
	return protocol.ConnAckUnacceptableProtocol
}

// HandleConnection implements server.ConnectionHandler.
//
// It consumes a protocol-neutral Conn whose handshake has already been
// performed by the network server's protocol adapter; the broker core does not
// decode the MQTT CONNECT itself.
//
// # Context scopes
//
// Two contexts reach this package and they are NOT interchangeable:
//
//   - ctx (this parameter) is the CONNECTION scope. Every per-connection path
//     takes it as its first parameter -- readLoop, handlePublish, handleSubscribe,
//     deliverToClient, queueOfflineMessage, plugin dispatch -- so a store write
//     or authorization check started by this connection ends when the connection
//     ends. This is what a ctx-taking function should use unless it says otherwise.
//   - b.ctx is the BROKER scope, owned by Start/Stop. It belongs to the three
//     background loops (session expiry sweep, retained expiry sweep, $SYS
//     publisher) and to work that outlives its connection: the will publish
//     callback (fires from the will handler's own timer), the QoS retry republish,
//     and the connection teardown store writes (see teardownCtx).
//
// The distinction is not cosmetic: a per-connection store write issued with
// b.ctx survives its client, and a teardown write issued with the connection ctx
// is cancelled before it stores anything, losing session state that MQTT 5.0
// 3.1.2.11.2 requires the server to keep.
func (b *Broker) HandleConnection(ctx context.Context, c *Conn) error {
	conn := c.Raw
	codec := c.Codec

	// Check connection limit. The slot is reserved, not just sampled: the rest
	// of CONNECT (authorization, session restore, store writes) takes long
	// enough that a concurrent burst would otherwise all see the same count and
	// all be admitted. The reservation is released below unless the connection
	// actually reaches the registry.
	if !b.reserveConnectionSlot() {
		b.metrics.IncRejections("max_connections")
		_ = conn.Close()
		return fmt.Errorf("broker: max connections (%d) reached", b.opts.maxConnections)
	}
	registered := false
	defer func() {
		if !registered {
			b.releaseConnectionSlot()
		}
	}()

	// Check connection rate limit
	if b.opts.maxConnRate > 0 {
		b.connRate.SetRate(b.opts.maxConnRate)
		if !b.connRate.Allow() {
			b.metrics.IncRejections("rate_limited")
			b.metrics.IncErrors("rate_limit")
			b.logger.Debug("connection rate limit exceeded",
				"remote", conn.RemoteAddr().String())
			b.sendConnAckRaw(conn, codec, protocol.ReasonCodeConnectionRateExceeded, false)
			_ = conn.Close()
			return fmt.Errorf("broker: connection rate limit exceeded")
		}
	}

	// Plugin hook: OnAccept
	b.dispatch(ctx, plugin.OnAccept, &plugin.Context{
		RemoteAddr: conn.RemoteAddr().String(),
	})

	// The adapter projects the decoded CONNECT onto ConnectInfo and preserves
	// the original *protocol.ConnectPacket in Extensions for version-specific
	// data (Properties). The MQTT semantics below still speak protocol.Packet.
	connectPkt, ok := c.Info.Extensions.(*protocol.ConnectPacket)
	if !ok || connectPkt == nil {
		b.metrics.IncRejections("invalid_packet")
		b.metrics.IncErrors("protocol")
		return fmt.Errorf("broker: connection carries no MQTT CONNECT info")
	}
	sp := c.Spec
	if sp.Version == 0 {
		sp, _ = protocol.SpecOf(connectPkt.ProtocolVersion)
	}

	// Push the client's Maximum Packet Size into the codec (MQTT 5.0
	// §3.1.2.11.4) before any CONNACK is produced, so every packet this
	// connection sends -- the rejection CONNACKs below included -- is bounded by
	// it. The property exists only in MQTT 5, so a 3.1.1 CONNECT leaves the codec
	// unbounded; the task is skipped entirely for an adapter whose codec cannot
	// enforce a bound.
	if connectPkt.Properties != nil && connectPkt.Properties.MaximumPacketSize != nil {
		if sizer, ok := codec.(protocol.OutboundPacketSizer); ok {
			sizer.SetOutboundMaxPacketSize(int(*connectPkt.Properties.MaximumPacketSize))
		}
	}

	// Validate CONNECT per MQTT spec. The CONNECT packet was already decoded by
	// the protocol adapter's handshake; validation (protocol name/version,
	// flags, ClientID rules) stays here so the rejection CONNACK, rejection
	// metrics and logs are identical to the pre-refactor behavior.
	if err := protocol.ValidateConnect(connectPkt); err != nil {
		b.metrics.IncRejections("invalid_connect")
		b.metrics.IncErrors("protocol")
		b.sendConnAckRaw(conn, codec, connAckForConnectRejection(sp, connectPkt), false)
		return fmt.Errorf("broker: CONNECT validation failed: %w", err)
	}

	// Connection flapping check, after the CONNECT is known to be well formed
	// but before the client is authenticated or registered.
	//
	// Position matters: doing it later would let a flapping client keep paying
	// for bcrypt verification on every attempt, which is precisely the cost the
	// limit exists to stop. Doing it before ValidateConnect would let a
	// malformed packet from an unpenalised key consume a slot.
	if d := b.flapping.Check(peerIP(conn.RemoteAddr().String()), connectPkt.ClientID); d != FlapAllow {
		b.rejectFlapping(conn, codec, sp, connectPkt, d)
		return fmt.Errorf("broker: connection flapping limit exceeded")
	}

	// Check client ID length limit to prevent resource exhaustion
	if b.opts.maxClientIDLength > 0 && len(connectPkt.ClientID) > b.opts.maxClientIDLength {
		b.metrics.IncRejections("client_id_too_long")
		b.metrics.IncErrors("protocol")
		var reasonCode byte
		if sp.ConnAckHasReasonCode {
			// MQTT 5.0 CONNACK reason for an invalid client identifier
			// (audit: 0x82 Protocol Error was not descriptive).
			reasonCode = protocol.ConnAckClientIdentifierNotValid
		} else {
			reasonCode = protocol.ConnAckIdentifierRejected
		}
		b.sendConnAckRaw(conn, codec, reasonCode, false)
		return fmt.Errorf("broker: client ID %d bytes exceeds max %d bytes",
			len(connectPkt.ClientID), b.opts.maxClientIDLength)
	}

	// Clear the handshake read deadline left by the adapter so the per-client
	// keep-alive deadline governs the read loop.
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		b.logger.Debug("failed to clear read deadline", "error", err)
	}

	// MQTT 5.0 enhanced authentication (§4.12): a CONNECT carrying an
	// AuthenticationMethod runs the enhanced auth exchange (AUTH packets)
	// instead of the traditional username/password path. The handshake sends
	// AUTH packets only; the final CONNACK 0x00 is sent later, once the session
	// is set up.
	var enhancedAuth EnhancedAuthenticator
	if connectPkt.Properties != nil && connectPkt.Properties.AuthenticationMethod != "" {
		enhancedAuth = b.findEnhancedAuth(connectPkt.Properties.AuthenticationMethod)
		if enhancedAuth == nil {
			b.metrics.IncAuthFailures()
			b.logger.Warn("connection rejected: unsupported authentication method",
				"clientID", connectPkt.ClientID,
				"method", connectPkt.Properties.AuthenticationMethod,
				"remote", conn.RemoteAddr().String())
			b.sendConnAckRaw(conn, codec, protocol.ReasonCodeBadAuthMethod, false)
			return fmt.Errorf("broker: unsupported authentication method %q", connectPkt.Properties.AuthenticationMethod)
		}
		if err := b.enhancedAuthHandshake(conn, codec, connectPkt, enhancedAuth); err != nil {
			b.metrics.IncAuthFailures()
			b.logger.Debug("enhanced auth failed", "clientID", connectPkt.ClientID, "error", err)
			return err
		}
	} else if b.opts.authenticator != nil {
		authErr := b.opts.authenticator.Authenticate(ctx, connectPkt.ClientID, connectPkt.Username, string(connectPkt.Password))
		if authErr != nil {
			b.metrics.IncAuthFailures()
			// Log the rejection with enough context to investigate (client ID,
			// username, peer) but never the credential itself. Without this the
			// default `info` level shows nothing when clients fail to
			// authenticate: the returned error is only logged at debug by the
			// server, so operators saw a metric with no audit trail.
			b.logger.Warn("authentication failed",
				"clientID", connectPkt.ClientID,
				"username", connectPkt.Username,
				"remote", conn.RemoteAddr().String(),
				"error", authErr)
			// MQTT 3.1.1 CONNACK carries 0x04; MQTT 5.0 defines the same
			// condition as 0x86 (Bad user name or password). Sending the
			// v3 code to a v5 client used to hand it an undefined reason
			// code (audit).
			reasonCode := byte(protocol.ConnAckBadUsernameOrPassword)
			if sp.ConnAckHasReasonCode {
				reasonCode = protocol.ConnAckBadUsernameOrPassword5
			}
			b.sendConnAckRaw(conn, codec, reasonCode, false)
			return fmt.Errorf("broker: auth failed: %w", authErr)
		}
	}

	if connectPkt.Flags.WillFlag && !protocol.ValidatePublishTopic(connectPkt.WillTopic) {
		b.sendConnAckRaw(conn, codec, protocol.ConnAckUnspecifiedError, false)
		return fmt.Errorf("broker: will topic %q contains wildcards", connectPkt.WillTopic)
	}

	// Bind a clientID to its owning username (audit H6). A session — with its
	// subscriptions, inflight state and queued offline messages — belongs to
	// the user that created it. Connecting with a different username to an
	// existing session (in memory or in the store) would otherwise let any
	// authenticated user hijack another user's session state (or wipe it with
	// a clean start), so the attempt is rejected with a not-authorized
	// CONNACK and the original session is left untouched.
	//
	// The stored record is kept in storedSession for the clean-start teardown
	// below, which needs its filters to release the topic-tree entries. This
	// is the only read of it on the connect path: reading it a second time
	// there would double the cost of the most common CONNECT there is.
	var storedSession *store.SessionData
	if connectPkt.ClientID != "" {
		if existing, ok := b.sessions.GetSession(connectPkt.ClientID); ok && existing.Username != connectPkt.Username {
			return b.rejectSessionOwnerMismatch(conn, codec, sp, connectPkt)
		}
		if b.sessionStore != nil {
			data, err := b.sessionStore.GetSession(ctx, connectPkt.ClientID)
			switch {
			case err != nil:
				if !errors.Is(err, store.ErrSessionNotFound) {
					b.logger.Debug("session store check failed", "clientID", connectPkt.ClientID, "error", err)
				}
			case data == nil:
				// Nothing stored: no owner to mismatch, nothing to tear down.
			case data.Username != connectPkt.Username:
				return b.rejectSessionOwnerMismatch(conn, codec, sp, connectPkt)
			default:
				storedSession = data
			}
		}
	}

	// Allocate assigned client ID if empty (MQTT 5.0 §3.1.3.6).
	// Must happen before session creation so the session uses the assigned ID.
	var assignedClientID string
	if len(connectPkt.ClientID) == 0 {
		assignedClientID = fmt.Sprintf("shark-%x", time.Now().UnixNano())
		connectPkt.ClientID = assignedClientID
	}

	// Tell the QoS engine which retransmission policy this connection is under
	// before anything is tracked for it. This has to happen before the resume
	// block below, because restoreSessionInflight re-registers the resumed
	// session's deliveries and a 5.0 session must never be retried in
	// connection (MQTT 5.0 §4.4 [MQTT-4.4.0-1]); a version recorded afterwards
	// would leave those entries under the 3.1.1 default.
	b.qos.SetClientVersion(connectPkt.ClientID, connectPkt.ProtocolVersion)

	// Create or resume session. Check in-memory first, then persistent store.
	// A clean session must never report SessionPresent=1 (MQTT 5.0 §3.2.2.2
	// / 3.1.1 §3.2.2.1).
	isResuming := !connectPkt.Flags.CleanSession && b.sessions.SessionExists(connectPkt.ClientID)
	if !isResuming && !connectPkt.Flags.CleanSession && b.sessionStore != nil {
		if exists, storeErr := b.sessionStore.IsSessionExists(ctx, connectPkt.ClientID); exists {
			restored, err := b.sessions.Restore(ctx, connectPkt.ClientID)
			if err == nil && restored != nil {
				isResuming = true
				for topic, qos := range restored.Subscriptions {
					// A restored filter may be a shared subscription. Those live
					// in TopicTree.sharedSubs, not in the trie: re-inserting
					// "$share/g/a" through Subscribe would park it under a
					// literal "$share" node that never matches a real topic, so
					// the subscription silently stopped receiving anything and
					// left an entry UnsubscribeShared could not remove.
					if IsSharedSubscription(topic) {
						group, filter, ok := ParseSharedFilter(topic)
						if !ok {
							b.logger.Debug("skipping malformed shared filter in restored session",
								"clientID", connectPkt.ClientID, "filter", topic)
							continue
						}
						b.topics.SubscribeShared(group, filter, connectPkt.ClientID, qos)
						continue
					}
					b.topics.Subscribe(topic, connectPkt.ClientID, qos)
				}
				b.restoreSessionInflight(connectPkt.ClientID, restored)
			}
		} else if storeErr != nil {
			b.logger.Debug("session store check failed, treating as new", "clientID", connectPkt.ClientID, "error", storeErr)
		}
	}
	sess := b.sessions.CreateSession(connectPkt.ClientID, connectPkt, isResuming)
	clientID := connectPkt.ClientID

	// The client may request Response Information in the CONNACK (MQTT 5.0
	// §3.2.2.3.8).
	if connectPkt.Properties != nil && connectPkt.Properties.RequestResponseInfo != nil && *connectPkt.Properties.RequestResponseInfo == 1 {
		sess.RequestResponseInfo = true
	}

	// Reconnecting to an existing session cancels any pending delayed will
	// (MQTT 5.0 §3.1.2.5): the session is no longer being abandoned.
	if isResuming {
		b.will.CancelWill(clientID)
	}

	// A clean-session connect discards any previously stored session and its
	// queued offline messages (MQTT session state is not carried over). Store
	// failures here must not go unnoticed: a silently surviving old session
	// could resurface on a later non-clean reconnect (audit).
	//
	// The client's topic-tree entries go with it, and this is a defect of its
	// own rather than a courtesy: a filter that outlives the session it
	// belonged to keeps matching PUBLISH packets for a client that has not
	// subscribed on this Network Connection, which turns a fresh start into a
	// silent continuation of the old one. Two places can hold them -- an
	// in-memory session (the old connection is still registered, so
	// CreateSession will kick it below) and the persisted one -- so both are
	// released here, before the new session takes the ClientID over.
	//
	// Store calls on this path use the connection ctx, not b.ctx: the work is
	// scoped to one CONNECT handshake and must be cancellable with that
	// connection. b.ctx is the broker-wide lifetime and belongs to the
	// background loops (session expiry sweep, $SYS publisher) only.
	if connectPkt.Flags.CleanSession {
		if sess, ok := b.sessions.GetSession(clientID); ok {
			b.unsubscribeSessionTopics(sess)
		}
		if b.sessionStore != nil {
			if storedSession != nil {
				topics := make([]string, 0, len(storedSession.Subscriptions))
				for _, sub := range storedSession.Subscriptions {
					topics = append(topics, sub.Topic)
				}
				b.unsubscribeTopics(clientID, topics)
			}
			if err := b.sessionStore.DeleteSession(ctx, clientID); err != nil {
				b.metrics.IncErrors("session_save")
				b.logger.Warn("failed to delete old session on clean connect", "clientID", clientID, "error", err)
			}
		}
		if b.messageStore != nil {
			if err := b.messageStore.ClearMessages(ctx, clientID); err != nil {
				b.metrics.IncErrors("message_store")
				b.logger.Warn("failed to clear offline messages on clean connect", "clientID", clientID, "error", err)
			}
		}
		b.offlineMu.Lock()
		delete(b.offlineCount, clientID)
		b.offlineMu.Unlock()
	}

	if assignedClientID != "" {
		sess.AssignedClientID = assignedClientID
	}

	// Apply server-enforced publish rate limit if configured
	if b.opts.maxPublishRate > 0 {
		sess.publishRate.SetMaxRate(b.opts.maxPublishRate)
	}

	// Set the session expiry interval (MQTT 5.0 §3.1.2.11.2).
	//
	// Clean Start and Session Expiry Interval are two independent settings,
	// and collapsing them into one was the defect this replaces:
	//
	//   - Clean Start (§3.1.2.4) "specifies whether the Connection starts a
	//     new Session or is a continuation of an existing Session". It is a
	//     decision about the session that existed BEFORE this CONNECT, taken
	//     at connect time, and [MQTT-3.1.2-4] is the whole of its obligation:
	//     "the Client and Server MUST discard any existing Session and start
	//     a new Session". That discard happens above, against the stored
	//     session and its queued messages.
	//   - Session Expiry Interval (§3.1.2.11.2) is about what happens AFTER
	//     this connection closes: "The Client and Server MUST store the
	//     Session State after the Network Connection is closed if the Session
	//     Expiry Interval is greater than 0 [MQTT-3.1.2-23]".
	//
	// The spec's non-normative note draws the line by its own wording:
	// "Setting Clean Start to 1 and a Session Expiry Interval of 0, is
	// equivalent to setting CleanSession to 1 in the MQTT Specification
	// Version 3.1.1" -- an equivalence between TWO settings, not a rule that
	// Clean Start implies a zero interval. A client that sends Clean Start = 1
	// with Session Expiry Interval = 3600 is asking to drop the old session
	// now and keep the new one for an hour after it disconnects, and it is
	// owed that: this value is what the CONNACK advertises (§3.2.2.3.2: "The
	// server uses this property to inform the Client that it is using a value
	// other than that sent by the Client"), what bounds a delayed will
	// (§3.1.3.2.2), and what decides whether disconnect() stores the state.
	//
	// So the interval is derived from the client's property alone, capped at
	// the server maximum:
	//
	//   - explicit 0, or absent with Clean Start -> the session ends when the
	//     Network Connection closes. §3.1.2.11.2: "If the Session Expiry
	//     Interval is absent the value 0 is used." A pre-v5 client has no way
	//     to send the property at all, and for it the Clean Session bit is the
	//     whole story (MQTT 3.1.1 §3.1.2.7), so a clean 3.1.1 session still
	//     negotiates 0 here.
	//   - above the server maximum              -> capped to it. The server
	//     may shorten the interval and reports the value it actually uses.
	//   - otherwise                             -> the client's value.
	//
	// The absent-with-continuation case keeps the historical server default:
	// "Setting Clean Start to 0 and no Session Expiry Interval, is equivalent
	// to setting CleanSession to 0 in the MQTT Specification Version 3.1.1",
	// i.e. a session that outlives the connection, which a server with no
	// configured maximum would otherwise have to treat as "ends immediately".
	serverMax := uint32(b.opts.sessionExpiry.Seconds())
	var negotiatedExpiry uint32
	if connectPkt.Properties != nil && connectPkt.Properties.SessionExpiryInterval != nil {
		clientVal := *connectPkt.Properties.SessionExpiryInterval
		if clientVal > serverMax {
			clientVal = serverMax
		}
		negotiatedExpiry = clientVal
	} else if connectPkt.Flags.CleanSession {
		negotiatedExpiry = 0 // session ends when the connection closes
	} else {
		negotiatedExpiry = serverMax // absent property: server default
	}
	// Written under the session lock: the session is already in the manager,
	// and Session.Save snapshots these fields from another goroutine.
	sess.mu.Lock()
	sess.ExpiryInterval = negotiatedExpiry
	sess.mu.Unlock()

	// Negotiate Receive Maximum (MQTT 5.0 §3.1.2.11.6 / §3.2.2.3.3).
	//
	// Property identifier 0x21 names two independent limits that point in
	// opposite directions, and conflating them was a defect. The CONNECT value
	// is how many QoS 1/2 publishes the CLIENT will hold unacknowledged from
	// the server; the CONNACK value is how many the SERVER will hold
	// unacknowledged from the client. The broker kept min(client, server) in
	// one field and used it for both, so a client that declared 1 -- asking to
	// receive one concurrent publish -- was told the server would accept only
	// one from it, which is a false statement about the server's capacity.
	//
	// The two assignments below are the whole fix: what is advertised and
	// enforced inbound is the server's own figure, and the outbound window is
	// the client's, capped by the server's policy.
	//
	// Both are per-Network-Connection, not session state (§3.1.2.11.6), so a
	// resumed session re-negotiates them here rather than restoring them.
	serverReceiveMax := uint16(b.opts.maxInflight)
	if serverReceiveMax == 0 {
		serverReceiveMax = 65535 // not configured: unbounded inbound
	}
	clientReceiveMax := uint16(65535)
	if connectPkt.Properties != nil && connectPkt.Properties.ReceiveMaximum != nil {
		clientReceiveMax = *connectPkt.Properties.ReceiveMaximum
	}
	// The write is taken under the session lock because the session is already
	// reachable: CreateSession published it in the manager a few hundred lines
	// above, and CanSendOutbound reads OutboundWindow from another goroutine
	// the moment a delivery is routed here. Without the lock a reconnect that
	// raised or lowered the window raced those readers, and -race flagged it.
	sess.mu.Lock()
	sess.ReceiveMax = serverReceiveMax
	sess.OutboundWindow = min(clientReceiveMax, serverReceiveMax)
	sess.mu.Unlock()

	// Negotiate Topic Alias Maximum (MQTT 5.0 §3.1.2.11.5 / §3.2.2.3.8).
	//
	// Property identifier 0x22 names two limits that point in opposite
	// directions, and conflating them was a defect:
	//   - CONNECT Topic Alias Maximum (§3.1.2.11.5) is the highest alias the
	//     CLIENT will accept FROM the server. It is not echoed in the CONNACK;
	//     it only bounds what the server may send.
	//   - CONNACK Topic Alias Maximum (§3.2.2.3.8) is the highest alias the
	//     SERVER accepts FROM the client. That is the limit the inbound guard
	//     enforces, and it is what the CONNACK must advertise.
	// Storing the client's value as the server's limit meant a client that
	// declared 0 (asking the server not to use aliases) lost its own right to
	// send aliases and had every alias publish torn down, while every other
	// client was capped at its own acceptance value instead of the server's.
	const serverTopicAliasMax uint16 = 64
	sess.TopicAliasMax = 0
	if sess.Spec().TopicAlias {
		sess.TopicAliasMax = serverTopicAliasMax
	}
	if connectPkt.Properties != nil && connectPkt.Properties.TopicAliasMaximum != nil {
		sess.ClientTopicAliasMax = *connectPkt.Properties.TopicAliasMaximum
	}

	// Negotiate Server Keep Alive (MQTT 5.0 §3.1.2.11.4).
	// If server-configured keepalive is shorter, override client's.
	if b.opts.keepAlive > 0 && b.opts.keepAlive < connectPkt.KeepAlive {
		ka := b.opts.keepAlive
		sess.ServerKeepAlive = &ka
		sess.KeepAlive = ka
	}

	// Register client connection — kick previous connection if one exists.
	// The old readLoop will call disconnect() asynchronously; it must not
	// remove the new connection from the map, so disconnect() checks conn
	// identity before deleting.
	//
	// Takeover: the previous connection ends abnormally, so its will must be
	// triggered before the new connection registers its own will under the
	// same clientID (P2-10). The trigger is scoped to the old connection so a
	// late abnormal-disconnect from the old readLoop cannot fire the NEW
	// connection's will (audit).
	//
	// The takeover DISCONNECT goes first, ahead of the will trigger and ahead
	// of the lock below: [MQTT-3.1.4-3] makes it the signal the displaced
	// connection is owed, and it must not be sent while b.mu is held because
	// the write is bounded rather than instant (writeFinal).
	b.mu.RLock()
	old, hadOld := b.connections[clientID]
	b.mu.RUnlock()
	if hadOld && old != nil {
		b.sendTakenOverDisconnect(clientID, old)
		if err := b.will.TriggerWill(clientID, old.conn); err != nil {
			b.logger.Debug("failed to trigger takeover will", "clientID", clientID, "error", err)
		}
	}

	b.mu.Lock()
	if old, exists := b.connections[clientID]; exists {
		if err := old.conn.Close(); err != nil {
			b.logger.Debug("failed to close previous connection", "clientID", clientID, "error", err)
		}
		// Stop the previous connection's writer goroutine; its readLoop will
		// later call disconnect() and bail out on the conn identity check, so
		// it must be stopped here (R1).
		if old.out != nil {
			old.stopWriter()
		}
		b.logger.Info("session takeover", "clientID", clientID)
	}
	qcap := b.opts.writeQueueSize
	if qcap < 1 {
		qcap = 1
	}
	cs := &clientState{
		conn:         conn,
		codec:        codec,
		writeTimeout: b.opts.writeTimeout,
		out:          make(chan protocol.Packet, qcap),
		stopWrites:   make(chan struct{}),
		syncWrites:   make(chan syncWrite),
		enhancedAuth: enhancedAuth,
		metrics:      b.metrics,
	}
	b.connections[clientID] = cs
	online := len(b.connections)
	b.mu.Unlock()
	// The reservation made at the top of HandleConnection is now spent: this
	// connection is counted by len(b.connections) from here on.
	registered = true
	go cs.writeLoop()
	b.metrics.SetOnlineSessions(online)

	// MQTT 5.0 §3.1.3.2.2 / §3.1.4: a new connection with Clean Start set to 1
	// ends the existing Session, so a Will Message of the previous connection
	// that is still waiting out its delay has to be published now rather than
	// carried past the Session it belongs to. This runs after the take-over
	// trigger above -- which is what arms that pending publication -- and
	// before the new will is registered below, which would otherwise replace
	// the very registration FireWill has to find.
	if connectPkt.Flags.CleanSession {
		b.will.FireWill(clientID)
	}

	// Register will message
	if connectPkt.Flags.WillFlag {
		var willDelay time.Duration
		if connectPkt.WillProperties != nil && connectPkt.WillProperties.WillDelayInterval != nil {
			willDelay = time.Duration(*connectPkt.WillProperties.WillDelayInterval) * time.Second
		}
		// Cap will delay to the configured maximum to prevent abuse. A
		// maxWillDelay of 0 disables will delay entirely (P2-5).
		if willDelay > b.opts.maxWillDelay {
			b.logger.Debug("capping will delay", "clientID", clientID,
				"requested", willDelay, "max", b.opts.maxWillDelay)
			willDelay = b.opts.maxWillDelay
		}
		// MQTT 5.0 §3.1.3.2.2: the Will Message is published when the Will
		// Delay Interval has passed *or the Session ends, whichever happens
		// first*. A Session whose Expiry Interval is 0 ends the moment the
		// Network Connection closes (§3.1.2.11.2), so such a will may not be
		// delayed at all; and a Session that expires sooner than the delay
		// bounds the wait to its own lifetime. Folding the Session lifetime
		// into the delay here -- rather than racing a second timer against it
		// -- makes "whichever first" hold by construction.
		if sessionEnds := time.Duration(sess.ExpiryInterval) * time.Second; sessionEnds < willDelay {
			willDelay = sessionEnds
		}
		if err := b.will.RegisterWill(clientID, connectPkt.Username, connectPkt.WillTopic, connectPkt.WillMessage, connectPkt.Flags.WillQoS, connectPkt.Flags.WillRetain, connectPkt.WillProperties, willDelay, conn); err != nil {
			b.metrics.IncErrors("will")
			return fmt.Errorf("broker: register will failed: %w", err)
		}
	}

	// Plugin hook + event: the session is registered at this point, so a
	// subscriber sees a client that is genuinely online.
	b.publishEvent(ctx, plugin.EventClientConnected, plugin.EventData{
		ClientID:   clientID,
		Username:   connectPkt.Username,
		RemoteAddr: conn.RemoteAddr().String(),
	}, plugin.OnConnected, &plugin.Context{
		ClientID: clientID,
		Username: connectPkt.Username,
	})

	// Metrics
	b.metrics.IncConnections()

	// Send CONNACK
	b.sendConnAck(clientID, protocol.ConnAckAccepted, isResuming, sess)

	// A resumed session must get its unacknowledged PUBLISH and PUBREL packets
	// back on the wire with their original Packet Identifiers. §4.4 makes the
	// reconnect the recovery moment and forbids resending at any other time, so
	// the debt is settled here, right after the CONNACK and in one pass, instead
	// of waiting for the retry ticker to notice.
	if isResuming {
		b.resendSessionInflight(clientID, sess)
	}

	// Deliver messages queued while this persistent session was offline (P1-5).
	b.deliverQueuedMessages(ctx, clientID, sess)

	// Set initial keep-alive deadline so idle clients are detected
	b.setKeepAliveDeadline(conn, clientID, sess.KeepAlive)

	// Run read loop (handles its own cleanup via abnormalDisconnect/gracefulDisconnect)
	b.readLoop(ctx, clientID, sess, codec, conn)

	return nil
}

func (b *Broker) rejectSessionOwnerMismatch(conn net.Conn, codec protocol.PacketCodec, sp protocol.Spec, connectPkt *protocol.ConnectPacket) error {
	b.metrics.IncRejections("session_user_mismatch")
	b.logger.Warn("connection rejected: client ID belongs to a different user",
		"clientID", connectPkt.ClientID,
		"username", connectPkt.Username,
		"remote", conn.RemoteAddr().String())
	reason := byte(protocol.ConnAckNotAuthorized)
	if sp.ConnAckHasReasonCode {
		reason = protocol.ConnAckNotAuthorized5
	}
	b.sendConnAckRaw(conn, codec, reason, false)
	return fmt.Errorf("broker: client ID %q belongs to a different user", connectPkt.ClientID)
}

func (b *Broker) rejectFlapping(conn net.Conn, codec protocol.PacketCodec, sp protocol.Spec, connectPkt *protocol.ConnectPacket, d FlappingDecision) {
	b.metrics.IncRejections("flapping")
	b.metrics.IncErrors("flapping")

	attrs := []any{
		"clientID", connectPkt.ClientID,
		"remote", conn.RemoteAddr().String(),
		"decision", d.String(),
	}
	// Naming how long the client is blocked is what makes the log actionable:
	// without it an operator sees a rejection and no idea whether to wait.
	if remaining, penalized := b.flapping.PenaltyRemaining(connectPkt.ClientID); penalized {
		attrs = append(attrs, "penaltyRemaining", remaining.String())
	}

	reason := byte(protocol.ConnAckServerUnavailable)
	if sp.ConnAckHasReasonCode {
		reason = protocol.ConnAckBanned
	}
	b.logger.Warn("connection rejected: flapping limit exceeded", attrs...)
	b.sendConnAckRaw(conn, codec, reason, false)
}

func (b *Broker) findEnhancedAuth(method string) EnhancedAuthenticator {
	for _, a := range b.opts.enhancedAuth {
		if a.Method() == method {
			return a
		}
	}
	return nil
}

func (b *Broker) enhancedAuthHandshake(conn net.Conn, codec protocol.PacketCodec, connectPkt *protocol.ConnectPacket, auth EnhancedAuthenticator) error {
	var data []byte
	if connectPkt.Properties != nil {
		data = connectPkt.Properties.AuthenticationData
	}
	reason, respData, err := auth.Initial(data)
	if err != nil {
		return fmt.Errorf("broker: enhanced auth initial: %w", err)
	}

	// Bound the exchange so a stalled client cannot hold the connection open.
	authTimeout := b.opts.connectTimeout
	if authTimeout <= 0 {
		authTimeout = 10 * time.Second
	}
	if err := conn.SetReadDeadline(time.Now().Add(authTimeout)); err != nil {
		b.logger.Debug("failed to set enhanced-auth read deadline", "error", err)
	}
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()

	continued := false
	for reason == protocol.AuthContinueAuth {
		continued = true
		if err := b.sendAuthPacket(conn, codec, reason, auth.Method(), respData); err != nil {
			return err
		}
		pkt, err := codec.Decode(conn)
		if err != nil {
			return fmt.Errorf("broker: enhanced auth: read AUTH: %w", err)
		}
		authPkt, ok := pkt.(*protocol.AuthPacket)
		if !ok {
			return fmt.Errorf("broker: enhanced auth: expected AUTH, got %T", pkt)
		}
		var in []byte
		if authPkt.Properties != nil {
			in = authPkt.Properties.AuthenticationData
		}
		reason, respData, err = auth.Continue(in)
		if err != nil {
			return fmt.Errorf("broker: enhanced auth continue: %w", err)
		}
	}
	if reason != protocol.AuthSuccess {
		// AUTH packets only carry 0x00/0x18/0x19; a failed exchange is signalled
		// with a DISCONNECT carrying the rejection reason (§4.12).
		_ = codec.Encode(conn, &protocol.DisconnectPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeDisconnect},
			ReasonCode:  protocol.ReasonCodeNotAuthorized,
		})
		return fmt.Errorf("broker: enhanced auth rejected (reason 0x%02X)", reason)
	}
	// A multi-step exchange ends with a final AUTH 0x00 (Success).
	if continued {
		if err := b.sendAuthPacket(conn, codec, reason, auth.Method(), respData); err != nil {
			return err
		}
	}
	return nil
}

func (b *Broker) sendAuthPacket(conn net.Conn, codec protocol.PacketCodec, reason byte, method string, data []byte) error {
	pkt := &protocol.AuthPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeAuth},
		ReasonCode:  reason,
		Properties: &protocol.Properties{
			AuthenticationMethod: method,
			AuthenticationData:   data,
		},
	}
	if err := codec.Encode(conn, pkt); err != nil {
		return fmt.Errorf("broker: send AUTH: %w", err)
	}
	// WebSocket transports buffer writes until FlushPacket; this handshake
	// path runs before the per-connection writer exists, so flush explicitly
	// or the enhanced-auth exchange deadlocks on WS (audit).
	if f, ok := conn.(packetFlusher); ok {
		if err := f.FlushPacket(); err != nil {
			return fmt.Errorf("broker: flush AUTH: %w", err)
		}
	}
	return nil
}

func (b *Broker) sendAuthPacketToClient(clientID string, reason byte, method string, data []byte) {
	b.writePacket(clientID, &protocol.AuthPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeAuth},
		ReasonCode:  reason,
		Properties: &protocol.Properties{
			AuthenticationMethod: method,
			AuthenticationData:   data,
		},
	})
}

func (b *Broker) disconnect(ctx context.Context, clientID string, conn net.Conn) {
	// The will is NOT touched here. gracefulDisconnect removes it and
	// abnormalDisconnect triggers it; calling RemoveWill here cancelled a
	// just-armed delayed will (P2-5b) and, on takeover, deleted the NEW
	// connection's will (P2-10).

	// Persist session BEFORE taking the lock, since Save() is a heavy
	// operation that should not block connection registration. A session
	// with Session Expiry Interval 0 ends when the connection closes
	// (MQTT 5.0 §3.1.2.11.2): it is not saved, and any previously stored
	// state is removed.
	//
	// The condition is the negotiated interval, not the Clean Start bit.
	// §3.1.2.11.2 conditions the obligation on the interval alone -- "The
	// Client and Server MUST store the Session State after the Network
	// Connection is closed if the Session Expiry Interval is greater than 0
	// [MQTT-3.1.2-23]" -- and Clean Start is about the session that existed
	// before the CONNECT, a decision already carried out at connect time.
	// Gating on the bit here made a v5 client that asked for Clean Start = 1
	// with Session Expiry Interval > 0 lose the state it had just asked the
	// server to keep, and made the DISCONNECT-carried override
	// (§3.14.2.2.2) a no-op for the same client.
	if sess, ok := b.sessions.GetSession(clientID); ok && b.sessionStore != nil {
		if sess.ExpiryInterval > 0 {
			if err := sess.Save(ctx, b.sessionStore); err != nil {
				b.logger.Warn("failed to save session", "clientID", clientID, "error", err)
				b.metrics.IncErrors("session_save")
			}
		} else {
			if err := b.sessionStore.DeleteSession(ctx, clientID); err != nil {
				b.logger.Warn("failed to delete expired session", "clientID", clientID, "error", err)
				b.metrics.IncErrors("session_save")
			}
			if b.messageStore != nil {
				if err := b.messageStore.ClearMessages(ctx, clientID); err != nil {
					b.metrics.IncErrors("message_store")
					b.logger.Warn("failed to clear messages of expiring session", "clientID", clientID, "error", err)
				}
			}
			b.offlineMu.Lock()
			delete(b.offlineCount, clientID)
			b.offlineMu.Unlock()
		}
	}

	// Atomic identity check + cleanup under a single Lock to prevent
	// session takeover race. Hold b.mu.Lock() throughout cleanup so
	// HandleConnection cannot register a new connection mid-cleanup.
	b.mu.Lock()
	cs, exists := b.connections[clientID]
	if !exists || cs.conn != conn {
		b.mu.Unlock()
		return
	}

	// Reset flow control counter before removing session. A session whose
	// expiry interval has run out also releases its topic-tree subscriptions
	// so stale entries do not accumulate across disconnect/reconnect cycles
	// (P2-13, NEW-3). Persistent sessions keep theirs for offline queueing
	// and are re-subscribed on reconnect; any flow-control-buffered
	// deliveries are persisted so they are not lost (P1-5 + P2-14).
	//
	// "Has the expiry interval run out" is the negotiated Session Expiry
	// Interval being 0, not the Clean Start bit: a clean start with an
	// interval > 0 keeps a session that must survive this disconnect, and a
	// non-clean connect that negotiated 0 has nothing to keep. See
	// [MQTT-3.1.2-23] and the expiry computation in HandleConnection.
	var persistSess *Session
	if sess, ok := b.sessions.GetSession(clientID); ok {
		sess.ResetOutboundUnacked()
		if sess.ExpiryInterval > 0 {
			persistSess = sess
		} else {
			b.unsubscribeSessionTopics(sess)
		}
	}

	b.sessions.RemoveSession(clientID)
	b.qos.RemoveClient(clientID)
	delete(b.connections, clientID)
	// Stop this connection's writer goroutine. Producers that already passed
	// the map lookup select on stopWrites and back off instead of enqueuing to
	// a torn-down connection (R1).
	if cs.out != nil {
		cs.stopWriter()
	}
	online := len(b.connections)
	b.mu.Unlock()

	// Persist flow-control-buffered deliveries OUTSIDE the global lock: store
	// writes (badger disk / redis network) must not stall connection
	// registration or teardown for every client (audit). The session is no
	// longer reachable at this point, so no takeover can race these writes.
	if persistSess != nil && b.messageStore != nil {
		b.persistBufferedOutbound(ctx, clientID, persistSess)
	}

	b.metrics.SetOnlineSessions(online)
	// Plugin hook + event: fired after the session state settled, so a
	// subscriber reading the store sees the final state, not a half-torn one.
	b.publishEvent(ctx, plugin.EventClientDisconnected, plugin.EventData{
		ClientID: clientID,
	}, plugin.OnClose, &plugin.Context{ClientID: clientID})

	// Metrics
	b.metrics.OnDisconnect()

	b.logger.Info("client disconnected", "clientID", clientID)
}

func (b *Broker) setKeepAliveDeadline(conn net.Conn, clientID string, keepAlive uint16) {
	if keepAlive == 0 {
		return
	}
	timeout := time.Duration(keepAlive) * time.Second * 3 / 2
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		b.logger.Debug("failed to set keep-alive deadline", "clientID", clientID, "error", err)
	}
}

func decodeErrorReasonCode(err error) (byte, bool) {
	switch {
	case errors.Is(err, protocol.ErrProtocolError):
		return protocol.ReasonCodeProtocolError, true
	case errors.Is(err, protocol.ErrMalformedPacket):
		return protocol.ReasonCodeMalformedPacket, true
	case errors.Is(err, protocol.ErrInvalidPacket):
		// Reserved-nibble violations, QoS 3, an unknown packet type and a
		// non-minimal Remaining Length all land here. §2.1.3 and §2.2.2.2 put
		// them all under Malformed Packet, and [MQTT-2.2.2-2] requires the
		// receiver to close the connection on the first two.
		return protocol.ReasonCodeMalformedPacket, true
	case errors.Is(err, protocol.ErrPacketTooLarge):
		return protocol.ReasonCodePacketTooLarge, true
	default:
		return 0, false
	}
}
func (b *Broker) readLoop(ctx context.Context, clientID string, sess *Session, codec protocol.PacketCodec, conn net.Conn) {
	for {
		pkt, err := codec.Decode(conn)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				// MQTT keep-alive expired: no packet within 1.5x KeepAlive.
				// This is the primary zombie-connection reaper — the peer is
				// considered dead and the session is torn down.
				b.logger.Info("keepalive timeout", "clientID", clientID)
				b.metrics.IncRejections("keepalive_timeout")
				// §3.1.2.10 lets the server tell the peer why the connection
				// is going away (0x8D, [MQTT-3.1.2-22] makes the notice
				// optional and the close mandatory). It is worth sending when
				// the peer is merely stalled rather than gone: a client that
				// missed its PINGREQ still gets to learn the session expired
				// instead of seeing a bare close. The teardown stays abnormal,
				// so the will fires exactly as [MQTT-3.1.2-22] -- "as if the
				// network had failed" -- requires.
				//
				// writeFinal bounds the attempt (2s), so a peer that really is
				// dead cannot hold the reaper open for the full writeTimeout.
				if sess != nil && sess.Spec().ServerDisconnect {
					b.sendDisconnectBeforeClose(clientID, &protocol.DisconnectPacket{
						FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeDisconnect},
						ReasonCode:  protocol.DisconnectKeepAliveTimeout,
					})
				}
			} else if reason, reportable := decodeErrorReasonCode(err); reportable {
				// §4.13.1: on a Malformed Packet or Protocol Error the server
				// MUST close the network connection, and SHOULD first send a
				// DISCONNECT carrying the reason code. Nothing else on this
				// path tells the peer why it was dropped: the read failed, no
				// packet was produced, and a bare close is indistinguishable
				// from a network fault. The write is confirmed before the
				// teardown (sendDisconnectBeforeClose), so the close cannot
				// overtake it.
				//
				// MQTT 3.1.1 has no server-to-client DISCONNECT, hence the
				// §3.1.1 gate. The teardown stays abnormal either way, so the
				// client's will still fires (§3.1.2.5).
				b.logger.Warn("malformed packet, disconnecting",
					"clientID", clientID, "reasonCode", reason, "error", err)
				b.metrics.IncErrors("protocol")
				if sess != nil && sess.Spec().ServerDisconnect {
					b.sendDisconnectBeforeClose(clientID, &protocol.DisconnectPacket{
						FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeDisconnect},
						ReasonCode:  reason,
					})
				}
			} else {
				b.logger.Debug("read error", "clientID", clientID, "error", err)
			}
			b.abnormalDisconnect(clientID, conn)
			return
		}

		// Plugin hook: OnMessage fires for every decoded packet, not only
		// PUBLISH (see the hook contract in plugin/manager.go). The
		// PUBLISH-specific fields are therefore filled only when a PUBLISH
		// actually arrived. They used to be left empty for every packet, which
		// contradicted the documented plugin examples -- both read exactly
		// these fields (data.Topic in docs/guides/API.md, an ACL on data.Topic
		// in docs/architecture/ARCHITECTURE.md).
		//
		// The nil guard keeps the no-plugin path allocation-free, since this
		// runs once per received packet. Payload is shared rather than copied:
		// decodePublish allocates a packet-owned slice and never hands out
		// pooled memory, so the hook may read it for the duration of the call.
		if b.pluginMgr != nil {
			hookCtx := &plugin.Context{ClientID: clientID}
			if pub, ok := pkt.(*protocol.PublishPacket); ok {
				hookCtx.Topic = pub.Topic
				hookCtx.Payload = pub.Payload
				hookCtx.QoS = pub.QoS
				hookCtx.Retain = pub.Retain
			}
			// OnMessage fires for every packet type, so the bus event is
			// published only for PUBLISH: a subscriber of
			// EventMessagePublished should not have to filter.
			var ev plugin.Event
			var payload plugin.EventData
			if pub, ok := pkt.(*protocol.PublishPacket); ok {
				ev = plugin.EventMessagePublished
				payload = plugin.EventData{
					ClientID: clientID,
					Topic:    pub.Topic,
					Payload:  pub.Payload,
					QoS:      pub.QoS,
					Retain:   pub.Retain,
				}
			}
			if ev != "" {
				b.publishEvent(ctx, ev, payload, plugin.OnMessage, hookCtx)
			} else {
				b.dispatch(ctx, plugin.OnMessage, hookCtx)
			}
		}

		// Update activity and refresh the keep-alive deadline
		sess.UpdateActivity()
		b.setKeepAliveDeadline(conn, clientID, sess.KeepAlive)

		switch p := pkt.(type) {
		case *protocol.PublishPacket:
			sess.TrackReceived(len(p.Payload) + len(p.Topic))
			b.handlePublish(ctx, clientID, sess, p)
		case *protocol.SubscribePacket:
			b.handleSubscribe(ctx, clientID, sess, p)
		case *protocol.UnsubscribePacket:
			b.handleUnsubscribe(clientID, sess, p)
		case *protocol.PingReqPacket:
			b.writePacket(clientID, &protocol.PingRespPacket{
				FixedHeader: protocol.FixedHeader{
					PacketType: protocol.PacketTypePingResp,
				},
			})
		case *protocol.DisconnectPacket:
			// MQTT 5.0 §3.14.2.2.2: the DISCONNECT may carry a new Session
			// Expiry Interval that overrides the one from CONNECT (0 = the
			// session ends now; values are capped at the server maximum).
			if p.Properties != nil && p.Properties.SessionExpiryInterval != nil {
				v := *p.Properties.SessionExpiryInterval
				if serverMax := uint32(b.opts.sessionExpiry.Seconds()); v > serverMax {
					v = serverMax
				}
				sess.mu.Lock()
				sess.ExpiryInterval = v
				sess.mu.Unlock()
			}
			b.logger.Info("graceful disconnect", "clientID", clientID)
			b.gracefulDisconnect(clientID, conn)
			return
		case *protocol.PubAckPacket:
			b.handlePubAck(clientID, p.PacketID)
		case *protocol.PubRecPacket:
			b.handlePubRec(clientID, p.PacketID, p.ReasonCode)
		case *protocol.PubRelPacket:
			b.handlePubRel(ctx, clientID, p.PacketID)
		case *protocol.PubCompPacket:
			b.handlePubComp(clientID, p.PacketID)
		case *protocol.AuthPacket:
			// MQTT 5.0 re-authentication (§4.12): a connection established via
			// enhanced auth may exchange more AUTH packets during the session.
			if cs := b.connection(clientID); cs != nil && cs.enhancedAuth != nil {
				var in []byte
				if p.Properties != nil {
					in = p.Properties.AuthenticationData
				}
				reason, respData, err := cs.enhancedAuth.Continue(in)
				if err != nil {
					b.logger.Debug("re-auth failed", "clientID", clientID, "error", err)
					b.writePacket(clientID, &protocol.DisconnectPacket{
						FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeDisconnect},
						ReasonCode:  protocol.ReasonCodeBadAuthMethod,
					})
					b.gracefulDisconnect(clientID, conn)
					return
				}
				b.sendAuthPacketToClient(clientID, reason, cs.enhancedAuth.Method(), respData)
				// §4.12.1: the exchange ends with either 0x18 (Continue
				// authentication, the client sends another AUTH) or 0x00
				// (Success, re-authentication completed and the connection
				// carries on). Only the success and continue codes keep the
				// connection; anything else is a failure and ends it.
				//
				// Treating every code other than 0x18 as a failure disconnected
				// each client immediately after it re-authenticated
				// successfully, and published its will on the way out.
				if reason == protocol.AuthContinueAuth || reason == protocol.ReasonCodeSuccess {
					continue
				}
				b.logger.Debug("re-auth rejected", "clientID", clientID, "reasonCode", reason)
				b.gracefulDisconnect(clientID, conn)
				return
			}
			// No enhanced auth: reject AUTH per §4.12.
			b.logger.Debug("AUTH packet received but no enhanced auth in use",
				"clientID", clientID, "reasonCode", p.ReasonCode)
			b.writePacket(clientID, &protocol.DisconnectPacket{
				FixedHeader: protocol.FixedHeader{
					PacketType: protocol.PacketTypeDisconnect,
				},
				ReasonCode: protocol.ReasonCodeBadAuthMethod,
			})
			b.gracefulDisconnect(clientID, conn)
			return
		case *protocol.ConnectPacket:
			// MQTT-3.1.0-2: a second CONNECT on an established connection is a
			// protocol violation. It was previously ignored, which also let a
			// crafted CONNECT rewrite the shared codec's protocol version and
			// silently downgrade parsing for the rest of the connection
			// (audit). Disconnect: MQTT 5 clients get a DISCONNECT with
			// reason 0x82; MQTT 3.1.1 has no server-to-client DISCONNECT, so
			// close directly. Either way the abnormal close publishes the
			// client's will per §3.1.2.5.
			b.logger.Debug("second CONNECT received, disconnecting", "clientID", clientID)
			if sess != nil && sess.Spec().ServerDisconnect {
				b.sendDisconnectBeforeClose(clientID, &protocol.DisconnectPacket{
					FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeDisconnect},
					ReasonCode:  protocol.ReasonCodeProtocolError,
				})
			}
			b.abnormalDisconnect(clientID, conn)
			return
		default:
			b.logger.Debug("unhandled packet type", "clientID", clientID, "type", fmt.Sprintf("%T", pkt))
		}
	}
}

func (b *Broker) sendDisconnectBeforeClose(clientID string, pkt protocol.Packet) bool {
	cs := b.connection(clientID)
	if cs == nil {
		return false
	}
	if sent := cs.writeFinal(pkt); !sent {
		b.logger.Debug("final packet not delivered before close", "clientID", clientID)
		return false
	}
	return true
}

func (b *Broker) sendTakenOverDisconnect(clientID string, old *clientState) {
	if old == nil || old.codec == nil || !old.codec.Spec().ServerDisconnect {
		return
	}
	if !old.writeFinal(&protocol.DisconnectPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeDisconnect},
		ReasonCode:  protocol.DisconnectSessionTakenOver,
	}) {
		b.logger.Debug("takeover DISCONNECT not delivered", "clientID", clientID)
	}
}

func (b *Broker) abnormalDisconnect(clientID string, conn net.Conn) {
	// A broker-initiated shutdown is not a client failure: publish no will, or
	// a rolling restart would fire one will per connected client at once.
	if b.shuttingDown.Load() {
		b.will.RemoveWill(clientID)
		ctx, cancel := b.teardownCtx()
		defer cancel()
		b.disconnect(ctx, clientID, conn)
		return
	}
	// The will is only fired when it belongs to THIS connection: after a
	// takeover the old readLoop's late error must not trigger the new
	// connection's will (audit).
	if err := b.will.TriggerWill(clientID, conn); err != nil {
		b.logger.Debug("failed to trigger will", "clientID", clientID, "error", err)
	}
	ctx, cancel := b.teardownCtx()
	defer cancel()
	b.disconnect(ctx, clientID, conn)
}

func (b *Broker) gracefulDisconnect(clientID string, conn net.Conn) {
	b.will.RemoveWill(clientID)
	ctx, cancel := b.teardownCtx()
	defer cancel()
	b.disconnect(ctx, clientID, conn)
}
