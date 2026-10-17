package broker

// Outbound QoS 1/2 flows: PUBACK/PUBREC/PUBREL/PUBCOMP handling, inflight
// retransmission, and Will publishing.

import (
	"context"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

func (b *Broker) handlePubAck(clientID string, packetID uint16) {
	b.qos.AckQoS1(clientID, packetID)
	if sess, ok := b.sessions.GetSession(clientID); ok {
		// Only count a real outbound message as acknowledged; a spurious
		// PUBACK must not drive the flow-control counter below zero (P2-16).
		if sess.RemoveInflight(packetID) {
			sess.DecOutboundUnacked()
		}
		b.flushBufferedOutbound(clientID, sess)
	}
}

func (b *Broker) handlePubRec(clientID string, packetID uint16, reasonCode byte) {
	if reasonCode >= protocol.ReasonCodeFailureBase {
		b.dropRejectedDelivery(clientID, packetID, reasonCode)
		return
	}

	// Record the handshake stage before the PUBREL leaves. A QoS 2 delivery
	// answered with a PUBREC owes its peer a PUBREL, and that -- not the
	// PUBLISH -- is what a reconnect has to resend (MQTT 5.0 §4.4,
	// [MQTT-4.3.3-6]). Marking first is the conservative order: re-sending a
	// PUBREL is always safe, re-sending the PUBLISH after one is not. The
	// refusing path above must never reach this, or the session would record a
	// PUBREL debt for a delivery the peer already refused.
	if sess, ok := b.sessions.GetSession(clientID); ok {
		sess.MarkInflightPubRel(packetID)
	}
	b.qos.AckPubRec(clientID, packetID)
}

func (b *Broker) dropRejectedDelivery(clientID string, packetID uint16, reasonCode byte) {
	b.qos.ReleaseInflight(clientID, packetID)
	if sess, ok := b.sessions.GetSession(clientID); ok {
		if sess.RemoveInflight(packetID) {
			sess.DecOutboundUnacked()
		}
		b.flushBufferedOutbound(clientID, sess)
	}
	b.metrics.IncMessagesDropped("pubrec_rejected")
	b.logger.Debug("outbound QoS 2 delivery refused by peer",
		"clientID", clientID, "packetID", packetID, "reasonCode", reasonCode)
}
func (b *Broker) handlePubRel(ctx context.Context, clientID string, packetID uint16) {
	// Route the staged inbound QoS 2 message to subscribers now that the
	// client completed its half of the handshake. Entries are session-owned
	// and persisted, so a restored session still delivers (audit H4); an
	// unknown packet id (e.g. discarded earlier) simply completes with
	// PUBCOMP.
	if sess, ok := b.sessions.GetSession(clientID); ok {
		if msg, taken := sess.TakeInboundQoS2(packetID); taken {
			pubPkt := &protocol.PublishPacket{
				FixedHeader: protocol.FixedHeader{
					PacketType: protocol.PacketTypePublish,
					QoS:        msg.QoS,
					// Carry the publisher's RETAIN flag through; doDeliver
					// applies Retain As Published per subscription.
					Retain: msg.Retain,
				},
				Topic:    msg.Topic,
				Payload:  msg.Payload,
				PacketID: msg.PacketID,
				// The staging record kept the publisher's MQTT 5 properties for
				// exactly this moment: doDeliver relays them to each subscriber
				// (§3.3.2.3), and it is the only chance to, since the record is
				// dropped below.
				Properties: msg.Properties,
			}
			subscribers := b.topics.Match(msg.Topic)
			for _, sub := range subscribers {
				b.deliverToClient(ctx, sub.ClientID, clientID, pubPkt)
			}
			b.routeSharedPublish(ctx, clientID, pubPkt)
		}
	}
	b.writePacket(clientID, &protocol.PubCompPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubComp},
		PacketID:    packetID,
	})
}
func (b *Broker) handlePubComp(clientID string, packetID uint16) {
	b.qos.AckPubComp(clientID, packetID)
	if sess, ok := b.sessions.GetSession(clientID); ok {
		// Only count a real outbound message as acknowledged; an inbound QoS 2
		// PUBCOMP has no outbound inflight entry (P2-16).
		if sess.RemoveInflight(packetID) {
			sess.DecOutboundUnacked()
		}
		b.flushBufferedOutbound(clientID, sess)
	}
}
func (b *Broker) sendPubAck(clientID string, packetID uint16) error {
	pkt := &protocol.PubAckPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePubAck,
		},
		PacketID: packetID,
	}
	b.writePacket(clientID, pkt)
	return nil
}
func (b *Broker) sendPubRel(clientID string, packetID uint16) error {
	b.writePacket(clientID, pubRelPacket(packetID))
	return nil
}

func pubRelPacket(packetID uint16) *protocol.PubRelPacket {
	return &protocol.PubRelPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePubRel,
			QoS:        1,
		},
		PacketID: packetID,
	}
}

func buildRetransmitPublish(msg InflightMsg) *protocol.PublishPacket {
	props := msg.Properties
	if props != nil && !msg.ExpiresAt.IsZero() {
		refreshed := *props
		refreshed.MessageExpiryInterval = remainingExpiry(msg.ExpiresAt)
		props = &refreshed
	}
	return &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        msg.QoS,
			Dup:        true,
			Retain:     msg.Retain,
		},
		PacketID:   msg.PacketID,
		Topic:      msg.Topic,
		Payload:    msg.Payload,
		Properties: props,
	}
}

func (b *Broker) restoreSessionInflight(clientID string, restored *Session) {
	for _, msg := range restored.InflightSnapshot() {
		if msg.QoS == 2 {
			if msg.PendingPubRel {
				_ = b.qos.TrackOutboundQoS2AwaitingPubComp(clientID, msg.PacketID, msg.Topic, msg.Payload, msg.Retain)
			} else {
				_ = b.qos.TrackOutboundQoS2(clientID, msg.PacketID, msg.Topic, msg.Payload, msg.Retain)
			}
			continue
		}
		_ = b.qos.TrackOutboundQoS1(clientID, msg.PacketID, msg.Topic, msg.Payload, msg.Retain)
	}
}

func (b *Broker) resendSessionInflight(clientID string, sess *Session) {
	for _, msg := range sess.InflightSnapshot() {
		if msg.QoS == 2 && msg.PendingPubRel {
			b.writePacket(clientID, pubRelPacket(msg.PacketID))
			continue
		}
		b.writePacket(clientID, buildRetransmitPublish(msg))
	}
}
func (b *Broker) sendPubComp(clientID string, packetID uint16) error {
	pkt := &protocol.PubCompPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePubComp,
		},
		PacketID: packetID,
	}
	b.writePacket(clientID, pkt)
	return nil
}
func (b *Broker) republish(ctx context.Context, clientID string, packetID uint16, topic string, payload []byte, qos uint8, retain bool) error {
	// The QoS engine's retry callback fires for both directions. An outbound
	// delivery (broker->subscriber, tracked in the session's Inflight) is
	// re-sent as a PUBLISH with the DUP flag. An inbound QoS 2 publish has the
	// client waiting on our PUBREC, so re-send the PUBREC.
	//
	// Only packetID is needed for the outbound branch: the session record is
	// the authority for the packet's content, and topic/payload/qos/retain are
	// kept in the signature because the QoS engine's callback type carries
	// them for the inbound case.
	//
	// Message Expiry is deliberately NOT applied here. [MQTT-3.3.2-5] only
	// obliges the Server to delete the copy when it "has not managed to start
	// onward delivery to a matching subscriber" -- and a record in the
	// session's Inflight is one that has already been sent. [MQTT-4.3.3-7]
	// goes further for QoS 2: the sender MUST NOT apply Application Message
	// expiry once a PUBLISH has been sent. The deadline still reaches the
	// subscriber as a re-derived interval (buildRetransmitPublish), which is
	// what [MQTT-3.3.2-6] asks for; dropping the delivery instead would end the
	// exchange on our side while the peer is still holding the packet id, and
	// would leave the QoS engine retrying a packet id the session no longer
	// knows about -- which falls through to the PUBREC branch below and puts a
	// stray PUBREC for an outbound delivery on the wire.
	if sess, ok := b.sessions.GetSession(clientID); ok {
		if msg, found := sess.GetInflight(packetID); found && msg.QoS == qos {
			b.writePacket(clientID, buildRetransmitPublish(*msg))
			return nil
		}
	}
	// Nothing left to resend. The session's Inflight is the authority, and it
	// no longer holds this packet id: either the delivery was acknowledged
	// between the engine's snapshot and this call, or the session is gone. Both
	// mean there is nothing to retransmit -- and a PUBREC here would be a
	// protocol error, since a PUBREC answers an inbound PUBLISH whose packet id
	// belongs to the peer, not to a delivery the broker sent.
	//
	// The inbound direction is not routed through this callback at all: inbound
	// QoS 2 is tracked by Session.inboundQoS2, and TrackQoS1/TrackQoS2 have no
	// caller outside the benchmarks. If inbound retry is ever wired up it needs
	// its own callback, not this fallback.
	return nil
}

func (b *Broker) publishWill(ctx context.Context, username string, topic string, payload []byte, qos uint8, retain bool, props *protocol.Properties) error {
	// A will message must respect publish authorization just like a normal
	// PUBLISH, otherwise any client could set a will on a topic it has no
	// permission to publish.
	if b.opts.authorizer != nil && !b.opts.authorizer.CanPublish(ctx, username, topic) {
		b.logger.Debug("will publish denied by authorizer", "topic", topic, "username", username)
		b.metrics.IncAuthFailures()
		return nil
	}

	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        qos,
			Retain:     retain,
		},
		Topic:      topic,
		Payload:    payload,
		Properties: props,
	}

	if retain {
		b.handleRetainedMessage(ctx, pubPkt)
	}

	// The live forward keeps the publisher's RETAIN flag; doDeliver applies
	// Retain As Published per subscription.
	subscribers := b.topics.Match(topic)
	for _, sub := range subscribers {
		b.deliverToClient(ctx, sub.ClientID, "", pubPkt)
	}
	b.routeSharedPublish(ctx, "", pubPkt)
	return nil
}
