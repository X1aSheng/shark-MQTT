package broker

// Regression tests for version-gated in-connection retransmission.
//
// MQTT 3.1.1 §4.4 caps what a peer is REQUIRED to redeliver without forbidding
// anything -- "This is the only circumstance where a Client or Server is
// REQUIRED to redeliver messages [MQTT-4.4.0-1]" -- and its non-normative note
// keeps the historic retransmission legitimate for lossy networks. MQTT 5.0
// §4.4 keeps that clause and adds the prohibition: "Clients and Servers MUST
// NOT resend messages at any other time [MQTT-4.4.0-1]." The retry loop
// therefore has to hold still for a 5.0 subscriber, and the decision has to
// come from the version the CONNECT negotiated.

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestBroker_RetransmissionFollowsTheSessionVersion drives the whole path over
// a real connection: a subscriber that never acknowledges its QoS 1 delivery is
// re-sent it only when its negotiated version allows retransmission.
//
// This is also the wiring guard for QoSEngine.SetClientVersion: without the
// call on the CONNECT path the engine would fall back to the 3.1.1 default and
// the 5.0 case below would retransmit.
func TestBroker_RetransmissionFollowsTheSessionVersion(t *testing.T) {
	const retryInterval = 40 * time.Millisecond

	tests := []struct {
		name       string
		version    protocol.Version
		wantResend bool
		why        string
	}{
		{
			name:       "mqtt 3.1.1 subscriber is retried",
			version:    protocol.Version311,
			wantResend: true,
			why:        "3.1.1 4.4 permits retransmission outside the reconnect case",
		},
		{
			name:       "mqtt 5.0 subscriber is not retried",
			version:    protocol.Version50,
			wantResend: false,
			why:        "[MQTT-4.4.0-1] forbids resending at any other time",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := New(
				WithAuth(AllowAllAuth{}),
				WithQoSOptions(WithRetryInterval(retryInterval), WithMaxRetries(100)),
			)
			if err := b.Start(); err != nil {
				t.Fatalf("broker start: %v", err)
			}
			t.Cleanup(b.Stop)
			addr := startTcpBroker(t, b)

			sub, subCodec := connectWithVersion(t, addr, "policy-sub", tt.version)
			t.Cleanup(func() { _ = sub.Close() })

			if err := subCodec.Encode(sub, &protocol.SubscribePacket{
				FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
				PacketID:    1,
				Topics:      []protocol.TopicFilter{{Topic: "policy/topic", QoS: 1}},
			}); err != nil {
				t.Fatalf("SUBSCRIBE: %v", err)
			}
			if _, err := subCodec.Decode(sub); err != nil { // SUBACK
				t.Fatalf("SUBACK: %v", err)
			}

			publishQoS1AndComplete(t, addr, "policy/topic", []byte("unacked"), tt.version)

			// Read across several retry intervals without ever acknowledging.
			// Only the first attempt may arrive for a version that has no
			// in-connection retransmission.
			_ = sub.SetReadDeadline(time.Now().Add(retryInterval * 8))
			deliveries := 0
			for {
				pkt, err := subCodec.Decode(sub)
				if err != nil {
					break
				}
				if _, ok := pkt.(*protocol.PublishPacket); ok {
					deliveries++
				}
			}

			if tt.wantResend {
				if deliveries < 2 {
					t.Fatalf("PUBLISH count = %d, want the delivery re-sent: %s",
						deliveries, tt.why)
				}
				return
			}
			if deliveries != 1 {
				t.Fatalf("PUBLISH count = %d, want exactly the original delivery: %s",
					deliveries, tt.why)
			}
		})
	}
}

// connectWithVersion opens a TCP connection to addr, completes the MQTT
// handshake with the requested protocol version and returns the connection with
// its codec. The read deadline the handshake needed is cleared so the caller
// sets its own.
func connectWithVersion(t *testing.T, addr, clientID string, ver protocol.Version) (net.Conn, *protocol.Codec) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", clientID, err)
	}
	codec := protocol.NewCodec(0)
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := codec.Encode(conn, connectPacketOfVersion(clientID, ver)); err != nil {
		_ = conn.Close()
		t.Fatalf("CONNECT %s: %v", clientID, err)
	}
	pkt, err := codec.Decode(conn)
	if err != nil {
		_ = conn.Close()
		t.Fatalf("CONNACK %s: %v", clientID, err)
	}
	ack, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		_ = conn.Close()
		t.Fatalf("CONNACK %s: got %T", clientID, pkt)
	}
	if ack.ReasonCode != protocol.ConnAckAccepted {
		_ = conn.Close()
		t.Fatalf("CONNACK %s: reason %d", clientID, ack.ReasonCode)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, codec
}

// connectPacketOfVersion builds a clean-session CONNECT for a protocol version,
// carrying the protocol name that version requires (3.1 renamed the protocol to
// "MQTT" in 3.1.1, so a 3.1 client must still announce "MQIsdp").
func connectPacketOfVersion(clientID string, ver protocol.Version) *protocol.ConnectPacket {
	name := protocol.ProtocolNameMQTT
	if ver == protocol.Version31 {
		name = protocol.ProtocolNameMQIsdp
	}
	return &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    name,
		ProtocolVersion: ver,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        clientID,
	}
}

// publishQoS1AndComplete publishes one QoS 1 message from a throwaway client and
// reads the PUBACK, which is what makes the broker route the message onward.
func publishQoS1AndComplete(t *testing.T, addr, topic string, payload []byte, ver protocol.Version) {
	t.Helper()
	conn, codec := connectWithVersion(t, addr, "policy-pub", ver)
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := codec.Encode(conn, &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    1,
		Topic:       topic,
		Payload:     payload,
	}); err != nil {
		t.Fatalf("PUBLISH %s: %v", topic, err)
	}
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("PUBACK for %s: %v", topic, err)
	}
	if _, ok := pkt.(*protocol.PubAckPacket); !ok {
		t.Fatalf("packet after QoS 1 PUBLISH = %T, want a PUBACK", pkt)
	}
}
