package protocol

import (
	"context"
	"net"
	"time"
)

// mqttProtocolName is the stable adapter identifier for MQTT.
const mqttProtocolName = "mqtt"

// defaultAdapterConnectTimeout bounds the CONNECT handshake when no explicit
// timeout is configured. It mirrors the broker's historical 10s default so the
// deadline behavior of the pre-refactor code is preserved.
const defaultAdapterConnectTimeout = 10 * time.Second

// MQTTAdapter implements Adapter for MQTT 3.1 / 3.1.1 / 5.0. All three versions
// share the same first byte (CONNECT = 0x1x), so Identify can only decide
// "this is MQTT"; the version is negotiated by Handshake.
type MQTTAdapter struct {
	maxPacketSize  int
	connectTimeout time.Duration
}

// NewMQTTAdapter creates an MQTT adapter with the given maximum packet size and
// the default CONNECT timeout.
func NewMQTTAdapter(maxPacketSize int) *MQTTAdapter {
	return NewMQTTAdapterWithTimeout(maxPacketSize, 0)
}

// NewMQTTAdapterWithTimeout creates an MQTT adapter with an explicit CONNECT
// handshake timeout (<= 0 selects the default).
func NewMQTTAdapterWithTimeout(maxPacketSize int, connectTimeout time.Duration) *MQTTAdapter {
	return &MQTTAdapter{maxPacketSize: maxPacketSize, connectTimeout: connectTimeout}
}

// Name returns the stable protocol identifier "mqtt".
func (a *MQTTAdapter) Name() string { return mqttProtocolName }

// Identify reports whether conn is an MQTT connection by inspecting the high
// nibble of its first byte: CONNECT is packet type 0x1, so (b[0] >> 4) == 1.
func (a *MQTTAdapter) Identify(conn PeekConn) (bool, error) {
	b, err := conn.Peek(1)
	if err != nil {
		return false, err
	}
	if len(b) < 1 {
		return false, nil
	}
	return (b[0] >> 4) == byte(PacketTypeConnect), nil
}

// Handshake reads and decodes the first packet (the MQTT CONNECT), verifies it
// is a CONNECT, and derives the version-bound Spec. It applies the CONNECT read
// deadline here (migrated out of Broker.HandleConnection). Semantic validation
// of the CONNECT fields (ValidateConnect) is intentionally left to the broker
// core so the rejection CONNACK and rejection metrics/logs are emitted exactly
// as before the refactor (see the deviation note reported with T03).
func (a *MQTTAdapter) Handshake(ctx context.Context, conn net.Conn) (*ConnectInfo, PacketCodec, error) {
	_ = ctx // reserved for future cancellation-aware handshakes

	timeout := a.connectTimeout
	if timeout <= 0 {
		timeout = defaultAdapterConnectTimeout
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, nil, err
	}

	codec := NewCodec(a.maxPacketSize)
	pkt, err := codec.Decode(conn)
	if err != nil {
		return nil, nil, err
	}
	cp, ok := pkt.(*ConnectPacket)
	if !ok {
		return nil, nil, ErrInvalidPacket
	}
	return toConnectInfo(cp), codec, nil
}

// Spec returns the capability description for a version.
func (a *MQTTAdapter) Spec(v Version) (Spec, bool) {
	return SpecOf(v)
}

// toConnectInfo projects an MQTT ConnectPacket onto the protocol-neutral
// ConnectInfo. The original packet is preserved in Extensions so the broker
// core can reach MQTT-5-only data (Properties) without re-decoding.
func toConnectInfo(cp *ConnectPacket) *ConnectInfo {
	return &ConnectInfo{
		Protocol:     mqttProtocolName,
		Version:      cp.ProtocolVersion,
		ClientID:     cp.ClientID,
		Username:     cp.Username,
		Password:     cp.Password,
		CleanSession: cp.Flags.CleanSession,
		KeepAlive:    cp.KeepAlive,
		WillTopic:    cp.WillTopic,
		WillPayload:  cp.WillMessage,
		WillQoS:      cp.Flags.WillQoS,
		WillRetain:   cp.Flags.WillRetain,
		Extensions:   cp,
	}
}
