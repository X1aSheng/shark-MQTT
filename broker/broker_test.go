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
	b.handlePublish(context.Background(), "client1", nil, pkt)
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
		b.handlePublish(context.Background(), "client1", nil, &protocol.PublishPacket{
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

	if err := b.publishWill(context.Background(), "user1", "will/retained", []byte("offline"), 1, true, nil); err != nil {
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
	b.handlePublish(context.Background(), "dup-client", sess, dupPkt)

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
	b.handlePublish(context.Background(), "dup-client", sess, newPkt)

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
	b.handleSubscribe(context.Background(), "test-client", sess, subPkt)

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

// TestBroker_CleanSessionUnsubscribesOnDisconnect pins what keeps a
// session's topic-tree subscriptions across a disconnect, so entries do not
// leak across reconnect cycles (P2-13) while persistent sessions keep theirs
// for offline queueing.
//
// The discriminator is the negotiated Session Expiry Interval, not the Clean
// Start bit -- §3.1.2.11.2 conditions the whole obligation on the interval
// ("The Client and Server MUST store the Session State after the Network
// Connection is closed if the Session Expiry Interval is greater than 0
// [MQTT-3.1.2-23]") and Clean Start is about the session that existed before
// the CONNECT. The table below is that decision rule in full, including the
// two combinations that prove the two settings really are independent: a
// clean start with an interval keeps its state, and a continuation with no
// interval does not.
func TestBroker_CleanSessionUnsubscribesOnDisconnect(t *testing.T) {
	for _, tc := range []struct {
		name    string
		clean   bool
		expiry  uint32
		wantSub int64
	}{
		{"clean-with-expiry-keeps", true, 3600, 1},
		{"clean-without-expiry-drops", true, 0, 0},
		{"persistent-with-expiry-keeps", false, 3600, 1},
		{"persistent-without-expiry-drops", false, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := New(WithAuth(AllowAllAuth{}))
			clientID := "leak-" + tc.name
			sess := &Session{
				ClientID:       clientID,
				IsClean:        tc.clean,
				ExpiryInterval: tc.expiry,
				Subscriptions:  make(map[string]uint8),
				SubOptions:     make(map[string]SubscriptionOptions),
				Inflight:       make(map[uint16]*InflightMsg),
			}
			conn, _ := net.Pipe()
			defer conn.Close()
			b.mu.Lock()
			b.connections[clientID] = &clientState{conn: conn, codec: protocol.NewCodec(0)}
			b.mu.Unlock()
			b.sessions.mu.Lock()
			b.sessions.sessions[clientID] = sess
			b.sessions.mu.Unlock()

			b.handleSubscribe(context.Background(), clientID, sess, &protocol.SubscribePacket{
				FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe},
				Topics:      []protocol.TopicFilter{{Topic: "leak/topic", QoS: 1}},
			})
			if got := b.topics.SubscriberCount(); got != 1 {
				t.Fatalf("expected 1 subscription after subscribe, got %d", got)
			}

			b.disconnect(context.Background(), clientID, conn)
			if got := b.topics.SubscriberCount(); got != tc.wantSub {
				t.Errorf("subscriptions after disconnect = %d, want %d", got, tc.wantSub)
			}
		})
	}
}

// ─── Clean Start vs Session Expiry Interval ──────────────────────────────
//
// MQTT 5.0 gives these two settings separate jobs, and the broker used to
// collapse them:
//
//   - Clean Start (§3.1.2.4) "specifies whether the Connection starts a new
//     Session or is a continuation of an existing Session". It is a decision
//     about the session that existed BEFORE this CONNECT, taken at connect
//     time, and [MQTT-3.1.2-4] is the whole of its obligation: "the Client
//     and Server MUST discard any existing Session and start a new Session".
//   - Session Expiry Interval (§3.1.2.11.2) is about what happens AFTER this
//     connection closes: "The Client and Server MUST store the Session State
//     after the Network Connection is closed if the Session Expiry Interval
//     is greater than 0 [MQTT-3.1.2-23]".
//
// Reading Clean Start = 1 as "expire now" zeroed the interval
// unconditionally, which the spec's own non-normative note contradicts by its
// wording: "Setting Clean Start to 1 and a Session Expiry Interval of 0, is
// equivalent to setting CleanSession to 1 in the MQTT Specification Version
// 3.1.1" -- an equivalence between TWO settings, not a rule that one implies
// the other. A client that sends Clean Start = 1 with Session Expiry
// Interval = 3600 asks to drop the old session now and keep the new one for
// an hour after it disconnects; the broker answered that request by
// advertising 0 in the CONNACK, publishing a delayed will immediately, and
// discarding the session at disconnect.

// TestCleanStartHonoursSessionExpiryInterval is the wire half: §3.2.2.3.2
// makes the CONNACK's Session Expiry Interval the broker telling the client
// "the value I am using, other than the one you sent", so a 0 there is a
// statement that the session ends at disconnect -- the exact opposite of what
// a client asking for 3600 requested.
func TestCleanStartHonoursSessionExpiryInterval(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))
	clientConn, cc := runRawClient(t, b)

	hour := uint32(3600)
	connect := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ClientID:        "clean-start-expiry",
		Properties:      &protocol.Properties{SessionExpiryInterval: &hour},
	}
	if err := cc.Encode(clientConn, connect); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	pkt, err := cc.Decode(clientConn)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	ca, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}
	if ca.Properties == nil || ca.Properties.SessionExpiryInterval == nil {
		t.Fatalf("CONNACK carried no Session Expiry Interval: %+v", ca.Properties)
	}
	if got := *ca.Properties.SessionExpiryInterval; got != hour {
		t.Errorf("CONNACK Session Expiry Interval = %d, want %d", got, hour)
	}
	sess, ok := b.sessions.GetSession("clean-start-expiry")
	if !ok {
		t.Fatal("session was not created")
	}
	if sess.ExpiryInterval != hour {
		t.Errorf("session ExpiryInterval = %d, want %d", sess.ExpiryInterval, hour)
	}
}

// TestCleanStartWithoutExpiryPropertyEndsSession is the control for the test
// above, and the case where zeroing the interval WAS correct: §3.1.2.11.2
// says "If the Session Expiry Interval is absent the value 0 is used. If it
// is set to 0, or is absent, the Session ends when the Network Connection is
// closed", and a pre-v5 client has no way to send the property at all, so for
// it the Clean Session bit is the whole story (MQTT 3.1.1 §3.1.2.7).
// Honouring the property must not quietly turn a clean start persistent.
func TestCleanStartWithoutExpiryPropertyEndsSession(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version protocol.Version
	}{
		{"v50-absent-property", protocol.Version50},
		{"v311", protocol.Version311},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := New(WithAuth(AllowAllAuth{}))
			clientConn, cc := runRawClient(t, b)

			connect := &protocol.ConnectPacket{
				FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
				ProtocolName:    protocol.ProtocolNameMQTT,
				ProtocolVersion: tc.version,
				Flags:           protocol.ConnectFlags{CleanSession: true},
				KeepAlive:       60,
				ClientID:        "clean-start-no-expiry",
			}
			if err := cc.Encode(clientConn, connect); err != nil {
				t.Fatalf("CONNECT: %v", err)
			}
			pkt, err := cc.Decode(clientConn)
			if err != nil {
				t.Fatalf("CONNACK: %v", err)
			}
			ca, ok := pkt.(*protocol.ConnAckPacket)
			if !ok {
				t.Fatalf("expected CONNACK, got %T", pkt)
			}
			if tc.version == protocol.Version50 {
				if ca.Properties == nil || ca.Properties.SessionExpiryInterval == nil {
					t.Fatalf("CONNACK carried no Session Expiry Interval: %+v", ca.Properties)
				}
				if got := *ca.Properties.SessionExpiryInterval; got != 0 {
					t.Errorf("CONNACK Session Expiry Interval = %d, want 0", got)
				}
			}
			sess, ok := b.sessions.GetSession("clean-start-no-expiry")
			if !ok {
				t.Fatal("session was not created")
			}
			if sess.ExpiryInterval != 0 {
				t.Errorf("session ExpiryInterval = %d, want 0", sess.ExpiryInterval)
			}
		})
	}
}

// TestCleanStartWithExpiryKeepsSubscriptionsOnDisconnect is the storage half
// of [MQTT-3.1.2-23], and it is the reason the fix could not stop at the
// interval arithmetic: a session whose negotiated interval is greater than 0
// outlives its Network Connection, so it keeps its topic-tree entries for
// offline delivery, and disconnect() must persist rather than delete it. The
// Clean Start bit is irrelevant to that decision -- it already did its job at
// connect time, where the previous session was discarded.
//
// The zero case is asserted alongside as the control: with no interval to
// honour the session still ends at disconnect and releases its entries, which
// is what keeps a clean start from leaking subscriptions.
func TestCleanStartWithExpiryKeepsSubscriptionsOnDisconnect(t *testing.T) {
	for _, tc := range []struct {
		name    string
		expiry  uint32
		wantSub int64
	}{
		{"clean-start-with-expiry", 3600, 1},
		{"clean-start-without-expiry", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := New(WithAuth(AllowAllAuth{}))
			clientID := "clean-start-keep-" + tc.name
			sess := &Session{
				ClientID:       clientID,
				IsClean:        true,
				ExpiryInterval: tc.expiry,
				Subscriptions:  make(map[string]uint8),
				SubOptions:     make(map[string]SubscriptionOptions),
				Inflight:       make(map[uint16]*InflightMsg),
			}
			conn, _ := net.Pipe()
			defer conn.Close()
			b.mu.Lock()
			b.connections[clientID] = &clientState{conn: conn, codec: protocol.NewCodec(0)}
			b.mu.Unlock()
			b.sessions.mu.Lock()
			b.sessions.sessions[clientID] = sess
			b.sessions.mu.Unlock()

			b.handleSubscribe(context.Background(), clientID, sess, &protocol.SubscribePacket{
				FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe},
				Topics:      []protocol.TopicFilter{{Topic: "keep/topic", QoS: 1}},
			})
			b.disconnect(context.Background(), clientID, conn)
			if got := b.topics.SubscriberCount(); got != tc.wantSub {
				t.Errorf("subscriptions after disconnect = %d, want %d", got, tc.wantSub)
			}
		})
	}
}

// TestCleanStartWillDelayBoundedBySessionExpiry covers the third place the
// zeroed interval leaked out. §3.1.3.2.2 publishes the Will Message when "the
// Will Delay Interval has passed or the Session ends, whichever happens
// first", so the delay is folded against the session's own lifetime when the
// will is registered. A session that was told to expire at disconnect made
// every delayed will fire immediately, ignoring the delay the client asked
// for -- here 60 seconds against a 300-second session.
func TestCleanStartWillDelayBoundedBySessionExpiry(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))
	clientConn, cc := runRawClient(t, b)

	willDelaySecs := uint32(60)
	expirySecs := uint32(300)
	connect := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
			WillFlag:     true,
			WillQoS:      0,
		},
		KeepAlive:      60,
		ClientID:       "clean-start-will",
		WillTopic:      "will/topic",
		WillMessage:    []byte("bye"),
		WillProperties: &protocol.Properties{WillDelayInterval: &willDelaySecs},
		Properties:     &protocol.Properties{SessionExpiryInterval: &expirySecs},
	}
	if err := cc.Encode(clientConn, connect); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	pkt, err := cc.Decode(clientConn)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	if _, ok := pkt.(*protocol.ConnAckPacket); !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}

	b.will.mu.Lock()
	registered, ok := b.will.wills["clean-start-will"]
	b.will.mu.Unlock()
	if !ok {
		t.Fatal("will was not registered")
	}
	if registered.Delay != 60*time.Second {
		t.Errorf("will delay = %v, want 1m0s (the client's Will Delay Interval)", registered.Delay)
	}
}

// subscribeThenReconnect drives the two-connection cycle the clean-start
// teardown tests need: a first connection subscribes to filter and closes, a
// second one then connects with secondClean. It returns the broker plus the
// second connection's client side and codec, so the caller can assert on what
// survived the cycle.
//
// The settle delay lets the broker's read loop finish the first teardown
// before the second CONNECT is read -- without it a takeover would
// legitimately keep the session.
func subscribeThenReconnect(t *testing.T, firstClean bool, expiry uint32, filter string, secondClean bool) (*Broker, net.Conn, *protocol.Codec) {
	t.Helper()
	b := New(
		WithAuth(AllowAllAuth{}),
		WithSessionStore(memory.NewSessionStore()),
		WithMessageStore(memory.NewMessageStore()),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(b.Stop)

	c1, cc := runRawClient(t, b)
	first := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: firstClean},
		KeepAlive:       30,
		ClientID:        "clean-teardown",
		Properties:      &protocol.Properties{SessionExpiryInterval: &expiry},
	}
	if err := cc.Encode(c1, first); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	if _, err := cc.Decode(c1); err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	if err := cc.Encode(c1, &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: filter, QoS: 1}},
	}); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}
	if _, err := cc.Decode(c1); err != nil {
		t.Fatalf("SUBACK: %v", err)
	}
	c1.Close()
	time.Sleep(300 * time.Millisecond)

	c2, cc2 := runRawClient(t, b)
	second := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: secondClean},
		KeepAlive:       30,
		ClientID:        "clean-teardown",
	}
	if err := cc2.Encode(c2, second); err != nil {
		t.Fatalf("reconnect CONNECT: %v", err)
	}
	resp, err := cc2.Decode(c2)
	if err != nil {
		t.Fatalf("reconnect CONNACK: %v", err)
	}
	if _, ok := resp.(*protocol.ConnAckPacket); !ok {
		t.Fatalf("expected CONNACK, got %T", resp)
	}
	return b, c2, cc2
}

// TestCleanConnectReleasesSharedSubscriptionEntry covers the sharp edge of a
// topic-tree entry that outlives the session it belonged to. A regular filter
// is re-checked against the session's own subscription list on delivery
// (deliverToClient -> MatchesSubscription), so a stale entry there is only
// untidy; a SHARED subscription is not, because the topic tree is the only
// authority the shared delivery path consults (deliverToSharedClient). A
// stale $share entry therefore routes PUBLISH packets to a client that has
// not subscribed on this Network Connection at all.
//
// [MQTT-3.1.2-4] makes the clean connect discard the existing Session, and
// the Session's filters are part of it.
func TestCleanConnectReleasesSharedSubscriptionEntry(t *testing.T) {
	b, conn, _ := subscribeThenReconnect(t, false, 3600, "$share/grp/clean/teardown", true)

	pubConn, pubCodec := runRawClient(t, b)
	if err := pubCodec.Encode(pubConn, &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "clean-teardown-publisher",
	}); err != nil {
		t.Fatalf("publisher CONNECT: %v", err)
	}
	if _, err := pubCodec.Decode(pubConn); err != nil {
		t.Fatalf("publisher CONNACK: %v", err)
	}
	if err := pubCodec.Encode(pubConn, &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
		PacketID:    1,
		Topic:       "clean/teardown",
		Payload:     []byte("stale"),
	}); err != nil {
		t.Fatalf("PUBLISH: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	if n, err := conn.Read(make([]byte, 64)); n > 0 {
		t.Errorf("clean-start client was handed %d bytes on a shared subscription it never made", n)
	} else if err == nil {
		t.Error("read returned no data and no error: expected a timeout")
	}
}

// TestCleanConnectReleasesStaleTopicTreeEntries is the resource half of the
// same defect. A filter that survives a clean reconnect keeps occupying the
// topic tree and keeps counting towards the subscription metric, and it
// accumulates without bound when a client changes its filter set between
// clean reconnects: only the filters it re-subscribes to are overwritten, the
// rest are never touched again.
//
// The continuation case is asserted alongside as the control -- a session
// that legitimately outlives its connection must keep its entries, because
// they are what lets an offline PUBLISH find its subscriber.
func TestCleanConnectReleasesStaleTopicTreeEntries(t *testing.T) {
	for _, tc := range []struct {
		name        string
		secondClean bool
		wantSubs    int64
	}{
		{"clean-reconnect-drops", true, 0},
		{"continuation-reconnect-keeps", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, _ := subscribeThenReconnect(t, false, 3600, "clean/teardown", tc.secondClean)
			if got := b.topics.SubscriberCount(); got != tc.wantSubs {
				t.Errorf("topic-tree subscriptions after reconnect = %d, want %d", got, tc.wantSubs)
			}
		})
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
	// A Session built by the manager, before any CONNECT negotiation has run,
	// must default both directions to 65535 (§3.1.2.11.6 / §3.2.2.3.3: an
	// absent property means 65535).
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
	if sess.OutboundWindow != 65535 {
		t.Errorf("default OutboundWindow = %d, want 65535", sess.OutboundWindow)
	}
}

// TestOutboundWindowGatesSeparatelyFromInboundLimit pins which of the two
// Receive Maximum directions gates the outbound window. They share property
// identifier 0x21 but are independent numbers, so a session whose server-side
// inbound limit is 100 and whose client declared 1 must stop after one
// unacknowledged delivery -- and one whose client declared 30 must not be
// capped at the server's inbound figure either.
func TestOutboundWindowGatesSeparatelyFromInboundLimit(t *testing.T) {
	sess := &Session{ReceiveMax: 100, OutboundWindow: 1}
	sess.IncOutboundUnacked()
	if sess.CanSendOutbound() {
		t.Error("outbound window 1 with one unacked delivery must be full, regardless of ReceiveMax 100")
	}

	sess = &Session{ReceiveMax: 1, OutboundWindow: 30}
	for i := 0; i < 29; i++ {
		sess.IncOutboundUnacked()
	}
	if !sess.CanSendOutbound() {
		t.Error("outbound window 30 with 29 unacked deliveries must still be open, regardless of ReceiveMax 1")
	}
}

// inboundWirePump installs a clientState for clientID on a net.Pipe and returns
// two handles on the broker's write side: collect, which closes the socket,
// waits for the drain to finish and decodes everything the broker wrote; and
// closed, which fires as soon as the broker closes the connection by itself.
//
// net.Pipe writes block until the peer reads, so a reader has to drain
// concurrently; collecting into a bytes.Buffer is safe because the test only
// reads it after the drain goroutine has finished (happens-after via close).
func inboundWirePump(t *testing.T, b *Broker, clientID string, version protocol.Version) (collect func() []protocol.Packet, closed <-chan struct{}) {
	t.Helper()

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })
	b.mu.Lock()
	b.connections[clientID] = &clientState{
		conn:  serverConn,
		codec: protocol.NewCodecForVersion(0, version),
	}
	b.mu.Unlock()

	var wire bytes.Buffer
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(&wire, clientConn)
	}()

	return func() []protocol.Packet {
		t.Helper()
		serverConn.Close() // idempotent: a teardown path may have closed it
		<-drained
		return decodePacketStream(t, wire.Bytes(), version)
	}, drained
}

// decodePacketStream decodes every packet in raw, failing on a truncated or
// undecodable frame -- which is itself a finding, since a refusal must write
// nothing.
func decodePacketStream(t *testing.T, raw []byte, version protocol.Version) []protocol.Packet {
	t.Helper()

	dec := protocol.NewCodecForVersion(0, version)
	r := bytes.NewReader(raw)
	var pkts []protocol.Packet
	for r.Len() > 0 {
		pkt, err := dec.Decode(r)
		if err != nil {
			t.Fatalf("decode wire stream at offset %d: %v (raw % X)", len(raw)-r.Len(), err, raw)
		}
		pkts = append(pkts, pkt)
	}
	return pkts
}

// TestInboundReceiveMaxExceededDisconnects covers §3.3.4: a client that leaves
// more QoS 1/2 PUBLISH packets unacknowledged than the Receive Maximum the
// server advertised has committed a Protocol Error, and the prescribed answer
// is a DISCONNECT carrying 0x93 (Receive Maximum exceeded).
//
// The broker used to answer with a PUBREC whose reason code was 0x93. Table 2-6
// lists 0x93 for DISCONNECT only -- 0x97 (Quota exceeded) is the code an
// acknowledgement may carry -- and §3.5.2.1 requires the sender of a PUBREC to
// use one of the listed codes, so a conformant client had to read that packet
// as a Malformed Packet while the connection stayed up and the violation went
// unrecorded.
func TestInboundReceiveMaxExceededDisconnects(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))

	sess := b.sessions.CreateSession("rm-inbound", &protocol.ConnectPacket{
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ProtocolVersion: protocol.Version50,
		ClientID:        "rm-inbound",
	}, false)
	// The advertised value is taken from the wire in
	// TestConnAckReceiveMaxIsServerLimit; here it is set directly so the test
	// reaches the boundary without pushing maxInflight (100) publishes.
	sess.ReceiveMax = 2

	collect, _ := inboundWirePump(t, b, "rm-inbound", protocol.Version50)

	publish := func(id uint16) {
		b.handlePublish(context.Background(), "rm-inbound", sess, &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 2},
			PacketID:    id,
			Topic:       "rm/inbound",
			Payload:     []byte("staged"),
		})
	}
	for _, id := range []uint16{1, 2} {
		publish(id)
	}
	// One past the advertised limit: the offending PUBLISH is dropped and the
	// connection is torn down.
	publish(3)

	pkts := collect()

	// The two accepted publishes are acknowledged so the client can proceed.
	if len(pkts) != 3 {
		t.Fatalf("broker wrote %d packets (%T), want 2 PUBREC + 1 DISCONNECT", len(pkts), pkts)
	}
	for i, wantID := range []uint16{1, 2} {
		rec, ok := pkts[i].(*protocol.PubRecPacket)
		if !ok {
			t.Fatalf("packet %d = %T, want *protocol.PubRecPacket", i, pkts[i])
		}
		if rec.PacketID != wantID || rec.ReasonCode != protocol.ReasonCodeSuccess {
			t.Errorf("PUBREC %d = {pid:%d rc:0x%02X}, want {pid:%d rc:0x00}",
				i, rec.PacketID, rec.ReasonCode, wantID)
		}
	}

	disc, ok := pkts[2].(*protocol.DisconnectPacket)
	if !ok {
		t.Fatalf("the over-limit publish was answered with %T; §3.3.4 requires a DISCONNECT "+
			"carrying 0x93, which is the only packet Table 2-6 allows that code in", pkts[2])
	}
	if disc.ReasonCode != protocol.DisconnectReceiveMaximumExceeded {
		t.Errorf("DISCONNECT reason = 0x%02X, want 0x93 (Receive Maximum exceeded)", disc.ReasonCode)
	}
}

// TestInboundBudgetPreV5KeepsConnection is the control for the version gate on
// this path. MQTT 3.1 and 3.1.1 have no Receive Maximum at all, so there is no
// negotiated limit to exceed: the per-client budget there is the broker's own
// resource guard, and a client that trips it must keep its connection (its
// PUBREC has no reason field to report anything with). Without this case a
// change that tore down every over-budget client would still pass the test
// above.
func TestInboundBudgetPreV5KeepsConnection(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))

	sess := b.sessions.CreateSession("rm-311", &protocol.ConnectPacket{
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ProtocolVersion: protocol.Version311,
		ClientID:        "rm-311",
	}, false)
	sess.ReceiveMax = 2

	collect, closed := inboundWirePump(t, b, "rm-311", protocol.Version311)

	for _, id := range []uint16{1, 2, 3} {
		b.handlePublish(context.Background(), "rm-311", sess, &protocol.PublishPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 2},
			PacketID:    id,
			Topic:       "rm/inbound",
			Payload:     []byte("staged"),
		})
	}

	// The drain goroutine only finishes when the socket is closed, so it
	// staying open here is the assertion: no teardown happened.
	select {
	case <-closed:
		t.Fatal("the broker closed the connection: MQTT 3.1.1 has no Receive Maximum to exceed")
	case <-time.After(200 * time.Millisecond):
	}

	pkts := collect()
	if len(pkts) != 3 {
		t.Errorf("broker wrote %d packets, want 3 PUBRECs (one per accepted-and-dropped publish)", len(pkts))
	}
	for i, pkt := range pkts {
		if _, ok := pkt.(*protocol.PubRecPacket); !ok {
			t.Errorf("packet %d = %T, want *protocol.PubRecPacket", i, pkt)
		}
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
	if err := b.publishWill(context.Background(), "u1", "secret/topic", []byte("leak"), 1, false, nil); err != nil {
		t.Fatalf("publishWill: %v", err)
	}

	// The subscriber must NOT receive anything. Decode reads from serverConn
	// (the side the broker writes to), so set the deadline there.
	_ = serverConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, err := codec.Decode(serverConn); err == nil {
		t.Fatal("subscriber received a message for a will on a non-authorized topic")
	}
}

// malformedTopicTeardown drives one malformed-topic scenario against a broker
// whose only connection is a net.Pipe pair, and returns whatever the broker
// managed to write before the socket went away.
//
// The scenario is expressed as a closure because the three entry points
// (handlePublish, handleSubscribe, handleUnsubscribe) take different packet
// types; keeping them in one table would need an interface this repo does not
// have for control packets. The session is passed in because every teardown
// gate reads the negotiated protocol version from it.
//
// On the pre-v5 cases the assertion is that nothing is written and the
// connection still goes away. That pins the wire contract, but it cannot tell a
// working broker-side version gate from a removed one: encodeDisconnect refuses
// a reason code on a pre-v5 codec and writes nothing, so the same empty stream
// results either way. The layer that actually guards the wire is pinned
// separately by protocol.TestDisconnectVersionGuard; the broker-side gate in
// rejectMalformedTopic is kept for the same reason the other teardown paths
// keep theirs -- it does not depend on that codec behaviour, and it is what
// decides whether closeProtocolViolationWithReason is reached at all.
func malformedTopicTeardown(t *testing.T, name string, version protocol.Version, drive func(b *Broker, clientID string, sess *Session)) []protocol.Packet {
	t.Helper()

	clientID := "mt-" + name
	b := New(WithAuth(AllowAllAuth{}))
	sess := b.sessions.CreateSession(clientID, &protocol.ConnectPacket{
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ProtocolVersion: version,
		ClientID:        clientID,
	}, false)

	collect, _ := inboundWirePump(t, b, clientID, version)
	drive(b, clientID, sess)
	return collect()
}

// TestMalformedTopicNameIsProtocolError covers a PUBLISH whose Topic Name is not
// well-formed.
//
// MQTT 5.0 §4.7.1.2 [MQTT-4.7.1-1] and §4.7.1.3 [MQTT-4.7.1-2] put MUST-level
// constraints on where '#' and '+' may appear, so a Topic Name containing one is
// a Protocol Error. §4.13.1 requires a Server that detects a Protocol Error to
// close the Network Connection [MQTT-4.13.1-1], sending a DISCONNECT with 0x82
// (Protocol Error) unless §3.14.2.1 defines a more specific code -- and it
// defines none for this case.
//
// The broker used to answer with a PUBACK/PUBREC carrying 0x90 (Topic Name
// invalid) and keep the connection open. Table 3-4 defines 0x90 as "The Topic
// Name is not malformed, but is not accepted by this Client or Server", so it
// was the wrong code for a malformed name; and on an MQTT 3.1 connection
// ackDiscardedPublish collapsed the reason to 0x00, which is a success
// acknowledgement for a publish that was never routed.
func TestMalformedTopicNameIsProtocolError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version protocol.Version
		qos     byte
	}{
		{"mqtt50_qos1", protocol.Version50, 1},
		{"mqtt50_qos2", protocol.Version50, 2},
		{"mqtt311_qos1", protocol.Version311, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			qos := tc.qos
			pkts := malformedTopicTeardown(t, tc.name, tc.version, func(b *Broker, clientID string, sess *Session) {
				b.handlePublish(context.Background(), clientID, sess, &protocol.PublishPacket{
					FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: qos},
					PacketID:    7,
					Topic:       "sport#", // '#' is illegal in a Topic Name
					Payload:     []byte("x"),
				})
			})

			if tc.version != protocol.Version50 {
				if len(pkts) != 0 {
					t.Fatalf("broker wrote %d packets (%T), want none: MQTT 3.1.1 has no server DISCONNECT, "+
						"but the connection must still be closed rather than acknowledged", len(pkts), pkts)
				}
				return
			}
			if len(pkts) != 1 {
				t.Fatalf("broker wrote %d packets (%T), want 1 DISCONNECT", len(pkts), pkts)
			}
			disc, ok := pkts[0].(*protocol.DisconnectPacket)
			if !ok {
				t.Fatalf("the malformed publish was answered with %T; §4.13.1 requires the connection to be "+
					"closed with a DISCONNECT carrying 0x82, and Table 3-4 reserves 0x90 for a Topic Name that is "+
					"\"not malformed\"", pkts[0])
			}
			if disc.ReasonCode != protocol.DisconnectProtocolError {
				t.Errorf("DISCONNECT reason = 0x%02X, want 0x82 (Protocol Error)", disc.ReasonCode)
			}
		})
	}
}

// TestMalformedSubscribeFilterIsProtocolError covers a SUBSCRIBE whose Topic
// Filter is not well-formed. Same reasoning as
// TestMalformedTopicNameIsProtocolError, applied to §3.8.4.
//
// The old behaviour was worse here than for PUBLISH: subAckFailureCode returns
// 0x00 (Granted QoS 0) for an MQTT 3.1 connection, whose SUBACK has no failure
// code at all, so a client subscribing to "sport#" was told its malformed
// subscription had been granted.
func TestMalformedSubscribeFilterIsProtocolError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version protocol.Version
	}{
		{"mqtt50", protocol.Version50},
		{"mqtt311", protocol.Version311},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkts := malformedTopicTeardown(t, tc.name, tc.version, func(b *Broker, clientID string, sess *Session) {
				b.handleSubscribe(context.Background(), clientID, sess, &protocol.SubscribePacket{
					FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe},
					PacketID:    3,
					Topics:      []protocol.TopicFilter{{Topic: "sport#", QoS: 1}},
				})
			})

			if tc.version != protocol.Version50 {
				if len(pkts) != 0 {
					t.Fatalf("broker wrote %d packets (%T), want none: MQTT 3.1.1 has no server DISCONNECT, "+
						"but the connection must still be closed rather than answered 0x00 (Granted QoS 0)",
						len(pkts), pkts)
				}
				return
			}
			if len(pkts) != 1 {
				t.Fatalf("broker wrote %d packets (%T), want 1 DISCONNECT", len(pkts), pkts)
			}
			disc, ok := pkts[0].(*protocol.DisconnectPacket)
			if !ok {
				t.Fatalf("the malformed subscribe was answered with %T; §4.13.1 requires the connection to be "+
					"closed with a DISCONNECT carrying 0x82, and 0x8F is defined for a Topic Filter that is "+
					"\"correctly formed but is not allowed\"", pkts[0])
			}
			if disc.ReasonCode != protocol.DisconnectProtocolError {
				t.Errorf("DISCONNECT reason = 0x%02X, want 0x82 (Protocol Error)", disc.ReasonCode)
			}
		})
	}
}

// TestMalformedUnsubscribeFilterIsProtocolError covers an UNSUBSCRIBE whose
// Topic Filter is not well-formed, the third entry point with the same defect.
//
// Here the old code did send a Table 3-9 code (0x8F) on an MQTT 5 connection, but
// 0x8F is defined for a filter that is "correctly formed but is not allowed",
// and the connection stayed open either way.
func TestMalformedUnsubscribeFilterIsProtocolError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version protocol.Version
	}{
		{"mqtt50", protocol.Version50},
		{"mqtt311", protocol.Version311},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkts := malformedTopicTeardown(t, tc.name, tc.version, func(b *Broker, clientID string, sess *Session) {
				b.handleUnsubscribe(clientID, sess, &protocol.UnsubscribePacket{
					FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeUnsubscribe},
					PacketID:    5,
					Topics:      []string{"sport#"},
				})
			})

			if tc.version != protocol.Version50 {
				if len(pkts) != 0 {
					t.Fatalf("broker wrote %d packets (%T), want none: MQTT 3.1.1 has no server DISCONNECT, "+
						"but the connection must still be closed", len(pkts), pkts)
				}
				return
			}
			if len(pkts) != 1 {
				t.Fatalf("broker wrote %d packets (%T), want 1 DISCONNECT", len(pkts), pkts)
			}
			disc, ok := pkts[0].(*protocol.DisconnectPacket)
			if !ok {
				t.Fatalf("the malformed unsubscribe was answered with %T; §4.13.1 requires the connection to be "+
					"closed with a DISCONNECT carrying 0x82", pkts[0])
			}
			if disc.ReasonCode != protocol.DisconnectProtocolError {
				t.Errorf("DISCONNECT reason = 0x%02X, want 0x82 (Protocol Error)", disc.ReasonCode)
			}
		})
	}
}

// TestWellFormedTopicIsNotTornDown is the control for the three tests above:
// well-formed Topic Names and Topic Filters must still be acknowledged, and the
// connection must survive. Without it a change that tore down every publish or
// subscribe would pass those tests just as well.
//
// The connection-survival assertion is made by driving two requests in a row and
// requiring an answer to both: if the first request had torn the connection
// down, the second write would have nowhere to go.
func TestWellFormedTopicIsNotTornDown(t *testing.T) {
	b := New(WithAuth(AllowAllAuth{}))
	sess := b.sessions.CreateSession("wf", &protocol.ConnectPacket{
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       60,
		ProtocolVersion: protocol.Version50,
		ClientID:        "wf",
	}, false)

	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})
	codec := protocol.NewCodecForVersion(0, protocol.Version50)
	b.mu.Lock()
	b.connections["wf"] = &clientState{conn: serverConn, codec: codec}
	b.mu.Unlock()

	// driveThenRead starts the reader before running drive, and waits for
	// exactly one decodable packet. The reader has to be running first:
	// net.Pipe delivers per Write, so a synchronous broker write with no
	// reader on the other end blocks forever. A timeout means the broker never
	// answered, which is what a teardown looks like from this side.
	driveThenRead := func(what string, drive func()) protocol.Packet {
		t.Helper()
		type result struct {
			pkt protocol.Packet
			err error
		}
		ch := make(chan result, 1)
		go func() {
			pkt, err := codec.Decode(clientConn)
			ch <- result{pkt, err}
		}()
		drive()
		select {
		case r := <-ch:
			if r.err != nil {
				t.Fatalf("decode %s: %v", what, r.err)
			}
			return r.pkt
		case <-time.After(time.Second):
			t.Fatalf("broker never wrote %s; the connection looks torn down", what)
			return nil
		}
	}

	// 1. Well-formed QoS 1 publish -> PUBACK, twice, to prove the connection lives.
	for i := 0; i < 2; i++ {
		id := uint16(10 + i)
		got := driveThenRead("PUBACK", func() {
			b.handlePublish(context.Background(), "wf", sess, &protocol.PublishPacket{
				FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 1},
				PacketID:    id,
				Topic:       "sport/tennis",
				Payload:     []byte("x"),
			})
		})
		ack, ok := got.(*protocol.PubAckPacket)
		if !ok {
			t.Fatalf("well-formed publish answered with %T, want *protocol.PubAckPacket", got)
		}
		if ack.ReasonCode != protocol.ReasonCodeSuccess {
			t.Errorf("PUBACK reason = 0x%02X, want 0x00", ack.ReasonCode)
		}
	}

	// 2. Well-formed subscribe -> SUBACK granting the requested QoS.
	gotSub := driveThenRead("SUBACK", func() {
		b.handleSubscribe(context.Background(), "wf", sess, &protocol.SubscribePacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe},
			PacketID:    20,
			Topics:      []protocol.TopicFilter{{Topic: "sport/+", QoS: 1}},
		})
	})
	sub, ok := gotSub.(*protocol.SubAckPacket)
	if !ok {
		t.Fatalf("well-formed subscribe answered with %T, want *protocol.SubAckPacket", gotSub)
	}
	if len(sub.ReasonCodes) != 1 || sub.ReasonCodes[0] != 1 {
		t.Errorf("SUBACK reason codes = %v, want [1] (Granted QoS 1)", sub.ReasonCodes)
	}

	// 3. Well-formed unsubscribe -> UNSUBACK success, even with no prior
	// subscription: [MQTT-3.10.4-5] requires a response either way.
	gotUnsub := driveThenRead("UNSUBACK", func() {
		b.handleUnsubscribe("wf", sess, &protocol.UnsubscribePacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeUnsubscribe},
			PacketID:    21,
			Topics:      []string{"sport/tennis"},
		})
	})
	unsub, ok := gotUnsub.(*protocol.UnsubAckPacket)
	if !ok {
		t.Fatalf("well-formed unsubscribe answered with %T, want *protocol.UnsubAckPacket", gotUnsub)
	}
	if len(unsub.ReasonCodes) != 1 || unsub.ReasonCodes[0] != protocol.ReasonCodeSuccess {
		t.Errorf("UNSUBACK reason codes = %v, want [0x00]", unsub.ReasonCodes)
	}
}
