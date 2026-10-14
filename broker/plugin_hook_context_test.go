package broker

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/X1aSheng/shark-mqtt/plugin"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// capturingPlugin records the Context handed to every hook. Execute runs on the
// plugin manager's timeout goroutine, so the snapshots are mutex-guarded.
type capturingPlugin struct {
	mu   sync.Mutex
	seen map[plugin.Hook][]plugin.Context
}

func newCapturingPlugin() *capturingPlugin {
	return &capturingPlugin{seen: make(map[plugin.Hook][]plugin.Context)}
}

func (p *capturingPlugin) Name() string { return "capturing" }

func (p *capturingPlugin) Hooks() []plugin.Hook {
	return []plugin.Hook{plugin.OnAccept, plugin.OnConnected, plugin.OnMessage, plugin.OnClose}
}

func (p *capturingPlugin) Execute(_ context.Context, hook plugin.Hook, data *plugin.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot := *data
	p.seen[hook] = append(p.seen[hook], snapshot)
	return nil
}

// snapshots returns a copy of everything captured for one hook.
func (p *capturingPlugin) snapshots(hook plugin.Hook) []plugin.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]plugin.Context, len(p.seen[hook]))
	copy(out, p.seen[hook])
	return out
}

// brokerWithCapturingPlugin wires a broker to a capturing plugin. Start is not
// called: the hooks under test all fire on the connection path, and New already
// installs the context dispatch needs.
func brokerWithCapturingPlugin(t *testing.T) (*Broker, *capturingPlugin) {
	t.Helper()
	cap := newCapturingPlugin()
	mgr := plugin.NewManager()
	mgr.Register(cap)
	return New(WithAuth(AllowAllAuth{}), WithPluginManager(mgr)), cap
}

// connectPipeClient runs a raw MQTT 5.0 handshake over net.Pipe and returns the
// connected client end.
func connectPipeClient(t *testing.T, b *Broker, clientID string) (net.Conn, *protocol.Codec) {
	t.Helper()
	conn, cc := runRawClient(t, b)
	if err := cc.Encode(conn, v5Connect(clientID, "", "", true)); err != nil {
		t.Fatalf("send CONNECT %s: %v", clientID, err)
	}
	pkt, err := cc.Decode(conn)
	if err != nil {
		t.Fatalf("read CONNACK %s: %v", clientID, err)
	}
	if _, ok := pkt.(*protocol.ConnAckPacket); !ok {
		t.Fatalf("CONNACK %s: got %T", clientID, pkt)
	}
	return conn, cc
}

// TestBroker_OnMessageContextCarriesThePublishedMessage guards the defect this
// hook used to ship with: OnMessage was dispatched with ClientID alone, so
// every documented plugin example read an empty Topic. data.Topic and
// data.Payload are what plugins filter on (docs/guides/API.md logs data.Topic,
// docs/architecture/ARCHITECTURE.md runs an ACL on it).
func TestBroker_OnMessageContextCarriesThePublishedMessage(t *testing.T) {
	b, cap := brokerWithCapturingPlugin(t)
	conn, cc := connectPipeClient(t, b, "hook-publisher")

	pub := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        1,
			Retain:     true,
		},
		PacketID: 3,
		Topic:    "hook/context",
		Payload:  []byte("payload-carried"),
	}
	if err := cc.Encode(conn, pub); err != nil {
		t.Fatalf("send PUBLISH: %v", err)
	}
	// OnMessage is dispatched before the packet-type switch, so the PUBACK is
	// written only once the hook has run: reading it is the barrier.
	pkt, err := cc.Decode(conn)
	if err != nil {
		t.Fatalf("read PUBACK: %v", err)
	}
	if _, ok := pkt.(*protocol.PubAckPacket); !ok {
		t.Fatalf("expected PUBACK, got %T", pkt)
	}

	snaps := cap.snapshots(plugin.OnMessage)
	if len(snaps) != 1 {
		t.Fatalf("OnMessage dispatched %d times, want 1", len(snaps))
	}
	got := snaps[0]
	if got.ClientID != "hook-publisher" {
		t.Errorf("ClientID = %q, want hook-publisher", got.ClientID)
	}
	if got.Topic != "hook/context" {
		t.Errorf("Topic = %q, want hook/context", got.Topic)
	}
	if string(got.Payload) != "payload-carried" {
		t.Errorf("Payload = %q, want payload-carried", got.Payload)
	}
	if got.QoS != 1 {
		t.Errorf("QoS = %d, want 1", got.QoS)
	}
	if !got.Retain {
		t.Error("Retain = false, want true")
	}
}

// TestBroker_OnMessageFiresForNonPublishPacketsWithEmptyMessageFields pins the
// other half of the contract: OnMessage covers every decoded packet, not just
// PUBLISH, and the message fields stay empty for the other packet types. The
// fix for the test above must not turn into "only fire on PUBLISH".
func TestBroker_OnMessageFiresForNonPublishPacketsWithEmptyMessageFields(t *testing.T) {
	b, cap := brokerWithCapturingPlugin(t)
	conn, cc := connectPipeClient(t, b, "hook-pinger")

	ping := &protocol.PingReqPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePingReq},
	}
	if err := cc.Encode(conn, ping); err != nil {
		t.Fatalf("send PINGREQ: %v", err)
	}
	// Same barrier argument as above: the PINGRESP follows the dispatch.
	pkt, err := cc.Decode(conn)
	if err != nil {
		t.Fatalf("read PINGRESP: %v", err)
	}
	if _, ok := pkt.(*protocol.PingRespPacket); !ok {
		t.Fatalf("expected PINGRESP, got %T", pkt)
	}

	snaps := cap.snapshots(plugin.OnMessage)
	if len(snaps) != 1 {
		t.Fatalf("OnMessage dispatched %d times, want 1 (it covers every packet)", len(snaps))
	}
	got := snaps[0]
	if got.ClientID != "hook-pinger" {
		t.Errorf("ClientID = %q, want hook-pinger", got.ClientID)
	}
	if got.Topic != "" || got.Payload != nil || got.QoS != 0 || got.Retain {
		t.Errorf("non-PUBLISH packet carried message fields: %+v", got)
	}
}
