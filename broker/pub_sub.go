package broker

// Publish and subscribe: inbound PUBLISH routing, retained messages,
// SUBSCRIBE/UNSUBSCRIBE, and outbound delivery to one session.
//
// This is where a message's path from the wire to a subscriber lives: budget
// enforcement, topic validation, retained handling, shared-subscription routing
// and offline queuing.

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store"
)

// unsubscribeTopics removes a set of topic filters (regular or $share shared)
// from the topic tree for a client.
func (b *Broker) unsubscribeTopics(clientID string, topics []string) {
	for _, topic := range topics {
		if IsSharedSubscription(topic) {
			shareName, realFilter, ok := ParseSharedFilter(topic)
			if ok {
				b.topics.UnsubscribeShared(shareName, realFilter, clientID)
			}
			continue
		}
		b.topics.Unsubscribe(topic, clientID)
	}
}

func (b *Broker) unsubscribeSessionTopics(sess *Session) {
	sess.mu.RLock()
	// maps.Keys returns an iterator over the keys (Go 1.23+); slices.Collect
	// drains it into a slice in one pass, replacing the manual preallocation loop.
	topics := slices.Collect(maps.Keys(sess.Subscriptions))
	sess.mu.RUnlock()
	b.unsubscribeTopics(sess.ClientID, topics)
}

func (b *Broker) handlePublish(ctx context.Context, clientID string, sess *Session, pkt *protocol.PublishPacket) {
	start := time.Now()
	defer func() {
		// Observe latency at the configured sampling rate; histogram
		// observations are comparatively expensive (WithLatencySampling).
		switch {
		case b.opts.latencySampling <= 0:
		case b.opts.latencySampling == 1:
			b.metrics.ObserveMessageLatency(time.Since(start).Seconds(), pkt.FixedHeader.QoS)
		default:
			if (b.latencyCounter.Add(1)-1)%uint64(b.opts.latencySampling) == 0 {
				b.metrics.ObserveMessageLatency(time.Since(start).Seconds(), pkt.FixedHeader.QoS)
			}
		}
	}()

	// Check client publish rate limit
	if sess != nil && sess.publishRate != nil && !sess.publishRate.Allow() {
		b.metrics.IncMessagesDropped("rate_limited")
		b.metrics.IncErrors("rate_limit")
		b.logger.Debug("publish rate limit exceeded", "clientID", clientID)
		// Acknowledge QoS 1/2 so the client can free its packet id instead
		// of retransmitting forever (audit: this used to drop silently).
		b.ackDiscardedPublish(clientID, sess, pkt, protocol.ReasonCodeQuotaExceeded)
		return
	}

	// Resolve Topic Alias (MQTT 5.0 §3.3.2.3.4).
	// If TopicAlias is set and TopicName is empty, resolve from alias map.
	// If both are set, register/replace the alias mapping.
	//
	// The two illegal-value branches below -- alias 0 and alias above the
	// maximum the server advertised -- are Protocol Errors and are answered
	// with a DISCONNECT carrying 0x94 (Topic Alias invalid, §4.13) instead of
	// being silently dropped. Dropping a publish left a QoS 1/2 client
	// retransmitting forever, and the byte tells a conformant client exactly
	// what it got wrong. The unmapped-alias branch keeps the generic 0x82: its
	// value is legal, it is simply not registered.
	//
	// The alias==0 branch is a second line of defence. The codec already
	// rejects Topic Alias 0 while decoding (protocol.Properties.validate), so
	// the value cannot normally reach here; it is kept correct anyway so a
	// future lenient decode path cannot turn it back into an ack-and-ignore.
	if pkt.Properties != nil && pkt.Properties.TopicAlias != nil {
		alias := *pkt.Properties.TopicAlias
		if alias == 0 {
			// [MQTT-3.3.2-8]: a PUBLISH packet must not carry a Topic Alias
			// which has the value 0.
			b.metrics.IncMessagesDropped("invalid_topic_alias")
			b.closeProtocolViolationWithReason(clientID, sess, protocol.ReasonCodeTopicAliasInvalid)
			return
		}
		if pkt.Topic == "" {
			// Resolve alias to topic.
			resolved, ok := sess.ResolveTopicAlias(alias)
			if !ok {
				// §3.3.2.3.4: an alias with no mapping, used with an empty
				// Topic Name, is a Protocol Error. The alias value itself is
				// not illegal, so the generic 0x82 is the right code here.
				b.metrics.IncMessagesDropped("unknown_topic_alias")
				b.closeProtocolViolation(clientID, sess)
				return
			}
			pkt.Topic = resolved
		} else {
			// Register alias mapping. An alias above the maximum the server
			// advertised in the CONNACK is a Protocol Error (§3.3.2.3.4); it
			// used to be a Debug log the client never learned about, leaving
			// the mapping absent and the next empty-topic publish a mystery
			// teardown.
			if err := sess.RegisterTopicAlias(alias, pkt.Topic); err != nil {
				b.metrics.IncMessagesDropped("invalid_topic_alias")
				b.closeProtocolViolationWithReason(clientID, sess, protocol.ReasonCodeTopicAliasInvalid)
				return
			}
		}
	}

	// Check Message Expiry Interval (MQTT 5.0 §3.3.2.3.3).
	// Drop messages that have already expired.
	if pkt.Properties != nil && pkt.Properties.MessageExpiryInterval != nil {
		if *pkt.Properties.MessageExpiryInterval == 0 {
			b.metrics.IncMessagesDropped("message_expired")
			b.ackDiscardedPublish(clientID, sess, pkt, protocol.ReasonCodeSuccess)
			return
		}
	}

	// A Topic Name containing '#' or '+' is not well-formed (§4.7.1.2
	// [MQTT-4.7.1-1], §4.7.1.3 [MQTT-4.7.1-2]), which makes it a Protocol Error
	// and the connection the thing that has to go -- not the publish, which is
	// what the old PUBACK/PUBREC 0x90 answered. See rejectMalformedTopic.
	if !protocol.ValidatePublishTopic(pkt.Topic) {
		b.rejectMalformedTopic(clientID, sess, "publish")
		return
	}

	b.metrics.IncMessagesPublished(pkt.QoS)

	// Check authorization
	username := ""
	if sess != nil {
		username = sess.Username
	}
	if b.opts.authorizer != nil && !b.opts.authorizer.CanPublish(ctx, username, pkt.Topic) {
		// A QoS 1/2 PUBLISH needs the matching acknowledgement so the client
		// can free its packet id; previously the v5 reason code reached v3.1.1
		// clients whose PUBACK/PUBREC format has no reason field, which made
		// the encoder fail and the broker drop the connection (audit).
		b.ackDiscardedPublish(clientID, sess, pkt, protocol.ReasonCodeNotAuthorized)
		b.metrics.IncAuthFailures()
		return
	}

	// QoS 2 flow: stage the message in the session's inbound table until
	// PUBREL completes the handshake, then route it to subscribers. The
	// inbound stage is separate from the outbound QoS engine and is persisted
	// with the session (audit H4): the previous shared (clientID, packetID)
	// engine table let an outbound delivery overwrite the inbound entry
	// (misdelivery / loss), and a disconnect discarded the inbound half so a
	// retransmitted PUBREL was acknowledged without ever delivering.
	if pkt.QoS == 2 {
		if sess != nil && sess.HasInboundQoS2(pkt.PacketID) {
			// A DUP resend (PUBREC was lost) re-sends PUBREC without
			// re-processing (MQTT §4.3.3). Reusing a packet id that still
			// awaits PUBREL without DUP is a protocol violation: answer
			// PUBREC 0x91 (packet id in use) and drop the new payload.
			reason := byte(protocol.ReasonCodeSuccess)
			if !pkt.Dup {
				reason = protocol.ReasonCodePacketIdentifierInUse
				b.metrics.IncMessagesDropped("qos2_pid_reuse")
				b.logger.Debug("QoS2 packet id reused before PUBREL",
					"clientID", clientID, "packetID", pkt.PacketID)
			}
			if sess == nil || !sess.Spec().ReasonCodes {
				reason = protocol.ReasonCodeSuccess
			}
			b.writePacket(clientID, &protocol.PubRecPacket{
				FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubRec},
				PacketID:    pkt.PacketID,
				ReasonCode:  reason,
			})
			return
		}

		// Accept: enforce the server's advertised inbound budget, store
		// retained state, then send PUBREC.
		//
		// The budget is sess.ReceiveMax -- the very number the CONNACK carried
		// -- rather than the raw configuration, so what is enforced and what
		// was promised cannot drift apart. (The previous code took
		// b.opts.maxInflight and, unconfigured, a hardcoded 100, while the
		// CONNACK advertised 65535.)
		//
		// Only QoS 2 needs a table to count against. §3.3.4 bounds the QoS 1
		// and QoS 2 PUBLISH packets "where it has not sent a PUBACK or PUBCOMP
		// in response", and a QoS 1 exchange ends with the PUBACK this function
		// queues below -- before the next PUBLISH is read -- so the outstanding
		// QoS 1 count cannot exceed one and never trips the budget. A QoS 2
		// exchange stays open until PUBREL, which is what the table tracks.
		if sess == nil {
			// No session to hold a budget; acknowledge and drop, which is what
			// the old combined nil guard did on this branch.
			b.ackDiscardedPublish(clientID, sess, pkt, protocol.ReasonCodeSuccess)
			return
		}
		if !sess.AddInboundQoS2(&InboundQoS2{
			PacketID:   pkt.PacketID,
			QoS:        pkt.QoS,
			Topic:      pkt.Topic,
			Payload:    pkt.Payload,
			Retain:     pkt.Retain,
			ReceivedAt: time.Now(),
			// The routing happens on PUBREL, so the publisher's properties have
			// to survive the wait here or the delivered message loses them
			// (MQTT 5.0 §3.3.2.3). doDeliver relays them from this packet.
			Properties: pkt.Properties,
		}, int(sess.ReceiveMax)) {
			b.rejectInboundOverBudget(clientID, sess, pkt)
			return
		}
		if pkt.Retain {
			b.handleRetainedMessage(ctx, pkt)
		}
		b.writePacket(clientID, &protocol.PubRecPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePubRec,
			},
			PacketID:   pkt.PacketID,
			ReasonCode: protocol.ReasonCodeSuccess,
		})
		return
	}

	// QoS 0/1: handle retained message for the delivered message.
	if pkt.Retain {
		b.handleRetainedMessage(ctx, pkt)
	}

	// Route to subscribers for QoS 0 and QoS 1. The outgoing RETAIN flag is
	// decided per subscription inside doDeliver (Retain As Published), so the
	// publisher's flag is carried through unchanged.
	forwardPkt := *pkt
	subscribers := b.topics.Match(forwardPkt.Topic)
	for _, sub := range subscribers {
		b.deliverToClient(ctx, sub.ClientID, clientID, &forwardPkt)
	}
	// Route to shared subscribers (round-robin, one per share group)
	b.routeSharedPublish(ctx, clientID, &forwardPkt)

	// Send PUBACK for QoS 1. Incoming QoS 1 has no client acknowledgment and
	// must NOT be tracked in the QoS engine: tracking it here caused the
	// retry loop to re-route the message to subscribers (duplicate delivery)
	// because the inflight entry could never be acked.
	if pkt.QoS == 1 {
		b.writePacket(clientID, &protocol.PubAckPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePubAck,
			},
			PacketID:   pkt.PacketID,
			ReasonCode: protocol.ReasonCodeSuccess,
		})
	}
}

func (b *Broker) rejectInboundOverBudget(clientID string, sess *Session, pkt *protocol.PublishPacket) {
	b.metrics.IncMessagesDropped("receive_max_exceeded")
	b.metrics.IncErrors("flow_control")

	receiveMax := uint16(0)
	if sess != nil {
		receiveMax = sess.ReceiveMax
	}
	b.logger.Debug("inbound receive maximum exceeded",
		"clientID", clientID, "packetID", pkt.PacketID, "receiveMax", receiveMax)

	if sess != nil && sess.Spec().ServerDisconnect {
		b.closeProtocolViolationWithReason(clientID, sess, protocol.DisconnectReceiveMaximumExceeded)
		return
	}
	b.ackDiscardedPublish(clientID, sess, pkt, protocol.ReasonCodeSuccess)
}

func (b *Broker) rejectMalformedTopic(clientID string, sess *Session, where string) {
	b.metrics.IncMessagesDropped("malformed_topic")
	b.logger.Debug("malformed topic rejected", "clientID", clientID, "where", where)
	b.closeProtocolViolationWithReason(clientID, sess, protocol.DisconnectProtocolError)
}

func (b *Broker) ackDiscardedPublish(clientID string, sess *Session, pkt *protocol.PublishPacket, v5Reason byte) {
	if pkt.QoS == 0 {
		return
	}
	reason := v5Reason
	if sess == nil || !sess.Spec().ReasonCodes {
		reason = protocol.ReasonCodeSuccess
	}
	switch pkt.QoS {
	case 1:
		b.writePacket(clientID, &protocol.PubAckPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubAck},
			PacketID:    pkt.PacketID,
			ReasonCode:  reason,
		})
	case 2:
		b.writePacket(clientID, &protocol.PubRecPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubRec},
			PacketID:    pkt.PacketID,
			ReasonCode:  reason,
		})
	}
}

func (b *Broker) closeProtocolViolation(clientID string, sess *Session) {
	b.closeProtocolViolationWithReason(clientID, sess, protocol.ReasonCodeProtocolError)
}

func (b *Broker) closeProtocolViolationWithReason(clientID string, sess *Session, reason byte) {
	if sess != nil && sess.Spec().ServerDisconnect {
		b.sendDisconnectBeforeClose(clientID, &protocol.DisconnectPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeDisconnect},
			ReasonCode:  reason,
		})
	}
	if cs := b.connection(clientID); cs != nil {
		cs.conn.Close()
	}
}
func (b *Broker) handleRetainedMessage(ctx context.Context, pkt *protocol.PublishPacket) {
	if b.retainedStore == nil {
		return
	}

	b.retainedMu.Lock()
	defer b.retainedMu.Unlock()

	existed := true
	if _, err := b.retainedStore.GetRetained(ctx, pkt.Topic); err != nil {
		if errors.Is(err, store.ErrRetainedNotFound) {
			existed = false
		} else {
			b.logger.Warn("failed to check retained message", "topic", pkt.Topic, "error", err)
			b.metrics.IncErrors("retained_store")
			return
		}
	}

	// Enforce the retained-message count limit for NEW topics. The check and
	// the store write share one lock, so concurrent publishers cannot both
	// pass the limit and exceed maxRetainedTopics (NEW-5).
	if len(pkt.Payload) > 0 && !existed && b.opts.maxRetainedTopics > 0 &&
		int(b.retainedCount.Load()) >= b.opts.maxRetainedTopics {
		b.logger.Debug("retained message limit reached, dropping",
			"topic", pkt.Topic, "max", b.opts.maxRetainedTopics)
		b.metrics.IncMessagesDropped("retained_limit")
		return
	}

	if len(pkt.Payload) == 0 {
		if err := b.retainedStore.DeleteRetained(ctx, pkt.Topic); err != nil {
			b.logger.Warn("failed to delete retained message", "topic", pkt.Topic, "error", err)
			b.metrics.IncErrors("retained_store")
			return
		}
		if existed {
			b.retainedCount.Add(-1)
		}
		delete(b.retainedExpirations, pkt.Topic)
		b.metrics.SetRetainedMessages(int(b.retainedCount.Load()))
		return
	}

	storedAt := time.Now()
	record := &store.RetainedMessage{
		Topic:     pkt.Topic,
		QoS:       pkt.FixedHeader.QoS,
		Payload:   pkt.Payload,
		Timestamp: storedAt,
		// The retained copy is delivered later to subscribers that were not
		// connected now, and §3.3.2.3 still applies to them: the publisher's
		// relayable properties have to be stored so they can be replayed
		// unaltered, and the Message Expiry Interval becomes an absolute
		// deadline so the replay can advertise the time actually left
		// (§3.3.2.3.3) and an expired retained message is discarded (§3.3.1.3).
		Properties: storeProperties(pkt.Properties),
		ExpiresAt:  messageExpiresAt(pkt),
	}
	if err := b.retainedStore.SaveRetained(ctx, record); err != nil {
		b.logger.Warn("failed to save retained message", "topic", pkt.Topic, "error", err)
		b.metrics.IncErrors("retained_store")
		return
	}
	if !existed {
		b.retainedCount.Add(1)
	}
	// Track the soonest deadline the retained message has: the operator's
	// retained TTL and the publisher's own Message Expiry Interval both bound
	// how long it may stay in the store.
	deadline := record.ExpiresAt
	if b.opts.retainedExpiry > 0 {
		ttl := storedAt.Add(b.opts.retainedExpiry)
		if deadline.IsZero() || ttl.Before(deadline) {
			deadline = ttl
		}
	}
	if deadline.IsZero() {
		delete(b.retainedExpirations, pkt.Topic)
	} else {
		b.retainedExpirations[pkt.Topic] = deadline
	}
	b.metrics.SetRetainedMessages(int(b.retainedCount.Load()))
}

func subAckFailureCode(sess *Session) byte {
	if sess != nil && !sess.Spec().SubAckFailureSupported {
		return protocol.ReasonCodeGrantedQoS0
	}
	return protocol.SubAckFailure
}
func (b *Broker) handleSubscribe(ctx context.Context, clientID string, sess *Session, pkt *protocol.SubscribePacket) {
	// Failure code for a rejected subscription filter (version-specific).
	failCode := subAckFailureCode(sess)

	// Enforce topic filter count limit to prevent resource exhaustion
	if b.opts.maxTopicFiltersPerSub > 0 && len(pkt.Topics) > b.opts.maxTopicFiltersPerSub {
		b.logger.Debug("too many topic filters in SUBSCRIBE",
			"clientID", clientID, "count", len(pkt.Topics),
			"max", b.opts.maxTopicFiltersPerSub)
		// Report failure for every filter; the code is version-specific
		// (0x80 for 3.1.1 / 5.0, granted QoS 0 for 3.1).
		codes := make([]byte, len(pkt.Topics))
		for i := range codes {
			codes[i] = failCode
		}
		b.writePacket(clientID, &protocol.SubAckPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypeSubAck,
			},
			PacketID:    pkt.PacketID,
			ReasonCodes: codes,
		})
		b.metrics.IncErrors("protocol")
		return
	}

	reasonCodes := make([]byte, len(pkt.Topics))
	deliverRetained := make([]bool, len(pkt.Topics))
	for i, topic := range pkt.Topics {
		username := ""
		if sess != nil {
			username = sess.Username
		}

		// Resolve shared subscriptions to their real filter first: the ACL
		// must be evaluated against "team/+" and not the literal
		// "$share/group/team/+" string, which no ACL pattern could match or
		// restrict correctly (audit H7).
		shared := false
		shareName := ""
		aclFilter := topic.Topic
		if IsSharedSubscription(topic.Topic) {
			var realFilter string
			var ok bool
			shareName, realFilter, ok = ParseSharedFilter(topic.Topic)
			// A malformed shared filter is a malformed filter: same Protocol
			// Error, same teardown (see rejectMalformedTopic).
			if !ok || !protocol.ValidateTopicFilter(realFilter) {
				b.rejectMalformedTopic(clientID, sess, "subscribe-shared")
				return
			}
			aclFilter = realFilter
			shared = true

			// [MQTT-3.8.3-4]: it is a Protocol Error to set the No Local bit
			// on a Shared Subscription. Accepting it made No Local silently
			// ineffective -- AllowsLocalPublish matches the delivered topic
			// against the stored filter, and "$share/g/a" never matches "a" --
			// so the subscriber kept receiving its own messages after asking
			// not to. Tear the connection down as the specification requires.
			if topic.NoLocal {
				b.closeProtocolViolation(clientID, sess)
				return
			}
		} else if !protocol.ValidateTopicFilter(topic.Topic) {
			b.rejectMalformedTopic(clientID, sess, "subscribe")
			return
		}

		// Check authorization against the effective (non-shared) filter.
		if b.opts.authorizer != nil && !b.opts.authorizer.CanSubscribe(ctx, username, aclFilter) {
			reasonCodes[i] = failCode
			continue
		}

		// Handle shared subscriptions ($share/{ShareName}/{filter})
		if shared {
			b.topics.SubscribeShared(shareName, aclFilter, clientID, topic.QoS)
			sess.AddSubscriptionFilter(topic)
			reasonCodes[i] = topic.QoS
			deliverRetained[i] = shouldDeliverRetained(topic.RetainHandling, false)
			continue
		}

		existed := sess.HasSubscription(topic.Topic)
		if !b.topics.Subscribe(topic.Topic, clientID, topic.QoS) {
			reasonCodes[i] = failCode
			continue
		}
		sess.AddSubscriptionFilter(topic)
		reasonCodes[i] = topic.QoS
		deliverRetained[i] = shouldDeliverRetained(topic.RetainHandling, existed)
	}

	b.writePacket(clientID, &protocol.SubAckPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeSubAck,
		},
		PacketID:    pkt.PacketID,
		ReasonCodes: reasonCodes,
	})

	b.metrics.SetSubscriptions(int(b.topics.SubscriberCount()))

	// Deliver retained messages matching the new subscriptions
	for i, topic := range pkt.Topics {
		if !deliverRetained[i] {
			continue
		}
		// For shared subscriptions, use the real topic filter for retained delivery
		if IsSharedSubscription(topic.Topic) {
			_, realFilter, ok := ParseSharedFilter(topic.Topic)
			if ok && protocol.ValidateTopicFilter(realFilter) {
				b.deliverRetainedMessages(ctx, clientID, sess, realFilter)
			}
			continue
		}
		b.deliverRetainedMessages(ctx, clientID, sess, topic.Topic)
	}
}
func shouldDeliverRetained(retainHandling uint8, existed bool) bool {
	switch retainHandling {
	case 1:
		return !existed
	case 2:
		return false
	default:
		return true
	}
}
func (b *Broker) handleUnsubscribe(clientID string, sess *Session, pkt *protocol.UnsubscribePacket) {
	var reasonCodes []byte
	if sess != nil && sess.Spec().ReasonCodes {
		reasonCodes = make([]byte, 0, len(pkt.Topics))
	}

	for _, topic := range pkt.Topics {
		// Handle shared subscription unsubscribe
		if IsSharedSubscription(topic) {
			shareName, realFilter, ok := ParseSharedFilter(topic)
			// Malformed shared filter: same Protocol Error and teardown as a
			// malformed plain one (see rejectMalformedTopic). The old 0x8F
			// (Topic Filter invalid) is defined for a filter that is
			// "correctly formed but is not allowed", which this is not.
			if !ok || !protocol.ValidateTopicFilter(realFilter) {
				b.rejectMalformedTopic(clientID, sess, "unsubscribe-shared")
				return
			}
			b.topics.UnsubscribeShared(shareName, realFilter, clientID)
			sess.RemoveSubscription(topic)
			if reasonCodes != nil {
				reasonCodes = append(reasonCodes, protocol.ReasonCodeSuccess)
			}
			continue
		}

		if !protocol.ValidateTopicFilter(topic) {
			b.rejectMalformedTopic(clientID, sess, "unsubscribe")
			return
		}
		b.topics.Unsubscribe(topic, clientID)
		sess.RemoveSubscription(topic)
		if reasonCodes != nil {
			reasonCodes = append(reasonCodes, protocol.ReasonCodeSuccess)
		}
	}

	b.writePacket(clientID, &protocol.UnsubAckPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeUnsubAck,
		},
		PacketID:    pkt.PacketID,
		ReasonCodes: reasonCodes,
	})

	b.metrics.SetSubscriptions(int(b.topics.SubscriberCount()))
}

func messageExpiresAt(pkt *protocol.PublishPacket) time.Time {
	if pkt.Properties != nil && pkt.Properties.MessageExpiryInterval != nil && *pkt.Properties.MessageExpiryInterval > 0 {
		return time.Now().Add(time.Duration(*pkt.Properties.MessageExpiryInterval) * time.Second)
	}
	return time.Time{}
}

func (b *Broker) deliverToClient(ctx context.Context, clientID, sourceClientID string, pkt *protocol.PublishPacket) {
	sess, ok := b.sessions.GetSession(clientID)
	if !ok {
		b.queueOfflineMessage(ctx, clientID, pkt, messageExpiresAt(pkt))
		return
	}
	if sourceClientID != "" && clientID == sourceClientID && !sess.AllowsLocalPublish(pkt.Topic) {
		return
	}

	// Look up subscription options for the matching subscription to get
	// the SubscriptionIdentifiers (MQTT 5.0) for the delivered PUBLISH.
	matches, subQoS, subOpts, subIDs := sess.MatchesSubscription(pkt.Topic)
	if !matches {
		return
	}
	b.deliverWithQoS(sess, pkt, subQoS, subOpts, subIDs)
}

func (b *Broker) deliverToSharedClient(ctx context.Context, clientID, sourceClientID string, pkt *protocol.PublishPacket, subQoS uint8) {
	sess, ok := b.sessions.GetSession(clientID)
	if !ok {
		return
	}
	if sourceClientID != "" && clientID == sourceClientID && !sess.AllowsLocalPublish(pkt.Topic) {
		return
	}
	b.deliverWithQoS(sess, pkt, subQoS, SubscriptionOptions{QoS: subQoS}, nil)
}

func (b *Broker) deliverWithQoS(sess *Session, pkt *protocol.PublishPacket, subQoS uint8, subOpts SubscriptionOptions, subIDs []uint32) {
	deliverQoS := pkt.FixedHeader.QoS
	if subQoS < deliverQoS {
		deliverQoS = subQoS
	}
	b.doDeliver(sess.ClientID, pkt, deliverQoS, messageExpiresAt(pkt), subOpts, subIDs, false)
}

func (b *Broker) queueOfflineMessage(ctx context.Context, clientID string, pkt *protocol.PublishPacket, expiresAt time.Time) {
	if b.messageStore == nil || b.sessionStore == nil || pkt.FixedHeader.QoS == 0 {
		return
	}
	exists, err := b.sessionStore.IsSessionExists(ctx, clientID)
	if err != nil || !exists {
		return
	}
	data, err := b.sessionStore.GetSession(ctx, clientID)
	if err != nil {
		return
	}
	if data.ExpiryInterval > 0 && !data.ExpiryTime.IsZero() && time.Now().After(data.ExpiryTime) {
		return // session already expired; nothing to deliver to
	}

	// Bound the offline queue per client (audit H2): an offline persistent
	// subscriber must not be able to accumulate an unbounded QoS 1/2 backlog
	// in memory (default backend) or on disk. Over-limit messages are dropped
	// and counted.
	if limit := b.opts.maxOfflineQueue; limit > 0 {
		b.offlineMu.Lock()
		n := b.offlineCount[clientID]
		b.offlineMu.Unlock()
		if n >= limit {
			b.metrics.IncMessagesDropped("offline_queue_full")
			b.logger.Debug("offline queue limit reached, dropping message",
				"clientID", clientID, "limit", limit)
			return
		}
	}

	// Tie a message without its own expiry to the session lifetime so store
	// backends with key TTLs (Redis) keep it exactly as long as the session,
	// and so the queue cannot outlive its owner session (audit).
	if expiresAt.IsZero() && data.ExpiryInterval > 0 && !data.ExpiryTime.IsZero() {
		expiresAt = data.ExpiryTime
	}

	msg := &store.StoredMessage{
		ID:        b.nextMessageID(),
		Topic:     pkt.Topic,
		QoS:       pkt.FixedHeader.QoS,
		Payload:   pkt.Payload,
		Retain:    pkt.Retain,
		Timestamp: time.Now(),
		ExpiresAt: expiresAt,
	}
	if err := b.messageStore.SaveMessage(ctx, clientID, msg); err != nil {
		b.metrics.IncErrors("message_store")
		b.logger.Warn("failed to queue offline message", "clientID", clientID, "error", err)
		return
	}
	b.offlineMu.Lock()
	b.offlineCount[clientID]++
	b.offlineMu.Unlock()
}

func (b *Broker) nextMessageID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), b.msgSeq.Add(1))
}

func (b *Broker) adjustOfflineCount(clientID string, delta int) {
	b.offlineMu.Lock()
	n := b.offlineCount[clientID] + delta
	if n <= 0 {
		delete(b.offlineCount, clientID)
	} else {
		b.offlineCount[clientID] = n
	}
	b.offlineMu.Unlock()
}

func (b *Broker) deliverQueuedMessages(ctx context.Context, clientID string, sess *Session) {
	if b.messageStore == nil {
		return
	}
	msgs, err := b.messageStore.ListMessages(ctx, clientID)
	if err != nil {
		b.logger.Warn("failed to list queued messages", "clientID", clientID, "error", err)
		return
	}
	for _, msg := range msgs {
		// Drop a queued message whose Message Expiry Interval has passed while
		// the client was offline (§3.3.2.3.3).
		if !msg.ExpiresAt.IsZero() && time.Now().After(msg.ExpiresAt) {
			b.metrics.IncMessagesDropped("message_expired")
			if err := b.messageStore.DeleteMessage(ctx, clientID, msg.ID); err != nil {
				b.logger.Warn("failed to delete expired queued message", "clientID", clientID, "error", err)
			} else {
				b.adjustOfflineCount(clientID, -1)
			}
			continue
		}
		matches, subQoS, subOpts, subIDs := sess.MatchesSubscription(msg.Topic)
		if !matches {
			// The subscription that caused this message to be queued no longer
			// exists; previously the message was skipped forever and leaked
			// (audit H2). Remove it so the queue cannot grow without bound.
			b.metrics.IncMessagesDropped("offline_stale")
			if err := b.messageStore.DeleteMessage(ctx, clientID, msg.ID); err != nil {
				b.logger.Warn("failed to delete stale queued message", "clientID", clientID, "error", err)
			} else {
				b.adjustOfflineCount(clientID, -1)
			}
			continue
		}
		deliverQoS := msg.QoS
		if subQoS < deliverQoS {
			deliverQoS = subQoS
		}
		if deliverQoS > 0 && !sess.CanSendOutbound() {
			continue // keep queued; the client must catch up on flow control first
		}
		pubPkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePublish,
				QoS:        deliverQoS,
				Retain:     msg.Retain,
			},
			Topic:   msg.Topic,
			Payload: msg.Payload,
		}
		// The Message Expiry property is MQTT 5.0 wire syntax only; an MQTT
		// 3.1.1 subscriber must not receive the property bytes (audit), even
		// though the deadline itself is still enforced by doDeliver.
		if sess.Spec().Properties && !msg.ExpiresAt.IsZero() {
			pubPkt.Properties = &protocol.Properties{MessageExpiryInterval: remainingExpiry(msg.ExpiresAt)}
		}
		b.doDeliver(clientID, pubPkt, deliverQoS, msg.ExpiresAt, subOpts, subIDs, false)
		if err := b.messageStore.DeleteMessage(ctx, clientID, msg.ID); err != nil {
			b.logger.Warn("failed to delete queued message", "clientID", clientID, "error", err)
		} else {
			b.adjustOfflineCount(clientID, -1)
		}
	}
}

func (b *Broker) doDeliver(clientID string, pkt *protocol.PublishPacket, deliverQoS uint8, expiresAt time.Time, subOpts SubscriptionOptions, subIDs []uint32, retainedDelivery bool) {
	sess, ok := b.sessions.GetSession(clientID)
	if !ok {
		return
	}

	// MQTT 5.0 §3.3.2.3.3: never onward-deliver a message whose Message Expiry
	// Interval has passed.
	if !expiresAt.IsZero() && time.Now().After(expiresAt) {
		b.metrics.IncMessagesDropped("message_expired")
		return
	}

	// MQTT 5.0 flow control (ReceiveMaximum). When the client's receive window
	// is full, buffer the QoS 1/2 message instead of silently dropping it
	// (P2-14); it is flushed once the client acknowledges earlier deliveries.
	// The buffer is bounded (R6): if a client never acknowledges and the buffer
	// is full, the new message is dropped rather than exhausting memory.
	if deliverQoS > 0 && !sess.CanSendOutbound() {
		if !sess.BufferOutbound(pkt, deliverQoS, subOpts, subIDs, expiresAt) {
			b.metrics.IncMessagesDropped("receive_max_buffered_overflow")
			b.metrics.IncErrors("flow_control")
			b.logger.Debug("outbound flow-control buffer full, dropping message",
				"clientID", clientID, "max", maxBufferedOutbound)
		}
		return
	}

	// MQTT-3.3.1-8: a retained message replayed because a subscription was
	// established always carries RETAIN=1. MQTT-3.3.1-9 / MQTT 5.0 3.8.3.1: a
	// live forward carries RETAIN=0 unless the matching subscription set
	// Retain As Published, in which case the publisher's flag is preserved.
	outRetain := retainedDelivery || (subOpts.RetainAsPublished && pkt.Retain)

	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        deliverQoS,
			Retain:     outRetain,
		},
		Topic:   pkt.Topic,
		Payload: pkt.Payload,
	}

	// MQTT 5.0 §3.3.2.3: the Server MUST relay the publisher's per-message
	// properties unaltered (Payload Format Indicator, Content Type, Response
	// Topic, Correlation Data and every User Property, order preserved) and adds
	// the Subscription Identifiers of every matching subscription plus the
	// decremented Message Expiry Interval. See forwardedPublishProperties.
	//
	// This is gated on the *subscriber's* negotiated version: properties exist
	// only in the MQTT 5 wire format, and emitting them to a v3.1.1 subscriber
	// embeds v5 property bytes into a v3.1.1 PUBLISH and corrupts the stream
	// (audit). The expiry deadline is enforced for bookkeeping regardless; only
	// its advertisement is version-gated.
	if sess.Spec().Properties {
		pubPkt.Properties = forwardedPublishProperties(pkt.Properties, subIDs, expiresAt)
	}

	if deliverQoS > 0 {
		pubPkt.PacketID = sess.NextPacketID()
		// Track the outbound delivery so it can be retried until the client
		// acknowledges it (NEW-1) and persisted across a disconnect (P2-3).
		// Record the *effective* retain flag so a retransmission reproduces the
		// original packet instead of the publisher's raw flag, and the property
		// set so the retransmission also keeps the forwarded properties
		// (MQTT 5.0 §3.3.2.3 requires them to survive).
		sess.AddInflight(&InflightMsg{
			PacketID:   pubPkt.PacketID,
			QoS:        deliverQoS,
			Topic:      pkt.Topic,
			Payload:    pkt.Payload,
			Retain:     outRetain,
			SentAt:     time.Now(),
			ExpiresAt:  expiresAt,
			Properties: pubPkt.Properties,
		})
		// A tracking failure means this delivery is not registered for retry,
		// so a lost PUBACK would silently drop the message. Report it instead
		// of discarding the error: the delivery still goes out (the client has
		// it either way), but the condition is now visible in metrics+logs.
		var trackErr error
		if deliverQoS == 1 {
			trackErr = b.qos.TrackOutboundQoS1(clientID, pubPkt.PacketID, pkt.Topic, pkt.Payload, outRetain)
		} else {
			trackErr = b.qos.TrackOutboundQoS2(clientID, pubPkt.PacketID, pkt.Topic, pkt.Payload, outRetain)
		}
		if trackErr != nil {
			b.metrics.IncErrors("qos_track")
			b.logger.Warn("failed to track outbound for retry",
				"clientID", clientID, "packetID", pubPkt.PacketID, "qos", deliverQoS, "error", trackErr)
		}
	}

	sess.TrackSent(len(pkt.Topic) + len(pkt.Payload))

	if deliverQoS > 0 {
		sess.IncOutboundUnacked()
	}

	b.writePacket(clientID, pubPkt)
	b.metrics.IncMessagesDelivered(deliverQoS)
}

func (b *Broker) flushBufferedOutbound(clientID string, sess *Session) {
	for sess.OutboundQueueLen() > 0 {
		if !sess.CanSendOutbound() {
			return
		}
		// The queue length is checked above, so a failed pop can only mean a
		// concurrent drain won the race. Bail out rather than deliver the
		// zero-value message that PopOutbound returns alongside ok=false.
		msg, ok := sess.PopOutbound()
		if !ok {
			return
		}
		b.doDeliver(clientID, msg.pkt, msg.deliverQoS, msg.expiresAt, msg.subOpts, msg.subIDs, false)
	}
}

func (b *Broker) persistBufferedOutbound(ctx context.Context, clientID string, sess *Session) {
	if b.messageStore == nil {
		return
	}
	for {
		msg, ok := sess.PopOutbound()
		if !ok {
			return
		}
		sm := &store.StoredMessage{
			ID:        b.nextMessageID(),
			Topic:     msg.pkt.Topic,
			QoS:       msg.deliverQoS,
			Payload:   msg.pkt.Payload,
			Retain:    msg.pkt.Retain,
			Timestamp: time.Now(),
			ExpiresAt: msg.expiresAt,
		}
		if err := b.messageStore.SaveMessage(ctx, clientID, sm); err != nil {
			b.metrics.IncErrors("message_store")
			continue
		}
		b.offlineMu.Lock()
		b.offlineCount[clientID]++
		b.offlineMu.Unlock()
	}
}

func (b *Broker) isClientOnline(clientID string) bool {
	_, ok := b.sessions.GetSession(clientID)
	return ok
}

func (b *Broker) connection(clientID string) *clientState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.connections[clientID]
}

func (b *Broker) routeSharedPublish(ctx context.Context, sourceClientID string, pkt *protocol.PublishPacket) {
	shared := b.topics.MatchSharedOnline(pkt.Topic, b.isClientOnline)
	for _, sub := range shared {
		b.deliverToSharedClient(ctx, sub.ClientID, sourceClientID, pkt, sub.QoS)
	}
}
func (b *Broker) deliverRetainedMessages(ctx context.Context, clientID string, sess *Session, topicFilter string) {
	if b.retainedStore == nil {
		return
	}

	retained, err := b.retainedStore.MatchRetained(ctx, topicFilter)
	if err != nil {
		b.logger.Warn("failed to match retained messages", "filter", topicFilter, "error", err)
		return
	}
	if len(retained) == 0 {
		return
	}

	for _, msg := range retained {
		// Use the sys-topic-protected matcher so a $SYS retained message is
		// never delivered to a bare '#' or '+' subscription (P3-4).
		matches, subQoS, subOpts, subIDs := sess.MatchesRetainedSubscription(msg.Topic)
		if !matches {
			continue
		}
		deliverQoS := msg.QoS
		if subQoS < deliverQoS {
			deliverQoS = subQoS
		}
		pubPkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePublish,
				QoS:        deliverQoS,
				Retain:     true,
			},
			Topic:   msg.Topic,
			Payload: msg.Payload,
			// The retained copy still is the publisher's Application Message, so
			// §3.3.2.3 applies to this replay exactly as it did to the live
			// forward: the relayable properties go out unaltered. The stored set
			// deliberately excludes the Message Expiry Interval, which is passed
			// as the absolute deadline below so doDeliver can re-derive the
			// remaining time (§3.3.2.3.3) and refuse an already-expired retained
			// message.
			Properties: protocolProperties(msg.Properties),
		}
		// doDeliver applies flow control (buffering when the window is full),
		// assigns the packet ID, and tracks the delivery (NEW-4). The operator's
		// retained TTL is policy rather than a publisher-set lifetime, so it is
		// not advertised as a Message Expiry Interval here.
		b.doDeliver(clientID, pubPkt, deliverQoS, msg.ExpiresAt, subOpts, subIDs, true)
	}
}

func (b *Broker) writePacket(clientID string, pkt protocol.Packet) {
	b.mu.RLock()
	cs, ok := b.connections[clientID]
	b.mu.RUnlock()
	if !ok {
		return
	}

	if pub, ok := pkt.(*protocol.PublishPacket); ok && pub.FixedHeader.QoS == 0 && cs.out != nil {
		select {
		case cs.out <- pkt:
			return
		case <-cs.stopWrites:
			return
		default:
			b.metrics.IncMessagesDropped("write_queue_full")
			return
		}
	}

	if err := cs.writeOrEnqueue(pkt); err != nil {
		b.logger.Warn("write error", "clientID", clientID, "error", err)
		// A failed socket write means the peer is gone or unreachable: close
		// the connection immediately so the readLoop unblocks and the session
		// is cleaned up, instead of lingering as a phantom (zombie) connection
		// until the keep-alive deadline.
		cs.conn.Close()
	}
}
