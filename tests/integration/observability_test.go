package integration

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
)

// capturingLogger records every log call so a test can assert on the operator
// audit trail -- including what must NOT be written, such as credentials.
type capturingLogger struct {
	mu    sync.Mutex
	lines []capturedLine
}

type capturedLine struct {
	level string
	msg   string
	kv    string
}

func (c *capturingLogger) record(level, msg string, keyvals []any) {
	var b strings.Builder
	b.WriteString(msg)
	for i := 0; i+1 < len(keyvals); i += 2 {
		b.WriteByte(' ')
		b.WriteString(fmt.Sprintf("%v", keyvals[i]))
		b.WriteByte('=')
		b.WriteString(fmt.Sprintf("%v", keyvals[i+1]))
	}
	c.mu.Lock()
	c.lines = append(c.lines, capturedLine{level: level, msg: msg, kv: b.String()})
	c.mu.Unlock()
}

func (c *capturingLogger) Debug(msg string, kv ...any) { c.record("debug", msg, kv) }
func (c *capturingLogger) Info(msg string, kv ...any)  { c.record("info", msg, kv) }
func (c *capturingLogger) Warn(msg string, kv ...any)  { c.record("warn", msg, kv) }
func (c *capturingLogger) Error(msg string, kv ...any) { c.record("error", msg, kv) }

// dump renders every recorded line, for failure messages.
func (c *capturingLogger) dump() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	for _, l := range c.lines {
		b.WriteString(l.level)
		b.WriteByte(' ')
		b.WriteString(l.kv)
		b.WriteByte('\n')
	}
	return b.String()
}

// waitForWarn waits until a warn line containing substr is recorded, so the
// assertion does not depend on cross-goroutine timing.
func (c *capturingLogger) waitForWarn(substr string, timeout time.Duration) (capturedLine, bool) {
	deadline := time.Now().Add(timeout)
	for {
		c.mu.Lock()
		for _, l := range c.lines {
			if l.level == "warn" && strings.Contains(l.kv, substr) {
				c.mu.Unlock()
				return l, true
			}
		}
		c.mu.Unlock()
		if time.Now().After(deadline) {
			return capturedLine{}, false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// observerBroker starts a broker whose logs go to cap and whose authenticator
// is auth.
func observerBroker(t *testing.T, cap *capturingLogger, auth broker.Authenticator) *api.Broker {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	brk := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(auth),
		api.WithLogger(cap),
	)
	if err := brk.Start(); err != nil {
		t.Fatalf("start broker: %v", err)
	}
	t.Cleanup(func() { brk.Stop() })
	return brk
}

// connectUsername sends a CONNECT carrying a username (no password) and
// returns the CONNACK, without asserting on its reason code.
func connectUsername(t *testing.T, conn net.Conn, codec *protocol.Codec, clientID, username string, version byte) *protocol.ConnAckPacket {
	t.Helper()
	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: version,
		Flags: protocol.ConnectFlags{
			CleanSession: false,
			UsernameFlag: true,
		},
		KeepAlive: 30,
		ClientID:  clientID,
		Username:  username,
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	ack, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	ca, ok := ack.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", ack)
	}
	return ca
}

// TestAuthFailureIsLoggedWithoutCredentials: a rejected CONNECT must leave an
// audit-trail line at a level operators see (the default is info, and the
// returned error is only logged at debug), naming the client ID and username
// but never the password.
func TestAuthFailureIsLoggedWithoutCredentials(t *testing.T) {
	cap := &capturingLogger{}
	brk := observerBroker(t, cap, broker.DenyAllAuth{})

	const password = "s3cret-do-not-log"
	conn := dialTestBroker(t, brk)
	codec := protocol.NewCodec(0)
	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
			UsernameFlag: true,
			PasswordFlag: true,
		},
		KeepAlive: 30,
		ClientID:  "obs-bad-creds",
		Username:  "alice",
		Password:  []byte(password),
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	ack, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	ca, ok := ack.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", ack)
	}
	if ca.ReasonCode != protocol.ConnAckBadUsernameOrPassword5 {
		t.Fatalf("CONNACK reason = 0x%02X, want 0x%02X",
			ca.ReasonCode, protocol.ConnAckBadUsernameOrPassword5)
	}

	line, ok := cap.waitForWarn("authentication failed", 2*time.Second)
	if !ok {
		t.Fatalf("authentication failure was not logged at warn level; logger saw:\n%s", cap.dump())
	}
	if !strings.Contains(line.kv, "alice") {
		t.Errorf("auth-failure log does not name the username: %q", line.kv)
	}
	if !strings.Contains(line.kv, "obs-bad-creds") {
		t.Errorf("auth-failure log does not name the client ID: %q", line.kv)
	}
	if all := cap.dump(); strings.Contains(all, password) {
		t.Errorf("the client password leaked into the logs:\n%s", all)
	}
}

// TestUnsupportedAuthMethodIsLogged: a CONNECT naming an authentication method
// the broker does not implement is rejected and logged.
func TestUnsupportedAuthMethodIsLogged(t *testing.T) {
	cap := &capturingLogger{}
	brk := observerBroker(t, cap, broker.AllowAllAuth{})

	conn := dialTestBroker(t, brk)
	codec := protocol.NewCodec(0)
	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags:           protocol.ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        "obs-bad-method",
		Properties:      &protocol.Properties{AuthenticationMethod: "SCRAM-SHA-999"},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	ack, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	ca, ok := ack.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", ack)
	}
	if ca.ReasonCode != protocol.ReasonCodeBadAuthMethod {
		t.Fatalf("CONNACK reason = 0x%02X, want 0x%02X", ca.ReasonCode, protocol.ReasonCodeBadAuthMethod)
	}

	line, ok := cap.waitForWarn("unsupported authentication method", 2*time.Second)
	if !ok {
		t.Fatalf("unsupported auth method was not logged at warn level; logger saw:\n%s", cap.dump())
	}
	if !strings.Contains(line.kv, "obs-bad-method") {
		t.Errorf("log does not name the client ID: %q", line.kv)
	}
}

// TestSessionOwnerMismatchIsLogged: connecting to a client ID that belongs to
// another user is a session-hijack attempt; it must be rejected and logged.
func TestSessionOwnerMismatchIsLogged(t *testing.T) {
	cap := &capturingLogger{}
	brk := observerBroker(t, cap, broker.AllowAllAuth{})

	// Client A claims the client ID with username alice and stays connected.
	a := dialTestBroker(t, brk)
	if ca := connectUsername(t, a, protocol.NewCodec(0), "obs-session", "alice", protocol.Version50); ca.ReasonCode != protocol.ReasonCodeSuccess {
		t.Fatalf("client A CONNACK reason = 0x%02X, want success", ca.ReasonCode)
	}

	// Client B claims the same client ID with a different username.
	b := dialTestBroker(t, brk)
	ca := connectUsername(t, b, protocol.NewCodec(0), "obs-session", "bob", protocol.Version50)
	if ca.ReasonCode != protocol.ConnAckNotAuthorized5 {
		t.Fatalf("client B CONNACK reason = 0x%02X, want 0x%02X (not authorized)",
			ca.ReasonCode, protocol.ConnAckNotAuthorized5)
	}

	line, ok := cap.waitForWarn("belongs to a different user", 2*time.Second)
	if !ok {
		t.Fatalf("session-owner mismatch was not logged at warn level; logger saw:\n%s", cap.dump())
	}
	if !strings.Contains(line.kv, "obs-session") {
		t.Errorf("log does not name the client ID: %q", line.kv)
	}
}
