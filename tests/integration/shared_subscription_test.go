package integration

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestSharedSubscriptionRejectsNoLocal verifies [MQTT-3.8.3-4]: it is a
// Protocol Error to set the No Local bit to 1 on a Shared Subscription, and the
// server must not accept it.
//
// Accepting it was worse than a missed check: No Local is enforced downstream
// by matching the delivered topic against the stored filter, and the stored
// filter is "$share/group/a" -- which never matches the real topic "a". The
// subscriber therefore kept receiving its own publishes after explicitly asking
// not to, with no error anywhere.
func TestSharedSubscriptionRejectsNoLocal(t *testing.T) {
	b := testBroker(t)
	conn := dialTestBroker(t, b)
	codec := protocol.NewCodec(0)

	connect := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "shared-nolocal",
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, connect); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	if _, err := codec.Decode(conn); err != nil {
		t.Fatalf("CONNACK: %v", err)
	}

	sub := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics: []protocol.TopicFilter{{
			Topic:   "$share/team/nolocal/topic",
			QoS:     1,
			NoLocal: true,
		}},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, sub); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}

	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("expected a DISCONNECT for shared subscription with No Local set, got read error: %v", err)
	}
	disc, ok := pkt.(*protocol.DisconnectPacket)
	if !ok {
		t.Fatalf("expected DISCONNECT (Protocol Error), got %T", pkt)
	}
	if disc.ReasonCode != protocol.ReasonCodeProtocolError {
		t.Fatalf("DISCONNECT reason = 0x%02X, want 0x82 (Protocol Error)", disc.ReasonCode)
	}
}

// TestSharedSubscriptionWithoutNoLocalStillWorks is the control: the guard must
// not reject ordinary shared subscriptions.
func TestSharedSubscriptionWithoutNoLocalStillWorks(t *testing.T) {
	b := testBroker(t)
	conn := dialTestBroker(t, b)
	codec := protocol.NewCodec(0)

	connect := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "shared-plain",
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, connect); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	if _, err := codec.Decode(conn); err != nil {
		t.Fatalf("CONNACK: %v", err)
	}

	sub := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "$share/team/plain/topic", QoS: 1}},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, sub); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}

	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("SUBACK: %v", err)
	}
	suback, ok := pkt.(*protocol.SubAckPacket)
	if !ok {
		t.Fatalf("expected SUBACK, got %T", pkt)
	}
	if len(suback.ReasonCodes) != 1 || suback.ReasonCodes[0] != 1 {
		t.Fatalf("SUBACK reason codes = %v, want [1]", suback.ReasonCodes)
	}
}
