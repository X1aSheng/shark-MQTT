package protocol

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
)

// ErrNoMatchingAdapter is returned by Registry.Select when no registered
// adapter claims an unbound connection.
var ErrNoMatchingAdapter = errors.New("protocol: no matching adapter")

// PeekConn is a connection whose leading bytes can be inspected without being
// consumed, so protocol fingerprinting (Adapter.Identify) does not disturb the
// byte stream that the eventual handshake will read.
type PeekConn interface {
	net.Conn
	Peek(n int) ([]byte, error)
}

// connFlusher mirrors the broker's packetFlusher for transports that frame each
// outbound packet as a discrete unit (e.g. one WebSocket message per MQTT
// packet). It is declared here so a peekConn can transparently forward the
// flush to its wrapped connection.
type connFlusher interface {
	FlushPacket() error
}

// peekConn wraps a net.Conn with a buffered reader so that Peek can inspect
// leading bytes while Read still returns the same (buffered) stream. All reads
// must therefore go through this wrapper — the handshake and the subsequent
// read loop both read from it, so no buffered byte is lost.
type peekConn struct {
	net.Conn
	r *bufio.Reader
}

// NewPeekConn wraps c so its prefix can be peeked. The returned connection must
// be used for every subsequent read of the stream.
func NewPeekConn(c net.Conn) PeekConn {
	return &peekConn{Conn: c, r: bufio.NewReader(c)}
}

// Peek returns the next n bytes without consuming them.
func (p *peekConn) Peek(n int) ([]byte, error) {
	return p.r.Peek(n)
}

// Read reads from the buffered stream so bytes previously returned by Peek are
// not skipped.
func (p *peekConn) Read(b []byte) (int, error) {
	return p.r.Read(b)
}

// FlushPacket forwards to the wrapped connection when it frames packets (WS);
// otherwise it is a no-op. Having peekConn always expose the method keeps the
// broker's "flush if supported" type assertion working through the wrapper.
func (p *peekConn) FlushPacket() error {
	if f, ok := p.Conn.(connFlusher); ok {
		return f.FlushPacket()
	}
	return nil
}

// ConnectInfo is the protocol-neutral result of a successful handshake. The
// broker core consumes ConnectInfo and no longer parses MQTT CONNECT packets
// itself.
type ConnectInfo struct {
	Protocol     string // "mqtt"
	Version      Version
	ClientID     string
	Username     string
	Password     []byte
	CleanSession bool
	KeepAlive    uint16
	WillTopic    string
	WillPayload  []byte
	WillQoS      uint8
	WillRetain   bool

	// Extensions carries version-specific data opaquely (e.g. the MQTT 5.0
	// Properties on the original *ConnectPacket); the core retrieves it via a
	// type assertion when needed. Non-MQTT protocols may place their own
	// structures here.
	Extensions any
}

// PacketCodec is the protocol-neutral encode/decode contract. The existing
// *protocol.Codec implements it.
//
// NOTE: the architecture design names this interface `Codec`, but the concrete
// `*protocol.Codec` type already occupies that identifier in this package, so
// the interface is named PacketCodec to avoid a redeclaration collision while
// keeping the contract identical.
type PacketCodec interface {
	Decode(r io.Reader) (Packet, error)
	Encode(w io.Writer, p Packet) error
	Spec() Spec
}

// OutboundPacketSizer is implemented by codecs that can bound the size of the
// packets they write, so the broker can push the client's Maximum Packet Size
// (MQTT 5.0 §3.1.2.11.4) down into the encoder.
//
// It is a separate, optional interface rather than a fourth method on
// PacketCodec for the same reason the broker declares packetFlusher rather than
// widening net.Conn: a codec that cannot size its output is not obliged to
// pretend it can, and a caller that needs the capability asks for it with a
// type assertion instead of the contract growing for every implementation.
// *protocol.Codec implements it.
type OutboundPacketSizer interface {
	// SetOutboundMaxPacketSize bounds outbound packets; 0 means unbounded.
	SetOutboundMaxPacketSize(n int)
}

// Adapter is the complete implementation contract for one access protocol.
// Adding a protocol (MQTT-over-QUIC / MQTT-SN / CoAP) requires only a new
// Adapter; MQTTServer and the broker core stay unchanged.
type Adapter interface {
	// Name returns a stable protocol identifier, e.g. "mqtt".
	Name() string

	// Identify reports whether the connection belongs to this protocol. An
	// implementation may only Peek, never consume bytes; when fewer bytes than
	// required are available it should return (false, nil) so other adapters or
	// the timeout can handle the connection.
	Identify(conn PeekConn) (bool, error)

	// Handshake completes the handshake: it reads and validates the first
	// packet (the MQTT CONNECT), negotiates the version, and returns a
	// protocol-neutral ConnectInfo plus a version-bound PacketCodec. This phase
	// only performs wire-format legality; authentication/session policy belongs
	// to the broker core.
	Handshake(ctx context.Context, conn net.Conn) (*ConnectInfo, PacketCodec, error)

	// Spec returns the semantic/capability description for a version (the MQTT
	// implementation reuses SpecOf).
	Spec(v Version) (Spec, bool)
}
