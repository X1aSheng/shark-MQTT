package integration

// Independent QA verification of MQTT 3.1 semantics and 3.1.1/5.0
// non-regression, written by the QA engineer (Yan). Where the engineer's suite
// inspects decoded structs, these tests additionally read the raw CONNACK wire
// bytes and drive the malformed/oversized/name-mismatch paths end-to-end.

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/pkg/metrics"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// qaRecordingMetrics records the rejection/error counters reachable from a real
// broker (server handshake + broker core share one collector via api.WithMetrics).
type qaRecordingMetrics struct {
	metrics.Metrics
	mu         sync.Mutex
	rejections map[string]int
	errs       map[string]int
}

func newQARecordingMetrics() *qaRecordingMetrics {
	return &qaRecordingMetrics{
		Metrics:    metrics.DefaultMetrics(),
		rejections: make(map[string]int),
		errs:       make(map[string]int),
	}
}

func (m *qaRecordingMetrics) IncRejections(reason string) {
	m.mu.Lock()
	m.rejections[reason]++
	m.mu.Unlock()
}

func (m *qaRecordingMetrics) IncErrors(component string) {
	m.mu.Lock()
	m.errs[component]++
	m.mu.Unlock()
}

func (m *qaRecordingMetrics) rejectionCount(reason string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rejections[reason]
}

func (m *qaRecordingMetrics) errorCount(component string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.errs[component]
}

// qaConnectBytes encodes a CONNECT packet to raw wire bytes.
func qaConnectBytes(t *testing.T, protoName string, version byte, clientID string, clean bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protoName,
		ProtocolVersion: version,
		Flags:           protocol.ConnectFlags{CleanSession: clean},
		KeepAlive:       30,
		ClientID:        clientID,
	}
	if err := protocol.NewCodec(0).Encode(&buf, pkt); err != nil {
		t.Fatalf("encode CONNECT: %v", err)
	}
	return buf.Bytes()
}

// qaRawConnect writes a CONNECT and returns the CONNACK's raw session-present
// byte and reason byte (read straight off the wire, not via the decoder).
func qaRawConnect(t *testing.T, conn net.Conn, protoName string, version byte, clientID string, clean bool) (spByte, reason byte) {
	t.Helper()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(qaConnectBytes(t, protoName, version, clientID, clean)); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	sp, rc, ok := qaReadRawConnAck(t, conn)
	if !ok {
		t.Fatal("no CONNACK received")
	}
	return sp, rc
}

// qaReadRawConnAck reads a CONNACK off the wire. Returns ok=false when the
// server closed without answering (expected on handshake rejection paths).
func qaReadRawConnAck(t *testing.T, conn net.Conn) (spByte, reason byte, ok bool) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		return 0, 0, false
	}
	if hdr[0] != 0x20 {
		t.Fatalf("response first byte = 0x%02X, want 0x20 (CONNACK)", hdr[0])
	}
	rl := int(hdr[1])
	if rl < 2 {
		t.Fatalf("CONNACK RemainingLength = %d, want >= 2", rl)
	}
	body := make([]byte, rl)
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatalf("read CONNACK body: %v", err)
	}
	return body[0], body[1], true
}

// qaSubscribe subscribes to one filter and returns the SUBACK.
func qaSubscribe(t *testing.T, conn net.Conn, codec *protocol.Codec, packetID uint16, filter string, qos byte) *protocol.SubAckPacket {
	t.Helper()
	sub := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    packetID,
		Topics:      []protocol.TopicFilter{{Topic: filter, QoS: qos}},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, sub); err != nil {
		t.Fatalf("encode SUBSCRIBE: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("decode SUBACK: %v", err)
	}
	sa, ok := pkt.(*protocol.SubAckPacket)
	if !ok {
		t.Fatalf("expected SUBACK, got %T", pkt)
	}
	return sa
}

// qaBroker builds a broker with explicit config/auth/authorizer and a connect
// timeout, for the error-path and ACL tests.
func qaBroker(t *testing.T, mutate func(*config.Config), opts ...api.Option) *api.Broker {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	if mutate != nil {
		mutate(cfg)
	}
	all := append([]api.Option{api.WithConfig(cfg), api.WithAuth(broker.AllowAllAuth{})}, opts...)
	b := api.NewBroker(all...)
	if err := b.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(b.Stop)
	return b
}

// qaDenySubscribe allows authentication/publish but denies every subscription,
// so the SUBACK failure code path is exercised via the ACL (not an invalid
// filter).
type qaDenySubscribe struct{}

func (qaDenySubscribe) CanPublish(context.Context, string, string) bool   { return true }
func (qaDenySubscribe) CanSubscribe(context.Context, string, string) bool { return false }

// ---------------------------------------------------------------------------
// A. MQTT 3.1 semantics
// ---------------------------------------------------------------------------

// TestQA_3_1_ClientIDBoundary proves the 23/24 boundary for 3.1 and, crucially,
// that 3.1.1 and 5.0 are NOT affected by the 23-char cap.
func TestQA_3_1_ClientIDBoundary(t *testing.T) {
	brk := testBroker(t)

	connect := func(protoName string, version byte, id string) *protocol.ConnAckPacket {
		t.Helper()
		conn := dialTestBroker(t, brk)
		codec := protocol.NewCodec(0)
		ca, err := connectVersion(t, conn, codec, protoName, version, id, true)
		if err != nil {
			t.Fatalf("%s id len %d: %v", protoName, len(id), err)
		}
		conn.Close()
		return ca
	}

	if ca := connect(protocol.ProtocolNameMQIsdp, protocol.Version31, strings.Repeat("a", 23)); ca.ReasonCode != protocol.ConnAckAccepted {
		t.Errorf("3.1 23-byte ID = 0x%02X, want 0x00 (accepted)", ca.ReasonCode)
	}
	if ca := connect(protocol.ProtocolNameMQIsdp, protocol.Version31, strings.Repeat("b", 24)); ca.ReasonCode != protocol.ConnAckIdentifierRejected {
		t.Errorf("3.1 24-byte ID = 0x%02X, want 0x02 (Identifier Rejected)", ca.ReasonCode)
	}
	// Control: the strict versions must still accept long IDs. Length is kept
	// at 100 ( > 23, <= the broker's 128-byte server-side cap) so this proves
	// the absence of the 3.1 23-char rule and not some other limit.
	if ca := connect(protocol.ProtocolNameMQTT, protocol.Version311, strings.Repeat("c", 100)); ca.ReasonCode != protocol.ConnAckAccepted {
		t.Errorf("3.1.1 100-byte ID = 0x%02X, want 0x00 (accepted)", ca.ReasonCode)
	}
	if ca := connect(protocol.ProtocolNameMQTT, protocol.Version50, strings.Repeat("d", 100)); ca.ReasonCode != protocol.ConnAckAccepted {
		t.Errorf("5.0 100-byte ID = 0x%02X, want 0x00 (accepted)", ca.ReasonCode)
	}
}

// TestQA_3_1_EmptyClientID proves 3.1 rejects an empty ClientID while 3.1.1 and
// 5.0 accept it with a clean session/start.
func TestQA_3_1_EmptyClientID(t *testing.T) {
	brk := testBroker(t)

	connect := func(protoName string, version byte, clean bool) *protocol.ConnAckPacket {
		t.Helper()
		conn := dialTestBroker(t, brk)
		codec := protocol.NewCodec(0)
		ca, err := connectVersion(t, conn, codec, protoName, version, "", clean)
		if err != nil {
			t.Fatalf("%s empty id: %v", protoName, err)
		}
		conn.Close()
		return ca
	}

	if ca := connect(protocol.ProtocolNameMQIsdp, protocol.Version31, true); ca.ReasonCode != protocol.ConnAckIdentifierRejected {
		t.Errorf("3.1 empty ID (clean) = 0x%02X, want 0x02", ca.ReasonCode)
	}
	if ca := connect(protocol.ProtocolNameMQIsdp, protocol.Version31, false); ca.ReasonCode != protocol.ConnAckIdentifierRejected {
		t.Errorf("3.1 empty ID (non-clean) = 0x%02X, want 0x02", ca.ReasonCode)
	}
	if ca := connect(protocol.ProtocolNameMQTT, protocol.Version311, true); ca.ReasonCode != protocol.ConnAckAccepted {
		t.Errorf("3.1.1 empty ID + clean = 0x%02X, want 0x00 (must not be broken)", ca.ReasonCode)
	}
	if ca := connect(protocol.ProtocolNameMQTT, protocol.Version50, true); ca.ReasonCode != protocol.ConnAckAccepted {
		t.Errorf("5.0 empty ID + clean = 0x%02X, want 0x00 (must not be broken)", ca.ReasonCode)
	}
}

// TestQA_3_1_SessionPresentZeroOnRealResume proves the CONNACK reserved byte is
// 0 on the raw wire for a 3.1 session restore, and uses an identical 3.1.1 flow
// whose raw SP byte is 1 — which is the evidence that the store-restore resume
// branch actually executed (SP is only ever set from isResuming).
func TestQA_3_1_SessionPresentZeroOnRealResume(t *testing.T) {
	brk := testBroker(t)

	// --- 3.1: persistent session, reconnect, functional resume proof. ---
	const id31 = "qa31-resume"
	c1 := dialTestBroker(t, brk)
	sp1, rc1 := qaRawConnect(t, c1, protocol.ProtocolNameMQIsdp, protocol.Version31, id31, false)
	if rc1 != protocol.ConnAckAccepted {
		t.Fatalf("3.1 first CONNACK reason = 0x%02X, want 0x00", rc1)
	}
	if sp1 != 0 {
		t.Errorf("3.1 first CONNACK SP byte = 0x%02X, want 0x00", sp1)
	}
	c1Codec := protocol.NewCodec(0)
	if sa := qaSubscribe(t, c1, c1Codec, 1, "qa31/resume/topic", 0); sa.ReasonCodes[0] != protocol.ReasonCodeGrantedQoS0 {
		t.Fatalf("3.1 subscribe code = 0x%02X, want 0x00", sa.ReasonCodes[0])
	}
	c1.Close()
	time.Sleep(200 * time.Millisecond)

	c2 := dialTestBroker(t, brk)
	sp2, rc2 := qaRawConnect(t, c2, protocol.ProtocolNameMQIsdp, protocol.Version31, id31, false)
	if rc2 != protocol.ConnAckAccepted {
		t.Fatalf("3.1 reconnect CONNACK reason = 0x%02X, want 0x00", rc2)
	}
	if sp2 != 0 {
		t.Errorf("3.1 reconnect CONNACK SP byte = 0x%02X, want 0x00 (3.1 has no Session Present flag)", sp2)
	}
	// Functional proof the session survived: a publish must reach the
	// reconnected client through its restored subscription.
	pub := dialTestBroker(t, brk)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pub, pubCodec, "qa31-resume-pub")
	pub.SetDeadline(time.Now().Add(2 * time.Second))
	if err := pubCodec.Encode(pub, &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish, QoS: 0},
		Topic:       "qa31/resume/topic",
		Payload:     []byte("after-resume"),
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	c2Codec := protocol.NewCodec(0)
	c2.SetReadDeadline(time.Now().Add(2 * time.Second))
	pkt, err := c2Codec.Decode(c2)
	if err != nil {
		t.Fatalf("reconnected 3.1 client did not receive the message (session not resumed): %v", err)
	}
	if p, ok := pkt.(*protocol.PublishPacket); !ok || string(p.Payload) != "after-resume" {
		t.Fatalf("reconnected 3.1 client got %T %v, want PUBLISH after-resume", pkt, pkt)
	}

	// --- 3.1.1 control: identical flow, raw SP byte must be 1. ---
	const id311 = "qa311-resume"
	d1 := dialTestBroker(t, brk)
	if _, rc := qaRawConnect(t, d1, protocol.ProtocolNameMQTT, protocol.Version311, id311, false); rc != protocol.ConnAckAccepted {
		t.Fatalf("3.1.1 first CONNACK reason = 0x%02X, want 0x00", rc)
	}
	d1.Close()
	time.Sleep(200 * time.Millisecond)

	d2 := dialTestBroker(t, brk)
	sp311, rc311 := qaRawConnect(t, d2, protocol.ProtocolNameMQTT, protocol.Version311, id311, false)
	if rc311 != protocol.ConnAckAccepted {
		t.Fatalf("3.1.1 reconnect CONNACK reason = 0x%02X, want 0x00", rc311)
	}
	if sp311 != 1 {
		t.Fatalf("3.1.1 reconnect CONNACK SP byte = 0x%02X, want 0x01 — if this is 0 the resume branch did NOT run and the 3.1 assertion above is vacuous", sp311)
	}
}

// TestQA_3_1_SubAckNoFailureCodeUnderACL drives the ACL-denied subscribe path:
// 3.1 falls back to granted QoS 0, 3.1.1 still returns 0x80.
func TestQA_3_1_SubAckNoFailureCodeUnderACL(t *testing.T) {
	brk := qaBroker(t, nil, api.WithAuthorizer(qaDenySubscribe{}))

	check := func(protoName string, version byte, want byte) {
		t.Helper()
		conn := dialTestBroker(t, brk)
		codec := protocol.NewCodec(0)
		if ca, err := connectVersion(t, conn, codec, protoName, version, "qa-acl-"+protoName, true); err != nil {
			t.Fatalf("connect: %v", err)
		} else if ca.ReasonCode != protocol.ConnAckAccepted {
			t.Fatalf("CONNACK = 0x%02X, want 0x00", ca.ReasonCode)
		}
		sa := qaSubscribe(t, conn, codec, 1, "qa/acl/denied", 1)
		if len(sa.ReasonCodes) != 1 {
			t.Fatalf("SUBACK codes = %v, want 1 entry", sa.ReasonCodes)
		}
		if sa.ReasonCodes[0] != want {
			t.Errorf("%s ACL-denied SUBACK = 0x%02X, want 0x%02X", protoName, sa.ReasonCodes[0], want)
		}
		conn.Close()
	}

	check(protocol.ProtocolNameMQIsdp, protocol.Version31, protocol.ReasonCodeGrantedQoS0) // 0x00
	check(protocol.ProtocolNameMQTT, protocol.Version311, protocol.SubAckFailure)          // 0x80
}

// TestQA_3_1_1_MalformedFilterTearsDown is the explicit control that the
// malformed-filter teardown is not gated to MQTT 5.0: §4.13.1
// [MQTT-4.13.1-1] applies to 3.1.1 as well, where the only difference is that
// no DISCONNECT can be sent.
//
// It replaces a control that drove the invalid-filter path and expected a
// SUBACK 0x80. That path no longer exists -- a malformed filter is a Protocol
// Error, not a per-filter refusal -- and the 3.1 / 3.1.1 SUBACK failure-code
// difference it was guarding is covered through the ACL by
// TestQA_3_1_SubAckNoFailureCodeUnderACL.
func TestQA_3_1_1_MalformedFilterTearsDown(t *testing.T) {
	brk := testBroker(t)
	conn := dialTestBroker(t, brk)
	defer conn.Close()
	codec := protocol.NewCodec(0)
	if _, err := connectVersion(t, conn, codec, protocol.ProtocolNameMQTT, protocol.Version311, "qa311-malformed-filter", true); err != nil {
		t.Fatalf("connect: %v", err)
	}

	sub := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "a/#/b", QoS: 0}},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, sub); err != nil {
		t.Fatalf("encode SUBSCRIBE: %v", err)
	}

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if pkt, err := codec.Decode(conn); err == nil {
		t.Fatalf("3.1.1 answered the malformed filter with %T; the connection must be closed instead, "+
			"with no SUBACK and no DISCONNECT", pkt)
	}
}

// ---------------------------------------------------------------------------
// D. Error paths / boundaries
// ---------------------------------------------------------------------------

// TestQA_ErrorPaths_Handshake drives the malformed, name/version-mismatch and
// oversized CONNECT paths.
func TestQA_ErrorPaths_Handshake(t *testing.T) {
	t.Run("remaining_length_zero", func(t *testing.T) {
		brk := qaBroker(t, func(c *config.Config) { c.ConnectTimeout = config.Duration(500 * time.Millisecond) })
		conn := dialTestBroker(t, brk)
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write([]byte{0x10, 0x00}); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, _, ok := qaReadRawConnAck(t, conn); ok {
			t.Error("server answered a CONNACK for a malformed CONNECT; want connection closed")
		}
	})

	t.Run("truncated_packet", func(t *testing.T) {
		brk := qaBroker(t, func(c *config.Config) { c.ConnectTimeout = config.Duration(500 * time.Millisecond) })
		conn := dialTestBroker(t, brk)
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		// Declares 10 payload bytes but only supplies 2, then stalls.
		if _, err := conn.Write([]byte{0x10, 0x0A, 0x00, 0x04}); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, _, ok := qaReadRawConnAck(t, conn); ok {
			t.Error("server answered a CONNACK for a truncated CONNECT; want connection closed")
		}
	})

	t.Run("oversized_packet", func(t *testing.T) {
		brk := qaBroker(t, func(c *config.Config) { c.MaxPacketSize = 64 })
		conn := dialTestBroker(t, brk)
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write(qaConnectBytes(t, protocol.ProtocolNameMQTT, protocol.Version311, strings.Repeat("x", 500), true)); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, _, ok := qaReadRawConnAck(t, conn); ok {
			t.Error("server answered a CONNACK for an oversized CONNECT; want connection closed")
		}
	})

	t.Run("name_version_mismatch", func(t *testing.T) {
		brk := testBroker(t)
		cases := []struct {
			name    string
			proto   string
			version byte
		}{
			{"MQIsdp+4", protocol.ProtocolNameMQIsdp, protocol.Version311},
			{"MQTT+3", protocol.ProtocolNameMQTT, protocol.Version31},
			{"MQTT+6", protocol.ProtocolNameMQTT, 6},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				conn := dialTestBroker(t, brk)
				codec := protocol.NewCodec(0)
				ca, err := connectVersion(t, conn, codec, tc.proto, tc.version, "qa-mismatch", true)
				if err != nil {
					t.Fatalf("decode CONNACK: %v", err)
				}
				if ca.ReasonCode != protocol.ConnAckUnacceptableProtocol {
					t.Errorf("%s CONNACK = 0x%02X, want 0x01 (Unacceptable Protocol Version)", tc.name, ca.ReasonCode)
				}
				conn.Close()
			})
		}
	})
}

// TestQA_MetricsValidateConnectFailureIsNotDecodeError proves the wire-decode
// counter is reserved for frame-decode failures: a CONNECT that decodes cleanly
// but fails semantic validation must be counted as invalid_connect and must NOT
// touch decode_error (guarding the "no double counting / no misrouting"
// requirement).
func TestQA_MetricsValidateConnectFailureIsNotDecodeError(t *testing.T) {
	m := newQARecordingMetrics()
	brk := qaBroker(t, nil, api.WithMetrics(m))

	conn := dialTestBroker(t, brk)
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	// "FOO" is a syntactically valid frame (decodes fine) but an invalid
	// protocol name, so the rejection comes from broker-side validation.
	if _, err := conn.Write(qaConnectBytes(t, "FOO", protocol.Version311, "qa-badname", true)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, rc, ok := qaReadRawConnAck(t, conn)
	if !ok {
		t.Fatal("no CONNACK for an invalid protocol name")
	}
	if rc != protocol.ConnAckUnacceptableProtocol {
		t.Errorf("CONNACK reason = 0x%02X, want 0x01", rc)
	}
	if got := m.rejectionCount("decode_error"); got != 0 {
		t.Errorf("decode_error = %d, want 0 (a semantic rejection is not a decode error)", got)
	}
	if got := m.errorCount("decode"); got != 0 {
		t.Errorf("decode = %d, want 0", got)
	}
	if got := m.rejectionCount("invalid_connect"); got != 1 {
		t.Errorf("invalid_connect = %d, want 1", got)
	}
}

// TestQA_MetricsMalformedConnectCountedDecodeError is the end-to-end
// counterpart: a malformed CONNECT (frame cannot be decoded) increments
// decode_error exactly once and leaves invalid_connect untouched.
func TestQA_MetricsMalformedConnectCountedDecodeError(t *testing.T) {
	m := newQARecordingMetrics()
	brk := qaBroker(t, func(c *config.Config) { c.ConnectTimeout = config.Duration(500 * time.Millisecond) }, api.WithMetrics(m))

	conn := dialTestBroker(t, brk)
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte{0x10, 0x00}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, ok := qaReadRawConnAck(t, conn); ok {
		t.Error("malformed CONNECT produced a CONNACK")
	}
	if got := m.rejectionCount("decode_error"); got != 1 {
		t.Errorf("decode_error = %d, want exactly 1", got)
	}
	if got := m.errorCount("decode"); got != 1 {
		t.Errorf("decode = %d, want exactly 1", got)
	}
	if got := m.rejectionCount("invalid_connect"); got != 0 {
		t.Errorf("invalid_connect = %d, want 0 (frame never decoded)", got)
	}
}
