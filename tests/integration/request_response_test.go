package integration

import (
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestRequestResponseInformation verifies the CONNACK honors the client's
// RequestResponseInformation request (MQTT 5.0 §3.2.2.3.8/.9): when requested,
// it advertises RequestResponseInfo=1 and returns the client ID as the
// Response Information base; when not requested, neither is sent.
func TestRequestResponseInformation(t *testing.T) {
	broker := testBroker(t)

	// Requested -> CONNACK carries RequestResponseInfo=1 and ResponseInfo.
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
	if ca.Properties == nil || ca.Properties.RequestResponseInfo == nil || *ca.Properties.RequestResponseInfo != 1 {
		t.Error("expected RequestResponseInfo=1 in CONNACK when requested")
	}
	if ca.Properties.ResponseInfo != "rr-client" {
		t.Errorf("expected ResponseInfo %q, got %q", "rr-client", ca.Properties.ResponseInfo)
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
