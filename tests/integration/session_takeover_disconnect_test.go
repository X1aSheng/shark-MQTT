// Package integration provides end-to-end integration tests for shark-mqtt.
// Run with: go test -race -tags=integration ./tests/integration/...
package integration

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestSessionTakenOverReceivesDisconnect covers [MQTT-3.1.4-3]: when an
// established connection's ClientID is reused, the Server must tell the
// *displaced* connection why it is going away before closing it --
//
//	"If the ClientID represents a Client already connected to the Server, the
//	 Server sends a DISCONNECT packet to the existing Client with Reason Code
//	 of 0x8E (Session taken over) as described in section 4.13 and MUST close
//	 the Network Connection of the existing Client."
//
// The broker closed the superseded socket outright. A bare close is
// indistinguishable from a network fault, so the displaced client saw a
// transport error instead of the one signal that tells it the takeover was
// intentional -- which is exactly what a client needs to decide whether to
// reconnect. The reason code is asserted as raw bytes rather than a decoded
// packet: a decoded assertion is checked by the same codec that would have to
// produce the defect (see disconnectRaw).
func TestSessionTakenOverReceivesDisconnect(t *testing.T) {
	brk := testBroker(t)

	const clientID = "takeover-disconnect"

	first := dialTestBroker(t, brk)
	firstCodec := protocol.NewCodec(0)
	connectClient(t, first, firstCodec, clientID)

	second := dialTestBroker(t, brk)
	secondCodec := protocol.NewCodec(0)
	connectClient(t, second, secondCodec, clientID)

	// The displaced connection is told first, then closed.
	expectDisconnectThenClose(t, first, protocol.DisconnectSessionTakenOver)

	// The new connection owns the ClientID and must still be live: the
	// teardown of the superseded connection must not take the new one with it.
	second.SetDeadline(time.Now().Add(2 * time.Second))
	if err := secondCodec.Encode(second, &protocol.PingReqPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePingReq},
	}); err != nil {
		t.Fatalf("PINGREQ on the new connection: %v", err)
	}
	second.SetDeadline(time.Now().Add(2 * time.Second))
	pong, err := secondCodec.Decode(second)
	if err != nil {
		t.Fatalf("PINGRESP on the new connection: %v", err)
	}
	if _, ok := pong.(*protocol.PingRespPacket); !ok {
		t.Fatalf("expected PINGRESP on the new connection, got %T", pong)
	}
}

// TestMQTT311SessionTakenOverClosesWithoutDisconnect pins the wire contract for
// a displaced pre-v5 connection: MQTT 3.1.1 has no server-to-client DISCONNECT,
// so it must see a bare close.
//
// What this case does *not* do is prove the broker-side ServerDisconnect gate
// is load-bearing -- removing that gate leaves this test green, because the
// codec refuses the frame first (encodeDisconnect rejects a non-zero reason
// code when the version has no reason codes, and writes nothing). The gate is
// still worth having at the call site for the reasons documented on
// sendTakenOverDisconnect, but the guard actually protecting the wire is the
// codec's, and that one is pinned by protocol.TestDisconnectVersionGuard.
func TestMQTT311SessionTakenOverClosesWithoutDisconnect(t *testing.T) {
	brk := testBroker(t)

	const clientID = "takeover-311"

	first := dialTestBroker(t, brk)
	firstCodec := protocol.NewCodecForVersion(0, protocol.Version311)
	connectV311(t, first, firstCodec, clientID)

	second := dialTestBroker(t, brk)
	secondCodec := protocol.NewCodecForVersion(0, protocol.Version311)
	connectV311(t, second, secondCodec, clientID)

	expectCloseWithoutDisconnect(t, first)
}
