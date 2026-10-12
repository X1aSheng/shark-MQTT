package broker

// Independent QA verification of the handshake-phase metrics and the
// protocol-neutral *Conn produced by MQTTServer.handleConn, written by the QA
// engineer (Yan). The metric assertions pin both the exact counts and the
// "no double counting / no misrouting" classification the manager ruled on.

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// qaCaptureHandler records the *Conn it is handed so the assembled
// protocol-neutral connection can be inspected.
type qaCaptureHandler struct {
	got chan *Conn
}

func (h *qaCaptureHandler) HandleConnection(_ context.Context, c *Conn) error {
	h.got <- c
	return nil
}

// TestQA_MetricsMalformedConnectNoDoubleCount verifies a CONNECT whose frame
// cannot be decoded increments decode_error/decode exactly once each (never
// twice) and leaves the deadline counter untouched.
func TestQA_MetricsMalformedConnectNoDoubleCount(t *testing.T) {
	m := newRecordingMetrics()
	srv := NewMQTTServer(config.DefaultConfig(), WithServerMetrics(m))
	srv.SetHandler(&mockHandler{})

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// 0x10 = CONNECT, Remaining Length 0 => mandatory protocol name missing.
	go func() { _, _ = client.Write([]byte{0x10, 0x00}) }()

	if err := srv.handleConn(server); err == nil {
		t.Fatal("handleConn = nil, want a decode error")
	}
	if got := m.rejectionCount("decode_error"); got != 1 {
		t.Errorf("decode_error = %d, want exactly 1", got)
	}
	if got := m.errorCount("decode"); got != 1 {
		t.Errorf("decode = %d, want exactly 1", got)
	}
	if got := m.errorCount("deadline"); got != 0 {
		t.Errorf("deadline = %d, want 0", got)
	}
}

// TestQA_MetricsSelectFailureMisclassified is the evidence for the manager's
// ruling that Registry.Select failures count as decode_error: it proves the
// classification AND that a non-MQTT probe (e.g. an HTTP GET / port scanner
// hitting the MQTT port) is counted as decode_error despite never being a
// CONNECT. This is an observability precision issue, not a functional bug.
func TestQA_MetricsSelectFailureMisclassified(t *testing.T) {
	m := newRecordingMetrics()
	srv := NewMQTTServer(config.DefaultConfig(), WithServerMetrics(m))
	srv.SetHandler(&mockHandler{})

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// "GE" — the first two bytes of an HTTP request. No adapter claims it.
	go func() { _, _ = client.Write([]byte("GET / HTTP/1.1\r\n\r\n")) }()

	if err := srv.handleConn(server); err == nil {
		t.Fatal("handleConn = nil, want a no-matching-adapter error")
	}
	if got := m.rejectionCount("decode_error"); got != 1 {
		t.Errorf("decode_error = %d, want 1 (Select failure classified as decode_error)", got)
	}
	if got := m.errorCount("decode"); got != 1 {
		t.Errorf("decode = %d, want 1", got)
	}
	if got := m.errorCount("deadline"); got != 0 {
		t.Errorf("deadline = %d, want 0", got)
	}
}

// TestQA_MetricsNoBytesIsReapedByDeadline verifies a client that connects but
// never sends a single byte does not hang: the armed CONNECT deadline trips,
// handleConn returns promptly, and the failure is counted as a decode error
// (item 13). The deadline counter itself is reserved for the "cannot arm the
// deadline" path.
func TestQA_MetricsNoBytesIsReapedByDeadline(t *testing.T) {
	m := newRecordingMetrics()
	srv := NewMQTTServer(config.DefaultConfig(), WithServerMetrics(m))
	srv.SetHandler(&mockHandler{})
	srv.connectTimeout = 200 * time.Millisecond

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	// No write from the client at all.

	start := time.Now()
	err := srv.handleConn(server)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("handleConn = nil, want a deadline error (no bytes sent)")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("handleConn took %v; a silent client was not reaped promptly", elapsed)
	}
	if got := m.rejectionCount("decode_error"); got != 1 {
		t.Errorf("decode_error = %d, want 1 (silent client surfaces as a decode failure)", got)
	}
	if got := m.errorCount("deadline"); got != 0 {
		t.Errorf("deadline = %d, want 0 (this path is decode_error, not deadline)", got)
	}
}

// TestQA_MetricsDeadlineArmFailure verifies the genuine "cannot arm deadline"
// path is counted as deadline and NOT as decode_error.
func TestQA_MetricsDeadlineArmFailure(t *testing.T) {
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
		t.Errorf("deadline = %d, want 1", got)
	}
	if got := m.rejectionCount("decode_error"); got != 0 {
		t.Errorf("decode_error = %d, want 0", got)
	}
}

// TestQA_ConnSpecMatchesVersion confirms the assembled *Conn carries a Spec
// consistent with the negotiated version, and that a valid connect leaves all
// three handshake counters at zero.
func TestQA_ConnSpecMatchesVersion(t *testing.T) {
	cases := []struct {
		name    string
		proto   string
		version byte
	}{
		{"3.1", protocol.ProtocolNameMQIsdp, protocol.Version31},
		{"3.1.1", protocol.ProtocolNameMQTT, protocol.Version311},
		{"5.0", protocol.ProtocolNameMQTT, protocol.Version50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newRecordingMetrics()
			srv := NewMQTTServer(config.DefaultConfig(), WithServerMetrics(m))
			h := &qaCaptureHandler{got: make(chan *Conn, 1)}
			srv.SetHandler(h)

			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()

			var buf bytes.Buffer
			if err := protocol.NewCodec(0).Encode(&buf, &protocol.ConnectPacket{
				FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
				ProtocolName:    tc.proto,
				ProtocolVersion: tc.version,
				Flags:           protocol.ConnectFlags{CleanSession: true},
				KeepAlive:       30,
				ClientID:        "qa-spec-" + tc.name,
			}); err != nil {
				t.Fatalf("encode CONNECT: %v", err)
			}
			go func() { _, _ = client.Write(buf.Bytes()) }()

			if err := srv.handleConn(server); err != nil {
				t.Fatalf("handleConn = %v, want nil", err)
			}
			var c *Conn
			select {
			case c = <-h.got:
			case <-time.After(time.Second):
				t.Fatal("handler was not invoked")
			}
			if c.Info == nil || c.Info.Version != tc.version {
				t.Fatalf("Conn.Info.Version = %+v, want %d", c.Info, tc.version)
			}
			if c.Spec.Version != tc.version {
				t.Errorf("Conn.Spec.Version = %d, want %d (Spec must match Info.Version)", c.Spec.Version, tc.version)
			}
			want, _ := protocol.SpecOf(tc.version)
			if c.Spec != want {
				t.Errorf("Conn.Spec = %+v, want %+v", c.Spec, want)
			}
			if c.Adapter == nil || c.Adapter.Name() != "mqtt" {
				t.Errorf("Conn.Adapter = %v, want mqtt", c.Adapter)
			}
			if c.Raw == nil || c.Codec == nil {
				t.Error("Conn.Raw/Codec must be non-nil")
			}
			if m.rejectionCount("decode_error") != 0 || m.errorCount("decode") != 0 || m.errorCount("deadline") != 0 {
				t.Errorf("valid connect touched handshake counters: decode_error=%d decode=%d deadline=%d",
					m.rejectionCount("decode_error"), m.errorCount("decode"), m.errorCount("deadline"))
			}
		})
	}
}

// TestQA_NilMetricsNeverPanics re-verifies WithServerMetrics(nil) normalizes to
// a usable collector on the failure path.
func TestQA_NilMetricsNeverPanics(t *testing.T) {
	srv := NewMQTTServer(config.DefaultConfig(), WithServerMetrics(nil))
	if srv.metrics == nil {
		t.Fatal("srv.metrics = nil after WithServerMetrics(nil)")
	}
	srv.SetHandler(&mockHandler{})

	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() { _, _ = client.Write([]byte{0x10, 0x00}) }()

	if err := srv.handleConn(server); err == nil {
		t.Fatal("handleConn = nil, want a decode error")
	}
}
