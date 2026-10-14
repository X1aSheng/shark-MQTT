// Package integration provides end-to-end integration tests for shark-mqtt.
// Run with: go test -race -tags=integration ./tests/integration/...
package integration

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// connectV50WithWillKeepAlive performs a v5 CONNECT that registers a will with an
// explicit Keep Alive, so a test can reach the keepalive reaper in a bounded
// time. It returns once the CONNACK is accepted.
func connectV50WithWillKeepAlive(t *testing.T, conn net.Conn, codec *protocol.Codec, clientID, willTopic, willMessage string, keepAlive uint16) {
	t.Helper()

	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
			WillFlag:     true,
		},
		KeepAlive:   keepAlive,
		ClientID:    clientID,
		WillTopic:   willTopic,
		WillMessage: []byte(willMessage),
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	got, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	if ca, ok := got.(*protocol.ConnAckPacket); !ok || ca.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("CONNECT rejected: %v", got)
	}
}

// TestKeepAliveTimeoutSendsDisconnect covers the last teardown path that was
// still silent. §3.1.2.10 makes the notice optional -- [MQTT-3.1.2-22] requires
// the close, not the DISCONNECT -- but it is the one thing that tells a merely
// stalled peer why the session went away, and it costs a single write that
// writeFinal bounds.
//
// The will assertion is the other half: [MQTT-3.1.2-22] closes "as if the
// network had failed", so routing this through gracefulDisconnect (which removes
// the will) would be a silent regression.
func TestKeepAliveTimeoutSendsDisconnect(t *testing.T) {
	b := testBroker(t)

	watcher := dialTestBroker(t, b)
	watcherCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, watcher, watcherCodec, "will-watcher-ka", "keepalive/will", 0)

	conn := dialTestBroker(t, b)
	codec := protocol.NewCodec(0)
	// Keep Alive 1 arms a 1.5s read deadline. Sending nothing is the whole point
	// of the test, so no PINGREQ is ever sent.
	connectV50WithWillKeepAlive(t, conn, codec, "keepalive-victim", "keepalive/will", "gone", 1)

	expectDisconnectThenClose(t, conn, protocol.DisconnectKeepAliveTimeout)

	watcher.SetReadDeadline(time.Now().Add(3 * time.Second))
	got, err := watcherCodec.Decode(watcher)
	if err != nil {
		t.Fatalf("expected the will PUBLISH: %v", err)
	}
	pub, ok := got.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("will = %T, want *protocol.PublishPacket", got)
	}
	if pub.Topic != "keepalive/will" || string(pub.Payload) != "gone" {
		t.Fatalf("will = %q %q, want %q %q", pub.Topic, pub.Payload, "keepalive/will", "gone")
	}
}

// TestMQTT311KeepAliveTimeoutClosesWithoutDisconnect is the negative control for
// the ServerDisconnect gate on this path: 3.1.1 has no server-to-client
// DISCONNECT, so an expired keepalive must still produce a bare close.
func TestMQTT311KeepAliveTimeoutClosesWithoutDisconnect(t *testing.T) {
	b := testBroker(t)

	conn := dialTestBroker(t, b)
	codec := protocol.NewCodecForVersion(0, protocol.Version311)

	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version311,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       1,
		ClientID:        "keepalive-311",
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

	expectCloseWithoutDisconnect(t, conn)
}
