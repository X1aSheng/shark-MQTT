package broker

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/pkg/metrics"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// recordingMetrics is a Metrics test double that records the rejection and
// error counters the network server is responsible for after the
// protocol-adapter refactor. All other methods delegate to the no-op
// implementation.
type recordingMetrics struct {
	metrics.Metrics
	mu         sync.Mutex
	rejections map[string]int
	errs       map[string]int
}

func newRecordingMetrics() *recordingMetrics {
	return &recordingMetrics{
		Metrics:    metrics.Default(),
		rejections: make(map[string]int),
		errs:       make(map[string]int),
	}
}

func (m *recordingMetrics) IncRejections(reason string) {
	m.mu.Lock()
	m.rejections[reason]++
	m.mu.Unlock()
}

func (m *recordingMetrics) IncErrors(component string) {
	m.mu.Lock()
	m.errs[component]++
	m.mu.Unlock()
}

func (m *recordingMetrics) rejectionCount(reason string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rejections[reason]
}

func (m *recordingMetrics) errorCount(component string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.errs[component]
}

// handlerFunc adapts a function to the ConnectionHandler interface.
type handlerFunc func(context.Context, *Conn) error

func (f handlerFunc) HandleConnection(ctx context.Context, c *Conn) error { return f(ctx, c) }

// deadlineErrConn is a net.Conn whose SetReadDeadline always fails, used to
// exercise the "arm the CONNECT deadline" error path.
type deadlineErrConn struct {
	net.Conn
}

func (deadlineErrConn) SetReadDeadline(time.Time) error {
	return errors.New("deadline not supported")
}

// TestServerHandshakeMetricsDecodeError is the regression guard for the
// observability counter that was silently dropped when the CONNECT decode moved
// from Broker.HandleConnection into the protocol adapter: a malformed CONNECT
// must still increment decode_error/decode.
func TestServerHandshakeMetricsDecodeError(t *testing.T) {
	m := newRecordingMetrics()
	srv := NewMQTTServer(config.DefaultConfig(), WithServerMetrics(m))
	srv.SetHandler(&mockHandler{})

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// CONNECT fixed header (0x10) declaring Remaining Length 0: the mandatory
	// protocol name is missing, so the adapter's Decode fails.
	go func() { _, _ = client.Write([]byte{0x10, 0x00}) }()

	if err := srv.handleConn(server); err == nil {
		t.Fatal("handleConn = nil, want a decode error")
	}
	if got := m.rejectionCount("decode_error"); got != 1 {
		t.Errorf("IncRejections(decode_error) = %d, want 1", got)
	}
	if got := m.errorCount("decode"); got != 1 {
		t.Errorf("IncErrors(decode) = %d, want 1", got)
	}
	if got := m.errorCount("deadline"); got != 0 {
		t.Errorf("IncErrors(deadline) = %d, want 0", got)
	}
}

// TestServerHandshakeMetricsDeadlineError verifies a failure to arm the CONNECT
// read deadline is counted as a "deadline" error (and is NOT miscounted as a
// decode error).
func TestServerHandshakeMetricsDeadlineError(t *testing.T) {
	m := newRecordingMetrics()
	srv := NewMQTTServer(config.DefaultConfig(), WithServerMetrics(m))
	srv.SetHandler(&mockHandler{})

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	if err := srv.handleConn(deadlineErrConn{Conn: server}); err == nil {
		t.Fatal("handleConn = nil, want a deadline error")
	}
	if got := m.errorCount("deadline"); got != 1 {
		t.Errorf("IncErrors(deadline) = %d, want 1", got)
	}
	if got := m.rejectionCount("decode_error"); got != 0 {
		t.Errorf("IncRejections(decode_error) = %d, want 0 (deadline failure is not a decode error)", got)
	}
}

// TestServerHandshakeMetricsSuccessNoDecodeError verifies a well-formed CONNECT
// reaches the handler and does NOT touch the decode counters (guarding against
// over-counting).
func TestServerHandshakeMetricsSuccessNoDecodeError(t *testing.T) {
	m := newRecordingMetrics()
	srv := NewMQTTServer(config.DefaultConfig(), WithServerMetrics(m))

	got := make(chan *Conn, 1)
	srv.SetHandler(handlerFunc(func(_ context.Context, c *Conn) error {
		got <- c
		return nil
	}))

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	var buf bytes.Buffer
	if err := protocol.NewCodec(0).Encode(&buf, &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version311,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "srv-metrics-ok",
	}); err != nil {
		t.Fatalf("encode CONNECT: %v", err)
	}
	go func() { _, _ = client.Write(buf.Bytes()) }()

	if err := srv.handleConn(server); err != nil {
		t.Fatalf("handleConn = %v, want nil", err)
	}
	select {
	case c := <-got:
		if c == nil || c.Info == nil || c.Info.ClientID != "srv-metrics-ok" {
			t.Fatalf("handler received %+v, want ClientID srv-metrics-ok", c)
		}
	case <-time.After(time.Second):
		t.Fatal("handler was not invoked for a valid CONNECT")
	}
	if m.rejectionCount("decode_error") != 0 {
		t.Errorf("IncRejections(decode_error) = %d, want 0 for a valid CONNECT", m.rejectionCount("decode_error"))
	}
	if m.errorCount("decode") != 0 {
		t.Errorf("IncErrors(decode) = %d, want 0 for a valid CONNECT", m.errorCount("decode"))
	}
}

// TestServerNilMetricsIsSafe verifies WithServerMetrics(nil) is normalized to a
// no-op collector so a handshake failure cannot panic on a nil interface.
func TestServerNilMetricsIsSafe(t *testing.T) {
	srv := NewMQTTServer(config.DefaultConfig(), WithServerMetrics(nil))
	if srv.metrics == nil {
		t.Fatal("srv.metrics = nil after WithServerMetrics(nil); want a no-op fallback")
	}
	srv.SetHandler(&mockHandler{})

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() { _, _ = client.Write([]byte{0x10, 0x00}) }()

	if err := srv.handleConn(server); err == nil {
		t.Fatal("handleConn = nil, want a decode error (must not panic on nil metrics)")
	}
}
