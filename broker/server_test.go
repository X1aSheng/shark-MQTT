package broker

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

func TestNewMQTTServer(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0" // random port
	srv := NewMQTTServer(cfg)
	if srv == nil {
		t.Fatal("NewMQTTServer returned nil")
	}
	if srv.handler != nil {
		t.Error("expected nil handler by default")
	}
	if srv.ConnCount() != 0 {
		t.Errorf("expected 0 connections, got %d", srv.ConnCount())
	}
}

func TestServerStartStop(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	srv := NewMQTTServer(cfg)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if srv.Addr() == nil {
		t.Fatal("expected non-nil Addr after Start")
	}

	srv.Stop()
}

func TestServerMaxConnections(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MaxConnections = 1
	srv := NewMQTTServer(cfg)

	// Use a blocking handler to keep connection alive
	blocking := &blockingHandler{done: make(chan struct{})}
	srv.SetHandler(blocking)

	if err := srv.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer srv.Stop()
	defer close(blocking.done)

	addr := srv.Addr()

	// First connection
	conn1, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	defer conn1.Close()

	// Small delay to let the server register the connection
	time.Sleep(50 * time.Millisecond)

	if srv.ConnCount() != 1 {
		t.Errorf("expected 1 connection, got %d", srv.ConnCount())
	}
}

type blockingHandler struct {
	done chan struct{}
}

func (h *blockingHandler) HandleConnection(ctx context.Context, conn net.Conn, codec *protocol.Codec) error {
	<-h.done
	return nil
}

func TestServerSetHandler(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	srv := NewMQTTServer(cfg)

	handler := &mockHandler{}
	srv.SetHandler(handler)

	if srv.handler == nil {
		t.Fatal("expected handler to be set")
	}
}

func TestDrainAndClose(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("Listen error: %v", err)
	}
	defer ln.Close()

	// Connect
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}

	// Close the server side
	serverConn, err := ln.Accept()
	if err != nil {
		t.Fatalf("Accept error: %v", err)
	}

	DrainAndClose(serverConn, 100*time.Millisecond)

	// Verify the client sees the close
	buf := make([]byte, 1024)
	conn.SetReadDeadline(time.Now().Add(time.Second))
	n, _ := conn.Read(buf)
	if n != 0 {
		t.Errorf("expected 0 bytes read, got %d", n)
	}
	conn.Close()
}

type mockHandler struct{}

func (h *mockHandler) HandleConnection(ctx context.Context, conn net.Conn, codec *protocol.Codec) error {
	return nil
}
