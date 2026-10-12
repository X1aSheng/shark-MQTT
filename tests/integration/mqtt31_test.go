package integration

// MQTT 3.1 semantic-completion regression matrix. Covers the four differences
// the protocol adapter layer must honor for MQTT 3.1 (MQIsdp):
//
//   1. ClientID maximum length is 23 bytes (24-byte IDs are rejected).
//   2. A zero-length ClientID is not allowed (Identifier Rejected, 0x02).
//   3. CONNACK carries no Session Present flag: the reserved byte stays 0.
//   4. SUBACK has no 0x80 subscription-failure code (failures fall back to a
//      granted QoS 0).
//
// MQTT 3.1.1 / 5.0 behavior is asserted alongside as a control, so a change
// that fixes 3.1 by loosening the strict versions is caught here.

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// connectVersion sends a CONNECT with an explicit protocol name/version and
// returns the CONNACK. It is the version-parameterised sibling of the package
// helpers (connectClient hard-codes MQTT 5.0).
func connectVersion(t *testing.T, conn net.Conn, codec *protocol.Codec,
	protoName string, version byte, clientID string, clean bool) (*protocol.ConnAckPacket, error) {
	t.Helper()
	pkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protoName,
		ProtocolVersion: version,
		Flags:           protocol.ConnectFlags{CleanSession: clean},
		KeepAlive:       30,
		ClientID:        clientID,
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := codec.Encode(conn, pkt); err != nil {
		return nil, fmt.Errorf("encode CONNECT: %w", err)
	}
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	resp, err := codec.Decode(conn)
	if err != nil {
		return nil, fmt.Errorf("decode CONNACK: %w", err)
	}
	ca, ok := resp.(*protocol.ConnAckPacket)
	if !ok {
		return nil, fmt.Errorf("expected CONNACK, got %T", resp)
	}
	return ca, nil
}

// TestMQTT31ClientIDLength pins the 23/24-byte ClientID boundary for MQTT 3.1.
func TestMQTT31ClientIDLength(t *testing.T) {
	brk := testBroker(t)

	// 23 bytes: at the MQTT 3.1 limit, accepted.
	conn := dialTestBroker(t, brk)
	codec := protocol.NewCodec(0)
	ca, err := connectVersion(t, conn, codec,
		protocol.ProtocolNameMQIsdp, protocol.Version31, strings.Repeat("a", 23), true)
	if err != nil {
		t.Fatalf("23-byte client ID: %v", err)
	}
	if ca.ReasonCode != protocol.ConnAckAccepted {
		t.Errorf("23-byte client ID CONNACK = 0x%02X, want 0x00 (accepted)", ca.ReasonCode)
	}
	conn.Close()

	// 24 bytes: over the MQTT 3.1 limit, rejected with Identifier Rejected.
	conn2 := dialTestBroker(t, brk)
	codec2 := protocol.NewCodec(0)
	ca2, err := connectVersion(t, conn2, codec2,
		protocol.ProtocolNameMQIsdp, protocol.Version31, strings.Repeat("a", 24), true)
	if err != nil {
		t.Fatalf("24-byte client ID: transport error: %v", err)
	}
	if ca2.ReasonCode != protocol.ConnAckIdentifierRejected {
		t.Errorf("24-byte client ID CONNACK = 0x%02X, want 0x02 (Identifier Rejected)", ca2.ReasonCode)
	}
}

// TestMQTT31EmptyClientIDRejected verifies MQTT 3.1 rejects a zero-length
// ClientID, while MQTT 3.1.1 accepts it with Clean Session = 1 (server-assigned
// ID). The 3.1.1 case is the control proving empty IDs are otherwise allowed.
func TestMQTT31EmptyClientIDRejected(t *testing.T) {
	brk := testBroker(t)

	// MQTT 3.1: empty ClientID rejected even with CleanSession=1.
	conn := dialTestBroker(t, brk)
	codec := protocol.NewCodec(0)
	ca, err := connectVersion(t, conn, codec,
		protocol.ProtocolNameMQIsdp, protocol.Version31, "", true)
	if err != nil {
		t.Fatalf("3.1 empty client ID: %v", err)
	}
	if ca.ReasonCode != protocol.ConnAckIdentifierRejected {
		t.Errorf("3.1 empty client ID CONNACK = 0x%02X, want 0x02 (Identifier Rejected)", ca.ReasonCode)
	}
	conn.Close()

	// MQTT 3.1.1 control: empty ClientID + CleanSession=1 is accepted.
	conn2 := dialTestBroker(t, brk)
	codec2 := protocol.NewCodec(0)
	ca2, err := connectVersion(t, conn2, codec2,
		protocol.ProtocolNameMQTT, protocol.Version311, "", true)
	if err != nil {
		t.Fatalf("3.1.1 empty client ID: %v", err)
	}
	if ca2.ReasonCode != protocol.ConnAckAccepted {
		t.Errorf("3.1.1 empty client ID CONNACK = 0x%02X, want 0x00 (accepted)", ca2.ReasonCode)
	}
}

// TestMQTT31ConnAckSessionPresentZero verifies that an MQTT 3.1 CONNACK never
// sets the Session Present flag, even when the broker internally resumes a
// persistent session. The MQTT 3.1.1 control (same flow) must still report
// SessionPresent=1, proving the resume really happened.
func TestMQTT31ConnAckSessionPresentZero(t *testing.T) {
	brk := testBroker(t)

	// --- MQTT 3.1 persistent session, reconnect. ---
	client31 := "mqtt31-present"
	conn1 := dialTestBroker(t, brk)
	codec1 := protocol.NewCodec(0)
	ca1, err := connectVersion(t, conn1, codec1,
		protocol.ProtocolNameMQIsdp, protocol.Version31, client31, false)
	if err != nil {
		t.Fatalf("3.1 first connect: %v", err)
	}
	if ca1.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("3.1 first CONNACK = 0x%02X, want 0x00", ca1.ReasonCode)
	}
	if ca1.SessionPresent {
		t.Fatal("3.1 first CONNACK SessionPresent = true, want false")
	}
	conn1.Close()
	time.Sleep(100 * time.Millisecond) // let the broker deregister the connection

	conn2 := dialTestBroker(t, brk)
	codec2 := protocol.NewCodec(0)
	ca2, err := connectVersion(t, conn2, codec2,
		protocol.ProtocolNameMQIsdp, protocol.Version31, client31, false)
	if err != nil {
		t.Fatalf("3.1 reconnect: %v", err)
	}
	if ca2.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("3.1 reconnect CONNACK = 0x%02X, want 0x00", ca2.ReasonCode)
	}
	if ca2.SessionPresent {
		t.Error("3.1 reconnect CONNACK SessionPresent = 1; MQTT 3.1 has no Session Present flag (want 0)")
	}

	// --- MQTT 3.1.1 control: the same flow DOES set Session Present. ---
	client311 := "mqtt311-present"
	ctrl1 := dialTestBroker(t, brk)
	ctrlCodec1 := protocol.NewCodec(0)
	if ca, err := connectVersion(t, ctrl1, ctrlCodec1,
		protocol.ProtocolNameMQTT, protocol.Version311, client311, false); err != nil {
		t.Fatalf("3.1.1 first connect: %v", err)
	} else if ca.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("3.1.1 first CONNACK = 0x%02X, want 0x00", ca.ReasonCode)
	}
	ctrl1.Close()
	time.Sleep(100 * time.Millisecond)

	ctrl2 := dialTestBroker(t, brk)
	ctrlCodec2 := protocol.NewCodec(0)
	ctrlCA, err := connectVersion(t, ctrl2, ctrlCodec2,
		protocol.ProtocolNameMQTT, protocol.Version311, client311, false)
	if err != nil {
		t.Fatalf("3.1.1 reconnect: %v", err)
	}
	if !ctrlCA.SessionPresent {
		t.Error("3.1.1 control reconnect SessionPresent = 0, want 1 (proves the resume path is exercised)")
	}
}

// TestMQTT31SubAckNoFailureCode verifies an MQTT 3.1 SUBACK never carries the
// 0x80 subscription-failure code (introduced in 3.1.1): a rejected filter
// yields a granted QoS 0 (0x00) instead. The 3.1.1 control uses the same
// invalid filter and must still get 0x80.
func TestMQTT31SubAckNoFailureCode(t *testing.T) {
	brk := testBroker(t)

	// An invalid filter ("#" is not the last level) is rejected server-side.
	const badFilter = "a/#/b"

	subscribe := func(conn net.Conn, codec *protocol.Codec) *protocol.SubAckPacket {
		t.Helper()
		sub := &protocol.SubscribePacket{
			FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
			PacketID:    1,
			Topics:      []protocol.TopicFilter{{Topic: badFilter, QoS: 0}},
		}
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		if err := codec.Encode(conn, sub); err != nil {
			t.Fatalf("encode SUBSCRIBE: %v", err)
		}
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		resp, err := codec.Decode(conn)
		if err != nil {
			t.Fatalf("decode SUBACK: %v", err)
		}
		subAck, ok := resp.(*protocol.SubAckPacket)
		if !ok {
			t.Fatalf("expected SUBACK, got %T", resp)
		}
		return subAck
	}

	// --- MQTT 3.1: no 0x80 failure code. ---
	conn31 := dialTestBroker(t, brk)
	codec31 := protocol.NewCodec(0)
	if ca, err := connectVersion(t, conn31, codec31,
		protocol.ProtocolNameMQIsdp, protocol.Version31, "mqtt31-suback", true); err != nil {
		t.Fatalf("3.1 connect: %v", err)
	} else if ca.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("3.1 CONNACK = 0x%02X, want 0x00", ca.ReasonCode)
	}
	subAck31 := subscribe(conn31, codec31)
	if len(subAck31.ReasonCodes) != 1 {
		t.Fatalf("3.1 SUBACK reason codes = %v, want exactly 1 entry", subAck31.ReasonCodes)
	}
	if subAck31.ReasonCodes[0] == protocol.SubAckFailure {
		t.Fatalf("3.1 SUBACK returned 0x80; MQTT 3.1 has no subscription-failure code")
	}
	if subAck31.ReasonCodes[0] != protocol.ReasonCodeGrantedQoS0 {
		t.Errorf("3.1 SUBACK code = 0x%02X, want 0x00 (granted QoS 0)", subAck31.ReasonCodes[0])
	}

	// --- MQTT 3.1.1 control: 0x80 is still used. ---
	conn311 := dialTestBroker(t, brk)
	codec311 := protocol.NewCodec(0)
	if ca, err := connectVersion(t, conn311, codec311,
		protocol.ProtocolNameMQTT, protocol.Version311, "mqtt311-suback", true); err != nil {
		t.Fatalf("3.1.1 connect: %v", err)
	} else if ca.ReasonCode != protocol.ConnAckAccepted {
		t.Fatalf("3.1.1 CONNACK = 0x%02X, want 0x00", ca.ReasonCode)
	}
	subAck311 := subscribe(conn311, codec311)
	if len(subAck311.ReasonCodes) != 1 || subAck311.ReasonCodes[0] != protocol.SubAckFailure {
		t.Errorf("3.1.1 SUBACK reason codes = %v, want [0x80]", subAck311.ReasonCodes)
	}
}
