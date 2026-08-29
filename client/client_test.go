package client

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/pkg/metrics"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// ─── Option tests ──────────────────────────────────────────────

func TestOptions(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		c := New()
		if c.opts.Host != "localhost" {
			t.Errorf("expected host localhost, got %s", c.opts.Host)
		}
		if c.opts.Port != 18983 {
			t.Errorf("expected port 18983, got %d", c.opts.Port)
		}
		if c.opts.KeepAlive != 60 {
			t.Errorf("expected keepalive 60, got %d", c.opts.KeepAlive)
		}
		if !c.opts.CleanSession {
			t.Error("expected clean session true")
		}
	})

	t.Run("WithHostPort", func(t *testing.T) {
		c := New(WithHostPort("broker.example.com", 18993))
		if c.opts.Host != "broker.example.com" {
			t.Errorf("expected host broker.example.com, got %s", c.opts.Host)
		}
		if c.opts.Port != 18993 {
			t.Errorf("expected port 18993, got %d", c.opts.Port)
		}
	})

	t.Run("WithAddr", func(t *testing.T) {
		c := New(WithAddr("10.0.0.1:1884"))
		if c.opts.Host != "10.0.0.1" {
			t.Errorf("expected host 10.0.0.1, got %s", c.opts.Host)
		}
		if c.opts.Port != 1884 {
			t.Errorf("expected port 1884, got %d", c.opts.Port)
		}
	})

	t.Run("WithClientID", func(t *testing.T) {
		c := New(WithClientID("test-client-123"))
		if c.opts.ClientID != "test-client-123" {
			t.Errorf("expected clientID test-client-123, got %s", c.opts.ClientID)
		}
	})

	t.Run("WithCredentials", func(t *testing.T) {
		c := New(WithCredentials("user", "pass"))
		if c.opts.Username != "user" {
			t.Errorf("expected username user, got %s", c.opts.Username)
		}
		if c.opts.Password != "pass" {
			t.Errorf("expected password pass, got %s", c.opts.Password)
		}
	})

	t.Run("WithKeepAlive", func(t *testing.T) {
		c := New(WithKeepAlive(30))
		if c.opts.KeepAlive != 30 {
			t.Errorf("expected keepalive 30, got %d", c.opts.KeepAlive)
		}
	})

	t.Run("WithCleanSession", func(t *testing.T) {
		c := New(WithCleanSession(false))
		if c.opts.CleanSession {
			t.Error("expected clean session false")
		}
	})

	t.Run("WithMaxPacketSize", func(t *testing.T) {
		c := New(WithMaxPacketSize(65536))
		if c.opts.MaxPacketSize != 65536 {
			t.Errorf("expected max packet size 65536, got %d", c.opts.MaxPacketSize)
		}
	})

	t.Run("WithConnectTimeout", func(t *testing.T) {
		c := New(WithConnectTimeout(10 * time.Second))
		if c.opts.ConnectTimeout != 10*time.Second {
			t.Errorf("expected connect timeout 10s, got %v", c.opts.ConnectTimeout)
		}
	})

	t.Run("Combined", func(t *testing.T) {
		c := New(
			WithHostPort("mqtt.example.com", 18983),
			WithClientID("my-client"),
			WithCredentials("admin", "secret"),
			WithKeepAlive(120),
			WithCleanSession(true),
			WithMaxPacketSize(1024*1024),
			WithConnectTimeout(15*time.Second),
		)
		if c.opts.Host != "mqtt.example.com" {
			t.Errorf("host: %s", c.opts.Host)
		}
		if c.opts.Port != 18983 {
			t.Errorf("port: %d", c.opts.Port)
		}
		if c.opts.ClientID != "my-client" {
			t.Errorf("clientID: %s", c.opts.ClientID)
		}
		if c.opts.Username != "admin" {
			t.Errorf("username: %s", c.opts.Username)
		}
		if c.opts.Password != "secret" {
			t.Errorf("password: %s", c.opts.Password)
		}
		if c.opts.KeepAlive != 120 {
			t.Errorf("keepalive: %d", c.opts.KeepAlive)
		}
		if !c.opts.CleanSession {
			t.Error("clean session not set")
		}
		if c.opts.MaxPacketSize != 1024*1024 {
			t.Errorf("maxPacketSize: %d", c.opts.MaxPacketSize)
		}
		if c.opts.ConnectTimeout != 15*time.Second {
			t.Errorf("connectTimeout: %v", c.opts.ConnectTimeout)
		}
	})
}

// ─── Simple state tests ───────────────────────────────────────

func TestIsConnected(t *testing.T) {
	c := New()
	if c.IsConnected() {
		t.Error("expected not connected initially")
	}
}

func TestSetOnMessage(t *testing.T) {
	c := New()
	called := false
	c.SetOnMessage(func(topic string, qos byte, payload []byte) {
		called = true
		if topic != "test/topic" {
			t.Errorf("expected topic test/topic, got %s", topic)
		}
		if qos != 1 {
			t.Errorf("expected qos 1, got %d", qos)
		}
		if string(payload) != "hello" {
			t.Errorf("expected payload hello, got %s", payload)
		}
	})
	c.msgMu.RLock()
	fn := c.onMessage
	c.msgMu.RUnlock()
	if fn != nil {
		fn("test/topic", 1, []byte("hello"))
	}
	if !called {
		t.Error("onMessage callback was not called")
	}
}

func TestSetOnError(t *testing.T) {
	c := New()
	var capturedArgs []interface{}
	c.SetOnError(func(format string, args ...interface{}) {
		capturedArgs = args
	})

	// Trigger logError
	c.logError("test error: %d", 42)

	c.msgMu.RLock()
	fn := c.onError
	c.msgMu.RUnlock()
	if fn == nil {
		t.Fatal("onError should be set")
	}
	if len(capturedArgs) != 1 || capturedArgs[0] != 42 {
		t.Errorf("expected captured args [42], got %v", capturedArgs)
	}
}

func TestLogErrorWithoutCallback(t *testing.T) {
	c := New()
	// Should not panic when onError is nil
	c.logError("something happened: %v", "test")
}

func TestNextPacketID(t *testing.T) {
	c := New()

	first := c.nextPacketID()
	if first != 1 {
		t.Errorf("expected first packet ID 1, got %d", first)
	}

	second := c.nextPacketID()
	if second != 2 {
		t.Errorf("expected second packet ID 2, got %d", second)
	}

	// Set nextPID near overflow and test wrap
	c.nextPID.Store(65535)
	pid := c.nextPacketID()
	if pid != 65535 {
		t.Errorf("expected packet ID 65535, got %d", pid)
	}

	// Should wrap to 1
	pid = c.nextPacketID()
	if pid != 1 {
		t.Errorf("expected wrapped packet ID 1, got %d", pid)
	}
}

// ─── Not-connected guard tests ────────────────────────────────

func TestConnectNotConnected(t *testing.T) {
	c := New(WithHostPort("127.0.0.1", 18983))

	err := c.Publish(context.Background(), "test", 0, false, []byte("data"))
	if err == nil {
		t.Error("expected error when publishing while not connected")
	}
}

func TestSubscribeNotConnected(t *testing.T) {
	c := New()
	_, err := c.Subscribe(context.Background(), []TopicSubscription{{Topic: "test", QoS: 0}})
	if err == nil {
		t.Error("expected error when subscribing while not connected")
	}
}

func TestUnsubscribeNotConnected(t *testing.T) {
	c := New()
	err := c.Unsubscribe(context.Background(), []string{"test"})
	if err == nil {
		t.Error("expected error when unsubscribing while not connected")
	}
}

func TestDisconnectWhenNotConnected(t *testing.T) {
	c := New()
	err := c.Disconnect(context.Background())
	if err != nil {
		t.Errorf("expected no error when disconnecting while not connected, got %v", err)
	}
}

// ─── handlePublish / deliverResponse internal tests ────────────

func TestHandlePublish_QoS0(t *testing.T) {
	c := New()

	msgChan := make(chan struct {
		topic   string
		qos     byte
		payload []byte
	}, 1)

	c.SetOnMessage(func(topic string, qos byte, payload []byte) {
		msgChan <- struct {
			topic   string
			qos     byte
			payload []byte
		}{topic, qos, payload}
	})

	pkt := &protocol.PublishPacket{}
	pkt.FixedHeader.PacketType = protocol.PacketTypePublish
	pkt.FixedHeader.QoS = 0
	pkt.Topic = "test/qos0"
	pkt.Payload = []byte("hello-qos0")

	c.handlePublish(pkt)

	select {
	case msg := <-msgChan:
		if msg.topic != "test/qos0" {
			t.Errorf("topic = %q, want %q", msg.topic, "test/qos0")
		}
		if string(msg.payload) != "hello-qos0" {
			t.Errorf("payload = %q, want %q", msg.payload, "hello-qos0")
		}
	case <-time.After(time.Second):
		t.Fatal("onMessage was not called")
	}
}

func TestHandlePublish_QoS1(t *testing.T) {
	conn := &mockConn{}
	c := New()
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	msgChan := make(chan string, 1)
	c.SetOnMessage(func(topic string, qos byte, payload []byte) {
		msgChan <- string(payload)
	})

	pkt := &protocol.PublishPacket{}
	pkt.FixedHeader.PacketType = protocol.PacketTypePublish
	pkt.FixedHeader.QoS = 1
	pkt.PacketID = 42
	pkt.Topic = "test/qos1"
	pkt.Payload = []byte("hello-qos1")

	c.handlePublish(pkt)

	// Verify onMessage was called
	select {
	case payload := <-msgChan:
		if payload != "hello-qos1" {
			t.Errorf("payload = %q, want %q", payload, "hello-qos1")
		}
	case <-time.After(time.Second):
		t.Fatal("onMessage was not called")
	}

	// Should also have written a PUBACK
	if !conn.wrotePubAck {
		t.Error("expected PUBACK to be written for QoS 1")
	}
}

func TestHandlePublish_QoS2_Dedup(t *testing.T) {
	conn := &mockConn{}
	c := New()
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	callCount := 0
	c.SetOnMessage(func(topic string, qos byte, payload []byte) {
		callCount++
	})

	pkt := &protocol.PublishPacket{}
	pkt.FixedHeader.PacketType = protocol.PacketTypePublish
	pkt.FixedHeader.QoS = 2
	pkt.PacketID = 7
	pkt.Topic = "test/qos2"
	pkt.Payload = []byte("dup-test")

	// First call: should deliver message
	c.handlePublish(pkt)
	if callCount != 1 {
		t.Errorf("expected onMessage called once, got %d", callCount)
	}
	// should have written a PUBREC
	pubrecCount := conn.pubrecCount

	// Second call with same PacketID: duplicate, should NOT deliver
	c.handlePublish(pkt)
	if callCount != 1 {
		t.Errorf("expected onMessage still called once (dup), got %d", callCount)
	}
	// Should still write second PUBREC for duplicate
	expectedPubRec := pubrecCount + 1
	if conn.pubrecCount != expectedPubRec {
		t.Errorf("expected %d PUBRECs (dup still acknowledged), got %d", expectedPubRec, conn.pubrecCount)
	}
}

func TestDeliverResponse(t *testing.T) {
	c := New()

	ch := make(chan protocol.Packet, 1)
	c.pendingMu.Lock()
	c.pending[100] = ch
	c.pendingMu.Unlock()

	ack := &protocol.PubAckPacket{PacketID: 100}
	ack.FixedHeader.PacketType = protocol.PacketTypePubAck
	c.deliverResponse(100, ack)

	select {
	case received := <-ch:
		if _, ok := received.(*protocol.PubAckPacket); !ok {
			t.Errorf("expected PubAckPacket, got %T", received)
		}
	default:
		t.Fatal("deliverResponse did not deliver to channel")
	}
}

func TestDeliverResponse_NoPending(t *testing.T) {
	c := New()
	// Should not panic when there's no pending channel for the packet ID
	c.deliverResponse(999, &protocol.PubAckPacket{})
}

// ─── Integration tests with real broker ───────────────────────

func startTestBroker(t *testing.T) (*api.Broker, string) {
	t.Helper()

	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"  // dynamic port
	cfg.MetricsAddr = ":0" // avoid port conflict with health server
	cfg.MaxConnections = 100

	b := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
		api.WithMetrics(metrics.Default()),
	)

	if err := b.Start(); err != nil {
		t.Fatalf("failed to start test broker: %v", err)
	}

	addr := b.Addr()
	if addr == "" {
		b.Stop()
		t.Fatal("broker returned empty address")
	}

	t.Cleanup(func() {
		b.Stop()
	})

	return b, addr
}

func TestConnectToBroker(t *testing.T) {
	_, addr := startTestBroker(t)
	host, port := splitAddr(addr)

	c := New(
		WithHostPort(host, port),
		WithClientID("test-connect-client"),
		WithConnectTimeout(5*time.Second),
	)

	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if !c.IsConnected() {
		t.Error("expected IsConnected() to return true after Connect")
	}

	// Test duplicate connect returns error
	err := c.Connect(ctx)
	if err == nil {
		t.Error("expected error on duplicate Connect")
	}

	if err := c.Disconnect(ctx); err != nil {
		t.Errorf("Disconnect failed: %v", err)
	}

	if c.IsConnected() {
		t.Error("expected IsConnected() to return false after Disconnect")
	}
}

func TestPublishQoS0(t *testing.T) {
	_, addr := startTestBroker(t)
	host, port := splitAddr(addr)

	c := New(
		WithHostPort(host, port),
		WithClientID("test-pub-qos0"),
		WithConnectTimeout(5*time.Second),
	)

	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer c.Disconnect(ctx)

	// QoS 0: no acknowledgment, should return immediately
	err := c.Publish(ctx, "test/qos0", 0, false, []byte("hello"))
	if err != nil {
		t.Errorf("Publish QoS 0 failed: %v", err)
	}
}

func TestPublishQoS1(t *testing.T) {
	_, addr := startTestBroker(t)
	host, port := splitAddr(addr)

	c := New(
		WithHostPort(host, port),
		WithClientID("test-pub-qos1"),
		WithConnectTimeout(5*time.Second),
	)

	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer c.Disconnect(ctx)

	// QoS 1: should receive PUBACK
	err := c.Publish(ctx, "test/qos1", 1, false, []byte("hello-qos1"))
	if err != nil {
		t.Errorf("Publish QoS 1 failed: %v", err)
	}
}

func TestPublishQoS2(t *testing.T) {
	_, addr := startTestBroker(t)
	host, port := splitAddr(addr)

	c := New(
		WithHostPort(host, port),
		WithClientID("test-pub-qos2"),
		WithConnectTimeout(5*time.Second),
	)

	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer c.Disconnect(ctx)

	// QoS 2: full flow (PUB->PUBREC->PUBREL->PUBCOMP)
	err := c.Publish(ctx, "test/qos2", 2, false, []byte("hello-qos2"))
	if err != nil {
		t.Errorf("Publish QoS 2 failed: %v", err)
	}
}

func TestSubscribe(t *testing.T) {
	_, addr := startTestBroker(t)
	host, port := splitAddr(addr)

	c := New(
		WithHostPort(host, port),
		WithClientID("test-subscribe-client"),
		WithConnectTimeout(5*time.Second),
	)

	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer c.Disconnect(ctx)

	reasonCodes, err := c.Subscribe(ctx, []TopicSubscription{
		{Topic: "test/subscribe", QoS: 1},
	})
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}
	if len(reasonCodes) != 1 {
		t.Errorf("expected 1 reason code, got %d", len(reasonCodes))
	}
}

func TestUnsubscribe(t *testing.T) {
	_, addr := startTestBroker(t)
	host, port := splitAddr(addr)

	c := New(
		WithHostPort(host, port),
		WithClientID("test-unsub-client"),
		WithConnectTimeout(5*time.Second),
	)

	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer c.Disconnect(ctx)

	// Subscribe first
	_, err := c.Subscribe(ctx, []TopicSubscription{
		{Topic: "test/unsub", QoS: 1},
	})
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Then unsubscribe
	err = c.Unsubscribe(ctx, []string{"test/unsub"})
	if err != nil {
		t.Errorf("Unsubscribe failed: %v", err)
	}
}

func TestOnMessageDelivery(t *testing.T) {
	_, addr := startTestBroker(t)
	host, port := splitAddr(addr)

	// Publisher client
	pub := New(
		WithHostPort(host, port),
		WithClientID("test-publisher"),
		WithConnectTimeout(5*time.Second),
	)

	// Subscriber client
	sub := New(
		WithHostPort(host, port),
		WithClientID("test-subscriber"),
		WithConnectTimeout(5*time.Second),
	)

	ctx := context.Background()

	if err := pub.Connect(ctx); err != nil {
		t.Fatalf("publisher Connect failed: %v", err)
	}
	defer pub.Disconnect(ctx)

	if err := sub.Connect(ctx); err != nil {
		t.Fatalf("subscriber Connect failed: %v", err)
	}
	defer sub.Disconnect(ctx)

	// Subscribe
	_, err := sub.Subscribe(ctx, []TopicSubscription{
		{Topic: "test/delivery", QoS: 1},
	})
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Set up message receiver
	msgChan := make(chan string, 1)
	sub.SetOnMessage(func(topic string, qos byte, payload []byte) {
		msgChan <- string(payload)
	})

	// Publish
	err = pub.Publish(ctx, "test/delivery", 1, false, []byte("delivery-test"))
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	// Verify delivery with timeout
	select {
	case payload := <-msgChan:
		if payload != "delivery-test" {
			t.Errorf("received payload = %q, want %q", payload, "delivery-test")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for message delivery")
	}
}

func TestConnectWithAutoClientID(t *testing.T) {
	_, addr := startTestBroker(t)
	host, port := splitAddr(addr)

	// No explicit ClientID — should auto-generate
	c := New(
		WithHostPort(host, port),
		WithConnectTimeout(5*time.Second),
	)

	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer c.Disconnect(ctx)

	if c.clientID == "" {
		t.Error("expected auto-generated client ID")
	}
}

func TestConnectInvalidAddr(t *testing.T) {
	c := New(
		WithHostPort("127.0.0.1", 1), // port 1 is unlikely to be listening
		WithConnectTimeout(time.Second),
	)

	ctx := context.Background()
	err := c.Connect(ctx)
	if err == nil {
		t.Fatal("expected error connecting to invalid address")
	}
}

// ─── ReadLoop error handling test ──────────────────────────────

func TestReadLoopDecodeErrorDisconnects(t *testing.T) {
	conn := newBlockingReadConn()

	c := New()
	connCtx, connCancel := context.WithCancel(context.Background())
	connDone := make(chan struct{})
	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.connCtx, c.connCancel, c.connDone = connCtx, connCancel, connDone
	c.mu.Unlock()

	go c.readLoop(conn, connCtx, connCancel, connDone)

	// Close the conn to trigger Decode error
	conn.Close()

	// Wait for readLoop to finish
	<-connDone

	if c.IsConnected() {
		t.Error("expected IsConnected() to be false after readLoop error")
	}

	// Canceled context means pending channels were drained
	c.pendingMu.RLock()
	pendingLen := len(c.pending)
	c.pendingMu.RUnlock()
	if pendingLen != 0 {
		t.Errorf("expected pending map to be drained, got %d items", pendingLen)
	}
}

// ─── Disconnect with blocking readLoop ─────────────────────────

func TestDisconnectClosesConnectionBeforeWaitingForReadLoop(t *testing.T) {
	conn := newBlockingReadConn()

	c := New()
	connCtx, connCancel := context.WithCancel(context.Background())
	connDone := make(chan struct{})
	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.connCtx, c.connCancel, c.connDone = connCtx, connCancel, connDone
	c.mu.Unlock()

	go c.readLoop(conn, connCtx, connCancel, connDone)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- c.Disconnect(ctx)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Disconnect returned error: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Disconnect blocked while readLoop was waiting in Decode")
	}

	if got := conn.writtenBytes(); got == 0 {
		t.Fatal("expected DISCONNECT packet bytes to be written")
	}
}

// ─── Mock helpers ──────────────────────────────────────────────

type mockConn struct {
	mu           sync.Mutex
	closed       bool
	wrotePubAck  bool
	pubrecCount  int
	pubcompCount int
}

func (m *mockConn) Read(p []byte) (int, error) {
	<-time.After(time.Hour) // Block forever in tests
	return 0, nil
}

func (m *mockConn) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Detect PUBACK (type 4), PUBREC (type 5) or PUBCOMP (type 7) packets
	if len(p) > 1 {
		packetType := p[0] >> 4
		if packetType == 4 {
			m.wrotePubAck = true
		}
		if packetType == 5 {
			m.pubrecCount++
		}
		if packetType == 7 {
			m.pubcompCount++
		}
	}
	return len(p), nil
}

func (m *mockConn) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockConn) LocalAddr() net.Addr                { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0} }
func (m *mockConn) RemoteAddr() net.Addr               { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0} }
func (m *mockConn) SetDeadline(_ time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(_ time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(_ time.Time) error { return nil }

type blockingReadConn struct {
	mu      sync.Mutex
	closed  chan struct{}
	written int
}

func newBlockingReadConn() *blockingReadConn {
	return &blockingReadConn{closed: make(chan struct{})}
}

func (c *blockingReadConn) Read(_ []byte) (int, error) {
	<-c.closed
	return 0, net.ErrClosed
}

func (c *blockingReadConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.written += len(p)
	return len(p), nil
}

func (c *blockingReadConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}

func (c *blockingReadConn) LocalAddr() net.Addr                { return nil }
func (c *blockingReadConn) RemoteAddr() net.Addr               { return nil }
func (c *blockingReadConn) SetDeadline(_ time.Time) error      { return nil }
func (c *blockingReadConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *blockingReadConn) SetWriteDeadline(_ time.Time) error { return nil }

func (c *blockingReadConn) writtenBytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.written
}

// splitAddr splits "host:port" into host string and int port.
// Replaces unspecified IPv6 "::" with "::1" (loopback) for dialing.
func splitAddr(addr string) (string, int) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1", 18983
	}
	// Strip IPv6 brackets: "[::1]" -> "::1"
	host = trimBrackets(host)
	// Unspecified address "::" can't be dialed; use IPv6 loopback
	if host == "" || host == "::" {
		host = "127.0.0.1"
	}
	port := 0
	_, err = fmt.Sscanf(portStr, "%d", &port)
	if err != nil || port == 0 {
		return host, 18983
	}
	return host, port
}

func trimBrackets(host string) string {
	if len(host) > 2 && host[0] == '[' && host[len(host)-1] == ']' {
		return host[1 : len(host)-1]
	}
	return host
}

// TestHandlePubRelSendsPubComp verifies the client completes an inbound QoS 2
// exchange by sending PUBCOMP for a broker PUBREL and clears the duplicate
// tracking entry.
func TestHandlePubRelSendsPubComp(t *testing.T) {
	conn := &mockConn{}
	c := New()
	c.mu.Lock()
	c.conn = conn
	c.receivedQoS2[7] = struct{}{}
	c.mu.Unlock()

	// Simulate the readLoop receiving a PUBREL for packet 7.
	c.handlePubRel(7)

	if conn.pubcompCount != 1 {
		t.Errorf("expected 1 PUBCOMP written, got %d", conn.pubcompCount)
	}
	c.mu.Lock()
	_, stillTracked := c.receivedQoS2[7]
	c.mu.Unlock()
	if stillTracked {
		t.Error("receivedQoS2 entry was not cleared after PUBCOMP")
	}
}

// TestNextPacketIDSkipsInFlight verifies the allocator skips packet IDs that are
// still in flight or awaiting a response (P2-12 wrap-around ACK correlation).
func TestNextPacketIDSkipsInFlight(t *testing.T) {
	c := New()
	c.inflightMu.Lock()
	c.inflight[3] = &inflightEntry{}
	c.inflightMu.Unlock()
	c.pendingMu.Lock()
	c.pending[5] = make(chan protocol.Packet, 1)
	c.pendingMu.Unlock()

	c.nextPID.Store(3)
	if pid := c.nextPacketID(); pid != 4 {
		t.Errorf("expected 4 (skipping inflight 3), got %d", pid)
	}
	if pid := c.nextPacketID(); pid != 6 {
		t.Errorf("expected 6 (skipping pending 5), got %d", pid)
	}
}

// TestReconnectAfterDisconnect verifies the client can reconnect after a
// Disconnect (P2-11: one-shot context is rebuilt on Connect).
func TestReconnectAfterDisconnect(t *testing.T) {
	_, addr := startTestBroker(t)
	host, port := splitAddr(addr)
	ctx := context.Background()

	c := New(
		WithHostPort(host, port),
		WithClientID("reconnect-client"),
		WithConnectTimeout(5*time.Second),
	)
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("first Connect failed: %v", err)
	}
	if err := c.Disconnect(ctx); err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("second Connect failed: %v", err)
	}
	defer c.Disconnect(ctx)
	if !c.IsConnected() {
		t.Error("expected connected after reconnect")
	}
	if err := c.Publish(ctx, "test/reconnect", 1, false, []byte("hi")); err != nil {
		t.Fatalf("Publish after reconnect failed: %v", err)
	}
}

// TestConcurrentConnectGuard verifies only one concurrent Connect proceeds
// (NEW-16: the connecting flag rejects a second in-flight Connect).
func TestConcurrentConnectGuard(t *testing.T) {
	_, addr := startTestBroker(t)
	host, port := splitAddr(addr)
	ctx := context.Background()

	c := New(
		WithHostPort(host, port),
		WithClientID("concurrent-connect"),
		WithConnectTimeout(5*time.Second),
	)

	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			results <- c.Connect(ctx)
		}()
	}
	success, failed := 0, 0
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			failed++
		} else {
			success++
		}
	}
	if success != 1 || failed != 1 {
		t.Errorf("expected exactly 1 success and 1 failure, got %d success / %d failed", success, failed)
	}
	if c.IsConnected() {
		_ = c.Disconnect(ctx)
	}
}

// TestKeepAliveSendsPing verifies an idle client with a keep-alive sends PINGREQ
// (P1-6: without it the broker disconnects the client after 1.5x keep-alive).
func TestKeepAliveSendsPing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var pingCount atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		codec := protocol.NewCodec(65536)
		if _, err := codec.Decode(conn); err != nil { // CONNECT
			return
		}
		connack := &protocol.ConnAckPacket{}
		connack.FixedHeader.PacketType = protocol.PacketTypeConnAck
		connack.ReasonCode = protocol.ConnAckAccepted
		if err := codec.Encode(conn, connack); err != nil {
			return
		}
		// Count PINGREQ and answer with PINGRESP for ~3 seconds.
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			p, err := codec.Decode(conn)
			if err != nil {
				return
			}
			if _, ok := p.(*protocol.PingReqPacket); ok {
				pingCount.Add(1)
				pingresp := &protocol.PingRespPacket{}
				pingresp.FixedHeader.PacketType = protocol.PacketTypePingResp
				_ = codec.Encode(conn, pingresp)
			}
		}
	}()

	host, port := splitAddr(ln.Addr().String())
	c := New(
		WithHostPort(host, port),
		WithClientID("keepalive-test"),
		WithKeepAlive(1),
		WithConnectTimeout(3*time.Second),
	)
	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)
	_ = c.Disconnect(ctx)
	<-done

	if pingCount.Load() < 1 {
		t.Error("expected at least one PINGREQ from the keep-alive loop")
	}
}

// TestReceivedQoS2ClearedOnReadLoopError verifies the dedup map is cleared when
// the connection dies (NEW-17: no unbounded leak on abnormal disconnect).
func TestReceivedQoS2ClearedOnReadLoopError(t *testing.T) {
	conn := newBlockingReadConn()
	c := New()
	connCtx, connCancel := context.WithCancel(context.Background())
	connDone := make(chan struct{})
	c.mu.Lock()
	c.conn = conn
	c.connected = true
	c.receivedQoS2[7] = struct{}{}
	c.connCtx, c.connCancel, c.connDone = connCtx, connCancel, connDone
	c.mu.Unlock()

	go c.readLoop(conn, connCtx, connCancel, connDone)
	conn.Close()
	<-connDone

	c.mu.Lock()
	remaining := len(c.receivedQoS2)
	c.mu.Unlock()
	if remaining != 0 {
		t.Errorf("expected receivedQoS2 cleared after readLoop error, got %d entries", remaining)
	}
}

// TestClientConnectTOCTOU_StaleReadLoopDoesNotKillNewConnection is the L-005
// regression: when a new Connect takes over a client ID, the OLD connection's
// readLoop exits with a decode error shortly after the new connection is
// established. The stale goroutine must only cancel its own connection
// generation — previously it cancelled the shared client context, killing the
// brand-new connection's pending operations.
func TestClientConnectTOCTOU_StaleReadLoopDoesNotKillNewConnection(t *testing.T) {
	_, addr := startTestBroker(t)
	host, port := splitAddr(addr)

	for round := 1; round <= 5; round++ {
		// First generation with the shared client ID.
		c1 := New(
			WithHostPort(host, port),
			WithClientID("toctou-client"),
			WithConnectTimeout(5*time.Second),
		)
		ctx := context.Background()
		if err := c1.Connect(ctx); err != nil {
			t.Fatalf("round %d: first Connect failed: %v", round, err)
		}

		// Let the first generation settle, then take over the client ID.
		time.Sleep(100 * time.Millisecond)
		c2 := New(
			WithHostPort(host, port),
			WithClientID("toctou-client"),
			WithConnectTimeout(5*time.Second),
		)
		if err := c2.Connect(ctx); err != nil {
			t.Fatalf("round %d: takeover Connect failed: %v", round, err)
		}

		// Give the stale first-generation readLoop time to hit its decode
		// error and run its shutdown path (the L-005 race window).
		time.Sleep(300 * time.Millisecond)

		if !c2.IsConnected() {
			t.Fatalf("round %d: new connection lost after stale readLoop exit", round)
		}

		// The new connection must still complete a QoS 1 round-trip.
		if err := c2.Publish(ctx, "toctou/test", 1, false, []byte("still-alive")); err != nil {
			t.Fatalf("round %d: Publish on new connection failed after stale readLoop exit: %v", round, err)
		}
		if _, err := c2.Subscribe(ctx, []TopicSubscription{{Topic: "toctou/test", QoS: 0}}); err != nil {
			t.Fatalf("round %d: Subscribe on new connection failed: %v", round, err)
		}

		_ = c2.Disconnect(ctx)
	}
}
