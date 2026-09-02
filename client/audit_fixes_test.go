package client

// Regression tests for audit fixes C10:
//   - Connect() must not hang forever when the peer accepts the connection
//     but never sends CONNACK (read deadline on the CONNACK wait)
//   - stale-generation ACKs are never written to a newer connection
//     (ackOnGen drops them)

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestConnectConnAckTimeout reproduces the audit hang: the peer accepts the
// TCP connection and stays silent. Connect must return an error after the
// configured connect timeout instead of blocking forever (ctx only used to
// bound the dial).
func TestConnectConnAckTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Accept and never answer: simulate a black-holed broker.
		time.Sleep(2 * time.Second)
		conn.Close()
	}()

	host, port, err := splitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	c := New(
		WithHostPort(host, port),
		WithConnectTimeout(250*time.Millisecond),
	)
	defer func() { _ = c.Disconnect(context.Background()) }()

	start := time.Now()
	err = c.Connect(context.Background())
	if err == nil {
		t.Fatal("expected Connect to fail when the broker never sends CONNACK")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Connect did not honor the CONNACK deadline: %v", elapsed)
	}
	<-acceptDone
}

// TestAckOnGenDropsStaleGeneration verifies an ACK produced by an old
// connection generation is not written into the current connection's stream.
func TestAckOnGenDropsStaleGeneration(t *testing.T) {
	newConn := &mockConn{}
	c := New()
	c.mu.Lock()
	c.conn = newConn
	c.mu.Unlock()

	staleConn := &mockConn{}
	ack := &protocol.PubAckPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePubAck},
		PacketID:    42,
	}
	c.ackOnGen(staleConn, ack) // stale generation: must be dropped

	if newConn.wrotePubAck {
		t.Error("stale-generation ack was written to the current connection")
	}
	if staleConn.wrotePubAck {
		t.Error("stale-generation ack should never be written at all")
	}

	c.ackOnGen(newConn, ack) // current generation: must be written
	if !newConn.wrotePubAck {
		t.Error("current-generation ack was not written")
	}
}

func splitHostPort(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return "", 0, err
	}
	return host, port, nil
}
