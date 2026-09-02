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

	"github.com/X1aSheng/shark-mqtt/config"
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

// ---------- C4: ACL filter coverage & session identity binding ----------

func TestStaticAuthACLCoverage(t *testing.T) {
	auth := NewStaticAuth()
	auth.AddCredentials("u", "p")
	auth.AddACL("u", &ACL{
		PublishTopics:   []string{"a/+", "sensors/#"},
		SubscribeTopics: []string{"a/+", "sensors/#"},
	})
	// A second user whose only grant is a root wildcard must not cover
	// $-prefixed topics (MQTT §4.7.2).
	auth.AddCredentials("w", "p")
	auth.AddACL("w", &ACL{
		SubscribeTopics: []string{"#"},
		PublishTopics:   []string{"#"},
	})
	ctx := context.Background()

	for _, tc := range []struct {
		name    string
		allowed bool
		topic   string
	}{
		{"sub exact under +", true, "a/b"},
		{"sub + itself", true, "a/+"},
		{"sub # under + must be denied", false, "a/#"},
		{"sub deeper under + denied", false, "a/b/c"},
		{"sub +/x under + denied", false, "a/+/x"},
		{"sub root # denied", false, "#"},
		{"sub exact sensors", true, "sensors"},
		{"sub deep under #", true, "sensors/temp/1"},
		{"sub # under #", true, "sensors/#"},
		{"sub sibling denied", false, "b/c"},
		{"wildcard cannot cover $SYS", false, "$SYS/uptime"},
	} {
		if got := auth.CanSubscribe(ctx, "u", tc.topic); got != tc.allowed {
			t.Errorf("CanSubscribe(%q) = %v, want %v", tc.topic, got, tc.allowed)
		}
	}

	for _, tc := range []struct {
		name    string
		allowed bool
		topic   string
	}{
		{"pub under +", true, "a/x"},
		{"pub deeper than + denied", false, "a/x/y"},
		{"pub sibling denied", false, "b/x"},
		{"pub under #", true, "sensors/x/y"},
	} {
		if got := auth.CanPublish(ctx, "u", tc.topic); got != tc.allowed {
			t.Errorf("CanPublish(%q) = %v, want %v", tc.topic, got, tc.allowed)
		}
	}

	for _, tc := range []struct {
		name    string
		allowed bool
		topic   string
	}{
		{"root # cannot publish $SYS", false, "$SYS/broker/version"},
		{"normal publish under root #", true, "data/x"},
	} {
		if got := auth.CanPublish(ctx, "w", tc.topic); got != tc.allowed {
			t.Errorf("CanPublish(%q) = %v, want %v", tc.topic, got, tc.allowed)
		}
	}
}

// v5Connect builds an MQTT 5.0 CONNECT packet with credentials.
func v5Connect(clientID, username, password string, clean bool) *protocol.ConnectPacket {
	flags := protocol.ConnectFlags{}
	if clean {
		flags.CleanSession = true
	}
	if username != "" {
		flags.UsernameFlag = true
	}
	if password != "" {
		flags.PasswordFlag = true
	}
	return &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           flags,
		KeepAlive:       60,
		ClientID:        clientID,
		Username:        username,
		Password:        []byte(password),
	}
}

func TestBroker_PersistentSessionBoundToUsername(t *testing.T) {
	auth := NewStaticAuth()
	if err := auth.SetHashedPassword("alice", "alice-pw"); err != nil {
		t.Fatalf("SetHashedPassword alice: %v", err)
	}
	if err := auth.SetHashedPassword("bob", "bob-pw"); err != nil {
		t.Fatalf("SetHashedPassword bob: %v", err)
	}
	b := New(
		WithAuth(auth),
		WithSessionStore(memory.NewSessionStore()),
		WithMessageStore(memory.NewMessageStore()),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("broker start: %v", err)
	}
	t.Cleanup(b.Stop)

	addr := startTcpBroker(t, b)
	connect := func(clientID, username, password string) (net.Conn, *protocol.Codec) {
		t.Helper()
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		cc := protocol.NewCodec(0)
		if err := cc.Encode(conn, v5Connect(clientID, username, password, false)); err != nil {
			t.Fatalf("CONNECT: %v", err)
		}
		pkt, err := cc.Decode(conn)
		if err != nil {
			t.Fatalf("CONNACK: %v", err)
		}
		if _, ok := pkt.(*protocol.ConnAckPacket); !ok {
			t.Fatalf("CONNACK type %T", pkt)
		}
		return conn, cc
	}

	// 1. Alice opens a persistent session, subscribes, and disconnects
	// gracefully (the session is persisted with owner "alice").
	aliceConn, aliceCodec := connect("shared-client", "alice", "alice-pw")
	sub := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "persist/t", QoS: 1}},
	}
	if err := aliceCodec.Encode(aliceConn, sub); err != nil {
		t.Fatalf("alice SUBSCRIBE: %v", err)
	}
	pkt, err := aliceCodec.Decode(aliceConn)
	if err != nil {
		t.Fatalf("alice SUBACK: %v", err)
	}
	if s, ok := pkt.(*protocol.SubAckPacket); !ok || len(s.ReasonCodes) != 1 || s.ReasonCodes[0] != 1 {
		t.Fatalf("alice SUBACK = %#v, want granted QoS 1", pkt)
	}
	if err := aliceCodec.Encode(aliceConn, &protocol.DisconnectPacket{FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeDisconnect}}); err != nil {
		t.Fatalf("alice DISCONNECT: %v", err)
	}
	waitForClose(t, aliceConn)

	// 2. Bob attempts to resume the same clientID: rejected with reason 0x87
	// (not authorized) and the connection is closed; the stored session stays
	// untouched.
	bobConn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("bob dial: %v", err)
	}
	defer bobConn.Close()
	bobCodec := protocol.NewCodec(0)
	if err := bobCodec.Encode(bobConn, v5Connect("shared-client", "bob", "bob-pw", false)); err != nil {
		t.Fatalf("bob CONNECT: %v", err)
	}
	_ = bobConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	pkt, err = bobCodec.Decode(bobConn)
	if err != nil {
		t.Fatalf("bob CONNACK: %v", err)
	}
	if ack, ok := pkt.(*protocol.ConnAckPacket); !ok || ack.ReasonCode != protocol.ConnAckNotAuthorized5 {
		t.Fatalf("bob CONNACK = %#v, want reason 0x87 (not authorized)", pkt)
	}

	// 3. Alice can still resume her session with SessionPresent=1.
	alice2Conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("alice2 dial: %v", err)
	}
	defer alice2Conn.Close()
	alice2Codec := protocol.NewCodec(0)
	if err := alice2Codec.Encode(alice2Conn, v5Connect("shared-client", "alice", "alice-pw", false)); err != nil {
		t.Fatalf("alice2 CONNECT: %v", err)
	}
	_ = alice2Conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	pkt, err = alice2Codec.Decode(alice2Conn)
	if err != nil {
		t.Fatalf("alice2 CONNACK: %v", err)
	}
	ack2, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("alice2 CONNACK type %T", pkt)
	}
	if ack2.ReasonCode != protocol.ConnAckAccepted || !ack2.SessionPresent {
		t.Fatalf("alice2 CONNACK = reason 0x%02X present=%v, want accepted + SessionPresent", ack2.ReasonCode, ack2.SessionPresent)
	}
}

// startTcpBroker serves b through a real TCP listener (127.0.0.1:0) and
// returns the dial address.
func startTcpBroker(t *testing.T, b *Broker) string {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	srv := NewMQTTServer(cfg)
	srv.SetHandler(b)
	if err := srv.Start(); err != nil {
		t.Fatalf("mqtt server start: %v", err)
	}
	t.Cleanup(srv.Stop)
	return srv.Addr().String()
}

// waitForClose blocks until the given connection reports EOF/close.
func waitForClose(t *testing.T, conn net.Conn) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 64)
	for {
		if _, err := conn.Read(buf); err != nil {
			return
		}
	}
}

func TestBroker_SharedSubscriptionAuthorizedByRealFilter(t *testing.T) {
	auth := NewStaticAuth()
	auth.AddCredentials("u", "p")
	auth.AddACL("u", &ACL{SubscribeTopics: []string{"team/+"}})
	b := New(WithAuth(auth), WithAuthorizer(auth))

	clientConn, cc := runRawClient(t, b)
	if err := cc.Encode(clientConn, v5Connect("worker", "u", "p", true)); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	if pkt, err := cc.Decode(clientConn); err != nil {
		t.Fatalf("CONNACK: %v", err)
	} else if _, ok := pkt.(*protocol.ConnAckPacket); !ok {
		t.Fatalf("CONNACK type %T", pkt)
	}

	// A shared subscription whose real filter is covered by the ACL must be
	// granted (the ACL is evaluated against "team/station", not the raw
	// "$share/g1/team/station").
	sub := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "$share/g1/team/station", QoS: 1}},
	}
	if err := cc.Encode(clientConn, sub); err != nil {
		t.Fatalf("SUBSCRIBE shared: %v", err)
	}
	pkt, err := cc.Decode(clientConn)
	if err != nil {
		t.Fatalf("SUBACK shared: %v", err)
	}
	s, ok := pkt.(*protocol.SubAckPacket)
	if !ok || len(s.ReasonCodes) != 1 || s.ReasonCodes[0] != 1 {
		t.Fatalf("shared SUBACK = %#v, want granted QoS 1", pkt)
	}

	// A shared subscription whose real filter is wider than the ACL ("team/#"
	// under "team/+") must be refused.
	sub2 := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe},
		PacketID:    2,
		Topics:      []protocol.TopicFilter{{Topic: "$share/g1/team/#", QoS: 1}},
	}
	if err := cc.Encode(clientConn, sub2); err != nil {
		t.Fatalf("SUBSCRIBE shared wide: %v", err)
	}
	pkt, err = cc.Decode(clientConn)
	if err != nil {
		t.Fatalf("SUBACK shared wide: %v", err)
	}
	s2, ok := pkt.(*protocol.SubAckPacket)
	if !ok || len(s2.ReasonCodes) != 1 || s2.ReasonCodes[0] != protocol.SubAckFailure {
		t.Fatalf("wide shared SUBACK = %#v, want failure", pkt)
	}
}

// ---------- C5: offline queue bounds & cleanup cascades ----------

func TestBroker_OfflineQueueBounded(t *testing.T) {
	sessStore := memory.NewSessionStore()
	msgStore := memory.NewMessageStore()
	b := New(
		WithAuth(AllowAllAuth{}),
		WithSessionStore(sessStore),
		WithMessageStore(msgStore),
		WithMaxOfflineQueue(10),
	)
	ctx := context.Background()
	data := &store.SessionData{
		ClientID:       "cid",
		ExpiryInterval: 3600,
		ExpiryTime:     time.Now().Add(time.Hour),
		Subscriptions:  []store.Subscription{{Topic: "q/t", QoS: 1}},
	}
	if err := sessStore.SaveSession(ctx, "cid", data); err != nil {
		t.Fatalf("save session: %v", err)
	}
	for i := 0; i < 25; i++ {
		b.queueOfflineMessage("cid", &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
			Topic:       "q/t",
			Payload:     []byte("m"),
		}, time.Time{})
	}
	msgs, err := msgStore.ListMessages(ctx, "cid")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 10 {
		t.Fatalf("queued %d messages, want cap of 10", len(msgs))
	}
}

func TestBroker_StaleQueuedMessageDeletedOnReconnect(t *testing.T) {
	sessStore := memory.NewSessionStore()
	msgStore := memory.NewMessageStore()
	b := New(
		WithAuth(AllowAllAuth{}),
		WithSessionStore(sessStore),
		WithMessageStore(msgStore),
	)
	ctx := context.Background()
	data := &store.SessionData{
		ClientID:       "cid",
		ExpiryInterval: 3600,
		ExpiryTime:     time.Now().Add(time.Hour),
		Subscriptions:  []store.Subscription{{Topic: "x/t", QoS: 1}},
	}
	if err := sessStore.SaveSession(ctx, "cid", data); err != nil {
		t.Fatalf("save session: %v", err)
	}
	// A queued message whose topic no longer matches any stored subscription
	// (e.g. left over from an older subscription set) must be removed when
	// the session reconnects, not skipped forever (audit H2).
	if err := msgStore.SaveMessage(ctx, "cid", &store.StoredMessage{
		ID: "stale-1", Topic: "y/t", QoS: 1, Payload: []byte("p"), Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("save message: %v", err)
	}

	sess, err := b.sessions.Restore(ctx, "cid")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	b.deliverQueuedMessages("cid", sess)

	left, err := msgStore.ListMessages(ctx, "cid")
	if err != nil {
		t.Fatalf("list after drain: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("%d stale messages remained in the queue", len(left))
	}
}

func TestBroker_ExpiredSessionCleanupCascadesToQueue(t *testing.T) {
	sessStore := memory.NewSessionStore()
	msgStore := memory.NewMessageStore()
	b := New(
		WithAuth(AllowAllAuth{}),
		WithSessionStore(sessStore),
		WithMessageStore(msgStore),
	)
	ctx := context.Background()
	exp := time.Now().Add(800 * time.Millisecond)
	data := &store.SessionData{
		ClientID:       "cid",
		ExpiryInterval: 1,
		ExpiryTime:     exp,
		Subscriptions:  []store.Subscription{{Topic: "q/t", QoS: 1}},
	}
	if err := sessStore.SaveSession(ctx, "cid", data); err != nil {
		t.Fatalf("save session: %v", err)
	}
	b.queueOfflineMessage("cid", &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		Topic:       "q/t",
		Payload:     []byte("m"),
	}, time.Time{})

	time.Sleep(1100 * time.Millisecond)
	b.cleanupExpiredSessions()

	if _, err := sessStore.GetSession(ctx, "cid"); !errors.Is(err, store.ErrSessionNotFound) {
		t.Fatalf("expired session still present: %v", err)
	}
	left, err := msgStore.ListMessages(ctx, "cid")
	if err != nil {
		t.Fatalf("list after cleanup: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("%d messages outlived their expired session", len(left))
	}
}
