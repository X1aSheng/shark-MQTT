package broker

import (
	"bytes"
	"context"
	"errors"
	"io"
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
	if b.opts.latencySampling != 1 {
		t.Errorf("default latency sampling should be 1, got %d", b.opts.latencySampling)
	}
}

// TestLatencySamplingOption verifies WithLatencySampling controls observation.
func TestLatencySamplingOption(t *testing.T) {
	if got := New(WithLatencySampling(0)).opts.latencySampling; got != 0 {
		t.Errorf("WithLatencySampling(0) => %d, want 0", got)
	}
	if got := New(WithLatencySampling(100)).opts.latencySampling; got != 100 {
		t.Errorf("WithLatencySampling(100) => %d, want 100", got)
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

	serverConn, clientConn := net.Pipe()
	go clientConn.Close()

	err := handleConnWithHandshake(context.Background(), b, serverConn, 0)
	if err == nil {
		t.Error("expected error for closed connection")
	}
}

func TestBroker_HandleConnection_AuthFailed(t *testing.T) {
	b := New(WithAuth(DenyAllAuth{}))

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		clientConn.Close()
	}()

	err := handleConnWithHandshake(context.Background(), b, serverConn, 0)
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

	// The leading 0 is the startup seed: with a retained store configured the
	// broker now rebuilds the retained gauge from the store on Start, so the
	// published sequence starts there rather than at the first publish. (It used
	// to be published only when a retained_expiry TTL was configured, which left
	// the gauge at 0 after a restart even though the store still held retained
	// messages.) The original assertions are unchanged and still form the tail:
	// first publish -> 1, overwrite -> 1 (no double count), delete -> 0,
	// redundant delete -> 0.
	want := []int{0, 1, 1, 0, 0}
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

	if err := b.publishWill("user1", "will/retained", []byte("offline"), 1, true, nil); err != nil {
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

	// Inbound QoS 2 state lives on the session now (audit H4): pre-stage
	// packet ID 100 as already accepted (PUBREC sent, PUBREL pending).
	sess := b.sessions.CreateSession("dup-client", &protocol.ConnectPacket{
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ProtocolVersion: protocol.Version50,
		ClientID:        "dup-client",
	}, false)
	if !sess.AddInboundQoS2(&InboundQoS2{
		PacketID: 100,
		QoS:      2,
		Topic:    "test/dup",
		Payload:  []byte("first"),
	}, 10) {
		t.Fatal("failed to stage pre-existing inbound QoS 2 message")
	}

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

	// handlePublish should detect the duplicate and re-send PUBREC without
	// replacing the staged message or touching the QoS engine.
	b.handlePublish("dup-client", sess, dupPkt)

	if !sess.HasInboundQoS2(100) {
		t.Error("packet ID should still be tracked after dup detection")
	}
	if msg, ok := sess.TakeInboundQoS2(100); !ok || string(msg.Payload) != "first" {
		t.Fatalf("dup PUBLISH replaced the staged payload: %#v ok=%v", msg, ok)
	}
	// Put the staged message back for the remainder of the test.
	if !sess.AddInboundQoS2(&InboundQoS2{PacketID: 100, QoS: 2, Topic: "test/dup", Payload: []byte("first")}, 10) {
		t.Fatal("failed to re-stage inbound QoS 2 message")
	}

	// New QoS 2 PUBLISH with fresh packet ID (not DUP) should be staged
	newPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        2,
		},
		Topic:    "test/dup",
		PacketID: 200,
		Payload:  []byte("new"),
	}
	b.handlePublish("dup-client", sess, newPkt)

	if !sess.HasInboundQoS2(200) {
		t.Error("new packet ID should be staged for non-dup PUBLISH")
	}
	if b.qos.InflightCount("dup-client") != 0 {
		t.Error("inbound QoS 2 publishes must not occupy the outbound QoS engine")
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

// TestBroker_CleanSessionUnsubscribesOnDisconnect verifies a clean session
// releases its topic-tree subscriptions on disconnect so entries do not leak
// across reconnect cycles (P2-13). Persistent sessions keep theirs so offline
// queueing can still match them.
func TestBroker_CleanSessionUnsubscribesOnDisconnect(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))

	// Clean session: subscribe, disconnect, expect the topic entry removed.
	cleanSess := &Session{
		ClientID:      "leak-clean",
		IsClean:       true,
		Subscriptions: make(map[string]uint8),
		SubOptions:    make(map[string]SubscriptionOptions),
		Inflight:      make(map[uint16]*InflightMsg),
	}
	cleanConn, _ := net.Pipe()
	defer cleanConn.Close()
	b.mu.Lock()
	b.connections["leak-clean"] = &clientState{conn: cleanConn, codec: protocol.NewCodec(0)}
	b.mu.Unlock()
	b.sessions.mu.Lock()
	b.sessions.sessions["leak-clean"] = cleanSess
	b.sessions.mu.Unlock()

	b.handleSubscribe("leak-clean", cleanSess, &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe},
		Topics:      []protocol.TopicFilter{{Topic: "leak/topic", QoS: 1}},
	})
	if got := b.topics.SubscriberCount(); got != 1 {
		t.Fatalf("expected 1 subscription after subscribe, got %d", got)
	}

	b.disconnect("leak-clean", cleanConn)
	if got := b.topics.SubscriberCount(); got != 0 {
		t.Errorf("expected 0 subscriptions after clean-session disconnect, got %d (leak)", got)
	}

	// Persistent session: subscribe, disconnect, expect the entry KEPT.
	persistSess := &Session{
		ClientID:      "leak-persist",
		IsClean:       false,
		Subscriptions: make(map[string]uint8),
		SubOptions:    make(map[string]SubscriptionOptions),
		Inflight:      make(map[uint16]*InflightMsg),
	}
	persistConn, _ := net.Pipe()
	defer persistConn.Close()
	b.mu.Lock()
	b.connections["leak-persist"] = &clientState{conn: persistConn, codec: protocol.NewCodec(0)}
	b.mu.Unlock()
	b.sessions.mu.Lock()
	b.sessions.sessions["leak-persist"] = persistSess
	b.sessions.mu.Unlock()

	b.handleSubscribe("leak-persist", persistSess, &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe},
		Topics:      []protocol.TopicFilter{{Topic: "persist/keep", QoS: 1}},
	})
	b.disconnect("leak-persist", persistConn)
	if got := b.topics.SubscriberCount(); got != 1 {
		t.Errorf("expected persistent session subscription to be kept, got count %d", got)
	}
}

// TestBroker_SessionTakeover covers what a displaced connection is owed when
// its ClientID is reused ([MQTT-3.1.4-3]).
//
// It drives sendTakenOverDisconnect -- the helper HandleConnection calls --
// rather than restating the close logic the way this test used to. A hand-copy
// can only ever assert that the copy is unchanged; this asserts that the
// production helper produces the right frame, so a change to the helper is
// caught here instead of being mirrored by hand.
//
// net.Pipe is synchronous per Write, so a reader has to be draining
// concurrently -- and because the codec writes the fixed header and the reason
// code in separate Writes, it has to drain the whole frame, not one chunk of
// it, or the trailing write blocks until the deadline.
func TestBroker_SessionTakeover(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	old := &clientState{
		conn:  serverConn,
		codec: protocol.NewCodecForVersion(0, protocol.Version50),
	}

	want := []byte{0xE0, 0x01, protocol.DisconnectSessionTakenOver}
	frame := make(chan []byte, 1)
	go func() {
		got := make([]byte, len(want))
		if _, err := io.ReadFull(clientConn, got); err != nil {
			return // leave the channel empty; the assertion below reports it
		}
		frame <- got
	}()

	b.sendTakenOverDisconnect("takeover-client", old)

	select {
	case got := <-frame:
		if !bytes.Equal(got, want) {
			t.Errorf("takeover frame = % X, want % X", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no takeover DISCONNECT: nothing was written to the displaced connection")
	}

	// The caller closes the socket straight after the send, so the peer must
	// then see the connection go away.
	serverConn.Close()
	clientConn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := clientConn.Read(make([]byte, 1)); err == nil {
		t.Error("connection still open after the takeover DISCONNECT")
	}
}

// TestBroker_SessionTakeoverMQTT311WritesNothing pins the wire contract for a
// displaced pre-v5 connection: MQTT 3.1.1 has no server-to-client DISCONNECT,
// so nothing may be written.
//
// It is a contract pin, not a gate-removal guard. Deleting the
// ServerDisconnect check in sendTakenOverDisconnect leaves this test green,
// because the codec refuses the frame first and writes nothing; the guard that
// actually protects the wire is protocol's encodeDisconnect, pinned by
// protocol.TestDisconnectVersionGuard.
func TestBroker_SessionTakeoverMQTT311WritesNothing(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	old := &clientState{
		conn:  serverConn,
		codec: protocol.NewCodecForVersion(0, protocol.Version311),
	}

	b.sendTakenOverDisconnect("takeover-client", old)

	// sendTakenOverDisconnect is synchronous, so anything it wrote has already
	// reached the pipe by the time it returns: a read that stays empty proves
	// nothing was sent.
	clientConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 8)
	n, err := clientConn.Read(buf)
	if n != 0 {
		t.Errorf("wrote % X to a displaced MQTT 3.1.1 connection, want nothing", buf[:n])
	}
	if err == nil {
		t.Error("read returned without data and without an error: expected a timeout")
	}
}

// TestBroker_SessionTakeoverNilConnectionIsSafe pins the guard the takeover
// path relies on: the connection map can hold a nil or half-built entry (unit
// tests register them by hand), and the DISCONNECT helper must not panic on it.
func TestBroker_SessionTakeoverNilConnectionIsSafe(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))

	b.sendTakenOverDisconnect("nil-conn", nil)
	b.sendTakenOverDisconnect("nil-codec", &clientState{})
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

	err := b.HandleConnection(context.Background(), newRawConn(serverConn2, protocol.NewCodec(0)))
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
	if !errors.Is(err, ErrTopicAliasOutOfRange) {
		t.Errorf("alias above TopicAliasMax: err = %v, want ErrTopicAliasOutOfRange", err)
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
	if !errors.Is(err, ErrTopicAliasOutOfRange) {
		t.Errorf("err = %v, want ErrTopicAliasOutOfRange when TopicAliasMax is 0", err)
	}
}

// TestSessionResumeClearsTopicAliases covers [MQTT-3.3.2-7]: a receiver must not
// carry Topic Alias mappings over from one Network Connection to the next.
// Resuming a session keeps its subscriptions, but the alias table belongs to
// the connection that negotiated it, so the resumed session must start empty --
// otherwise a client that reconnects and reuses an alias number without
// re-registering it silently publishes to the previous connection's topic.
func TestSessionResumeClearsTopicAliases(t *testing.T) {
	mgr := NewManager(nil)
	connectPkt := &protocol.ConnectPacket{
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: false},
		KeepAlive:       60,
		ClientID:        "alias-resume",
	}
	sess := mgr.CreateSession("alias-resume", connectPkt, false)
	sess.TopicAliasMax = 64
	if err := sess.RegisterTopicAlias(1, "sensors/temperature/room1"); err != nil {
		t.Fatalf("RegisterTopicAlias: %v", err)
	}

	resumed := mgr.CreateSession("alias-resume", connectPkt, true)
	if resumed != sess {
		t.Fatal("expected the persistent session to be resumed, not recreated")
	}
	if topic, ok := resumed.ResolveTopicAlias(1); ok {
		t.Errorf("alias 1 survived the reconnect as %q, want no mapping", topic)
	}
}

// TestBroker_ConnAckTopicAliasMaxAdvertisesServerLimit pins the direction of
// property 0x22. CONNECT's Topic Alias Maximum is the highest alias the CLIENT
// accepts from the server; CONNACK's is the highest alias the SERVER accepts
// from the client. A session whose client declared 0 (refusing server-sent
// aliases) must still be told the server's own limit, because the inbound guard
// enforces that limit -- conflating the two told the client the wrong number
// and, worse, denied it aliases it had never refused.
func TestBroker_ConnAckTopicAliasMaxAdvertisesServerLimit(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))

	props := b.buildConnAckProperties(&Session{TopicAliasMax: 64, ClientTopicAliasMax: 0})
	if props.TopicAliasMaximum == nil {
		t.Fatal("CONNACK omitted Topic Alias Maximum although the server accepts 64")
	}
	if got := *props.TopicAliasMaximum; got != 64 {
		t.Errorf("CONNACK TopicAliasMaximum = %d, want 64 (the server's inbound limit)", got)
	}

	// The reverse case: a client that accepts 10 aliases from the server must
	// not make the CONNACK claim the server accepts 10.
	props = b.buildConnAckProperties(&Session{TopicAliasMax: 64, ClientTopicAliasMax: 10})
	if props.TopicAliasMaximum == nil || *props.TopicAliasMaximum != 64 {
		t.Errorf("CONNACK TopicAliasMaximum = %v, want 64 and not the client's 10", props.TopicAliasMaximum)
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

// TestBroker_WillAuthDenied verifies a will message respects the authorizer:
// a client without publish permission for the will topic must not have its
// will delivered (authorization bypass regression).
func TestBroker_WillAuthDenied(t *testing.T) {
	auth := NewStaticAuth()
	auth.AddCredentials("u1", "pass")
	auth.AddACL("u1", &ACL{
		PublishTopics:   []string{"public/#"},
		SubscribeTopics: []string{"#"},
	})

	b := New(WithAuth(auth), WithAuthorizer(auth))
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	// Subscriber on the protected topic.
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	codec := protocol.NewCodec(0)
	b.mu.Lock()
	b.connections["sub"] = &clientState{conn: serverConn, codec: codec}
	b.mu.Unlock()
	b.topics.Subscribe("secret/topic", "sub", 1)
	go func() {
		buf := make([]byte, 4096)
		_, _ = clientConn.Read(buf)
	}()

	// Publishing a will to a topic the user cannot publish must be denied.
	if err := b.publishWill("u1", "secret/topic", []byte("leak"), 1, false, nil); err != nil {
		t.Fatalf("publishWill: %v", err)
	}

	// The subscriber must NOT receive anything. Decode reads from serverConn
	// (the side the broker writes to), so set the deadline there.
	_ = serverConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := codec.Decode(serverConn); err == nil {
		t.Fatal("subscriber received a message for a will on a non-authorized topic")
	}
}
