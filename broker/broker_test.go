package broker

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/pkg/metrics"
	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/X1aSheng/shark-mqtt/store"
)

// TestNewBroker_Defaults tests the basic broker creation with default options
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

// TestNewBroker_WithOptions tests broker creation with custom options
func TestNewBroker_WithOptions(t *testing.T) {
	auth := &mockAuthenticator{}
	metrics := &mockMetrics{}

	opts := []Option{
		WithAuthenticator(auth),
		WithMetrics(metrics),
		WithLogger(mockLogger{}),
	}

	b := New(opts...)
	if b == nil {
		t.Fatal("expected broker, got nil")
	}

	if b.opts.authenticator != auth {
		t.Error("authenticator not set correctly")
	}

	if b.metrics != metrics {
		t.Error("metrics not set correctly")
	}
}

// TestBroker_StartStop tests broker start and stop functionality
func TestBroker_StartStop(t *testing.T) {
	b := New()

	// Start should not return error
	err := b.Start()
	if err != nil {
		t.Errorf("broker start failed: %v", err)
	}

	// Stop should not return error
	b.Stop()
}

// TestBroker_HandleConnection_InvalidPacket tests handling of invalid CONNECT packet
func TestBroker_HandleConnection_InvalidPacket(t *testing.T) {
	b := New()

	// Create a mock connection
	conn := &mockConn{}

	// Create codec that returns a non-ConnectPacket
	codec := &mockCodec{
		decodeFunc: func(r net.Conn) (protocol.Packet, error) {
			return &protocol.PublishPacket{Topic: "test", Payload: []byte("test")}, nil
		},
	}

	err := b.HandleConnection(context.Background(), conn, codec)
	if err == nil {
		t.Error("expected error for invalid packet")
	}
}

// TestBroker_HandleConnection_AuthFailed tests handling of authentication failure
func TestBroker_HandleConnection_AuthFailed(t *testing.T) {
	b := New()

	// Create failing authenticator
	auth := &mockAuthenticator{
		authenticateFunc: func(ctx context.Context, clientID, username, password string) error {
			return errors.New("authentication failed")
		},
	}

	b.opts.authenticator = auth

	// Create mock connection
	conn := &mockConn{}

	// Create CONNECT packet
	connectPkt := &protocol.ConnectPacket{
		ClientID: "test-client",
		Username: "user",
		Password: []byte("pass"),
	}

	codec := &mockCodec{
		decodeFunc: func(r net.Conn) (protocol.Packet, error) {
			return connectPkt, nil
		},
	}

	err := b.HandleConnection(context.Background(), conn, codec)
	if err == nil {
		t.Error("expected error for auth failure")
	}
}

// TestBroker_HandleConnection_Success tests successful connection handling
func TestBroker_HandleConnection_Success(t *testing.T) {
	b := New()

	// Create mock connection
	conn := &mockConn{}

	// Create CONNECT packet
	connectPkt := &protocol.ConnectPacket{
		ClientID: "test-client",
		Username: "user",
		Password: []byte("pass"),
	}

	codec := &mockCodec{
		decodeFunc: func(r net.Conn) (protocol.Packet, error) {
			return connectPkt, nil
		},
	}

	err := b.HandleConnection(context.Background(), conn, codec)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

// TestBroker_Disconnect tests client disconnect handling
func TestBroker_Disconnect(t *testing.T) {
	b := New()

	clientID := "test-client"

	// Add connection
	b.connections[clientID] = &clientState{
		conn:  &mockConn{},
		codec: &mockCodec{},
	}

	b.disconnect(clientID)

	// Verify connection is removed
	if _, exists := b.connections[clientID]; exists {
		t.Error("connection should be removed")
	}
}

// TestBroker_Publish tests message publishing
func TestBroker_Publish(t *testing.T) {
	b := New()

	clientID := "test-client"

	// Create mock session
	sess := &mockSession{}

	// Create PUBLISH packet
	publishPkt := &protocol.PublishPacket{
		Topic:   "test/topic",
		Payload: []byte("test message"),
		QoS:     0,
		Retain:  false,
	}

	// Test publish handling
	b.handlePublish(clientID, sess, publishPkt, &mockConn{}, &mockCodec{})
}

// TestBroker_Subscribe tests subscription handling
func TestBroker_Subscribe(t *testing.T) {
	b := New()

	clientID := "test-client"

	// Create mock session
	sess := &mockSession{}

	// Create SUBSCRIBE packet
	subscribePkt := &protocol.SubscribePacket{
		Topics: []protocol.TopicFilter{
			{Filter: "test/topic", QoS: 1},
		},
	}

	// Test subscribe handling
	b.handleSubscribe(clientID, sess, subscribePkt, &mockConn{}, &mockCodec{})
}

// TestBroker_Unsubscribe tests unsubscription handling
func TestBroker_Unsubscribe(t *testing.T) {
	b := New()

	clientID := "test-client"

	// Create mock session
	sess := &mockSession{}

	// Create UNSUBSCRIBE packet
	unsubscribePkt := &protocol.UnsubscribePacket{
		Topics: []string{"test/topic"},
	}

	// Test unsubscribe handling
	b.handleUnsubscribe(clientID, sess, unsubscribePkt, &mockConn{}, &mockCodec{})
}

// mockSession is a mock implementation of Session for testing
type mockSession struct{}

func (m *mockSession) IsConnected() bool { return true }
func (m *mockSession) ClientID() string { return "test-client" }

// mockAuthenticator is a mock implementation of Authenticator
type mockAuthenticator struct {
	authenticateFunc func(ctx context.Context, clientID, username, password string) error
}

func (m *mockAuthenticator) Authenticate(ctx context.Context, clientID, username, password string) error {
	if m.authenticateFunc != nil {
		return m.authenticateFunc(ctx, clientID, username, password)
	}
	return nil
}

// mockMetrics is a mock implementation of metrics.Metrics
type mockMetrics struct{}

func (m *mockMetrics) IncConnections()        {}
func (m *mockMetrics) DecConnections()        {}
func (m *mockMetrics) IncRejections(reason string) {}
func (m *mockMetrics) IncAuthFailures()       {}
func (m *mockMetrics) IncMessagesPublished(topic string, qos uint8) {}
func (m *mockMetrics) IncMessagesDelivered(clientID string, qos uint8) {}
func (m *mockMetrics) IncMessagesDropped(reason string) {}
func (m *mockMetrics) IncInflight(clientID string)      {}
func (m *mockMetrics) DecInflight(clientID string)      {}
func (m *mockMetrics) DecInflightBatch(clientID string, count int) {}
func (m *mockMetrics) IncInflightDropped(clientID string) {}
func (m *mockMetrics) IncRetries(clientID string)       {}
func (m *mockMetrics) SetOnlineSessions(count int)     {}
func (m *mockMetrics) SetOfflineSessions(count int)    {}
func (m *mockMetrics) SetRetainedMessages(count int)   {}
func (m *mockMetrics) SetSubscriptions(count int)      {}
func (m *mockMetrics) IncErrors(component string)      {}

// mockLogger is a mock implementation of logger.Logger
type mockLogger struct{}

func (m mockLogger) Debug(msg string, args ...interface{}) {}
func (m mockLogger) Info(msg string, args ...interface{})  {}
func (m mockLogger) Warn(msg string, args ...interface{})  {}
func (m mockLogger) Error(msg string, args ...interface{}) {}

// mockConn is a mock implementation of net.Conn
type mockConn struct{}

func (m *mockConn) Read(b []byte) (n int, err error)  { return 0, nil }
func (m *mockConn) Write(b []byte) (n int, err error) { return len(b), nil }
func (m *mockConn) Close() error                      { return nil }
func (m *mockConn) LocalAddr() net.Addr               { return nil }
func (m *mockConn) RemoteAddr() net.Addr              { return nil }
func (m *mockConn) SetDeadline(t time.Time) error     { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }

// mockCodec is a mock implementation of protocol.Codec
type mockCodec struct {
	decodeFunc func(r net.Conn) (protocol.Packet, error)
}

func (m *mockCodec) Decode(r net.Conn) (protocol.Packet, error) {
	if m.decodeFunc != nil {
		return m.decodeFunc(r)
	}
	return &protocol.PublishPacket{Topic: "test", Payload: []byte("test")}, nil
}

func (m *mockCodec) DecodeConnect(r net.Conn) (*protocol.ConnectPacket, error) {
	return &protocol.ConnectPacket{ClientID: "test"}, nil
}

func (m *mockCodec) Encode(p protocol.Packet) ([]byte, error) {
	return []byte("encoded"), nil
}

func (m *mockCodec) SendConnAck(w io.Writer, code protocol.ConnAckReturnCode, sessionPresent bool) error {
	return nil
}