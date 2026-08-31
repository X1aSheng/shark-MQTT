package broker

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestBroker_SysStatusTopics verifies $SYS broker status topics are generated
// (R8): an explicit $SYS/# subscriber receives the status payloads.
func TestBroker_SysStatusTopics(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}), WithSysInterval(0), WithVersion("test-1.0"))
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	sess := &Session{
		ClientID:      "sys-sub",
		Subscriptions: make(map[string]uint8),
		SubOptions:    make(map[string]SubscriptionOptions),
		Inflight:      make(map[uint16]*InflightMsg),
	}
	b.mu.Lock()
	b.connections["sys-sub"] = &clientState{conn: serverConn, codec: protocol.NewCodec(0)}
	b.mu.Unlock()
	b.sessions.mu.Lock()
	b.sessions.sessions["sys-sub"] = sess
	b.sessions.mu.Unlock()

	// Start the reader BEFORE subscribing: writes on this directly-registered
	// clientState are synchronous (net.Pipe writes block until the peer
	// reads), so the SUBACK and every delivered publish need a live reader.
	topics := make(chan string, 8)
	go func() {
		codec := protocol.NewCodec(0)
		for {
			pkt, err := codec.Decode(clientConn)
			if err != nil {
				return
			}
			if p, ok := pkt.(*protocol.PublishPacket); ok {
				topics <- p.Topic
			}
		}
	}()

	b.handleSubscribe("sys-sub", sess, &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe},
		PacketID:    1, // required for a well-formed SUBACK
		Topics:      []protocol.TopicFilter{{Topic: "$SYS/#", QoS: 0}},
	})

	b.publishSystemStatus()

	got := map[string]bool{}
	for i := 0; i < 5; i++ {
		select {
		case topic := <-topics:
			got[topic] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for $SYS topic %d, got %v", i, got)
		}
	}
	for _, topic := range []string{
		"$SYS/broker/version",
		"$SYS/broker/uptime",
		"$SYS/broker/connections",
		"$SYS/broker/retained",
		"$SYS/broker/subscriptions",
	} {
		if !got[topic] {
			t.Errorf("missing generated $SYS topic %q, got %v", topic, got)
		}
	}
}
