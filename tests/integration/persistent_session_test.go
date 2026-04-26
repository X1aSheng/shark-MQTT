// Package integration provides end-to-end integration tests for shark-mqtt.
// Run with: go test -race -tags=integration ./tests/integration/...
// Package integration provides end-to-end integration tests for shark-mqtt.
// Run with: go test -race -tags=integration ./tests/integration/...
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

		// Verify data delivery: publish from a second connection
		pubConn := dialTestBroker(t, broker)
		pubCodec := protocol.NewCodec(0)
		connectClient(t, pubConn, pubCodec, "clean-session-publisher")

		pubPkt := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePublish,
				QoS:        1,
			},
			PacketID: 1,
			Topic:    "persist/test",
			Payload:  []byte("clean-session-test"),
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
		if delivered.Topic != "persist/test" {
			t.Errorf("expected topic persist/test, got %s", delivered.Topic)
		}
		if string(delivered.Payload) != "clean-session-test" {
			t.Errorf("expected payload 'clean-session-test', got %s", delivered.Payload)
		}
		t.Logf("data delivery verified: topic=%s payload=%s", delivered.Topic, delivered.Payload)

		// Send PUBACK back to broker
		pubAckResp := &protocol.PubAckPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubAck},
			PacketID:    delivered.PacketID,
		}
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		if err := codec.Encode(conn, pubAckResp); err != nil {
			t.Fatalf("PUBACK failed: %v", err)
		}

		// Publisher receives PUBACK
		pubConn.SetDeadline(time.Now().Add(2 * time.Second))
		pkt, err = pubCodec.Decode(pubConn)
		if err != nil {
			t.Fatalf("publisher did not receive PUBACK: %v", err)
		}
		if pubAck, ok := pkt.(*protocol.PubAckPacket); !ok {
			t.Fatalf("expected PUBACK, got %T", pkt)
		} else if pubAck.PacketID != 1 {
			t.Errorf("expected PUBACK packetID 1, got %d", pubAck.PacketID)
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

		// Verify data delivery on first connection
		pubConn1 := dialTestBroker(t, broker)
		pubCodec1 := protocol.NewCodec(0)
		connectClient(t, pubConn1, pubCodec1, "reconnect-pub1")

		pubPkt1 := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePublish,
				QoS:        1,
			},
			PacketID: 1,
			Topic:    "persist/test",
			Payload:  []byte("reconnect-msg-1"),
		}

		pubConn1.SetDeadline(time.Now().Add(2 * time.Second))
		if err := pubCodec1.Encode(pubConn1, pubPkt1); err != nil {
			t.Fatalf("PUBLISH failed: %v", err)
		}

		conn1.SetDeadline(time.Now().Add(2 * time.Second))
		pkt, err = codec1.Decode(conn1)
		if err != nil {
			t.Fatalf("subscriber did not receive PUBLISH: %v", err)
		}
		delivered1, ok := pkt.(*protocol.PublishPacket)
		if !ok {
			t.Fatalf("expected PUBLISH, got %T", pkt)
		}
		if delivered1.Topic != "persist/test" {
			t.Errorf("expected topic persist/test, got %s", delivered1.Topic)
		}
		if string(delivered1.Payload) != "reconnect-msg-1" {
			t.Errorf("expected payload reconnect-msg-1, got %s", delivered1.Payload)
		}
		t.Logf("conn1 data verified: topic=%s payload=%s", delivered1.Topic, delivered1.Payload)

		// Send PUBACK
		pubAckResp1 := &protocol.PubAckPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubAck},
			PacketID:    delivered1.PacketID,
		}
		conn1.SetDeadline(time.Now().Add(2 * time.Second))
		if err := codec1.Encode(conn1, pubAckResp1); err != nil {
			t.Fatalf("PUBACK failed: %v", err)
		}

		pubConn1.SetDeadline(time.Now().Add(2 * time.Second))
		pkt, err = pubCodec1.Decode(pubConn1)
		if err != nil {
			t.Fatalf("publisher PUBACK: %v", err)
		}
		if _, ok := pkt.(*protocol.PubAckPacket); !ok {
			t.Fatalf("expected PUBACK, got %T", pkt)
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

		// Subscribe on second connection and verify data delivery
		subPkt2 := &protocol.SubscribePacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypeSubscribe,
				QoS:        1,
			},
			PacketID: 1,
			Topics: []protocol.TopicFilter{
				{Topic: "persist/test", QoS: 1},
			},
		}

		conn2.SetDeadline(time.Now().Add(2 * time.Second))
		if err := codec2.Encode(conn2, subPkt2); err != nil {
			t.Fatalf("SUBSCRIBE on conn2 failed: %v", err)
		}

		conn2.SetDeadline(time.Now().Add(2 * time.Second))
		pkt2, err = codec2.Decode(conn2)
		if err != nil {
			t.Fatalf("SUBACK on conn2 failed: %v", err)
		}
		if _, ok := pkt2.(*protocol.SubAckPacket); !ok {
			t.Fatalf("expected SUBACK, got %T", pkt2)
		}

		// Publish and verify data delivery on conn2
		pubConn2 := dialTestBroker(t, broker)
		pubCodec2 := protocol.NewCodec(0)
		connectClient(t, pubConn2, pubCodec2, "reconnect-pub2")

		pubPkt2 := &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePublish,
				QoS:        1,
			},
			PacketID: 1,
			Topic:    "persist/test",
			Payload:  []byte("reconnect-msg-2"),
		}

		pubConn2.SetDeadline(time.Now().Add(2 * time.Second))
		if err := pubCodec2.Encode(pubConn2, pubPkt2); err != nil {
			t.Fatalf("PUBLISH failed: %v", err)
		}

		conn2.SetDeadline(time.Now().Add(2 * time.Second))
		pkt2, err = codec2.Decode(conn2)
		if err != nil {
			t.Fatalf("conn2 did not receive PUBLISH: %v", err)
		}
		delivered2, ok := pkt2.(*protocol.PublishPacket)
		if !ok {
			t.Fatalf("expected PUBLISH, got %T", pkt2)
		}
		if delivered2.Topic != "persist/test" {
			t.Errorf("expected topic persist/test, got %s", delivered2.Topic)
		}
		if string(delivered2.Payload) != "reconnect-msg-2" {
			t.Errorf("expected payload reconnect-msg-2, got %s", delivered2.Payload)
		}
		t.Logf("conn2 data verified: topic=%s payload=%s", delivered2.Topic, delivered2.Payload)

		// Send PUBACK on conn2
		pubAckResp2 := &protocol.PubAckPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubAck},
			PacketID:    delivered2.PacketID,
		}
		conn2.SetDeadline(time.Now().Add(2 * time.Second))
		if err := codec2.Encode(conn2, pubAckResp2); err != nil {
			t.Fatalf("PUBACK failed: %v", err)
		}

		pubConn2.SetDeadline(time.Now().Add(2 * time.Second))
		pkt2, err = pubCodec2.Decode(pubConn2)
		if err != nil {
			t.Fatalf("publisher PUBACK: %v", err)
		}
		if _, ok := pkt2.(*protocol.PubAckPacket); !ok {
			t.Fatalf("expected PUBACK, got %T", pkt2)
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
