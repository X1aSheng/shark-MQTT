package integration

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestAuth_Failure tests that a client with wrong credentials is rejected.
func TestAuth_Failure(t *testing.T) {
	auth := broker.NewStaticAuth()
	auth.AddCredentials("admin", "secret")

	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"

	b := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(auth),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { b.Stop() })

	conn, err := net.DialTimeout("tcp", b.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	codec := protocol.NewCodec(0)
	connectPkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "bad-auth-client",
		Username:        "admin",
		Password:        []byte("wrong-password"),
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, connectPkt); err != nil {
		t.Fatalf("encode CONNECT: %v", err)
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("decode CONNACK: %v", err)
	}

	connAck, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}
	if connAck.ReasonCode == protocol.ConnAckAccepted {
		t.Error("expected rejection, got ConnAckAccepted")
	}
}

// TestDuplicateClientID tests that a second connection with the same client ID
// kicks the first connection.
func TestDuplicateClientID(t *testing.T) {
	b := testBroker(t)

	// First connection
	conn1 := dialTestBroker(t, b)
	codec1 := protocol.NewCodec(0)
	connectClient(t, conn1, codec1, "dup-client")

	// Subscribe on first connection
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "test/topic", QoS: 0}},
	}
	conn1.SetDeadline(time.Now().Add(2 * time.Second))
	codec1.Encode(conn1, subPkt)
	conn1.SetDeadline(time.Now().Add(2 * time.Second))
	codec1.Decode(conn1) // SUBACK

	// Verify data delivery on conn1 before takeover
	pubConn := dialTestBroker(t, b)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "dup-publisher")

	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 0},
		Topic:       "test/topic",
		Payload:     []byte("dup-test-msg"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pubPkt); err != nil {
		t.Fatalf("PUBLISH failed: %v", err)
	}

	conn1.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec1.Decode(conn1)
	if err != nil {
		t.Fatalf("conn1 did not receive PUBLISH: %v", err)
	}
	delivered, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if delivered.Topic != "test/topic" {
		t.Errorf("expected topic test/topic, got %s", delivered.Topic)
	}
	if string(delivered.Payload) != "dup-test-msg" {
		t.Errorf("expected payload dup-test-msg, got %s", delivered.Payload)
	}
	t.Logf("conn1 data verified before takeover: topic=%s payload=%s", delivered.Topic, delivered.Payload)

	// Second connection with same client ID
	conn2 := dialTestBroker(t, b)
	codec2 := protocol.NewCodec(0)
	connectClient(t, conn2, codec2, "dup-client")

	// The first connection is displaced by the takeover, so [MQTT-3.1.4-3]
	// requires it to be told why (DISCONNECT 0x8E) before the close.
	expectDisconnectThenClose(t, conn1, protocol.DisconnectSessionTakenOver)
}

// TestInvalidTopicFilter tests that invalid topic filters are rejected.
// TestInvalidTopicFilter drives a SUBSCRIBE whose Topic Filter is not
// well-formed. MQTT 5.0 §4.7.1.2 [MQTT-4.7.1-1] and §4.7.1.3 [MQTT-4.7.1-2]
// put MUST-level constraints on where '#' and '+' may appear, so a filter that
// breaks them is a Protocol Error, and §4.13.1 requires the Server to close the
// Network Connection with a DISCONNECT carrying 0x82 (Protocol Error).
//
// This used to assert a SUBACK carrying 0x80 instead. That was the wrong
// answer twice over: 0x8F (Topic Filter invalid) is the code defined for a
// filter that is "correctly formed but is not allowed", and 0x80 (Unspecified
// error) says nothing while leaving the connection up -- so a conformant client
// was told its malformed subscription had merely been refused, and could keep
// sending.
//
// Each filter gets its own connection: the first one tears the connection down,
// so a single connection cannot carry all three.
func TestInvalidTopicFilter(t *testing.T) {
	invalidFilters := []string{
		"a/#/b", // # must be last
		"+foo",  // + must be entire level
		"a+/b",  // + must be entire level
	}

	for _, filter := range invalidFilters {
		t.Run(filter, func(t *testing.T) {
			b := testBroker(t)
			conn := dialTestBroker(t, b)
			defer conn.Close()
			codec := protocol.NewCodec(0)
			connectClient(t, conn, codec, "invalid-filter-client")

			subPkt := &protocol.SubscribePacket{
				FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
				PacketID:    1,
				Topics:      []protocol.TopicFilter{{Topic: filter, QoS: 0}},
			}
			conn.SetDeadline(time.Now().Add(2 * time.Second))
			if err := codec.Encode(conn, subPkt); err != nil {
				t.Fatalf("encode SUBSCRIBE: %v", err)
			}

			conn.SetDeadline(time.Now().Add(2 * time.Second))
			pkt, err := codec.Decode(conn)
			if err != nil {
				t.Fatalf("decode response: %v", err)
			}
			disc, ok := pkt.(*protocol.DisconnectPacket)
			if !ok {
				t.Fatalf("malformed filter %q answered with %T, want *protocol.DisconnectPacket", filter, pkt)
			}
			if disc.ReasonCode != protocol.DisconnectProtocolError {
				t.Errorf("filter %q: DISCONNECT reason = 0x%02X, want 0x82 (Protocol Error)",
					filter, disc.ReasonCode)
			}

			// The DISCONNECT must be followed by the close, not by a live
			// connection that can keep publishing.
			conn.SetDeadline(time.Now().Add(2 * time.Second))
			if _, err := codec.Decode(conn); err == nil {
				t.Errorf("filter %q: broker kept the connection open after the DISCONNECT", filter)
			}
		})
	}
}

// TestMaxConnections tests that connections beyond the limit are rejected.
func TestMaxConnections(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"

	b := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
		api.WithMaxConnections(1),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(func() { b.Stop() })

	// Connect first client
	conn1, err := net.DialTimeout("tcp", b.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	defer conn1.Close()
	codec1 := protocol.NewCodec(0)
	connectClient(t, conn1, codec1, "max-conn-1")
	// connectClient reads CONNACK, so the connection is already registered

	// 2nd connection should be rejected
	conn2, err := net.DialTimeout("tcp", b.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial 2nd: %v", err)
	}
	defer conn2.Close()

	codec2 := protocol.NewCodec(0)
	connectPkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "overflow-client",
	}

	conn2.SetDeadline(time.Now().Add(2 * time.Second))
	codec2.Encode(conn2, connectPkt)

	// Connection should be closed by server
	conn2.SetDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1)
	_, err = conn2.Read(buf)
	if err == nil {
		t.Error("expected connection to be rejected/closed for exceeding max connections")
	}
}

// TestEmptyClientID tests connecting with an empty client ID.
func TestEmptyClientID(t *testing.T) {
	b := testBroker(t)
	conn := dialTestBroker(t, b)
	codec := protocol.NewCodec(0)

	connectPkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "",
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, connectPkt); err != nil {
		t.Fatalf("encode CONNECT: %v", err)
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	connAck, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}
	if connAck.ReasonCode != protocol.ConnAckAccepted {
		t.Errorf("empty client ID rejected: reason=0x%02x", connAck.ReasonCode)
	}

	// Subscribe and verify data delivery
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "empty/topic", QoS: 0}},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, subPkt); err != nil {
		t.Fatalf("SUBSCRIBE failed: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err = codec.Decode(conn)
	if err != nil {
		t.Fatalf("SUBACK failed: %v", err)
	}
	if _, ok := pkt.(*protocol.SubAckPacket); !ok {
		t.Fatalf("expected SUBACK, got %T", pkt)
	}

	// Publish from a second connection
	pubConn := dialTestBroker(t, b)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "empty-id-publisher")
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 0},
		Topic:       "empty/topic",
		Payload:     []byte("empty-id-test"),
	}
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pubPkt); err != nil {
		t.Fatalf("PUBLISH failed: %v", err)
	}

	// Subscriber receives PUBLISH
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err = codec.Decode(conn)
	if err != nil {
		t.Fatalf("subscriber did not receive PUBLISH: %v", err)
	}
	delivered, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if delivered.Topic != "empty/topic" {
		t.Errorf("expected topic empty/topic, got %s", delivered.Topic)
	}
	if string(delivered.Payload) != "empty-id-test" {
		t.Errorf("expected payload empty-id-test, got %s", delivered.Payload)
	}
	t.Logf("data delivery verified: topic=%s payload=%s", delivered.Topic, delivered.Payload)
}
