package broker

// CONNACK construction and delivery, including the MQTT 5.0 reason string
// and capability properties.

import (
	"net"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

func (b *Broker) sendConnAck(clientID string, reasonCode byte, sessionPresent bool, sess *Session) {
	b.mu.RLock()
	cs, ok := b.connections[clientID]
	b.mu.RUnlock()
	if !ok {
		return
	}

	// MQTT 3.1 has no Session Present flag: the first CONNACK byte is reserved
	// and must be 0 regardless of the internal resume state
	// (Spec.ConnAckSessionPresent is false for 3.1).
	if sess != nil && !sess.Spec().ConnAckSessionPresent {
		sessionPresent = false
	}

	pkt := buildConnAck(reasonCode, sessionPresent)
	if sess != nil && sess.Spec().Properties {
		pkt.Properties = b.buildConnAckProperties(sess)
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

func (b *Broker) buildConnAckProperties(sess *Session) *protocol.Properties {
	xretainAvailable := byte(0)
	if b.retainedStore != nil {
		xretainAvailable = 1
	}
	wildcardAvailable := byte(1)
	// The broker fully supports Subscription Identifiers (MQTT 5.0 §3.8.2.1.2):
	// the SUBSCRIBE packet-level SubscriptionIdentifier property is parsed and
	// stored per subscription, and the identifier is echoed back in delivered
	// PUBLISH packets (§3.3.2.3.7). Advertise support so clients are not
	// misled by a SubIDAvailable=0 CONNACK.
	subIDAvailable := byte(1)
	sharedSubAvailable := byte(1)

	receiveMax := sess.ReceiveMax
	if receiveMax == 0 {
		receiveMax = 65535
	}
	maxPktSize := uint32(b.opts.maxPacketSize)

	props := &protocol.Properties{
		SessionExpiryInterval: &sess.ExpiryInterval,
		ReceiveMaximum:        &receiveMax,
		RetainAvailable:       &xretainAvailable,
		MaximumPacketSize:     &maxPktSize,
		WildcardSubAvailable:  &wildcardAvailable,
		SubIDAvailable:        &subIDAvailable,
		SharedSubAvailable:    &sharedSubAvailable,
	}
	// Response Information: the Server may send it only when the client asked
	// for it with a Request Response Information of 1, and the client ID is the
	// basis for building a response topic.
	//
	// The request itself must NOT be echoed back. Table 2-4 lists Request
	// Response Information (0x19) for CONNECT only and Response Information
	// (0x1A) for CONNACK, so a CONNACK carrying 0x19 is a Malformed Packet
	// (§2.2.2.2) that a conformant client has to reject -- and its content is
	// redundant anyway, since the presence of Response Information is what
	// conveys the answer.
	if sess.RequestResponseInfo {
		props.ResponseInfo = sess.ClientID
	}

	// Advertise the largest Topic Alias the server accepts FROM the client
	// (§3.2.2.3.8). The client's own CONNECT value is a different property
	// that happens to share identifier 0x22 and is deliberately not echoed
	// here (see Session.ClientTopicAliasMax).
	if sess.TopicAliasMax > 0 {
		tam := sess.TopicAliasMax
		props.TopicAliasMaximum = &tam
	}

	// Server Keep Alive: if server enforces a shorter keep-alive than client requests.
	if sess.ServerKeepAlive != nil {
		props.ServerKeepAlive = sess.ServerKeepAlive
	}

	// Assigned Client ID: if server generated the client ID.
	if sess.AssignedClientID != "" {
		props.AssignedClientID = sess.AssignedClientID
	}

	return props
}

func buildConnAck(reasonCode byte, sessionPresent bool) *protocol.ConnAckPacket {
	return &protocol.ConnAckPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnAck,
		},
		ReasonCode:     reasonCode,
		SessionPresent: sessionPresent,
	}
}

func writeConnAck(conn net.Conn, codec protocol.PacketCodec, reasonCode byte, sessionPresent bool) error {
	if err := codec.Encode(conn, buildConnAck(reasonCode, sessionPresent)); err != nil {
		return err
	}
	if f, ok := conn.(packetFlusher); ok {
		return f.FlushPacket()
	}
	return nil
}
func (b *Broker) sendConnAckRaw(conn net.Conn, codec protocol.PacketCodec, reasonCode byte, sessionPresent bool) {
	if err := writeConnAck(conn, codec, reasonCode, sessionPresent); err != nil {
		b.logger.Debug("failed to send CONNACK", "error", err)
	}
}
