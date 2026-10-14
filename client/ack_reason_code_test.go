package client

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// ─── Defensive reason-code guards ─────────────────────────────
//
// The client must never report success when the peer declined. Today the path
// is unreachable on the wire: Connect pins protocol.Version311, and a 3.1.1
// codec rejects any ack longer than its two-byte form (decodeAckFields), so a
// Reason Code cannot survive decoding. The guards are defensive depth for the
// day the client negotiates MQTT 5.0, which is why these tests have to opt the
// codec in explicitly via protocol.NewCodecForVersion.

// pipeReasonCodeClient wires a client onto one end of an in-memory pipe with an
// MQTT 5.0 codec so acknowledgements carrying Reason Codes can be decoded, and
// returns the peer end for the test to drive by hand.
func pipeReasonCodeClient(t *testing.T) (*MQTTClient, net.Conn) {
	t.Helper()
	clientConn, peerConn := net.Pipe()

	c := New()
	c.codec = protocol.NewCodecForVersion(c.opts.MaxPacketSize, protocol.Version50)

	connCtx, connCancel := context.WithCancel(context.Background())
	connDone := make(chan struct{})
	// The codec is installed before readLoop starts, so the goroutine sees it
	// through the usual happens-before of a goroutine start.
	c.mu.Lock()
	c.conn = clientConn
	c.connected = true
	c.connCtx, c.connCancel, c.connDone = connCtx, connCancel, connDone
	c.mu.Unlock()

	go c.readLoop(clientConn, connCtx, connCancel, connDone)

	t.Cleanup(func() {
		connCancel()
		_ = clientConn.Close()
		_ = peerConn.Close()
	})
	return c, peerConn
}

// TestPublishQoS2StopsBeforePubRelWhenPubRecRejects mirrors the broker's
// handlePubRec: a PUBREC of 0x80 or greater ends the exchange, so no PUBREL may
// follow ([MQTT-4.3.3-4], [MQTT-4.4.0-2]) and the caller must learn why.
func TestPublishQoS2StopsBeforePubRelWhenPubRecRejects(t *testing.T) {
	c, peer := pipeReasonCodeClient(t)
	peerCodec := protocol.NewCodecForVersion(0, protocol.Version50)

	peerDone := make(chan error, 1)
	go func() {
		pkt, err := peerCodec.Decode(peer)
		if err != nil {
			peerDone <- fmt.Errorf("read PUBLISH: %w", err)
			return
		}
		pub, ok := pkt.(*protocol.PublishPacket)
		if !ok {
			peerDone <- fmt.Errorf("expected PUBLISH, got %T", pkt)
			return
		}
		if pub.FixedHeader.QoS != 2 {
			peerDone <- fmt.Errorf("PUBLISH QoS = %d, want 2", pub.FixedHeader.QoS)
			return
		}

		rec := &protocol.PubRecPacket{
			PacketID:   pub.PacketID,
			ReasonCode: protocol.ReasonCodeQuotaExceeded,
		}
		rec.FixedHeader.PacketType = protocol.PacketTypePubRec
		if err := peerCodec.Encode(peer, rec); err != nil {
			peerDone <- fmt.Errorf("send PUBREC: %w", err)
			return
		}

		// Nothing may follow the refusing PUBREC. A deadline turns "no packet"
		// into an error, which is exactly the expected outcome.
		_ = peer.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		if extra, err := peerCodec.Decode(peer); err == nil {
			peerDone <- fmt.Errorf("a packet followed the refusing PUBREC: %T", extra)
			return
		}
		peerDone <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := c.Publish(ctx, "qos2/refused", 2, false, []byte("payload"))
	if err == nil {
		t.Fatal("Publish returned nil for a PUBREC that declined the delivery")
	}
	if !strings.Contains(err.Error(), "0x97") {
		t.Errorf("Publish error = %v, want it to carry reason code 0x97", err)
	}
	if perr := <-peerDone; perr != nil {
		t.Fatal(perr)
	}
}

// TestPublishQoS2ReportsPubCompFailure covers the other end of the QoS 2
// exchange: a PUBCOMP carrying a failure code (0x92 Packet Identifier not
// found) means the message was never accepted, so a bare nil would be a lie.
func TestPublishQoS2ReportsPubCompFailure(t *testing.T) {
	c, peer := pipeReasonCodeClient(t)
	peerCodec := protocol.NewCodecForVersion(0, protocol.Version50)

	peerDone := make(chan error, 1)
	go func() {
		pkt, err := peerCodec.Decode(peer)
		if err != nil {
			peerDone <- fmt.Errorf("read PUBLISH: %w", err)
			return
		}
		pub, ok := pkt.(*protocol.PublishPacket)
		if !ok {
			peerDone <- fmt.Errorf("expected PUBLISH, got %T", pkt)
			return
		}

		rec := &protocol.PubRecPacket{PacketID: pub.PacketID} // 0x00: accepted
		rec.FixedHeader.PacketType = protocol.PacketTypePubRec
		if err := peerCodec.Encode(peer, rec); err != nil {
			peerDone <- fmt.Errorf("send PUBREC: %w", err)
			return
		}

		pkt, err = peerCodec.Decode(peer)
		if err != nil {
			peerDone <- fmt.Errorf("read PUBREL: %w", err)
			return
		}
		rel, ok := pkt.(*protocol.PubRelPacket)
		if !ok {
			peerDone <- fmt.Errorf("expected PUBREL, got %T", pkt)
			return
		}

		comp := &protocol.PubCompPacket{
			PacketID:   rel.PacketID,
			ReasonCode: protocol.ReasonCodePacketIdentifierNotFound,
		}
		comp.FixedHeader.PacketType = protocol.PacketTypePubComp
		peerDone <- peerCodec.Encode(peer, comp)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := c.Publish(ctx, "qos2/nopid", 2, false, []byte("payload"))
	if err == nil {
		t.Fatal("Publish returned nil for a PUBCOMP carrying a failure code")
	}
	if !strings.Contains(err.Error(), "0x92") {
		t.Errorf("Publish error = %v, want it to carry reason code 0x92", err)
	}
	if perr := <-peerDone; perr != nil {
		t.Fatal(perr)
	}
}

// TestUnsubscribeReportsUnsubAckFailure covers the third place a peer's
// refusal used to be dropped: Unsubscribe has no return payload, so a
// per-filter failure code can only surface as an error.
func TestUnsubscribeReportsUnsubAckFailure(t *testing.T) {
	c, peer := pipeReasonCodeClient(t)
	peerCodec := protocol.NewCodecForVersion(0, protocol.Version50)

	peerDone := make(chan error, 1)
	go func() {
		pkt, err := peerCodec.Decode(peer)
		if err != nil {
			peerDone <- fmt.Errorf("read UNSUBSCRIBE: %w", err)
			return
		}
		sub, ok := pkt.(*protocol.UnsubscribePacket)
		if !ok {
			peerDone <- fmt.Errorf("expected UNSUBSCRIBE, got %T", pkt)
			return
		}

		ack := &protocol.UnsubAckPacket{
			PacketID:    sub.PacketID,
			ReasonCodes: []byte{protocol.ReasonCodeTopicFilterInvalid},
		}
		ack.FixedHeader.PacketType = protocol.PacketTypeUnsubAck
		peerDone <- peerCodec.Encode(peer, ack)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := c.Unsubscribe(ctx, []string{"bad/#/filter"})
	if err == nil {
		t.Fatal("Unsubscribe returned nil for an UNSUBACK that refused the filter")
	}
	if !strings.Contains(err.Error(), "0x8f") {
		t.Errorf("Unsubscribe error = %v, want it to carry reason code 0x8f", err)
	}
	if perr := <-peerDone; perr != nil {
		t.Fatal(perr)
	}
}
