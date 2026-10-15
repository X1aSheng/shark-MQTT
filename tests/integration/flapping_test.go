package integration

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
)

// startFlappingBroker starts a broker with connection-flap detection configured
// exactly as the test needs.
func startFlappingBroker(t *testing.T, enabled bool, maxConnects int) *api.Broker {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0" // random port
	cfg.MetricsAddr = ":0"
	cfg.FlappingEnabled = enabled
	cfg.FlappingMaxConnects = maxConnects
	cfg.FlappingWindow = config.Duration(time.Minute)
	cfg.FlappingPenaltyDuration = config.Duration(time.Minute)

	b := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(b.Stop)
	return b
}

// tryConnect performs one full CONNECT and returns the CONNACK reason code.
//
// 0x00 means accepted. The connection is closed immediately: the question is
// whether the broker served the attempt at all, and holding sessions open would
// measure concurrency rather than connection rate.
func tryConnect(t *testing.T, addr, clientID string) (byte, error) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	if _, err := conn.Write(encodeConnect(clientID)); err != nil {
		return 0, err
	}

	// Read exactly the 4 CONNACK bytes. A plain Read is not enough: TCP may
	// deliver the packet in two segments (observed as a 2-byte read of "20 02"
	// followed by the reason codes), and a short read would look like an
	// all-zero, i.e. "accepted", CONNACK.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return 0, err
	}
	if len(buf) < 4 || buf[0] != 0x20 {
		return 0, errBadConnAck
	}
	return buf[3], nil
}

// encodeConnect builds an MQTT 3.1.1 CONNECT with CleanSession and no
// credentials.
func encodeConnect(clientID string) []byte {
	var body []byte
	body = append(body, 0x00, 0x04) // protocol name length + "MQTT"
	body = append(body, 'M', 'Q', 'T', 'T')
	body = append(body, 0x04)       // protocol level 3.1.1
	body = append(body, 0x02)       // connect flags: CleanSession
	body = append(body, 0x00, 0x3C) // keep alive 60
	body = append(body, byte(len(clientID)>>8), byte(len(clientID)))
	body = append(body, clientID...)

	return append([]byte{0x10, byte(len(body))}, body...)
}

type constError string

func (e constError) Error() string { return string(e) }

const errBadConnAck = constError("malformed CONNACK")

// TestFlapping_RejectsStorm drives the real broker and proves a reconnect storm
// is eventually refused. The unit tests cover the detector; this proves the
// wiring -- that the check sits on the connection path and produces a CONNACK.
func TestFlapping_RejectsStorm(t *testing.T) {
	b := startFlappingBroker(t, true, 3)
	addr := b.Addr()

	const maxAttempts = 30
	accepted := 0
	for i := 0; i < maxAttempts; i++ {
		code, err := tryConnect(t, addr, "flap")
		if err != nil {
			t.Logf("attempt %d refused at the transport level: %v", i, err)
			break
		}
		if code != 0x00 {
			t.Logf("attempt %d refused with CONNACK 0x%02x", i, code)
			break
		}
		accepted++
	}

	if accepted >= maxAttempts {
		t.Fatalf("all %d attempts accepted; flapping protection is not wired in", maxAttempts)
	}
	t.Logf("accepted %d before the first refusal", accepted)
}

// TestFlapping_DisabledServesEverything is the guard against a limit that
// rejects healthy traffic: with the feature off, well past the limit, every
// connection is still served.
func TestFlapping_DisabledServesEverything(t *testing.T) {
	b := startFlappingBroker(t, false, 1) // limit 1, but disabled
	addr := b.Addr()

	for i := 0; i < 25; i++ {
		code, err := tryConnect(t, addr, "ok")
		if err != nil {
			t.Fatalf("attempt %d failed: %v", i, err)
		}
		if code != 0x00 {
			t.Fatalf("attempt %d got CONNACK 0x%02x, want accepted with detection disabled", i, code)
		}
	}
}

// TestFlapping_PenaltyReasonCode pins the wire behaviour: a throttled client is
// told the server is unavailable, not that its credentials are bad.
//
// MQTT 3.1.1 has no "connecting too often" code, so 0x03 is the closest
// available. Asserting it keeps a future change from silently swapping in a
// code a client would misread (0x04 would suggest retrying with the same
// credentials, which would never help).
func TestFlapping_PenaltyReasonCode(t *testing.T) {
	b := startFlappingBroker(t, true, 2)
	addr := b.Addr()

	got := byte(0)
	for i := 0; i < 20; i++ {
		code, err := tryConnect(t, addr, "reason")
		if err != nil {
			continue
		}
		if code != 0x00 {
			got = code
			break
		}
	}
	if got == 0 {
		t.Fatal("never refused")
	}
	// 0x03 Server Unavailable for 3.1.1; MQTT 5 would use 0x8A Banned.
	if got != 0x03 {
		t.Errorf("CONNACK reason = 0x%02x, want 0x03 (Server Unavailable)", got)
	}
}
