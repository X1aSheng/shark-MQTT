package integration

// Topic Alias contract tests (MQTT 5.0 §3.3.2.3.4).
//
// Property identifier 0x22 appears in two packets and means two different
// things: in CONNECT it is the highest alias the CLIENT accepts FROM the
// server (§3.1.2.11.5), in CONNACK it is the highest alias the SERVER accepts
// FROM the client (§3.2.2.3.8). The tests here pin both directions, because
// treating them as one value is invisible in a happy-path test: everything
// still works until a client declares a value different from the server's own
// limit.

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestTopicAliasAboveMaximumReceivesDisconnectBeforeClose covers a PUBLISH whose
// Topic Alias exceeds the maximum the server advertised (MQTT 5.0 §3.3.2.3.4).
// The alias value itself is illegal, so §4.13 answers it with 0x94 (Topic Alias
// invalid). The failure used to be swallowed by a Debug-level log: the mapping
// was never registered, and the client learned nothing until its next
// empty-topic publish was torn down as an unmapped alias.
func TestTopicAliasAboveMaximumReceivesDisconnectBeforeClose(t *testing.T) {
	brk := testBroker(t)

	conn := dialTestBroker(t, brk)
	codec := protocol.NewCodec(0)
	connectClient(t, conn, codec, "violator-alias-range")

	// The server advertises 64 (serverTopicAliasMax); 65 is one past it.
	alias := uint16(65)
	pub := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
		Topic:       "violation/alias",
		Properties:  &protocol.Properties{TopicAlias: &alias},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pub); err != nil {
		t.Fatalf("PUBLISH with an out-of-range alias: %v", err)
	}

	expectDisconnectThenClose(t, conn, protocol.ReasonCodeTopicAliasInvalid)
}

// TestTopicAliasUsableWhenClientDeclaresZero covers the direction of property
// 0x22. A client sending "Topic Alias Maximum = 0" is telling the server it
// will accept no aliases; it is not refusing to send them. The broker used to
// store that value as its own inbound limit, so the CONNACK advertised 0 --
// "this server accepts no aliases" -- and every alias the client sent was
// treated as an out-of-range mapping and closed the connection.
//
// The QoS 1 publish through the registered alias is the load-bearing assertion:
// its PUBACK can only arrive if the alias registered and the connection
// survived.
func TestTopicAliasUsableWhenClientDeclaresZero(t *testing.T) {
	brk := testBroker(t)

	conn := dialTestBroker(t, brk)
	codec := protocol.NewCodec(0)

	zero := uint16(0)
	connectPkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "alias-zero-declared",
		Properties:      &protocol.Properties{TopicAliasMaximum: &zero},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, connectPkt); err != nil {
		t.Fatalf("CONNECT with Topic Alias Maximum 0: %v", err)
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
	if ca.Properties == nil || ca.Properties.TopicAliasMaximum == nil {
		t.Fatal("CONNACK omitted Topic Alias Maximum: the server accepts 64 aliases, so the client was told it accepts none")
	}
	if got := *ca.Properties.TopicAliasMaximum; got != 64 {
		t.Fatalf("CONNACK TopicAliasMaximum = %d, want 64 (the server's inbound limit, not the client's declared 0)", got)
	}

	// Register alias 1 with an explicit topic name, at QoS 0.
	alias := uint16(1)
	register := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
		Topic:       "alias/direction",
		Payload:     []byte("registered"),
		Properties:  &protocol.Properties{TopicAlias: &alias},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, register); err != nil {
		t.Fatalf("PUBLISH registering alias 1: %v", err)
	}

	// Reuse the alias with an empty Topic Name: the broker must resolve it
	// server-side and stay connected.
	use := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    7,
		Payload:     []byte("via-alias"),
		Properties:  &protocol.Properties{TopicAlias: &alias},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, use); err != nil {
		t.Fatalf("PUBLISH via alias 1: %v", err)
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	ackPkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("expected a PUBACK for the aliased publish, so the connection was torn down: %v", err)
	}
	ack, ok := ackPkt.(*protocol.PubAckPacket)
	if !ok {
		t.Fatalf("aliased publish answered with %T, want *protocol.PubAckPacket", ackPkt)
	}
	if ack.PacketID != 7 {
		t.Fatalf("PUBACK packetID = %d, want 7", ack.PacketID)
	}
}
