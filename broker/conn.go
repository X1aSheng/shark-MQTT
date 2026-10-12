package broker

import (
	"context"
	"net"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// Conn is a connection that has completed protocol identification and
// handshake. It is protocol-neutral: the connection handler consumes Conn and
// never sees an MQTT-specific codec or a raw CONNECT packet.
type Conn struct {
	Raw     net.Conn              // underlying transport (peekable wrapper)
	Adapter protocol.Adapter      // adapter that owns the handshake
	Info    *protocol.ConnectInfo // protocol-neutral handshake result
	Codec   protocol.PacketCodec  // version-bound packet codec
	Spec    protocol.Spec         // capability table for Info.Version
}

// ConnectionHandler is invoked for each accepted connection after its
// handshake has completed. It receives a protocol-neutral *Conn and must not
// depend on MQTT-specific types.
type ConnectionHandler interface {
	HandleConnection(ctx context.Context, c *Conn) error
}
