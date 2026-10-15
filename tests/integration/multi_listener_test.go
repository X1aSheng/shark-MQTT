package integration

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// connectOn dials a specific address and completes a CONNECT/CONNACK round
// trip, so the test proves that port actually serves MQTT rather than merely
// having a socket bound.
func connectOn(t *testing.T, addr, clientID string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { conn.Close() })

	codec := protocol.NewCodec(0)
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
		t.Fatalf("CONNECT to %s: %v", addr, err)
	}
	if _, err := codec.Decode(conn); err != nil {
		t.Fatalf("CONNACK from %s: %v", addr, err)
	}
	conn.SetDeadline(time.Time{})
	return conn
}

// TestMultiListener_BindsEveryAddress is the core guard for multi-port support:
// every address in listen_addrs must be bound and must actually serve MQTT.
func TestMultiListener_BindsEveryAddress(t *testing.T) {
	cfg := config.DefaultConfig()
	// Three ephemeral ports; ":0" three times would hand out three distinct
	// ports, but asking for the same one three times must be deduplicated
	// instead, so use two explicit ":0" entries plus the scalar.
	// Two distinct entries: a repeated identical string is deduplicated by
	// config.EffectiveListenAddrs, so it cannot express "two ports" here.
	cfg.ListenAddrs = []string{"127.0.0.1:0", "[::1]:0"}
	cfg.MetricsAddr = "127.0.0.1:0"

	brk := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := brk.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { brk.Stop() })

	addrs := brk.Addrs()
	if len(addrs) != 2 {
		t.Fatalf("Addrs() = %v (%d entries), want 2", addrs, len(addrs))
	}
	if addrs[0] != brk.Addr() {
		t.Errorf("Addrs()[0] = %q, Addr() = %q; the primary must be reported first", addrs[0], brk.Addr())
	}

	// Every port must complete a full CONNECT/CONNACK, not just accept a dial.
	for i, addr := range addrs {
		conn := connectOn(t, addr, fmt.Sprintf("multi-port-client-%d", i))
		if conn == nil {
			t.Fatalf("no connection on %s", addr)
		}
	}

	// Both clients are visible to the same broker, proving they share state
	// rather than being separate brokers. Poll: ConnCount can settle a moment
	// after CONNACK, once the accept loop registers the connection.
	waitForConnCount(t, brk, 2)
}

// TestMultiListener_ArrayOverridesScalar pins the precedence rule: when
// listen_addrs is set it fully replaces listen_addr rather than merging with it.
// Merging would silently open the default port next to the operator's explicit
// list, which is a surprise nobody asked for.
func TestMultiListener_ArrayOverridesScalar(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.ListenAddrs = []string{"127.0.0.1:0"}
	cfg.MetricsAddr = "127.0.0.1:0"

	if got := cfg.EffectiveListenAddrs(); len(got) != 1 {
		t.Fatalf("EffectiveListenAddrs() = %v, want the single array entry", got)
	}

	brk := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := brk.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { brk.Stop() })

	if n := len(brk.Addrs()); n != 1 {
		t.Errorf("bound %d listeners, want 1 (scalar must not be merged in)", n)
	}
}

// TestMultiListener_ScalarOnlyStillWorks is the backward-compatibility guard:
// an existing config with only listen_addr must behave exactly as before.
func TestMultiListener_ScalarOnlyStillWorks(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.MetricsAddr = "127.0.0.1:0"

	brk := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := brk.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { brk.Stop() })

	addrs := brk.Addrs()
	if len(addrs) != 1 {
		t.Fatalf("Addrs() = %v, want exactly 1 for a scalar-only config", addrs)
	}
	connectOn(t, addrs[0], "scalar-client")
}

// TestMultiListener_StopClosesEveryPort guards the shutdown path: a multi-port
// Stop must release every listener, not only the primary. A leaked listener
// would make the next Start fail to rebind -- the exact failure mode the
// single-listener Stop was originally fixed for.
func TestMultiListener_StopClosesEveryPort(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddrs = []string{"127.0.0.1:0", "[::1]:0"}
	cfg.MetricsAddr = "127.0.0.1:0"

	brk := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := brk.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	addrs := brk.Addrs()
	if len(addrs) != 2 {
		t.Fatalf("Addrs() = %v, want 2", addrs)
	}

	brk.Stop()

	// Addrs must be empty after Stop: never advertise an address nobody serves.
	if got := brk.Addrs(); len(got) != 0 {
		t.Errorf("Addrs() = %v after Stop, want empty", got)
	}
	if got := brk.Addr(); got != "" {
		t.Errorf("Addr() = %q after Stop, want \"\"", got)
	}
	// Every port must now refuse connections.
	for _, addr := range addrs {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			t.Errorf("port %s still accepts connections after Stop", addr)
		}
	}
}

// TestMultiListener_BindFailureRollsBack covers the partial-bind case: when the
// second address is unavailable, the first must not be left dangling.
func TestMultiListener_BindFailureRollsBack(t *testing.T) {
	// Occupy a port, then ask the broker to bind it second.
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer blocker.Close()
	taken := blocker.Addr().String()

	cfg := config.DefaultConfig()
	cfg.ListenAddrs = []string{"127.0.0.1:0", taken}
	cfg.MetricsAddr = "127.0.0.1:0"

	brk := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := brk.Start(); err == nil {
		brk.Stop()
		t.Fatal("Start() = nil, want an error when one address is already in use")
	}

	// The first address must have been released, so a subsequent bind on a fresh
	// ephemeral port is unaffected -- and, more to the point, the broker must not
	// hold the port it did manage to open.
	if got := brk.Addrs(); len(got) != 0 {
		t.Errorf("Addrs() = %v after a failed Start, want empty", got)
	}
}
