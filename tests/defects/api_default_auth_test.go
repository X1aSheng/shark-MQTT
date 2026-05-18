package defects

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/client"
	"github.com/X1aSheng/shark-mqtt/config"
)

func TestDefectAPIDefaultBrokerRejectsUnauthenticatedClient(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.MetricsAddr = "127.0.0.1:0"

	b := api.NewBroker(api.WithConfig(cfg))
	if err := b.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(func() { b.Stop() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cli := client.New(
		client.WithAddr(b.Addr()),
		client.WithClientID("default-auth-reject"),
		client.WithCleanSession(true),
	)

	err := cli.Connect(ctx)
	if err == nil {
		_ = cli.Disconnect(ctx)
		t.Fatal("Connect succeeded without an explicit authenticator")
	}
	if !strings.Contains(err.Error(), "connection rejected") {
		t.Fatalf("expected CONNACK rejection, got %v", err)
	}
}
