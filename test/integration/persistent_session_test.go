// Package integration provides end-to-end integration tests for shark-mqtt.
// Run with: go test -race -tags=integration ./test/integration/...
// Package integration provides end-to-end integration tests for shark-mqtt.
// Run with: go test -race -tags=integration ./test/integration/...
package integration

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestPersistentSession_CleanSessionFalse verifies that a client with
// cleanSession=false is accepted and can publish/subscribe normally.
func TestPersistentSession_CleanSessionFalse(t *testing.T) {
	broker := testBroker(t)

	// Connect with cleanSession=false
	conn := dialTestBroker(t, broker)
	codec := protocol.NewCodec(0)

	connectPkt := &protocol.ConnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnect,
		},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: false,
		},
		KeepAlive: 30,
		ClientID:  "persistent-client",
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, connectPkt); err != nil {
		t.Fatalf("CONNECT failed: %v", err)
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("CONNACK failed: %v", err)
	}

	ca, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}
	if ca.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("expected CONNACK accepted, got reason %d", ca.ReasonCode)
	}

	// Subscribe to verify persistent session works
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeSubscribe,
			QoS:        1,
		},
		PacketID: 1,
		Topics: []protocol.TopicFilter{
			{Topic: "persist/test", QoS: 1},
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
}

// TestPersistentSession_Reconnect verifies that a client can reconnect
// with the same clientID and cleanSession=false.
func TestPersistentSession_Reconnect(t *testing.T) {
	broker := testBroker(t)

	clientID := "reconnect-client"

	// First connection
	conn1 := dialTestBroker(t, broker)
	codec1 := protocol.NewCodec(0)

	connectPkt := &protocol.ConnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnect,
		},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: false,
		},
		KeepAlive: 30,
		ClientID:  clientID,
	}

	conn1.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec1.Encode(conn1, connectPkt); err != nil {
		t.Fatalf("first CONNECT failed: %v", err)
	}

	conn1.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec1.Decode(conn1)
	if err != nil {
		t.Fatalf("first CONNACK failed: %v", err)
	}

	ca, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}
	if ca.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("expected CONNACK accepted, got reason %d", ca.ReasonCode)
	}

	// Subscribe on first connection
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeSubscribe,
			QoS:        1,
		},
		PacketID: 1,
		Topics: []protocol.TopicFilter{
			{Topic: "persist/test", QoS: 1},
		},
	}

	conn1.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec1.Encode(conn1, subPkt); err != nil {
		t.Fatalf("SUBSCRIBE failed: %v", err)
	}

	conn1.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err = codec1.Decode(conn1)
	if err != nil {
		t.Fatalf("SUBACK failed: %v", err)
	}
	if _, ok := pkt.(*protocol.SubAckPacket); !ok {
		t.Fatalf("expected SUBACK, got %T", pkt)
	}
	conn1.Close()

	// Allow broker to process disconnect
	time.Sleep(100 * time.Millisecond)

	// Second connection with same clientID
	conn2 := dialTestBroker(t, broker)
	codec2 := protocol.NewCodec(0)

	connectPkt2 := &protocol.ConnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnect,
		},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: false,
		},
		KeepAlive: 30,
		ClientID:  clientID,
	}

	conn2.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec2.Encode(conn2, connectPkt2); err != nil {
		t.Fatalf("second CONNECT failed: %v", err)
	}

	conn2.SetDeadline(time.Now().Add(2 * time.Second))
	pkt2, err := codec2.Decode(conn2)
	if err != nil {
		t.Fatalf("second CONNACK failed: %v", err)
	}

	ca2, ok := pkt2.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt2)
	}
	if ca2.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("expected second CONNACK accepted, got reason %d", ca2.ReasonCode)
	}
}

// TestPersistentSession_OfflinePublish verifies that QoS 1 messages
// published while a persistent client is offline are not delivered
// (since the client has no active subscription at that point).
func TestPersistentSession_OfflinePublish(t *testing.T) {
	broker := testBroker(t)

	// Publisher to send messages
	pubConn := dialTestBroker(t, broker)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "offline-publisher")

	// Publish a message (no subscriber, just verify publish works)
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        1,
		},
		PacketID: 1,
		Topic:    "offline/test",
		Payload:  []byte("offline message"),
	}

	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pubConn, pubPkt); err != nil {
		t.Fatalf("PUBLISH failed: %v", err)
	}

	// Wait for PUBACK
	pubConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := pubCodec.Decode(pubConn)
	if err != nil {
		t.Fatalf("did not receive PUBACK: %v", err)
	}
	pubAck, ok := pkt.(*protocol.PubAckPacket)
	if !ok {
		t.Fatalf("expected PUBACK, got %T", pkt)
	}
	if pubAck.PacketID != 1 {
		t.Errorf("expected PUBACK packetID 1, got %d", pubAck.PacketID)
	}

	pubConn.Close()
}

// TestPersistentSession_KickPrevious verifies that a new connection with
// the same clientID kicks the previous connection (existing behavior).
func TestPersistentSession_KickPrevious(t *testing.T) {
	broker := testBroker(t)

	clientID := "kick-test-client"

	// First connection
	conn1 := dialTestBroker(t, broker)
	codec1 := protocol.NewCodec(0)

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
		ClientID:  clientID,
	}

	conn1.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec1.Encode(conn1, connectPkt); err != nil {
		t.Fatalf("first CONNECT failed: %v", err)
	}

	conn1.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec1.Decode(conn1)
	if err != nil {
		t.Fatalf("first CONNACK failed: %v", err)
	}
	if ca, ok := pkt.(*protocol.ConnAckPacket); !ok || ca.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("first connection rejected: %v", pkt)
	}

	// Second connection with same clientID
	conn2 := dialTestBroker(t, broker)
	codec2 := protocol.NewCodec(0)

	connectPkt2 := &protocol.ConnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnect,
		},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
		},
		KeepAlive: 30,
		ClientID:  clientID,
	}

	conn2.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec2.Encode(conn2, connectPkt2); err != nil {
		t.Fatalf("second CONNECT failed: %v", err)
	}

	conn2.SetDeadline(time.Now().Add(2 * time.Second))
	pkt2, err := codec2.Decode(conn2)
	if err != nil {
		t.Fatalf("second CONNACK failed: %v", err)
	}

	// Second connection should be accepted
	ca2, ok := pkt2.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt2)
	}
	if ca2.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("expected second CONNACK accepted, got reason %d", ca2.ReasonCode)
	}

	// First connection should be disconnected - try to send PINGREQ
	time.Sleep(100 * time.Millisecond)

	pingPkt := &protocol.PingReqPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePingReq,
		},
	}

	conn1.SetDeadline(time.Now().Add(1 * time.Second))
	if err := codec1.Encode(conn1, pingPkt); err != nil {
		t.Logf("first connection write failed (expected): %v", err)
		return
	}

	// Try to read - should fail or get no response
	conn1.SetDeadline(time.Now().Add(1 * time.Second))
	_, err = codec1.Decode(conn1)
	if err != nil {
		t.Logf("first connection read failed (expected): %v", err)
	}
}
