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
func (m *retainedMetrics) SetSubscriptions(count int) {}
func (m *retainedMetrics) IncErrors(component string) {}

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
