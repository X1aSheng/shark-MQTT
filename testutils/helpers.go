// Package testutils provides mock implementations for testing shark-mqtt components.
package testutils

import (
	"net"
	"time"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// MustEncodeConnect encodes a CONNECT packet to bytes.
func MustEncodeConnect(clientID string, keepAlive uint16, cleanSession bool) []byte {
	pkt := &protocol.ConnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeConnect,
		},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: cleanSession,
		},
		KeepAlive: keepAlive,
		ClientID:  clientID,
	}
	codec := protocol.NewCodec(0)
	conn := NewMockConn()
	if err := codec.Encode(conn, pkt); err != nil {
		panic(err)
	}
	return conn.WrittenData()
}

// MustEncodePingReq encodes a PINGREQ packet to bytes.
func MustEncodePingReq() []byte {
	pkt := &protocol.PingReqPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePingReq,
		},
	}
	codec := protocol.NewCodec(0)
	conn := NewMockConn()
	if err := codec.Encode(conn, pkt); err != nil {
		panic(err)
	}
	return conn.WrittenData()
}

// MustEncodeDisconnect encodes a DISCONNECT packet to bytes.
func MustEncodeDisconnect() []byte {
	pkt := &protocol.DisconnectPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeDisconnect,
		},
	}
	codec := protocol.NewCodec(0)
	conn := NewMockConn()
	if err := codec.Encode(conn, pkt); err != nil {
		panic(err)
	}
	return conn.WrittenData()
}

// MustEncodePublish encodes a PUBLISH packet to bytes.
func MustEncodePublish(topic string, payload []byte, qos byte, retain bool) []byte {
	pkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypePublish,
			QoS:        qos,
			Retain:     retain,
		},
		Topic:   topic,
		Payload: payload,
	}
	if qos > 0 {
		pkt.PacketID = 1
	}
	codec := protocol.NewCodec(0)
	conn := NewMockConn()
	if err := codec.Encode(conn, pkt); err != nil {
		panic(err)
	}
	return conn.WrittenData()
}

// MustEncodeSubscribe encodes a SUBSCRIBE packet to bytes.
func MustEncodeSubscribe(packetID uint16, topics []protocol.TopicFilter) []byte {
	pkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{
			PacketType: protocol.PacketTypeSubscribe,
			QoS:        1,
		},
		PacketID: packetID,
		Topics:   topics,
	}
	codec := protocol.NewCodec(0)
	conn := NewMockConn()
	if err := codec.Encode(conn, pkt); err != nil {
		panic(err)
	}
	return conn.WrittenData()
}

// NewPipe creates a pair of connected net.Conns.
func NewPipe() (net.Conn, net.Conn) {
	return net.Pipe()
}

// WaitForChannel waits for a value on a channel with timeout.
func WaitForChannel[T any](ch <-chan T, timeout time.Duration) (T, bool) {
	var zero T
	select {
	case v := <-ch:
		return v, true
	case <-time.After(timeout):
		return zero, false
	}
}

// WaitReady waits for a signal on a channel with timeout.
func WaitReady(ch <-chan struct{}, timeout time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(timeout):
		return false
	}
}

// GenerateClientIDs generates n unique client IDs with the given prefix.
func GenerateClientIDs(prefix string, n int) []string {
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = prefix
	}
	return ids
}

// TopicPermutations returns common topic patterns for testing.
func TopicPermutations() []string {
	return []string{
		"a",
		"a/b",
		"a/b/c",
		"$SYS/broker/connections",
		"sensors/temperature/room1",
		"sensors/temperature/room2",
		"devices/+/status",
		"devices/#",
	}
}

