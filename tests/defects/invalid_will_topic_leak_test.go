package defects

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

func TestDefectInvalidWillTopicDoesNotConsumeBrokerConnectionSlot(t *testing.T) {
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

	badConn, err := net.DialTimeout("tcp", b.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial bad client: %v", err)
	}
	badCodec := protocol.NewCodec(0)
	badConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := badCodec.Encode(badConn, &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
			WillFlag:     true,
		},
		KeepAlive:   30,
		ClientID:    "bad-will-client",
		WillTopic:   "bad/+/will",
		WillMessage: []byte("must-not-register"),
	}); err != nil {
		t.Fatalf("encode bad CONNECT: %v", err)
	}
	pkt, err := badCodec.Decode(badConn)
	if err != nil {
		t.Fatalf("decode bad CONNACK: %v", err)
	}
	if connAck, ok := pkt.(*protocol.ConnAckPacket); !ok {
		t.Fatalf("expected CONNACK for bad will topic, got %T", pkt)
	} else if connAck.ReasonCode == protocol.ConnAckAccepted {
		t.Fatal("bad will topic was accepted")
	}
	_ = badConn.Close()

	goodConn, err := net.DialTimeout("tcp", b.Addr(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial good client: %v", err)
	}
	defer goodConn.Close()
	goodCodec := protocol.NewCodec(0)
	goodConn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := goodCodec.Encode(goodConn, &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "good-client-after-bad-will",
	}); err != nil {
		t.Fatalf("encode good CONNECT: %v", err)
	}
	pkt, err = goodCodec.Decode(goodConn)
	if err != nil {
		t.Fatalf("decode good CONNACK after rejected will topic: %v", err)
	}
	connAck, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK for good client, got %T", pkt)
	}
	if connAck.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("good client rejected after bad will topic: reason=0x%02x", connAck.ReasonCode)
	}
}
