package broker

import (
	"context"
	"net"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// handleConnWithHandshake runs the MQTT adapter handshake on conn and then
// invokes b.HandleConnection with the resulting protocol-neutral Conn, mirroring
// what MQTTServer.handleConn does in production. It preserves the pre-refactor
// test intent (the test drives a client codec on the other end of a net.Pipe)
// while adapting to the ConnectionHandler(ctx, *Conn) signature. A
// maxPacketSize <= 0 selects the codec default, and the CONNECT deadline uses
// the broker's configured connectTimeout so timeout behavior stays under test.
func handleConnWithHandshake(ctx context.Context, b *Broker, conn net.Conn, maxPacketSize int) error {
	adapter := protocol.NewMQTTAdapterWithTimeout(maxPacketSize, b.opts.connectTimeout)
	info, codec, err := adapter.Handshake(ctx, conn)
	if err != nil {
		return err
	}
	spec, _ := adapter.Spec(info.Version)
	return b.HandleConnection(ctx, &Conn{
		Raw:     conn,
		Adapter: adapter,
		Info:    info,
		Codec:   codec,
		Spec:    spec,
	})
}

// newRawConn builds a protocol-neutral Conn without running a handshake. It is
// used by tests that exercise pre-handshake broker checks (e.g. the connection
// limit) which must return before any CONNECT is read.
func newRawConn(conn net.Conn, codec *protocol.Codec) *Conn {
	return &Conn{Raw: conn, Codec: codec, Info: &protocol.ConnectInfo{}}
}
