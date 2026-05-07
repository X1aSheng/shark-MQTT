package defects

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
)

func TestDefectMQTTServerRestartAfterStopAcceptsConnections(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"

	srv := broker.NewMQTTServer(cfg)
	if err := srv.Start(); err != nil {
		t.Fatalf("first start: %v", err)
	}
	srv.Stop()

	if err := srv.Start(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer srv.Stop()

	conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial restarted server: %v", err)
	}
	conn.Close()
}
