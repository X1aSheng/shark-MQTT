// Package integration provides end-to-end integration tests for shark-mqtt.
// Run with: go test -race -tags=integration ./test/integration/...
package integration

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// connectTestClient sends a CONNECT packet and verifies CONNACK.
func connectTestClient(t *testing.T, conn net.Conn, codec *protocol.Codec, clientID string) {
	t.Helper()
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

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, connectPkt); err != nil {
		t.Fatalf("failed to send CONNECT: %v", err)
	}

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
		t.Fatalf("expected CONNACK accepted, got %d", connAck.ReasonCode)
	}
}

// subscribeToTopic sends SUBSCRIBE and verifies SUBACK.
func subscribeToTopic(t *testing.T, conn net.Conn, codec *protocol.Codec, topic string, qos uint8, packetID uint16) {
	t.Helper()
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeSubscribe,
		},
		PacketID: packetID,
		Topics: []protocol.TopicFilter{
			{
				Topic: topic,
				QoS:   qos,
			},
		},
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, subPkt); err != nil {
		t.Fatalf("failed to send SUBSCRIBE: %v", err)
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("failed to read SUBACK: %v", err)
	}

	subAck, ok := pkt.(*protocol.SubAckPacket)
	if !ok {
		t.Fatalf("expected SUBACK, got %T", pkt)
	}

	if subAck.PacketID != packetID {
		t.Errorf("expected SUBACK packetID %d, got %d", packetID, subAck.PacketID)
	}
}

// disconnectClient sends DISCONNECT.
func disconnectClient(t *testing.T, conn net.Conn, codec *protocol.Codec) {
	t.Helper()
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

// TestWillMessageOnAbnormalDisconnect tests that a will message is published
// when a client disconnects without sending DISCONNECT.
func TestWillMessageOnAbnormalDisconnect(t *testing.T) {
	broker := testBroker(t)

	// Connect subscriber to will topic
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectTestClient(t, subConn, subCodec, "will-subscriber")
	subscribeToTopic(t, subConn, subCodec, "client1/will", 1, 1)

	// Connect client with will message
	willConn := dialTestBroker(t, broker)
	willCodec := protocol.NewCodec(0)

	connectPkt := &protocol.ConnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnect,
		},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
			WillFlag:     true,
			WillQoS:      1,
			WillRetain:   false,
		},
		KeepAlive:  30,
		ClientID:   "client1",
		WillTopic:  "client1/will",
		WillMessage: []byte("client1 offline"),
	}

	willConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := willCodec.Encode(willConn, connectPkt); err != nil {
		t.Fatalf("failed to send CONNECT with will: %v", err)
	}

	willConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := willCodec.Decode(willConn)
	if err != nil {
		t.Fatalf("failed to read CONNACK: %v", err)
	}

	connAck, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}

	if connAck.ReasonCode != protocol.ReasonCodeSuccess {
		t.Fatalf("expected CONNACK accepted, got %d", connAck.ReasonCode)
	}

	// Simulate abnormal disconnect (no DISCONNECT)
	willConn.Close()

	// Wait for will to be published
	time.Sleep(200 * time.Millisecond)

	// Check subscriber received the will message
	subConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err = subCodec.Decode(subConn)
	if err != nil {
		t.Fatalf("subscriber did not receive will message: %v", err)
	}

	pubPkt, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}

	if pubPkt.Topic != "client1/will" {
		t.Errorf("expected topic client1/will, got %s", pubPkt.Topic)
	}

	if string(pubPkt.Payload) != "client1 offline" {
		t.Errorf("expected payload 'client1 offline', got %s", pubPkt.Payload)
	}

	subConn.Close()
}

// TestWillMessageNotPublishedOnGracefulDisconnect tests that will is NOT published
// when client disconnects gracefully with DISCONNECT.
func TestWillMessageNotPublishedOnGracefulDisconnect(t *testing.T) {
	broker := testBroker(t)

	// Connect subscriber to will topic
	subConn := dialTestBroker(t, broker)
	subCodec := protocol.NewCodec(0)
	connectTestClient(t, subConn, subCodec, "will-subscriber")
	subscribeToTopic(t, subConn, subCodec, "client2/will", 1, 1)

	// Connect client with will message
	willConn := dialTestBroker(t, broker)
	willCodec := protocol.NewCodec(0)

	connectPkt := &protocol.ConnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnect,
		},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
			WillFlag:     true,
			WillQoS:      1,
			WillRetain:   false,
		},
		KeepAlive:  30,
		ClientID:   "client2",
		WillTopic:  "client2/will",
		WillMessage: []byte("client2 offline"),
	}

	willConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := willCodec.Encode(willConn, connectPkt); err != nil {
		t.Fatalf("failed to send CONNECT with will: %v", err)
	}

	willConn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := willCodec.Decode(willConn)
	if err != nil {
		t.Fatalf("failed to read CONNACK: %v", err)
	}

	connAck, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}

	if connAck.ReasonCode != protocol.ReasonCodeSuccess {
		t.Fatalf("expected CONNACK accepted, got %d", connAck.ReasonCode)
	}

	// Graceful disconnect
	disconnectClient(t, willConn, willCodec)
	willConn.Close()

	// Wait to ensure no will message is published
	time.Sleep(200 * time.Millisecond)

	// Check subscriber did NOT receive the will message
	subConn.SetDeadline(time.Now().Add(200 * time.Millisecond))
	_, err = subCodec.Decode(subConn)
	if err == nil {
		t.Error("expected no will message on graceful disconnect, but got one")
	}

	subConn.Close()
}

// TestWillMessageQoS tests will messages are published with the correct QoS.
func TestWillMessageQoS(t *testing.T) {
	tests := []struct {
		name     string
		willQoS  uint8
		expected uint8
	}{
		{"QoS 0", 0, 0},
		{"QoS 1", 1, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			broker := testBroker(t)

			subConn := dialTestBroker(t, broker)
			subCodec := protocol.NewCodec(0)
			connectClient(t, subConn, subCodec, "will-subscriber")
			subscribeToTopic(t, subConn, subCodec, "client/will", tc.willQoS, 1)

			willConn := dialTestBroker(t, broker)
			willCodec := protocol.NewCodec(0)

			connectPkt := &protocol.ConnectPacket{
				FixedHeader: protocol.FixedHeader{
					PacketType: protocol.PacketTypeConnect,
				},
				ProtocolName:    protocol.ProtocolNameMQTT,
				ProtocolVersion: protocol.Version50,
				Flags: protocol.ConnectFlags{
					CleanSession: true,
					WillFlag:     true,
					WillQoS:      tc.willQoS,
					WillRetain:   false,
				},
				KeepAlive:  30,
				ClientID:   "client",
				WillTopic:  "client/will",
				WillMessage: []byte("will message"),
			}

			willConn.SetDeadline(time.Now().Add(2 * time.Second))
			if err := willCodec.Encode(willConn, connectPkt); err != nil {
				t.Fatalf("failed to send CONNECT with will: %v", err)
			}

			willConn.SetDeadline(time.Now().Add(2 * time.Second))
			pkt, err := willCodec.Decode(willConn)
			if err != nil {
				t.Fatalf("failed to read CONNACK: %v", err)
			}

			if connAck, ok := pkt.(*protocol.ConnAckPacket); !ok || connAck.ReasonCode != protocol.ReasonCodeSuccess {
				t.Fatalf("expected CONNACK accepted")
			}

			// Abnormal disconnect
			willConn.Close()

			// Wait for will to be published
			time.Sleep(200 * time.Millisecond)

			subConn.SetDeadline(time.Now().Add(2 * time.Second))
			pkt, err = subCodec.Decode(subConn)
			if err != nil {
				t.Fatalf("subscriber did not receive will message: %v", err)
			}

			pubPkt, ok := pkt.(*protocol.PublishPacket)
			if !ok {
				t.Fatalf("expected PUBLISH, got %T", pkt)
			}

			if pubPkt.FixedHeader.QoS != tc.expected {
				t.Errorf("expected QoS %d, got %d", tc.expected, pubPkt.FixedHeader.QoS)
			}

			subConn.Close()
		})
	}
}
