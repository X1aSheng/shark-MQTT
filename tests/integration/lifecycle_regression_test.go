package integration

// Regression tests for broker session/session-present semantics and
// shared-subscription retained delivery.

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store/memory"
)

// connectClientReturnConnAck connects and returns the CONNACK, honoring the
// clean-session flag.
func connectClientReturnConnAck(t *testing.T, conn net.Conn, codec *protocol.Codec, clientID string, clean bool) (*protocol.ConnAckPacket, error) {
	t.Helper()
	connectPkt := &protocol.ConnectPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: clean,
		},
		KeepAlive: 30,
		ClientID:  clientID,
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, connectPkt); err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		return nil, err
	}
	ca, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		return nil, fmt.Errorf("expected CONNACK, got %T", pkt)
	}
	if ca.ReasonCode != protocol.ConnAckAccepted {
		return nil, fmt.Errorf("client rejected: %d", ca.ReasonCode)
	}
	return ca, nil
}

// connectClientFlags connects with an explicit clean-session flag.
func connectClientFlags(t *testing.T, conn net.Conn, codec *protocol.Codec, clientID string, clean bool) {
	t.Helper()
	if _, err := connectClientReturnConnAck(t, conn, codec, clientID, clean); err != nil {
		t.Fatal(err)
	}
}

// TestCleanSessionPresentZero verifies a clean-session reconnect of an
// existing client returns SessionPresent=0 (MQTT 5.0 §3.2.2.2).
func TestCleanSessionPresentZero(t *testing.T) {
	brk := testBroker(t)
	clientID := "clean-present"

	// First connect: clean=0 persistent session.
	conn1 := dialTestBroker(t, brk)
	codec1 := protocol.NewCodec(0)
	connectClientFlags(t, conn1, codec1, clientID, false)
	conn1.Close()

	// Reconnect with clean=1: SessionPresent must be 0.
	conn2 := dialTestBroker(t, brk)
	codec2 := protocol.NewCodec(0)
	pkt, err := connectClientReturnConnAck(t, conn2, codec2, clientID, true)
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if pkt.SessionPresent {
		t.Fatal("clean-session reconnect returned SessionPresent=1, want 0")
	}
}

// TestSharedSubscriptionReceivesRetained verifies a $share subscription
// receives retained messages that match its real filter.
func TestSharedSubscriptionReceivesRetained(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	brk := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
		api.WithRetainedStore(memory.NewRetainedStore()),
	)
	if err := brk.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	defer brk.Stop()

	// Publisher stores a retained message.
	pub := dialTestBroker(t, brk)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pub, pubCodec, "shared-ret-pub")
	pub.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pub, &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1, Retain: true},
		PacketID:    1,
		Topic:       "sport/tennis/score",
		Payload:     []byte("6-4"),
	}); err != nil {
		t.Fatalf("publish retained: %v", err)
	}
	if _, err := pubCodec.Decode(pub); err != nil { // read PUBACK
		t.Fatalf("puback: %v", err)
	}

	// Subscriber joins a shared group on a matching filter.
	sub := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectClient(t, sub, subCodec, "shared-ret-sub")
	sub.SetDeadline(time.Now().Add(2 * time.Second))
	if err := subCodec.Encode(sub, &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "$share/g/sport/#", QoS: 1}},
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if pkt, err := subCodec.Decode(sub); err != nil {
		t.Fatalf("suback: %v", err)
	} else if _, ok := pkt.(*protocol.SubAckPacket); !ok {
		t.Fatalf("expected SUBACK, got %T", pkt)
	}

	// The retained message must be delivered to the shared subscriber.
	sub.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		pkt, err := subCodec.Decode(sub)
		if err != nil {
			t.Fatalf("no retained message delivered to shared subscriber: %v", err)
		}
		pubPkt, ok := pkt.(*protocol.PublishPacket)
		if !ok {
			continue
		}
		if pubPkt.Topic == "sport/tennis/score" && string(pubPkt.Payload) == "6-4" {
			return // delivered
		}
	}
}
