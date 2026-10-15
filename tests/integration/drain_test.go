package integration

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// connectSubscriber connects a plain MQTT 5.0 client with no Will and returns
// its connection and codec.
func connectSubscriber(t *testing.T, conn net.Conn, codec *protocol.Codec, clientID string) {
	t.Helper()
	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        clientID,
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := codec.Decode(conn); err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
}

// TestDrain_WaitsForInflightAckBeforeClosing is the regression guard for the
// shutdown drain ORDER: Stop must wait for in-flight QoS 1 messages to be
// acknowledged BEFORE closing the sockets.
//
// Acknowledgments travel over the client socket, so the previous order
// (close first, then poll the in-flight count) could never see the count
// reach zero -- PUBACK needs a live connection. The wait therefore always ran
// to its full deadline, and a broker with a healthy client paid the full
// shutdown timeout on every restart.
//
// This test asserts the property that distinguishes the two orders: while Stop
// is draining, the subscriber's socket must still be readable and writable, so
// the PUBACK actually lands.
func TestDrain_WaitsForInflightAckBeforeClosing(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	cfg.DrainTimeout = config.Duration(3 * time.Second)

	brk := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := brk.Start(); err != nil {
		t.Fatalf("failed to start broker: %v", err)
	}
	t.Cleanup(func() { brk.Stop() })

	sub := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectSubscriber(t, sub, subCodec, "drain-sub")

	pub := dialTestBroker(t, brk)
	pubCodec := protocol.NewCodec(0)
	connectSubscriber(t, pub, pubCodec, "drain-pub")

	// Subscribe the subscriber at QoS 1 so the delivery must be acknowledged.
	sub.SetDeadline(time.Now().Add(2 * time.Second))
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics: []protocol.TopicFilter{
			{Topic: "drain/topic", QoS: 1},
		},
	}
	if err := subCodec.Encode(sub, subPkt); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}
	if _, err := subCodec.Decode(sub); err != nil {
		t.Fatalf("SUBACK: %v", err)
	}

	pub.SetDeadline(time.Now().Add(2 * time.Second))
	publish := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    7,
		Topic:       "drain/topic",
		Payload:     []byte("drain-me"),
	}
	if err := pubCodec.Encode(pub, publish); err != nil {
		t.Fatalf("PUBLISH: %v", err)
	}
	if _, err := pubCodec.Decode(pub); err != nil {
		t.Fatalf("PUBACK for publisher: %v", err)
	}

	// Read the forwarded PUBLISH on the subscriber side.
	sub.SetDeadline(time.Now().Add(2 * time.Second))
	got, err := subCodec.Decode(sub)
	if err != nil {
		t.Fatalf("forwarded PUBLISH: %v", err)
	}
	fwd, ok := got.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", got)
	}
	if !bytes.Equal(fwd.Payload, []byte("drain-me")) {
		t.Errorf("payload = %q, want %q", fwd.Payload, "drain-me")
	}

	// Stop runs in the background; acknowledge the delivery while it drains.
	// If the sockets were already closed, this Encode fails and the test fails.
	stopDone := make(chan struct{})
	go func() {
		brk.Stop()
		close(stopDone)
	}()

	time.Sleep(100 * time.Millisecond) // let Stop enter the drain phase

	ack := &protocol.PubAckPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubAck},
		PacketID:    fwd.PacketID,
	}
	if err := subCodec.Encode(sub, ack); err != nil {
		t.Fatalf("PUBACK during drain failed (socket closed before drain?): %v", err)
	}

	select {
	case <-stopDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return within 10s")
	}
}

// TestDrain_TimeoutIsBounded covers the other half of the contract: a client
// that never acknowledges must not hold shutdown open. Stop must still return,
// bounded by the configured drain timeout.
func TestDrain_TimeoutIsBounded(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	cfg.DrainTimeout = config.Duration(300 * time.Millisecond)

	brk := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := brk.Start(); err != nil {
		t.Fatalf("failed to start broker: %v", err)
	}
	t.Cleanup(func() { brk.Stop() })

	sub := dialTestBroker(t, brk)
	subCodec := protocol.NewCodec(0)
	connectSubscriber(t, sub, subCodec, "drain-silent")

	pub := dialTestBroker(t, brk)
	pubCodec := protocol.NewCodec(0)
	connectSubscriber(t, pub, pubCodec, "drain-pub2")

	sub.SetDeadline(time.Now().Add(2 * time.Second))
	if err := subCodec.Encode(sub, &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "drain/silent", QoS: 1}},
	}); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}
	if _, err := subCodec.Decode(sub); err != nil {
		t.Fatalf("SUBACK: %v", err)
	}

	pub.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pub, &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    8,
		Topic:       "drain/silent",
		Payload:     []byte("never-acked"),
	}); err != nil {
		t.Fatalf("PUBLISH: %v", err)
	}
	if _, err := pubCodec.Decode(pub); err != nil {
		t.Fatalf("PUBACK for publisher: %v", err)
	}

	// Drain the forwarded PUBLISH but never acknowledge it.
	sub.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := subCodec.Decode(sub); err != nil {
		t.Fatalf("forwarded PUBLISH: %v", err)
	}

	start := time.Now()
	brk.Stop()
	elapsed := time.Since(start)

	// Bounded by the drain timeout plus slack for socket teardown. The point is
	// that it returns at all, and does not sit on the 5s broker default.
	if elapsed > 3*time.Second {
		t.Errorf("Stop took %v with a 300ms drain timeout; want it bounded by the configured value", elapsed)
	}
}
