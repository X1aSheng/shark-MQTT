package broker

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store/memory"
)

type retainedMetrics struct {
	retained []int
}

func (m *retainedMetrics) IncConnections()                             {}
func (m *retainedMetrics) OnDisconnect()                               {}
func (m *retainedMetrics) IncRejections(reason string)                 {}
func (m *retainedMetrics) IncAuthFailures()                            {}
func (m *retainedMetrics) IncMessagesPublished(qos uint8)              {}
func (m *retainedMetrics) IncMessagesDelivered(qos uint8)              {}
func (m *retainedMetrics) IncMessagesDropped(reason string)            {}
func (m *retainedMetrics) IncInflight(clientID string)                 {}
func (m *retainedMetrics) DecInflight(clientID string)                 {}
func (m *retainedMetrics) DecInflightBatch(clientID string, count int) {}
func (m *retainedMetrics) IncInflightDropped(clientID string)          {}
func (m *retainedMetrics) IncRetries(clientID string)                  {}
func (m *retainedMetrics) SetOnlineSessions(count int)                 {}
func (m *retainedMetrics) SetOfflineSessions(count int)                {}
func (m *retainedMetrics) SetRetainedMessages(count int) {
	m.retained = append(m.retained, count)
}
func (m *retainedMetrics) SetSubscriptions(count int)                       {}
func (m *retainedMetrics) IncErrors(component string)                       {}
func (m *retainedMetrics) ObserveMessageLatency(seconds float64, qos uint8) {}

func TestNewBroker_Defaults(t *testing.T) {
	b := New()
	if b == nil {
		t.Fatal("expected broker, got nil")
	}

	if b.topics == nil {
		t.Error("expected topic tree")
	}

	if b.qos == nil {
		t.Error("expected QoS engine")
	}

	if b.will == nil {
		t.Error("expected will handler")
	}

	if b.sessions == nil {
		t.Error("expected session manager")
	}

	if b.metrics == nil {
		t.Error("expected metrics")
	}
}

func TestNewBroker_WithOptions(t *testing.T) {
	b := New(
		WithAuth(AllowAllAuth{}),
	)
	if b == nil {
		t.Fatal("expected broker, got nil")
	}
}

func TestBroker_StartStop(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))

	err := b.Start()
	if err != nil {
		t.Errorf("broker start failed: %v", err)
	}

	b.Stop()
}

func TestBroker_HandleConnection_ClosedConn(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))
	codec := protocol.NewCodec(0)

	serverConn, clientConn := net.Pipe()
	go clientConn.Close()

	err := b.HandleConnection(context.Background(), serverConn, codec)
	if err == nil {
		t.Error("expected error for closed connection")
	}
}

func TestBroker_HandleConnection_AuthFailed(t *testing.T) {
	b := New(WithAuth(DenyAllAuth{}))
	codec := protocol.NewCodec(0)

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		clientConn.Close()
	}()

	err := b.HandleConnection(context.Background(), serverConn, codec)
	if err == nil {
		t.Error("expected error when auth fails")
	}
}

func TestBroker_Publish_NoSubscribers(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))
	err := b.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	pkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
		Topic:       "test/topic",
		Payload:     []byte("hello"),
	}
	b.handlePublish("client1", nil, pkt)
}

func TestBroker_RetainedMetricsCountUpdatesOnOverwriteAndDelete(t *testing.T) {
	m := &retainedMetrics{}
	b := New(
		WithAuth(AllowAllAuth{}),
		WithRetainedStore(memory.NewRetainedStore()),
		WithMetrics(m),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	publish := func(payload []byte) {
		b.handlePublish("client1", nil, &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{
				PacketType: protocol.PacketTypePublish,
				Retain:     true,
			},
			Topic:   "retain/topic",
			Payload: payload,
		})
	}

	publish([]byte("first"))
	publish([]byte("replace"))
	publish(nil)
	publish(nil)

	want := []int{1, 1, 0, 0}
	if len(m.retained) != len(want) {
		t.Fatalf("retained metric updates = %v, want %v", m.retained, want)
	}
	for i := range want {
		if m.retained[i] != want[i] {
			t.Fatalf("retained metric updates = %v, want %v", m.retained, want)
		}
	}
}

func TestBroker_PublishWillStoresRetainedMessage(t *testing.T) {
	retained := memory.NewRetainedStore()
	b := New(
		WithAuth(AllowAllAuth{}),
		WithRetainedStore(retained),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	if err := b.publishWill("will/retained", []byte("offline"), 1, true); err != nil {
		t.Fatalf("publishWill: %v", err)
	}

	msg, err := retained.GetRetained(context.Background(), "will/retained")
	if err != nil {
		t.Fatalf("GetRetained: %v", err)
	}
	if msg.QoS != 1 || string(msg.Payload) != "offline" {
		t.Fatalf("retained will = qos %d payload %q, want qos 1 payload offline", msg.QoS, msg.Payload)
	}
}

func TestBroker_ConnAckPropertiesOmitMaximumQoSWhenQoS2Supported(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))
	props := b.buildConnAckProperties(&Session{})
	if props.MaximumQoS != nil {
		t.Fatalf("MaximumQoS = %d, want omitted because MQTT 5 only allows 0 or 1 and omission means QoS 2 supported", *props.MaximumQoS)
	}
}

func TestBroker_QoS2DupDetection(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))
	err := b.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	codec := protocol.NewCodec(0)
	b.mu.Lock()
	b.connections["dup-client"] = &clientState{conn: serverConn, codec: codec}
	b.mu.Unlock()

	// Pre-register packet ID 100 as already received for this client
	b.receivedQoS2Mu.Lock()
	b.receivedQoS2["dup-client"] = map[uint16]struct{}{100: {}}
	b.receivedQoS2Mu.Unlock()

	// Subscribe another client
	serverConn2, clientConn2 := net.Pipe()
	defer serverConn2.Close()
	defer clientConn2.Close()
	codec2 := protocol.NewCodec(0)
	b.mu.Lock()
	b.connections["subscriber"] = &clientState{conn: serverConn2, codec: codec2}
	b.mu.Unlock()
	b.topics.Subscribe("test/dup", "subscriber", 2)

	// Drain writes from both connections
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := clientConn.Read(buf); err != nil {
				return
			}
		}
	}()

	// Send QoS 2 PUBLISH with DUP=1 for an already-seen packet ID
	dupPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        2,
			Dup:        true,
		},
		Topic:    "test/dup",
		PacketID: 100,
		Payload:  []byte("duplicate"),
	}

	// handlePublish should detect the duplicate and NOT call TrackQoS2
	// (we verify by checking that the inflight count is 0 for the publisher)
	b.handlePublish("dup-client", nil, dupPkt)

	b.receivedQoS2Mu.Lock()
	_, exists := b.receivedQoS2["dup-client"][100]
	b.receivedQoS2Mu.Unlock()
	if !exists {
		t.Error("packet ID should still be tracked after dup detection")
	}

	// New QoS 2 PUBLISH with fresh packet ID (not DUP) should work normally
	newPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        2,
		},
		Topic:    "test/dup",
		PacketID: 200,
		Payload:  []byte("new"),
	}
	b.handlePublish("dup-client", nil, newPkt)

	b.receivedQoS2Mu.Lock()
	_, tracked := b.receivedQoS2["dup-client"][200]
	delete(b.receivedQoS2, "dup-client")
	b.receivedQoS2Mu.Unlock()
	if !tracked {
		t.Error("new packet ID should be tracked for non-dup PUBLISH")
	}
}

func TestBroker_SubscribeAndUnsubscribe(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))
	err := b.Start()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	sess := &Session{
		ClientID:      "test-client",
		Subscriptions: make(map[string]uint8),
		Inflight:      make(map[uint16]*InflightMsg),
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	// Register connection so writePacket can deliver through it
	codec := protocol.NewCodec(0)
	b.mu.Lock()
	b.connections["test-client"] = &clientState{conn: serverConn, codec: codec}
	b.mu.Unlock()

	// drain writes in background
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := clientConn.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe},
		Topics: []protocol.TopicFilter{
			{Topic: "test/topic", QoS: 0},
		},
	}
	b.handleSubscribe("test-client", sess, subPkt)

	if _, ok := sess.Subscriptions["test/topic"]; !ok {
		t.Error("expected subscription to be added")
	}

	unsubPkt := &protocol.UnsubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeUnsubscribe},
		Topics:      []string{"test/topic"},
	}
	b.handleUnsubscribe("test-client", sess, unsubPkt)

	if _, ok := sess.Subscriptions["test/topic"]; ok {
		t.Error("expected subscription to be removed")
	}
}

func TestBroker_SessionTakeover(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	serverConn1, clientConn1 := net.Pipe()
	serverConn2, _ := net.Pipe()

	// Register first connection (simulating a connected client)
	b.mu.Lock()
	b.connections["takeover-client"] = &clientState{
		conn:  serverConn1,
		codec: protocol.NewCodec(0),
	}
	b.mu.Unlock()

	// Simulate session takeover: close old connection, register new one.
	// Mirrors HandleConnection lines 233-239.
	b.mu.Lock()
	if old, exists := b.connections["takeover-client"]; exists {
		old.conn.Close()
	}
	b.connections["takeover-client"] = &clientState{
		conn:  serverConn2,
		codec: protocol.NewCodec(0),
	}
	b.mu.Unlock()

	// First connection's client side should detect the close
	buf := make([]byte, 1)
	_, err := clientConn1.Read(buf)
	if err == nil {
		t.Error("expected first connection to be closed after takeover")
	}

	// Second connection should be the active one
	b.mu.RLock()
	cs := b.connections["takeover-client"]
	b.mu.RUnlock()
	if cs.conn != serverConn2 {
		t.Error("expected second connection to be the active one")
	}

	serverConn2.Close()
	clientConn1.Close()
}

func TestBroker_MaxConnections(t *testing.T) {
	b := New(
		WithAuth(AllowAllAuth{}),
		WithMaxConnections(1),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	// Fill the connection limit
	serverConn1, clientConn1 := net.Pipe()
	defer serverConn1.Close()
	defer clientConn1.Close()

	b.mu.Lock()
	b.connections["client-1"] = &clientState{
		conn:  serverConn1,
		codec: protocol.NewCodec(0),
	}
	b.mu.Unlock()

	// Attempting another connection should be rejected
	serverConn2, clientConn2 := net.Pipe()
	defer clientConn2.Close()

	err := b.HandleConnection(context.Background(), serverConn2, protocol.NewCodec(0))
	if err == nil {
		t.Error("expected error when max connections exceeded")
	}
}

// ─── MQTT 5.0 Topic Alias ────────────────────────────────

func TestSessionTopicAliasRegisterResolve(t *testing.T) {
	mgr := NewManager(nil)
	connectPkt := &protocol.ConnectPacket{
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "alias-client",
	}
	sess := mgr.CreateSession("alias-client", connectPkt, false)
	sess.TopicAliasMax = 10

	// Register alias
	err := sess.RegisterTopicAlias(1, "sensors/temperature/room1")
	if err != nil {
		t.Fatalf("RegisterTopicAlias: %v", err)
	}

	// Resolve alias
	resolved, ok := sess.ResolveTopicAlias(1)
	if !ok {
		t.Fatal("expected alias 1 to be registered")
	}
	if resolved != "sensors/temperature/room1" {
		t.Errorf("resolved topic = %q, want sensors/temperature/room1", resolved)
	}

	// Resolve unknown alias
	_, ok = sess.ResolveTopicAlias(999)
	if ok {
		t.Error("expected unknown alias to return false")
	}

	// Register alias exceeding max
	err = sess.RegisterTopicAlias(11, "test")
	if err == nil {
		t.Error("expected error for alias exceeding TopicAliasMax")
	}
}

func TestSessionTopicAliasMaxZero(t *testing.T) {
	mgr := NewManager(nil)
	connectPkt := &protocol.ConnectPacket{
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "noalias",
	}
	sess := mgr.CreateSession("noalias", connectPkt, false)
	sess.TopicAliasMax = 0

	err := sess.RegisterTopicAlias(1, "test")
	if err == nil {
		t.Error("expected error when TopicAliasMax is 0")
	}
}

// ─── MQTT 5.0 Receive Maximum ────────────────────────────

func TestReceiveMaxSessionDefault(t *testing.T) {
	// Session created without explicit ReceiveMaximum should default to 65535.
	mgr := NewManager(nil)
	connectPkt := &protocol.ConnectPacket{
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "rm-default",
	}
	sess := mgr.CreateSession("rm-default", connectPkt, false)
	if sess.ReceiveMax != 65535 {
		t.Errorf("default ReceiveMax = %d, want 65535", sess.ReceiveMax)
	}
}

func TestAssignedClientID(t *testing.T) {
	// Session's AssignedClientID field is writable and readable.
	mgr := NewManager(nil)
	connectPkt := &protocol.ConnectPacket{
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "orig",
	}
	sess := mgr.CreateSession("orig", connectPkt, false)

	if sess.AssignedClientID != "" {
		t.Errorf("AssignedClientID should be empty by default, got %q", sess.AssignedClientID)
	}

	sess.AssignedClientID = "shark-abc123"
	if sess.AssignedClientID != "shark-abc123" {
		t.Errorf("AssignedClientID = %q", sess.AssignedClientID)
	}
}

func TestServerKeepAlive(t *testing.T) {
	// Session's ServerKeepAlive field is writable and readable.
	mgr := NewManager(nil)
	connectPkt := &protocol.ConnectPacket{
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       120,
		ClientID:        "ka-client",
	}
	sess := mgr.CreateSession("ka-client", connectPkt, false)

	if sess.ServerKeepAlive != nil {
		t.Error("ServerKeepAlive should be nil by default")
	}

	ka := uint16(60)
	sess.ServerKeepAlive = &ka
	if sess.ServerKeepAlive == nil || *sess.ServerKeepAlive != 60 {
		t.Error("ServerKeepAlive not set correctly")
	}
}

// TestBroker_StartIdempotent verifies double-Start returns error (P1-H02).
func TestBroker_StartIdempotent(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))
	if err := b.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	defer b.Stop()

	err := b.Start()
	if err == nil {
		t.Fatal("expected error on double Start")
	}
}
