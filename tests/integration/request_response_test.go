package integration

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestRequestResponseInformation verifies the CONNACK answers the client's
// request for Response Information: when requested, the CONNACK carries
// Response Information built from the client ID; when not requested, it carries
// none.
//
// The request property itself must never be echoed: Table 2-4 defines Request
// Response Information (0x19) for CONNECT only and Response Information (0x1A)
// for CONNACK, so a CONNACK carrying 0x19 is a Malformed Packet (§2.2.2.2) that
// a conformant client has to reject. This test used to assert the echo, which
// pinned a CONNACK no conformant peer would accept.
func TestRequestResponseInformation(t *testing.T) {
	broker := testBroker(t)

	// Requested -> CONNACK carries ResponseInfo.
	req := byte(1)
	conn := dialTestBroker(t, broker)
	codec := protocol.NewCodec(0)
	connectPkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
		},
		KeepAlive: 30,
		ClientID:  "rr-client",
		Properties: &protocol.Properties{
			RequestResponseInfo: &req,
		},
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, connectPkt); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	pkt, err := codec.Decode(conn)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	ca, ok := pkt.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}
	if ca.Properties == nil || ca.Properties.ResponseInfo != "rr-client" {
		t.Errorf("expected ResponseInfo %q, got %+v", "rr-client", ca.Properties)
	}
	if ca.Properties != nil && ca.Properties.RequestResponseInfo != nil {
		t.Errorf("CONNACK must not carry Request Response Information (0x19), got %d",
			*ca.Properties.RequestResponseInfo)
	}
	conn.Close()

	// Not requested -> no ResponseInfo in the CONNACK.
	conn2 := dialTestBroker(t, broker)
	codec2 := protocol.NewCodec(0)
	connectPkt2 := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
		},
		KeepAlive: 30,
		ClientID:  "rr-client-2",
	}
	conn2.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec2.Encode(conn2, connectPkt2); err != nil {
		t.Fatalf("CONNECT: %v", err)
	}
	conn2.SetDeadline(time.Now().Add(2 * time.Second))
	pkt2, err := codec2.Decode(conn2)
	if err != nil {
		t.Fatalf("CONNACK: %v", err)
	}
	ca2, ok := pkt2.(*protocol.ConnAckPacket)
	if !ok {
		t.Fatalf("expected CONNACK, got %T", pkt2)
	}
	if ca2.Properties != nil && ca2.Properties.ResponseInfo != "" {
		t.Errorf("expected no ResponseInfo when not requested, got %q", ca2.Properties.ResponseInfo)
	}
	conn2.Close()
}
