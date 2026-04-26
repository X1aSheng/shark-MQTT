// Package integration provides end-to-end integration tests for shark-mqtt.
// Run with: go test -race -tags=integration ./tests/integration/...
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

// testBroker creates and starts a broker for testing, returning cleanup func.
func testBroker(t *testing.T) *api.Broker {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0" // random port

	broker := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := broker.Start(); err != nil {
		t.Fatalf("failed to start broker: %v", err)
	}
	t.Cleanup(func() { broker.Stop() })
	return broker
}

func dialTestBroker(t *testing.T, broker *api.Broker) net.Conn {
	t.Helper()
	addr := broker.Addr()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to dial broker at %s: %v", addr, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// TestConnectFlow tests the basic CONNECT/CONNACK flow.
func TestConnectFlow(t *testing.T) {
	broker := testBroker(t)
	conn := dialTestBroker(t, broker)

	codec := protocol.NewCodec(0)

	// Send CONNECT
	connectPkt := &protocol.ConnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnect,
		},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
		},
		KeepAlive: 30,
		ClientID:  "test-client",
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, connectPkt); err != nil {
		t.Fatalf("failed to send CONNECT: %v", err)
	}

	// Read CONNACK
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("failed to read CONNACK: %v", err)
	}

	connAck, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}

	if connAck.ReasonCode != protocol.ReasonCodeSuccess {
		t.Errorf("expected CONNACK accepted, got %d", connAck.ReasonCode)
	}

	// Subscribe and verify data delivery
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeSubscribe,
			QoS:        1,
		},
		PacketID: 1,
		Topics: []protocol.TopicFilter{
			{Topic: "connect/topic", QoS: 0},
		},
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
	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "connect-publisher")
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 0},
		Topic:   "connect/topic",
		Payload: []byte("connect-test"),
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
	if delivered.Topic != "connect/topic" {
		t.Errorf("expected topic connect/topic, got %s", delivered.Topic)
	}
	if string(delivered.Payload) != "connect-test" {
		t.Errorf("expected payload connect-test, got %s", delivered.Payload)
	}
	t.Logf("data delivery verified: topic=%s payload=%s", delivered.Topic, delivered.Payload)

	// Send PINGREQ
	pingPkt := &protocol.PingReqPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePingReq,
		},
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pingPkt); err != nil {
		t.Fatalf("failed to send PINGREQ: %v", err)
	}

	// Read PINGRESP
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err = codec.Decode(conn)
	if err != nil {
		t.Fatalf("failed to read PINGRESP: %v", err)
	}

	_, ok = pkt.(*protocol.PingRespPacket)
	if !ok {
		t.Fatalf("expected PINGRESP, got %T", pkt)
	}

	// Send DISCONNECT
	disconnectPkt := &protocol.DisconnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeDisconnect,
		},
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, disconnectPkt); err != nil {
		t.Fatalf("failed to send DISCONNECT: %v", err)
	}
}
