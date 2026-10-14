// Package integration provides end-to-end integration tests for shark-mqtt.
// Run with: go test -race -tags=integration ./tests/integration/...
package integration

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// connackRaw is the exact wire form of an MQTT 5 CONNACK carrying only a reason
// code: fixed header 0x20, Remaining Length 3, session present 0, the reason
// code, then the property section. The trailing 0x00 is not padding -- a v5
// CONNACK always has a property section, and with no properties that section is
// the single length byte 0. Asserting the raw bytes rather than a decoded
// CONNACK is deliberate: decoding would be checked by the same codec that would
// have to reject the frame (see TESTING.md, 缺陷回归测试).
func connackRaw(reason byte) []byte {
	return []byte{0x20, 0x03, 0x00, reason, 0x00}
}

var (
	// A v5 CONNECT whose property section carries Session Expiry Interval (0x11)
	// twice; §3.1.2.11.2 makes the repeat a Protocol Error. Unlike the
	// post-CONNECT frames, this one needs no care about leftover bytes:
	// decodeConnect reads the whole declared body into memory before parsing it,
	// so the socket is drained whatever the verdict.
	rawDuplicatePropertyConnect = []byte{
		// CONNECT, 27 bytes
		0x10, 0x1B,
		// protocol name "MQTT", version 5.0, clean start, keep alive 30
		0x00, 0x04, 'M', 'Q', 'T', 'T', 0x05, 0x02, 0x00, 0x1E,
		// property length 10: Session Expiry Interval, then the repeat
		0x0A,
		0x11, 0x00, 0x00, 0x00, 0x05,
		0x11, 0x00, 0x00, 0x00, 0x05,
		// client id "test"
		0x00, 0x04, 't', 'e', 's', 't',
	}

	// A v5 CONNECT carrying Topic Alias (0x23), which Table 2-4 does not list for
	// CONNECT: a Malformed Packet per §2.2.2.2, and so a CONNACK 0x81 rather
	// than 0x82.
	rawConnectPropertyWrongLocus = []byte{
		// CONNECT, 20 bytes
		0x10, 0x14,
		// protocol name "MQTT", version 5.0, clean start, keep alive 30
		0x00, 0x04, 'M', 'Q', 'T', 'T', 0x05, 0x02, 0x00, 0x1E,
		// property length 3: Topic Alias
		0x03,
		0x23, 0x00, 0x01,
		// client id "test"
		0x00, 0x04, 't', 'e', 's', 't',
	}

	// A structurally valid 3.1.1 CONNECT with one byte of trailing garbage in the
	// payload, which decodeConnect rejects as a Malformed Packet. The version
	// byte says 4, so the codec's spec is 3.1.1 and no CONNACK may be answered.
	rawTrailingGarbageConnect311 = []byte{
		// CONNECT, 17 bytes
		0x10, 0x11,
		// protocol name "MQTT", version 3.1.1, clean session, keep alive 30
		0x00, 0x04, 'M', 'Q', 'T', 'T', 0x04, 0x02, 0x00, 0x1E,
		// client id "test", then a byte no field accounts for
		0x00, 0x04, 't', 'e', 's', 't', 0xFF,
	}

	// A CONNECT that fails before the version byte is ever read: the protocol
	// name length claims a body the frame does not have. The codec still reports
	// its 3.1.1 default, so the version gate stays closed.
	rawTruncatedNameConnect = []byte{0x10, 0x01, 0x00}
)

// TestConnectDecodeErrorSendsConnAck covers the pre-CONNECT CONNACK. §4.13.1
// allows the server to answer an error in a CONNECT packet with a CONNACK
// carrying the reason code, and it is the only chance to say anything: the
// failure produces no session, so nothing else can carry the diagnosis.
func TestConnectDecodeErrorSendsConnAck(t *testing.T) {
	cases := []struct {
		name   string
		frame  []byte
		reason byte
	}{
		{"repeated at-most-once property", rawDuplicatePropertyConnect, protocol.ReasonCodeProtocolError},
		{"property not valid for CONNECT", rawConnectPropertyWrongLocus, protocol.ReasonCodeMalformedPacket},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := testBroker(t)

			conn := dialTestBroker(t, b)
			conn.SetDeadline(time.Now().Add(2 * time.Second))
			if _, err := conn.Write(tc.frame); err != nil {
				t.Fatalf("write CONNECT: %v", err)
			}

			want := connackRaw(tc.reason)
			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			got := make([]byte, len(want))
			if _, err := io.ReadFull(conn, got); err != nil {
				t.Fatalf("expected a CONNACK 0x%02X, got %v", tc.reason, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("CONNACK = % X, want % X", got, want)
			}

			conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			if n, err := conn.Read(make([]byte, 8)); err == nil {
				t.Errorf("connection still open after the rejection CONNACK: read %d more bytes", n)
			}
		})
	}
}

// TestConnectDecodeErrorSilentWithoutV5Version pins the narrow side of the
// change. A CONNACK is only answered when the codec reports a version whose
// CONNACK carries a reason code, which -- given that Handshake returns the codec
// even on failure and decodeConnect adopts the declared version as soon as it
// reads that byte -- means "the peer said 5.0 before the frame went wrong".
//
// Without this case a change that answered every failed CONNECT with a v5-shaped
// CONNACK would still pass, and a 3.1.1 client would receive a frame it cannot
// parse. The behaviour here is unchanged from before the CONNACK existed, which
// is exactly the point.
func TestConnectDecodeErrorSilentWithoutV5Version(t *testing.T) {
	cases := []struct {
		name  string
		frame []byte
	}{
		{"3.1.1 malformed payload", rawTrailingGarbageConnect311},
		{"failure before the version byte", rawTruncatedNameConnect},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := testBroker(t)

			conn := dialTestBroker(t, b)
			conn.SetDeadline(time.Now().Add(2 * time.Second))
			if _, err := conn.Write(tc.frame); err != nil {
				t.Fatalf("write CONNECT: %v", err)
			}

			expectCloseWithoutDisconnect(t, conn)
		})
	}
}
