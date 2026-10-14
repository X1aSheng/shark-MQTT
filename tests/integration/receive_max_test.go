package integration

// Receive Maximum contract tests (MQTT 5.0 §3.1.2.11.6 / §3.2.2.3.3 / §3.3.4).
//
// Property identifier 0x21 appears in two packets and means two different
// things: in CONNECT it is how many QoS 1/2 publishes the CLIENT is willing to
// process concurrently (§3.1.2.11.6), in CONNACK it is how many the SERVER is
// willing to process from the client (§3.2.2.3.3). The two values are
// independent -- a client may declare 1 while the server declares 100 -- so
// treating them as one value is invisible on a happy path: the numbers only
// disagree when the client declares something other than the server's own
// limit.

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestConnAckReceiveMaxIsServerLimit covers the direction of property 0x21.
// A client sending "Receive Maximum = 1" is telling the server it will process
// one concurrent QoS 1/2 publish; it is not asking to be allowed only one.
// The broker stored min(client, server) in a single field and echoed it, so
// the CONNACK told such a client the server would accept only one in-flight
// publish from it -- a false statement about the server's own capacity, which
// throttled the client for no reason.
func TestConnAckReceiveMaxIsServerLimit(t *testing.T) {
	brk := testBroker(t)

	conn := dialTestBroker(t, brk)
	codec := protocol.NewCodec(0)

	declared := uint16(1)
	connectPkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "receive-max-direction",
		Properties:      &protocol.Properties{ReceiveMaximum: &declared},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, connectPkt); err != nil {
		t.Fatalf("CONNECT with Receive Maximum 1: %v", err)
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	ca, ok := pkt.(*protocol.ConnAckPacket)
	if !ok || ca.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("CONNECT rejected: %v", pkt)
	}
	if ca.Properties == nil || ca.Properties.ReceiveMaximum == nil {
		t.Fatal("CONNACK omitted Receive Maximum: a v5 server must state the inbound limit it enforces")
	}

	// 100 is the broker's own inbound limit (brokerOptions.maxInflight). The
	// assertion is about whose number this is, not about the value: it must be
	// the server's, never the client's declared 1.
	if got := *ca.Properties.ReceiveMaximum; got != 100 {
		t.Fatalf("CONNACK ReceiveMaximum = %d, want 100 (the server's inbound limit, not the client's declared 1)", got)
	}
}
