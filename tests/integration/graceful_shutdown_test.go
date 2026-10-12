package integration

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// willSpec describes the Will Message to register on a test CONNECT.
type willSpec struct {
	topic   string
	payload []byte
	retain  bool
}

// readUntilClosed drains every packet still readable from conn, stopping at
// the first read error (EOF once the broker closed the socket). Used after a
// shutdown to inspect whatever the broker managed to send before closing.
func readUntilClosed(t *testing.T, conn net.Conn, codec *protocol.Codec) []protocol.Packet {
	t.Helper()
	var pkts []protocol.Packet
	for {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		pkt, err := codec.Decode(conn)
		if err != nil {
			return pkts
		}
		pkts = append(pkts, pkt)
	}
}

// connectWillClient connects with a Will Message for an explicit protocol
// version and verifies CONNACK.
func connectWillClient(t *testing.T, conn net.Conn, codec *protocol.Codec, clientID string, version byte, w willSpec) {
	t.Helper()
	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: version,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
			WillFlag:     true,
			WillQoS:      1,
			WillRetain:   w.retain,
		},
		KeepAlive:   30,
		ClientID:    clientID,
		WillTopic:   w.topic,
		WillMessage: w.payload,
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		t.Fatalf("CONNECT(will): %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	ack, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	ca, ok := ack.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", ack)
	}
	if ca.ReasonCode != protocol.ReasonCodeSuccess {
		t.Fatalf("expected CONNACK accepted, got %d", ca.ReasonCode)
	}
}

// TestGracefulShutdown_NotifiesMQTT5Clients covers the rolling-restart notice:
// every connected MQTT 5.0 client must be told the server is going away with
// DISCONNECT 0x8B (MQTT 5.0 3.14.2.1) before its socket is closed.
func TestGracefulShutdown_NotifiesMQTT5Clients(t *testing.T) {
	brk := testBroker(t)

	conn := dialTestBroker(t, brk)
	codec := protocol.NewCodec(0)
	connectWillClient(t, conn, codec, "shutdown-notify", protocol.Version50,
		willSpec{topic: "shutdown/will", payload: []byte("bye")})

	brk.Stop()

	var sawDisconnect bool
	for _, p := range readUntilClosed(t, conn, codec) {
		d, ok := p.(*protocol.DisconnectPacket)
		if !ok {
			continue
		}
		sawDisconnect = true
		if d.ReasonCode != protocol.DisconnectServerShuttingDown {
			t.Errorf("DISCONNECT reason = 0x%02X, want 0x%02X",
				d.ReasonCode, protocol.DisconnectServerShuttingDown)
		}
	}
	if !sawDisconnect {
		t.Error("expected DISCONNECT 0x8B (server shutting down) before the socket closed")
	}
}

// TestGracefulShutdown_NoWillStorm is the core regression guard. A broker-wide
// Stop must NOT publish the wills of connected clients: a rolling restart
// closes every socket at once, and firing one will per client would flood every
// will topic even though no client failed.
//
// The will is registered as RETAINED so the effect is observable across the
// shutdown: if (and only if) the will was wrongly published, the retained store
// keeps it and a subscriber on a restarted broker receives it.
func TestGracefulShutdown_NoWillStorm(t *testing.T) {
	brk := testBrokerWithRetain(t)

	// Two clients, one retained will and one plain, to mirror a mixed fleet.
	retained := dialTestBroker(t, brk)
	connectWillClient(t, retained, protocol.NewCodec(0), "storm-retained", protocol.Version50,
		willSpec{topic: "storm/retained", payload: []byte("boom"), retain: true})

	plain := dialTestBroker(t, brk)
	connectWillClient(t, plain, protocol.NewCodec(0), "storm-plain", protocol.Version50,
		willSpec{topic: "storm/plain", payload: []byte("boom")})

	brk.Stop()
	if err := brk.Start(); err != nil {
		t.Fatalf("re-Start after Stop: %v", err)
	}

	// A fresh subscription on the restarted broker must find NO retained will.
	sub := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectTestClient(t, sub, subCodec, "storm-sub")
	subscribeToTopic(t, sub, subCodec, "storm/retained", 1, 1)

	_ = sub.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if pkt, err := subCodec.Decode(sub); err == nil {
		if pub, ok := pkt.(*protocol.PublishPacket); ok {
			t.Errorf("will storm: retained will %q survived the graceful shutdown", pub.Payload)
		}
	}
}

// TestGracefulShutdown_MQTT311QuietClose: MQTT 3.1.1 has no server-sent
// DISCONNECT packet (its DISCONNECT is client-to-server only), so a graceful
// Stop must just close the socket -- no stray packet.
func TestGracefulShutdown_MQTT311QuietClose(t *testing.T) {
	brk := testBroker(t)

	sub := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectWithVersion(t, sub, subCodec, "v311-shutdown-sub", protocol.Version311, protocol.ProtocolNameMQTT)
	subscribeToTopic(t, sub, subCodec, "v311/will", 1, 1)

	willConn := dialTestBroker(t, brk)
	willCodec := protocol.NewCodec(0)
	connectWillClient(t, willConn, willCodec, "v311-shutdown-will", protocol.Version311,
		willSpec{topic: "v311/will", payload: []byte("bye")})

	brk.Stop()

	for _, p := range readUntilClosed(t, sub, subCodec) {
		t.Errorf("MQTT 3.1.1 client received unexpected %T after graceful shutdown", p)
	}
}

// TestGracefulShutdown_StartClearsShutdownFlag guards the other direction: the
// suppression flag must be cleared by a Start, or a broker restarted after a
// graceful Stop would silently stop publishing wills forever.
func TestGracefulShutdown_StartClearsShutdownFlag(t *testing.T) {
	brk := testBroker(t)
	brk.Stop()
	if err := brk.Start(); err != nil {
		t.Fatalf("re-Start after Stop: %v", err)
	}

	sub := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectTestClient(t, sub, subCodec, "restart-sub")
	subscribeToTopic(t, sub, subCodec, "restart/will", 1, 1)

	willConn := dialTestBroker(t, brk)
	willCodec := protocol.NewCodec(0)
	connectWillClient(t, willConn, willCodec, "restart-will", protocol.Version50,
		willSpec{topic: "restart/will", payload: []byte("offline")})

	// A genuine abnormal disconnect must still fire the will after a restart.
	willConn.Close()

	got := receivePublish(t, sub, subCodec)
	if got.Topic != "restart/will" {
		t.Errorf("topic = %q, want restart/will", got.Topic)
	}
	if string(got.Payload) != "offline" {
		t.Errorf("payload = %q, want offline", got.Payload)
	}
}
