// Package integration provides end-to-end integration tests for shark-mqtt.
// Run with: go test -race -tags=integration ./tests/integration/...
package integration

import (
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// The frames below are hand-built rather than produced by the local codec, so
// the assertion is not checked by the same code that would have to reject them
// (the blindness described in TESTING.md, 缺陷回归测试). Each one is also
// deliberately consumed to its last byte by the broker's decoder.
//
// That last property is not cosmetic. The decoder rejects these packets without
// reading the remainder of the frame, and any byte left in the socket receive
// buffer turns the subsequent close into an RST, which can discard the
// DISCONNECT that was just written. Frames consumed exactly to the last byte
// keep the close a normal FIN, so "the DISCONNECT arrives" is deterministic
// here instead of a matter of timing.
var (
	// PUBLISH on topic "t" whose property section carries Message Expiry
	// Interval (0x02) twice. §3.3.2.3.3 spells the second occurrence out as a
	// Protocol Error. Property length 6 = 02 00000005 followed by the repeated
	// identifier 02, so the duplicate is detected on the frame's final byte.
	rawDuplicateProperty = []byte{0x30, 0x0A, 0x00, 0x01, 't', 0x06, 0x02, 0x00, 0x00, 0x00, 0x05, 0x02}

	// PINGREQ declaring a 1-byte body. §2.2.1 requires Remaining Length 0
	// ([MQTT-2.2.1-1]), so this is a Malformed Packet. Only the two header
	// bytes exist, so the decoder reads the whole frame before rejecting it.
	rawMalformedPingReq = []byte{0xC0, 0x01}

	// PINGREQ with the DUP bit set. The reserved nibble of every packet type
	// other than PUBLISH is 0b0000 (§2.1.3), so the fixed header itself is
	// malformed; §2.2.2.2 answers it with 0x81.
	rawBadFixedHeader = []byte{0xC8, 0x00}

	// DISCONNECT carrying Topic Alias (0x23). Table 2-4 does not list it for
	// DISCONNECT, and §2.2.2.2 makes an Identifier that is not valid for its
	// packet type a Malformed Packet. Property length 1 puts the offending
	// identifier on the frame's final byte.
	rawPropertyWrongLocus = []byte{0xE0, 0x03, 0x00, 0x01, 0x23}

	// PUBLISH declaring a 200-byte body that is never sent. The size check runs
	// against Remaining Length before the body is read, so this is an oversized
	// packet (0x95) with the receive buffer left empty.
	rawOversizePublish = []byte{0x30, 0xC8, 0x01}
)

// expectCloseWithoutDisconnect asserts the broker closed the connection without
// first writing anything. It is the negative half of every test below: without
// it a change that sent a DISCONNECT unconditionally would still pass.
func expectCloseWithoutDisconnect(t *testing.T, conn net.Conn) {
	t.Helper()

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 8)
	n, err := conn.Read(buf)
	if n != 0 {
		t.Fatalf("expected no packet before the close, read % X", buf[:n])
	}
	if err == nil {
		t.Fatal("expected the connection to be closed")
	}
}

// testBrokerWithMaxPacketSize starts a broker whose codec caps the accepted
// packet size. Both the handshake codec and the broker codec read
// config.MaxPacketSize, so one setting covers the CONNECT and the read loop.
func testBrokerWithMaxPacketSize(t *testing.T, n int) *api.Broker {
	t.Helper()

	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	cfg.MaxPacketSize = n

	b := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("failed to start broker: %v", err)
	}
	t.Cleanup(func() { b.Stop() })
	return b
}

// TestDecodeErrorSendsDisconnect covers the decode-failure branch of readLoop,
// which used to be silent: the read failed, no packet was produced, and the
// peer saw a bare close indistinguishable from a network fault. MQTT 5.0
// §4.13.1 asks for a DISCONNECT carrying the reason code first, and the code
// now maps each decode sentinel onto one.
func TestDecodeErrorSendsDisconnect(t *testing.T) {
	cases := []struct {
		name   string
		frame  []byte
		reason byte
	}{
		{"repeated at-most-once property", rawDuplicateProperty, protocol.ReasonCodeProtocolError},
		{"malformed remaining length", rawMalformedPingReq, protocol.ReasonCodeMalformedPacket},
		{"reserved fixed-header nibble", rawBadFixedHeader, protocol.ReasonCodeMalformedPacket},
		{"property not valid for packet type", rawPropertyWrongLocus, protocol.ReasonCodeMalformedPacket},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := testBroker(t)

			conn := dialTestBroker(t, b)
			codec := protocol.NewCodec(0)
			connectClient(t, conn, codec, "decode-error-"+tc.name)

			conn.SetDeadline(time.Now().Add(2 * time.Second))
			if _, err := conn.Write(tc.frame); err != nil {
				t.Fatalf("write frame: %v", err)
			}

			expectDisconnectThenClose(t, conn, tc.reason)
		})
	}
}

// TestOversizedPacketSendsPacketTooLarge covers the one sentinel that maps to a
// code of its own. §4.13.1 says to use 0x81/0x82 "unless a more specific Reason
// Code has been defined", and Maximum Packet Size has one (0x95, §3.1.2.11).
func TestOversizedPacketSendsPacketTooLarge(t *testing.T) {
	b := testBrokerWithMaxPacketSize(t, 128)

	conn := dialTestBroker(t, b)
	codec := protocol.NewCodec(0)
	connectClient(t, conn, codec, "oversize-client")

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(rawOversizePublish); err != nil {
		t.Fatalf("write oversize frame: %v", err)
	}

	expectDisconnectThenClose(t, conn, protocol.ReasonCodePacketTooLarge)
}

// TestDecodeErrorStillFiresWill is the other half of the teardown contract. A
// server-sent DISCONNECT must not be mistaken for a graceful close: the client
// never sent DISCONNECT 0x00, so the will still fires (§3.1.2.5, §3.14.4). The
// pre-existing DISCONNECT paths all route through abnormalDisconnect; this pins
// the new one to the same behaviour.
func TestDecodeErrorStillFiresWill(t *testing.T) {
	b := testBroker(t)

	watcher := dialTestBroker(t, b)
	watcherCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, watcher, watcherCodec, "will-watcher-decode", "decode/will", 0)

	conn := dialTestBroker(t, b)
	codec := protocol.NewCodec(0)
	connectV50WithWill(t, conn, codec, "will-owner-decode", "decode/will", "gone")

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(rawMalformedPingReq); err != nil {
		t.Fatalf("write malformed frame: %v", err)
	}

	expectDisconnectThenClose(t, conn, protocol.ReasonCodeMalformedPacket)

	watcher.SetReadDeadline(time.Now().Add(3 * time.Second))
	got, err := watcherCodec.Decode(watcher)
	if err != nil {
		t.Fatalf("expected the will PUBLISH: %v", err)
	}
	pub, ok := got.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("will = %T, want *protocol.PublishPacket", got)
	}
	if pub.Topic != "decode/will" || string(pub.Payload) != "gone" {
		t.Fatalf("will = %q %q, want %q %q", pub.Topic, pub.Payload, "decode/will", "gone")
	}
}

// TestMQTT311DecodeErrorClosesWithoutDisconnect is the negative control for the
// ServerDisconnect gate: MQTT 3.1.1 has no server-to-client DISCONNECT, so the
// same malformed frame must produce a bare close.
func TestMQTT311DecodeErrorClosesWithoutDisconnect(t *testing.T) {
	b := testBroker(t)

	conn := dialTestBroker(t, b)
	codec := protocol.NewCodecForVersion(0, protocol.Version311)
	connectV311(t, conn, codec, "decode-error-311")

	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(rawMalformedPingReq); err != nil {
		t.Fatalf("write malformed frame: %v", err)
	}

	expectCloseWithoutDisconnect(t, conn)
}

// TestTransportErrorClosesWithoutDisconnect pins the other side of the mapping:
// a transport-level failure carries no protocol judgement to report, so the
// teardown stays silent. A half-close is the cheapest way to produce one -- the
// broker's read returns io.EOF, which is not one of the decode sentinels.
func TestTransportErrorClosesWithoutDisconnect(t *testing.T) {
	b := testBroker(t)

	conn := dialTestBroker(t, b)
	codec := protocol.NewCodec(0)
	connectClient(t, conn, codec, "transport-close-client")

	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		t.Fatalf("dialed conn is %T, want *net.TCPConn", conn)
	}
	if err := tcp.CloseWrite(); err != nil {
		t.Fatalf("CloseWrite: %v", err)
	}

	expectCloseWithoutDisconnect(t, conn)
}
