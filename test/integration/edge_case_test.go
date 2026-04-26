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
		Topic:   "test/topic",
		Payload: []byte("dup-test-msg"),
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

	// First connection should be dead
	conn1.SetDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 1)
	_, err = conn1.Read(buf)
	if err == nil {
		t.Error("expected first connection to be closed")
	}
}

// TestInvalidTopicFilter tests that invalid topic filters are rejected.
func TestInvalidTopicFilter(t *testing.T) {
	b := testBroker(t)
	conn := dialTestBroker(t, b)
	codec := protocol.NewCodec(0)
	connectClient(t, conn, codec, "invalid-filter-client")

	invalidFilters := []string{
		"a/#/b", // # must be last
		"+foo",  // + must be entire level
		"a+/b",  // + must be entire level
	}

	for i, filter := range invalidFilters {
		subPkt := &protocol.SubscribePacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
			PacketID:    uint16(i + 1),
			Topics:      []protocol.TopicFilter{{Topic: filter, QoS: 0}},
		}
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		if err := codec.Encode(conn, subPkt); err != nil {
			t.Fatalf("encode SUBSCRIBE: %v", err)
		}

		conn.SetDeadline(time.Now().Add(2 * time.Second))
		pkt, err := codec.Decode(conn)
		if err != nil {
			t.Fatalf("decode SUBACK: %v", err)
		}
		subAck, ok := pkt.(*protocol.SubAckPacket)
		if !ok {
			t.Fatalf("expected SUBACK, got %T", pkt)
		}
		if len(subAck.ReasonCodes) == 0 {
			t.Fatalf("SUBACK for %q: no reason codes", filter)
		}
		if subAck.ReasonCodes[0] != protocol.SubAckFailure {
			t.Errorf("filter %q: expected failure (0x80), got 0x%02x", filter, subAck.ReasonCodes[0])
		}
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

	// Give the broker time to register the connection
	time.Sleep(50 * time.Millisecond)

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
		Topic:   "empty/topic",
		Payload: []byte("empty-id-test"),
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
