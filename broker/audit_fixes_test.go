package broker

// Regression tests for audit fixes C2 (protocol reason codes & discard
// semantics):
//   - MQTT 5 auth failure CONNACK carries 0x86, not the v3 code 0x04
//   - MQTT 5 client-id-too-long CONNACK carries 0x85, not 0x82
//   - A discarded QoS 1 PUBLISH from a MQTT 3.1.1 client is PUBACKed and the
//     connection stays usable (no encoder failure -> disconnect)
//   - A discarded QoS 2 PUBLISH receives PUBREC (never PUBACK); the handshake
//     completes with PUBCOMP

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store"
	"github.com/X1aSheng/shark-mqtt/store/memory"
)

// denyAuthorizer accepts authentication but denies every publish.
type denyAuthorizer struct{}

func (denyAuthorizer) CanPublish(ctx context.Context, username, topic string) bool { return false }
func (denyAuthorizer) CanSubscribe(ctx context.Context, username, topic string) bool {
	return true
}

// runRawClient drives HandleConnection from one end of a net.Pipe and returns
// the client-side connection plus a client codec for packet exchange.
func runRawClient(t *testing.T, b *Broker) (net.Conn, *protocol.Codec) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	codec := protocol.NewCodec(0)
	done := make(chan error, 1)
	go func() {
		done <- b.HandleConnection(context.Background(), serverConn, codec)
	}()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("HandleConnection did not return")
		}
	})
	return clientConn, protocol.NewCodec(0)
}

func TestBroker_V5AuthFailureConnAckReasonCode(t *testing.T) {
	b := New(WithAuth(DenyAllAuth{}))
	clientConn, cc := runRawClient(t, b)

	conn := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "v5-client",
		Username:        "u",
	}
	if err := cc.Encode(clientConn, conn); err != nil {
		t.Fatalf("send CONNECT: %v", err)
	}
	pkt, err := cc.Decode(clientConn)
	if err != nil {
		t.Fatalf("read CONNACK: %v", err)
	}
	ack, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}
	if ack.ReasonCode != protocol.ConnAckBadUsernameOrPassword5 {
		t.Fatalf("v5 auth failure reason = 0x%02X, want 0x86", ack.ReasonCode)
	}
}

func TestBroker_V5ClientIDTooLongConnAckReasonCode(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))
	clientConn, cc := runRawClient(t, b)

	longID := make([]byte, 200)
	for i := range longID {
		longID[i] = 'a'
	}
	conn := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        string(longID),
	}
	if err := cc.Encode(clientConn, conn); err != nil {
		t.Fatalf("send CONNECT: %v", err)
	}
	pkt, err := cc.Decode(clientConn)
	if err != nil {
		t.Fatalf("read CONNACK: %v", err)
	}
	ack, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}
	if ack.ReasonCode != protocol.ConnAckClientIdentifierNotValid {
		t.Fatalf("client-id-too-long reason = 0x%02X, want 0x85", ack.ReasonCode)
	}
}

func TestBroker_V3DiscardedQoS1PublishAckedAndStaysConnected(t *testing.T) {
	b := New(
		WithAuth(AllowAllAuth{}),
		WithAuthorizer(denyAuthorizer{}),
	)
	clientConn, cc := runRawClient(t, b)

	conn := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version311,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "v3-client",
	}
	if err := cc.Encode(clientConn, conn); err != nil {
		t.Fatalf("send CONNECT: %v", err)
	}
	if _, err := cc.Decode(clientConn); err != nil {
		t.Fatalf("read CONNACK: %v", err)
	}

	pub := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        1,
		},
		PacketID: 5,
		Topic:    "denied/topic",
		Payload:  []byte("x"),
	}
	if err := cc.Encode(clientConn, pub); err != nil {
		t.Fatalf("send PUBLISH: %v", err)
	}
	pkt, err := cc.Decode(clientConn)
	if err != nil {
		t.Fatalf("read PUBACK: %v", err)
	}
	ack, ok := pkt.(*protocol.PubAckPacket)
	if !ok {
		t.Fatalf("expected PUBACK for discarded v3 QoS1 publish, got %T", pkt)
	}
	if ack.PacketID != 5 {
		t.Fatalf("PUBACK packet id = %d, want 5", ack.PacketID)
	}

	// The connection must remain usable: PINGREQ gets a PINGRESP.
	if err := cc.Encode(clientConn, &protocol.PingReqPacket{FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePingReq}}); err != nil {
		t.Fatalf("send PINGREQ: %v", err)
	}
	pkt, err = cc.Decode(clientConn)
	if err != nil {
		t.Fatalf("read PINGRESP: %v", err)
	}
	if _, ok := pkt.(*protocol.PingRespPacket); !ok {
		t.Fatalf("expected PINGRESP, got %T", pkt)
	}
}

func TestBroker_V5DiscardedQoS2PublishReceivesPubRecThenPubComp(t *testing.T) {
	b := New(
		WithAuth(AllowAllAuth{}),
		WithAuthorizer(denyAuthorizer{}),
	)
	clientConn, cc := runRawClient(t, b)

	conn := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "v5-qos2",
	}
	if err := cc.Encode(clientConn, conn); err != nil {
		t.Fatalf("send CONNECT: %v", err)
	}
	if _, err := cc.Decode(clientConn); err != nil {
		t.Fatalf("read CONNACK: %v", err)
	}

	// QoS 2 PUBLISH denied by the authorizer (topic is wire-valid so the
	// discard happens at the authorization step).
	pub := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        2,
		},
		PacketID: 7,
		Topic:    "denied/qos2",
		Payload:  []byte("x"),
	}
	if err := cc.Encode(clientConn, pub); err != nil {
		t.Fatalf("send PUBLISH: %v", err)
	}
	pkt, err := cc.Decode(clientConn)
	if err != nil {
		t.Fatalf("read first response: %v", err)
	}
	if _, ok := pkt.(*protocol.PubRecPacket); !ok {
		t.Fatalf("expected PUBREC for discarded QoS2 publish, got %T", pkt)
	}

	rel := &protocol.PubRelPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubRel, QoS: 1},
		PacketID:    7,
	}
	if err := cc.Encode(clientConn, rel); err != nil {
		t.Fatalf("send PUBREL: %v", err)
	}
	pkt, err = cc.Decode(clientConn)
	if err != nil {
		t.Fatalf("read PUBCOMP: %v", err)
	}
	if _, ok := pkt.(*protocol.PubCompPacket); !ok {
		t.Fatalf("expected PUBCOMP after PUBREL, got %T", pkt)
	}
}

func TestAllowAllAuthBlocksSysTopicPublish(t *testing.T) {
	var a AllowAllAuth
	if a.CanPublish(context.Background(), "u", "$SYS/broker/version") {
		t.Error("AllowAllAuth must deny publishing to $SYS/broker/version")
	}
	if a.CanPublish(context.Background(), "u", "$anything") {
		t.Error("AllowAllAuth must deny publishing to any $-prefixed topic")
	}
	if !a.CanPublish(context.Background(), "u", "data/room1") {
		t.Error("AllowAllAuth must keep allowing normal topics")
	}
	// Reading system topics stays allowed (wildcard subscription protection
	// still applies at the topic tree level).
	if !a.CanSubscribe(context.Background(), "u", "$SYS/broker/version") {
		t.Error("AllowAllAuth must allow subscribing to system topics")
	}
}

func TestBroker_ClientCannotForgeSysRetainedMessage(t *testing.T) {
	retained := memory.NewRetainedStore()
	b := New(
		WithAuth(AllowAllAuth{}),
		WithRetainedStore(retained),
	)
	clientConn, cc := runRawClient(t, b)

	conn := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "sys-forger",
	}
	if err := cc.Encode(clientConn, conn); err != nil {
		t.Fatalf("send CONNECT: %v", err)
	}
	if _, err := cc.Decode(clientConn); err != nil {
		t.Fatalf("read CONNACK: %v", err)
	}

	// A retained QoS 1 publish to a $SYS topic must be acknowledged but must
	// NOT reach the retained store (forged broker status).
	pub := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        1,
			Retain:     true,
		},
		PacketID: 11,
		Topic:    "$SYS/broker/version",
		Payload:  []byte("9.9.9-forged"),
	}
	if err := cc.Encode(clientConn, pub); err != nil {
		t.Fatalf("send PUBLISH: %v", err)
	}
	pkt, err := cc.Decode(clientConn)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if _, ok := pkt.(*protocol.PubAckPacket); !ok {
		t.Fatalf("expected PUBACK, got %T", pkt)
	}
	if _, err := retained.GetRetained(context.Background(), "$SYS/broker/version"); !errors.Is(err, store.ErrRetainedNotFound) {
		t.Fatalf("forged $SYS retained message was stored (err=%v)", err)
	}

	// A normal retained topic still works, proving the guard is scoped to
	// $-prefixed topics.
	pub2 := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        1,
			Retain:     true,
		},
		PacketID: 12,
		Topic:    "normal/retained",
		Payload:  []byte("ok"),
	}
	if err := cc.Encode(clientConn, pub2); err != nil {
		t.Fatalf("send second PUBLISH: %v", err)
	}
	if pkt, err = cc.Decode(clientConn); err != nil {
		t.Fatalf("read second response: %v", err)
	} else if _, ok := pkt.(*protocol.PubAckPacket); !ok {
		t.Fatalf("expected PUBACK for normal topic, got %T", pkt)
	}
	if msg, err := retained.GetRetained(context.Background(), "normal/retained"); err != nil || string(msg.Payload) != "ok" {
		t.Fatalf("normal retained message not stored: msg=%v err=%v", msg, err)
	}
}
