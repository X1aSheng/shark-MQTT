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

// TestCleanStartWithSessionExpiryIntervalRetainsSession is the end-to-end
// proof of [MQTT-3.1.2-23] for the combination the broker used to get wrong.
// Clean Start and Session Expiry Interval are independent: the first decides
// what happens to the session that existed before the CONNECT (§3.1.2.4,
// [MQTT-3.1.2-4]), the second what happens to this one after the connection
// closes (§3.1.2.11.2). The spec's non-normative note equates "Clean Start to
// 1 AND a Session Expiry Interval of 0" with 3.1.1's CleanSession = 1 -- an
// equivalence between two settings, not a rule that one implies the other.
//
// So a client that connects with Clean Start = 1 and Session Expiry Interval
// = 3600 must still have its session, subscriptions and queued messages an
// hour later, and a continuation reconnect must be told SessionPresent = 1.
func TestCleanStartWithSessionExpiryIntervalRetainsSession(t *testing.T) {
	broker := testBroker(t)

	hour := uint32(3600)

	// First connection: clean start together with a non-zero interval.
	conn := dialTestBroker(t, broker)
	codec := protocol.NewCodec(0)
	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "clean-start-retain",
		Properties:      &protocol.Properties{SessionExpiryInterval: &hour},
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
	if ca.Properties == nil || ca.Properties.SessionExpiryInterval == nil {
		t.Fatalf("CONNACK carried no Session Expiry Interval: %+v", ca.Properties)
	}
	if got := *ca.Properties.SessionExpiryInterval; got != hour {
		t.Fatalf("CONNACK Session Expiry Interval = %d, want %d", got, hour)
	}
	if ca.SessionPresent {
		t.Error("a clean start must not report SessionPresent=1")
	}

	// Subscribe so there is session state worth keeping.
	sub := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "clean/start/retain", QoS: 1}},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, sub); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	resp, err = codec.Decode(conn)
	if err != nil {
		t.Fatalf("SUBACK: %v", err)
	}
	sa, ok := resp.(*protocol.SubAckPacket)
	if !ok {
		t.Fatalf("expected SUBACK, got %T", resp)
	}
	if len(sa.ReasonCodes) != 1 || sa.ReasonCodes[0] != 1 {
		t.Fatalf("SUBACK reason codes = %v, want [1]", sa.ReasonCodes)
	}

	// Graceful disconnect: the session outlives the connection.
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

	// Reconnect with Clean Start = 0: this is a continuation, so the session
	// must be reported present.
	conn2 := dialTestBroker(t, broker)
	codec2 := protocol.NewCodec(0)
	reconnect := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: false},
		KeepAlive:       30,
		ClientID:        "clean-start-retain",
	}
	conn2.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec2.Encode(conn2, reconnect); err != nil {
		t.Fatalf("reconnect CONNECT: %v", err)
	}
	conn2.SetDeadline(time.Now().Add(2 * time.Second))
	resp, err = codec2.Decode(conn2)
	if err != nil {
		t.Fatalf("reconnect CONNACK: %v", err)
	}
	ca2, ok := resp.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", resp)
	}
	if !ca2.SessionPresent {
		t.Fatal("expected SessionPresent=1: the session had a non-zero expiry interval")
	}

	// The subscription must have survived too -- publish from a second
	// connection and expect it on the reconnected one.
	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "clean-start-publisher")
	pub := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    1,
		Topic:       "clean/start/retain",
		Payload:     []byte("after-reconnect"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pub); err != nil {
		t.Fatalf("PUBLISH: %v", err)
	}
	conn2.SetDeadline(time.Now().Add(2 * time.Second))
	resp, err = codec2.Decode(conn2)
	if err != nil {
		t.Fatalf("reconnected client did not receive the PUBLISH: %v", err)
	}
	delivered, ok := resp.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", resp)
	}
	if delivered.Topic != "clean/start/retain" || string(delivered.Payload) != "after-reconnect" {
		t.Errorf("delivered %q on %q, want \"after-reconnect\" on \"clean/start/retain\"",
			delivered.Payload, delivered.Topic)
	}
}

// TestCleanStartDropsPreviousSubscriptions is the control for the test above,
// and it covers a leak the fix would otherwise have opened. [MQTT-3.1.2-4]
// makes a clean connect discard the existing Session, and "the Session"
// includes its topic-tree entries: a filter that outlives the session it
// belonged to keeps matching PUBLISH packets for a client that has not
// subscribed on this Network Connection, which turns a fresh start into a
// silent continuation of the old one.
//
// The filter is a SHARED one on purpose. A regular filter is re-checked
// against the receiving session's own subscription list on delivery, so a
// stale entry there is merely untidy and a "nothing arrives" assertion could
// not tell the release from its absence. The shared delivery path has no such
// re-check -- the topic tree is its only authority -- so a stale $share entry
// really does route a PUBLISH to a client that never subscribed, and this
// assertion can actually fail.
func TestCleanStartDropsPreviousSubscriptions(t *testing.T) {
	broker := testBroker(t)

	hour := uint32(3600)

	// First connection: persistent session with a subscription.
	conn := dialTestBroker(t, broker)
	codec := protocol.NewCodec(0)
	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: false},
		KeepAlive:       30,
		ClientID:        "clean-start-drops",
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
	sub := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "$share/grp/clean/start/drops", QoS: 1}},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, sub); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := codec.Decode(conn); err != nil {
		t.Fatalf("SUBACK: %v", err)
	}
	conn.Close()
	time.Sleep(300 * time.Millisecond)

	// Reconnect with Clean Start = 1: the old session and its subscription
	// are discarded, so nothing must arrive on the old topic any more.
	conn2 := dialTestBroker(t, broker)
	codec2 := protocol.NewCodec(0)
	fresh := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "clean-start-drops",
	}
	conn2.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec2.Encode(conn2, fresh); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	conn2.SetDeadline(time.Now().Add(2 * time.Second))
	resp, err := codec2.Decode(conn2)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	ca, ok := resp.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", resp)
	}
	if ca.SessionPresent {
		t.Error("a clean start must not report SessionPresent=1")
	}

	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "clean-start-drops-publisher")
	pub := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 0},
		Topic:       "clean/start/drops",
		Payload:     []byte("stale"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pub); err != nil {
		t.Fatalf("PUBLISH: %v", err)
	}

	conn2.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if n, err := conn2.Read(make([]byte, 64)); n > 0 {
		t.Errorf("clean-start client received %d bytes on a shared subscription it never made", n)
	} else if err == nil {
		t.Error("read returned no data and no error: expected a timeout")
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
