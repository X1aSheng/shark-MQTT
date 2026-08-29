package integration

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// connectV5WithExpiry connects an MQTT 5.0 client with the given session
// expiry interval property (nil = property absent) and returns the CONNACK.
func connectV5WithExpiry(t *testing.T, broker *api.Broker, clientID string, expiry *uint32) *protocol.ConnAckPacket {
	t.Helper()
	conn := dialTestBroker(t, broker)
	codec := protocol.NewCodec(0)
	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: false},
		KeepAlive:       30,
		ClientID:        clientID,
	}
	if expiry != nil {
		pkt.Properties = &protocol.Properties{SessionExpiryInterval: expiry}
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	resp, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	ca, ok := resp.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", resp)
	}
	return ca
}

// TestSessionExpiryZero_EndsSessionOnDisconnect verifies MQTT 5.0
// §3.1.2.11.2: an explicit Session Expiry Interval of 0 ends the session when
// the connection closes — the CONNACK must report 0 and a reconnect must not
// resume the session (SessionPresent=0).
func TestSessionExpiryZero_EndsSessionOnDisconnect(t *testing.T) {
	broker := testBroker(t)

	zero := uint32(0)

	// First connection: clean=false with an explicit expiry of 0.
	conn := dialTestBroker(t, broker)
	codec := protocol.NewCodec(0)
	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: false},
		KeepAlive:       30,
		ClientID:        "expiry-zero",
		Properties:      &protocol.Properties{SessionExpiryInterval: &zero},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	resp, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	ca, ok := resp.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", resp)
	}
	if ca.Properties == nil || ca.Properties.SessionExpiryInterval == nil ||
		*ca.Properties.SessionExpiryInterval != 0 {
		t.Fatalf("expected CONNACK SessionExpiryInterval=0, got %+v", ca.Properties)
	}

	// Graceful disconnect: the session must end with the connection close.
	disc := &protocol.DisconnectPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeDisconnect},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, disc); err != nil {
		t.Fatalf("DISCONNECT: %v", err)
	}
	conn.Close()
	// Give the broker's read loop time to process the disconnect before the
	// reconnect below (a takeover would legitimately keep the session).
	time.Sleep(300 * time.Millisecond)

	// Reconnect with the same client ID and clean=false: the previous session
	// ended with the connection close, so SessionPresent must be 0.
	ca2 := connectV5WithExpiry(t, broker, "expiry-zero", &zero)
	if ca2.SessionPresent {
		t.Error("expected SessionPresent=0 after expiry-0 session disconnected")
	}
}

// TestSessionExpiryAbsent_UsesServerDefault verifies an absent Session Expiry
// Interval property uses the server-configured default (24h), and the session
// survives a reconnect (SessionPresent=1).
func TestSessionExpiryAbsent_UsesServerDefault(t *testing.T) {
	broker := testBroker(t)

	ca := connectV5WithExpiry(t, broker, "expiry-absent", nil)
	if ca.Properties == nil || ca.Properties.SessionExpiryInterval == nil {
		t.Fatal("expected CONNACK SessionExpiryInterval property")
	}
	if *ca.Properties.SessionExpiryInterval != 24*3600 {
		t.Errorf("expected server default 86400s, got %d", *ca.Properties.SessionExpiryInterval)
	}

	// The session must survive a disconnect/reconnect cycle.
	ca2 := connectV5WithExpiry(t, broker, "expiry-absent", nil)
	if !ca2.SessionPresent {
		t.Error("expected SessionPresent=1 for server-default expiry session")
	}
}

// TestDisconnectUpdatesSessionExpiry verifies MQTT 5.0 §3.14.2.2.2: a
// DISCONNECT carrying Session Expiry Interval 0 ends the session immediately,
// so a later reconnect must not resume it.
func TestDisconnectUpdatesSessionExpiry(t *testing.T) {
	broker := testBroker(t)

	hour := uint32(3600)
	conn := dialTestBroker(t, broker)
	codec := protocol.NewCodec(0)
	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: false},
		KeepAlive:       30,
		ClientID:        "expiry-disconnect",
		Properties:      &protocol.Properties{SessionExpiryInterval: &hour},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := codec.Decode(conn); err != nil {
		t.Fatalf("CONNACK: %v", err)
	}

	// DISCONNECT with Session Expiry Interval 0: session ends now.
	zero := uint32(0)
	disc := &protocol.DisconnectPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeDisconnect},
		Properties:  &protocol.Properties{SessionExpiryInterval: &zero},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, disc); err != nil {
		t.Fatalf("DISCONNECT: %v", err)
	}
	conn.Close()

	// Reconnect: the session must be gone.
	ca := connectV5WithExpiry(t, broker, "expiry-disconnect", &hour)
	if ca.SessionPresent {
		t.Error("expected SessionPresent=0 after DISCONNECT with expiry 0")
	}
}
