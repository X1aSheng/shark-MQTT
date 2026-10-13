package broker

// Regression tests for the PUBREC Reason Code (audit fix C2 follow-up).
//
// The Reason Code on a subscriber's PUBREC selects which of the two handshake
// endings applies, and the broker used to ignore it entirely:
// `handlePubRec(clientID, p.PacketID)` never passed it on, so every PUBREC was
// treated as accepting.
//
//   - [MQTT-4.3.3-4]: a PUBREC "with a Reason Code value less than 0x80" obliges
//     the sender to send the PUBREL that carries the exchange forward.
//   - §3.5.2.1: the sub-0x80 codes are the accepting ones -- 0x00 Success and
//     0x10 No matching subscribers, whose own description is "The message is
//     accepted but there are no subscribers".
//   - [MQTT-4.4.0-2]: "If PUBACK or PUBREC is received containing a Reason Code
//     of 0x80 or greater the corresponding PUBLISH packet is treated as
//     acknowledged, and MUST NOT be retransmitted."
//   - §4.3.3: the Packet Identifier "becomes available for reuse once the sender
//     has received the PUBCOMP packet or a PUBREC with a Reason Code of 0x80 or
//     greater".
//
// So a refusing PUBREC must end the exchange on the spot, with no PUBREL and no
// retransmission -- and it must still release the receive-window slot, or a
// subscriber that keeps refusing would shrink the broker's window to nothing.

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestBroker_QoS2PubRecReasonCodeSelectsTheHandshakeEnding drives a real QoS 2
// delivery and answers it with each kind of PUBREC, asserting that the packet
// going back out (or not going out) matches the Reason Code.
func TestBroker_QoS2PubRecReasonCodeSelectsTheHandshakeEnding(t *testing.T) {
	const topic = "c2/pubrec"

	tests := []struct {
		name       string
		reasonCode byte
		wantPubRel bool
		why        string
	}{
		{
			name:       "success continues with the PUBREL",
			reasonCode: protocol.ReasonCodeSuccess,
			wantPubRel: true,
			why:        "[MQTT-4.3.3-4]: any Reason Code below 0x80 owes a PUBREL",
		},
		{
			name:       "no matching subscribers still continues",
			reasonCode: 0x10,
			wantPubRel: true,
			why:        "0x10 is an accepting code -- the message is accepted, there are just no subscribers",
		},
		{
			name:       "not authorized ends it without a PUBREL",
			reasonCode: protocol.ReasonCodeNotAuthorized,
			wantPubRel: false,
			why:        "[MQTT-4.4.0-2]: the PUBLISH is treated as acknowledged and must not be retransmitted",
		},
		{
			name:       "quota exceeded ends it too",
			reasonCode: protocol.ReasonCodeQuotaExceeded,
			wantPubRel: false,
			why:        "every code at or above 0x80 refuses the message",
		},
		{
			name:       "the boundary value itself refuses",
			reasonCode: protocol.ReasonCodeFailureBase,
			wantPubRel: false,
			why:        "0x80 is the first refusing code, so the comparison must be >= and not >",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const clientID = "pubrec-sub"
			b := publishRelayBroker(t)
			addr := startTcpBroker(t, b)

			sub, subCodec, _ := dialAndConnect(t, addr, clientID, false)
			t.Cleanup(func() { _ = sub.Close() })
			if err := subCodec.Encode(sub, &protocol.SubscribePacket{
				FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
				PacketID:    1,
				Topics:      []protocol.TopicFilter{{Topic: topic, QoS: 2}},
			}); err != nil {
				t.Fatalf("SUBSCRIBE: %v", err)
			}
			if _, err := subCodec.Decode(sub); err != nil { // SUBACK
				t.Fatalf("SUBACK: %v", err)
			}

			publishQoS2AndComplete(t, addr, topic, []byte("verdict"))

			pkt, err := subCodec.Decode(sub)
			if err != nil {
				t.Fatalf("waiting for the QoS 2 delivery: %v", err)
			}
			pub, ok := pkt.(*protocol.PublishPacket)
			if !ok {
				t.Fatalf("delivery = %T, want a PUBLISH", pkt)
			}
			packetID := pub.PacketID

			if err := subCodec.Encode(sub, &protocol.PubRecPacket{
				FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubRec},
				PacketID:    packetID,
				ReasonCode:  tc.reasonCode,
			}); err != nil {
				t.Fatalf("PUBREC 0x%02x: %v", tc.reasonCode, err)
			}

			if tc.wantPubRel {
				pkt, err = subCodec.Decode(sub)
				if err != nil {
					t.Fatalf("waiting for the PUBREL: %v", err)
				}
				pubrel, ok := pkt.(*protocol.PubRelPacket)
				if !ok {
					t.Fatalf("packet after PUBREC 0x%02x = %T, want a PUBREL: %s",
						tc.reasonCode, pkt, tc.why)
				}
				if pubrel.PacketID != packetID {
					t.Errorf("PUBREL PacketID = %d, want %d ([MQTT-4.3.3-4] pins it to the PUBLISH)",
						pubrel.PacketID, packetID)
				}
				if err := subCodec.Encode(sub, &protocol.PubCompPacket{
					FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubComp},
					PacketID:    packetID,
				}); err != nil {
					t.Fatalf("PUBCOMP: %v", err)
				}
			} else {
				expectNoPacket(t, sub, subCodec, 300*time.Millisecond,
					"a PUBREC with Reason Code 0x%02x refuses the message: %s", tc.reasonCode, tc.why)
			}

			// Either ending releases the delivery: a refusal frees the Packet
			// Identifier just like a PUBCOMP does (§4.3.3), and the window slot
			// goes back with it. Leaving the entry behind would both leak the
			// identifier and, once the window filled up, stall delivery to this
			// subscriber for good.
			waitInflightDrained(t, b, clientID)
		})
	}
}

// expectNoPacket asserts the broker sends nothing more within a short window.
// A read timeout is the expected outcome; a packet or a broken connection is a
// failure.
func expectNoPacket(t *testing.T, conn net.Conn, codec *protocol.Codec, within time.Duration, format string, args ...any) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(within)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	pkt, err := codec.Decode(conn)
	if err == nil {
		t.Fatalf("broker sent %T but nothing may follow: "+format, append([]any{pkt}, args...)...)
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("expected a quiet connection, got read error %v", err)
	}
}
