package api

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
)

func TestNewBroker(t *testing.T) {
	b := NewBroker()
	if b == nil {
		t.Fatal("NewBroker returned nil")
	}
	if b.srv == nil {
		t.Fatal("expected non-nil server")
	}
	if b.broker == nil {
		t.Fatal("expected non-nil broker")
	}
}

func TestBrokerStartStop(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0" // random port

	b := NewBroker(
		WithConfig(cfg),
		WithAuth(broker.AllowAllAuth{}),
	)

	if err := b.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Should be listening
	if b.Addr() == "" {
		t.Error("expected non-empty Addr")
	}

	b.Stop()
}

func TestBrokerWithAuthorizer(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"

	b := NewBroker(
		WithConfig(cfg),
		WithAuth(broker.AllowAllAuth{}),
	)

	if err := b.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	b.Stop()
}

func TestBrokerConnCount(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"

	b := NewBroker(WithConfig(cfg))

	if err := b.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer b.Stop()

	if b.ConnCount() != 0 {
		t.Errorf("expected 0 connections, got %d", b.ConnCount())
	}
}
