package broker

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// testEnhancedAuth is a toy MQTT 5.0 enhanced authenticator for the "token"
// method: a single-step path (CONNECT data "good-token") and a two-step
// challenge-response path (challenge "challenge", expected response
// "good-response"). It exercises both flows in tests.
type testEnhancedAuth struct{}

func (testEnhancedAuth) Method() string { return "token" }

func (testEnhancedAuth) Initial(data []byte) (byte, []byte, error) {
	if string(data) == "good-token" {
		return protocol.AuthSuccess, nil, nil
	}
	return protocol.AuthContinueAuth, []byte("challenge"), nil
}

func (testEnhancedAuth) Continue(data []byte) (byte, []byte, error) {
	if string(data) == "good-response" {
		return protocol.AuthSuccess, nil, nil
	}
	return protocol.ReasonCodeNotAuthorized, nil, nil
}

// runEnhancedClient drives the client side of a HandleConnection on net.Pipe
// and returns the decoded CONNACK. onAuth, if non-nil, is invoked with each
// AUTH packet the server sends so the test can respond.
func runEnhancedClient(t *testing.T, conn net.Conn, codec *protocol.Codec, authMethod string, authData []byte, onAuth func(*protocol.AuthPacket) *protocol.AuthPacket) *protocol.ConnAckPacket {
	t.Helper()

	connectPkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
		},
		KeepAlive: 30,
		ClientID:  "enhanced-client",
		Properties: &protocol.Properties{
			AuthenticationMethod: authMethod,
			AuthenticationData:   authData,
		},
	}
	var buf bytes.Buffer
	if err := codec.Encode(&buf, connectPkt); err != nil {
		t.Fatalf("CONNECT encode: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(buf.Bytes()); err != nil {
		t.Fatalf("CONNECT write: %v", err)
	}

	// Read AUTH packets until the server completes the exchange, then the
	// CONNACK. A final non-success AUTH (reject) ends the exchange without a
	// CONNACK, in which case nil is returned.
	for {
		pkt, err := codec.Decode(conn)
		if err != nil {
			t.Fatalf("decode server packet: %v", err)
		}
		switch p := pkt.(type) {
		case *protocol.AuthPacket:
			if p.ReasonCode == protocol.AuthContinueAuth {
				if onAuth != nil {
					reply := onAuth(p)
					var rbuf bytes.Buffer
					if err := codec.Encode(&rbuf, reply); err != nil {
						t.Fatalf("AUTH encode: %v", err)
					}
					if _, err := conn.Write(rbuf.Bytes()); err != nil {
						t.Fatalf("AUTH write: %v", err)
					}
				}
			} else if p.ReasonCode != protocol.AuthSuccess {
				// Exchange rejected — the server closes without a CONNACK.
				return nil
			}
			// AuthSuccess: keep reading for the CONNACK.
		case *protocol.ConnAckPacket:
			return p
		case *protocol.DisconnectPacket:
			// Rejected exchange: server signals failure with DISCONNECT (§4.12).
			return nil
		default:
			t.Fatalf("expected CONNACK or AUTH, got %T", pkt)
		}
	}
}

// TestEnhancedAuth_MultiStep verifies a two-step challenge-response exchange
// (§4.12): CONNECT → AUTH 0x18 → AUTH 0x18 → AUTH 0x00 → CONNACK 0x00.
func TestEnhancedAuth_MultiStep(t *testing.T) {
	b := New(WithAuth(DenyAllAuth{}), WithEnhancedAuth(testEnhancedAuth{}))
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	done := make(chan error, 1)
	go func() {
		done <- b.HandleConnection(context.Background(), serverConn, nil)
	}()

	clientCodec := protocol.NewCodec(0)
	connAck := runEnhancedClient(t, clientConn, clientCodec, "token", []byte("bad-token"), func(authPkt *protocol.AuthPacket) *protocol.AuthPacket {
		if string(authPkt.Properties.AuthenticationData) != "challenge" {
			t.Errorf("expected challenge, got %q", authPkt.Properties.AuthenticationData)
		}
		return &protocol.AuthPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeAuth},
			ReasonCode:  protocol.AuthContinueAuth,
			Properties: &protocol.Properties{
				AuthenticationData: []byte("good-response"),
			},
		}
	})

	if connAck.ReasonCode != protocol.ConnAckAccepted {
		t.Errorf("expected CONNACK 0x00, got 0x%02X", connAck.ReasonCode)
	}
	// Close the client side so the broker's readLoop (blocked on keep-alive)
	// unblocks and HandleConnection returns promptly.
	clientConn.Close()
	if err := <-done; err != nil {
		t.Errorf("HandleConnection returned error after successful auth: %v", err)
	}
}

// TestEnhancedAuth_SingleStep verifies the one-shot path: the CONNECT data is
// sufficient, so the server goes straight to CONNACK 0x00 with no AUTH exchange.
func TestEnhancedAuth_SingleStep(t *testing.T) {
	b := New(WithAuth(DenyAllAuth{}), WithEnhancedAuth(testEnhancedAuth{}))
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	go func() {
		_ = b.HandleConnection(context.Background(), serverConn, nil)
	}()

	clientCodec := protocol.NewCodec(0)
	connAck := runEnhancedClient(t, clientConn, clientCodec, "token", []byte("good-token"), nil)
	if connAck.ReasonCode != protocol.ConnAckAccepted {
		t.Errorf("expected CONNACK 0x00, got 0x%02X", connAck.ReasonCode)
	}
}

// TestEnhancedAuth_UnsupportedMethod verifies a CONNECT with an unregistered
// AuthenticationMethod is rejected with CONNACK 0x8C (Bad Authentication Method).
func TestEnhancedAuth_UnsupportedMethod(t *testing.T) {
	b := New(WithAuth(DenyAllAuth{}), WithEnhancedAuth(testEnhancedAuth{}))
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	done := make(chan error, 1)
	go func() {
		done <- b.HandleConnection(context.Background(), serverConn, nil)
	}()

	clientCodec := protocol.NewCodec(0)
	connAck := runEnhancedClient(t, clientConn, clientCodec, "unsupported", []byte("x"), nil)
	if connAck.ReasonCode != protocol.ReasonCodeBadAuthMethod {
		t.Errorf("expected CONNACK 0x8C for unsupported method, got 0x%02X", connAck.ReasonCode)
	}
	if err := <-done; err == nil {
		t.Error("expected HandleConnection error for unsupported auth method")
	}
}

// TestEnhancedAuth_Rejected verifies a failed response ends the exchange with
// an AUTH carrying the reject reason.
func TestEnhancedAuth_Rejected(t *testing.T) {
	b := New(WithAuth(DenyAllAuth{}), WithEnhancedAuth(testEnhancedAuth{}))
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer b.Stop()

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	go func() {
		_ = b.HandleConnection(context.Background(), serverConn, nil)
	}()

	clientCodec := protocol.NewCodec(0)
	connAck := runEnhancedClient(t, clientConn, clientCodec, "token", []byte("bad-token"), func(authPkt *protocol.AuthPacket) *protocol.AuthPacket {
		return &protocol.AuthPacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeAuth},
			ReasonCode:  protocol.AuthContinueAuth,
			Properties: &protocol.Properties{
				AuthenticationData: []byte("wrong-response"),
			},
		}
	})
	// The exchange fails with a final non-success AUTH and no CONNACK.
	if connAck != nil {
		t.Errorf("expected no CONNACK for rejected auth, got reason 0x%02X", connAck.ReasonCode)
	}
}
