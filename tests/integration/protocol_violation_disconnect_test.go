// Package integration provides end-to-end integration tests for shark-mqtt.
// Run with: go test -race -tags=integration ./tests/integration/...
package integration

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// disconnectRaw is the exact wire form of an MQTT 5 DISCONNECT that carries only
// a reason code: fixed header 0xE0, Remaining Length 1, then the reason code.
//
// Asserting raw bytes rather than a decoded packet is deliberate. A decoded
// assertion is checked by the same codec that would have to reject the packet,
// so it passes for anything the local codec happens to accept -- exactly the
// blindness that let the CONNACK 0x19 defect ship (see TESTING.md, 缺陷回归测试).
func disconnectRaw(reason byte) []byte { return []byte{0xE0, 0x01, reason} }

// connectV50WithWill performs a v5 CONNECT that registers a will and returns
// once the CONNACK is accepted.
func connectV50WithWill(t *testing.T, conn net.Conn, codec *protocol.Codec, clientID, willTopic, willMessage string) {
	t.Helper()
	connectV50WithWillKeepAlive(t, conn, codec, clientID, willTopic, willMessage, 30)
}

// connectV311 connects with MQTT 3.1.1, which has no server-to-client
// DISCONNECT and therefore behaves differently on every teardown path.
func connectV311(t *testing.T, conn net.Conn, codec *protocol.Codec, clientID string) {
	t.Helper()

	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version311,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        clientID,
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		t.Fatalf("CONNECT (3.1.1): %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	got, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("CONNACK (3.1.1): %v", err)
	}
	if ca, ok := got.(*protocol.ConnAckPacket); !ok || ca.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("CONNECT (3.1.1) rejected: %v", got)
	}
}

// expectDisconnectThenClose asserts the exact DISCONNECT bytes and then that the
// broker closed the connection.
func expectDisconnectThenClose(t *testing.T, conn net.Conn, reason byte) {
	t.Helper()

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	got := make([]byte, len(disconnectRaw(reason)))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("expected a DISCONNECT 0x%02X, got %v", reason, err)
	}
	if want := disconnectRaw(reason); !bytes.Equal(got, want) {
		t.Fatalf("DISCONNECT = % X, want % X", got, want)
	}

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if n, err := conn.Read(make([]byte, 8)); err == nil {
		t.Errorf("connection still open after DISCONNECT: read %d more bytes", n)
	}
}

// TestSecondConnectReceivesDisconnectBeforeClose covers a teardown path that
// already sent a DISCONNECT before this change. It handed the packet to the
// connection's write queue and then tore the connection down; the writer's
// select then picks the closed teardown channel over a queued packet often
// enough that the packet reached the peer only some of the time. The write is
// now confirmed before the close (sendDisconnectBeforeClose).
//
// The will assertion is the other half of the contract: a server-sent DISCONNECT
// must not turn the close into a graceful one, because the client never sent
// DISCONNECT 0x00 (§3.1.2.5, §3.14.4).
func TestSecondConnectReceivesDisconnectBeforeClose(t *testing.T) {
	broker := testBroker(t)

	watcher := dialTestBroker(t, broker)
	watcherCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, watcher, watcherCodec, "will-watcher", "violation/will", 0)

	conn := dialTestBroker(t, broker)
	codec := protocol.NewCodec(0)
	connectV50WithWill(t, conn, codec, "violator-second-connect", "violation/will", "gone")

	// MQTT-3.1.0-2: a second CONNECT on an established connection is a protocol
	// violation (0x82).
	second := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "violator-second-connect",
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, second); err != nil {
		t.Fatalf("second CONNECT: %v", err)
	}

	expectDisconnectThenClose(t, conn, protocol.ReasonCodeProtocolError)

	// abnormalDisconnect still runs, so the will is published.
	watcher.SetReadDeadline(time.Now().Add(3 * time.Second))
	got, err := watcherCodec.Decode(watcher)
	if err != nil {
		t.Fatalf("expected the will PUBLISH: %v", err)
	}
	pub, ok := got.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("will = %T, want *protocol.PublishPacket", got)
	}
	if pub.Topic != "violation/will" || string(pub.Payload) != "gone" {
		t.Fatalf("will = %q %q, want %q %q", pub.Topic, pub.Payload, "violation/will", "gone")
	}
}

// TestUnknownTopicAliasReceivesDisconnectBeforeClose covers the other teardown
// path that sends a DISCONNECT: closeProtocolViolation, reached by a PUBLISH that
// uses a Topic Alias with no mapping (§3.3.2.3.4). It enqueued the packet and
// closed the socket immediately afterwards, which made the packet's arrival a
// matter of scheduling; the write is now confirmed first.
func TestUnknownTopicAliasReceivesDisconnectBeforeClose(t *testing.T) {
	broker := testBroker(t)

	conn := dialTestBroker(t, broker)
	codec := protocol.NewCodec(0)
	connectClient(t, conn, codec, "violator-unknown-alias")

	alias := uint16(7)
	pub := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
		Properties:  &protocol.Properties{TopicAlias: &alias},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pub); err != nil {
		t.Fatalf("PUBLISH with an unmapped alias: %v", err)
	}

	expectDisconnectThenClose(t, conn, protocol.ReasonCodeProtocolError)
}

// TestMQTT311SecondConnectClosesWithoutDisconnect is the negative control for the
// ServerDisconnect gate: MQTT 3.1.1 has no server-to-client DISCONNECT, so the
// same violation must produce a bare close. Without this case a change that sent
// 0x82 to every client would still pass the tests above.
func TestMQTT311SecondConnectClosesWithoutDisconnect(t *testing.T) {
	broker := testBroker(t)

	conn := dialTestBroker(t, broker)
	codec := protocol.NewCodecForVersion(0, protocol.Version311)
	connectV311(t, conn, codec, "violator-311")

	second := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version311,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "violator-311",
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, second); err != nil {
		t.Fatalf("second CONNECT (3.1.1): %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 8)
	if n, err := conn.Read(buf); err == nil {
		t.Fatalf("3.1.1 must not receive a DISCONNECT: read % X", buf[:n])
	}
}
