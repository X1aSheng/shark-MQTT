package broker

import (
	"errors"
	"io"
	"net"
	"time"

	"github.com/gorilla/websocket"
)

// wsConn adapts a WebSocket connection to net.Conn so the broker's
// HandleConnection (read loop, keep-alive deadlines, per-connection write
// queue) works unchanged over MQTT-over-WebSocket (R5). Each MQTT packet is
// carried in a binary WebSocket message; messages may concatenate several
// packets, so a partially-consumed message is buffered across Read calls.
// packetFlusher is implemented by transports that frame each outbound MQTT
// packet as a discrete unit (e.g. one WebSocket message per packet, R5). The
// broker's write loop calls FlushPacket after encoding each packet.
type packetFlusher interface {
	FlushPacket() error
}

type wsConn struct {
	conn     *websocket.Conn
	reader   io.Reader // current message reader, nil when between messages
	err      error     // sticky read error
	writeBuf []byte    // accumulates one MQTT packet's writes until FlushPacket
}

func newWSConn(c *websocket.Conn) *wsConn {
	return &wsConn{conn: c}
}

// Read returns the next bytes from the WebSocket stream. Only binary messages
// are accepted; MQTT is a binary protocol (MQTT-1.5.2 / WebSocket binding).
func (c *wsConn) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	for {
		if c.reader == nil {
			msgType, r, err := c.conn.NextReader()
			if err != nil {
				c.err = err
				return 0, err
			}
			if msgType != websocket.BinaryMessage {
				c.err = errors.New("broker: MQTT-over-WebSocket requires binary frames")
				return 0, c.err
			}
			c.reader = r
		}
		n, err := c.reader.Read(p)
		if err == io.EOF {
			c.reader = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		if err != nil {
			c.err = err
		}
		return n, err
	}
}

// Write buffers p. The broker's codec emits a packet as several Write calls
// (fixed header, then body); FlushPacket sends them as one binary WebSocket
// message so each MQTT packet maps to one message (MQTT 5.0 §6 / R5).
func (c *wsConn) Write(p []byte) (int, error) {
	c.writeBuf = append(c.writeBuf, p...)
	return len(p), nil
}

// FlushPacket sends the buffered writes as a single binary WebSocket message.
func (c *wsConn) FlushPacket() error {
	if len(c.writeBuf) == 0 {
		return nil
	}
	if err := c.conn.WriteMessage(websocket.BinaryMessage, c.writeBuf); err != nil {
		return err
	}
	c.writeBuf = c.writeBuf[:0]
	return nil
}

func (c *wsConn) Close() error { return c.conn.Close() }

func (c *wsConn) LocalAddr() net.Addr  { return c.conn.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }
func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.conn.SetReadDeadline(t); err != nil {
		return err
	}
	return c.conn.SetWriteDeadline(t)
}
func (c *wsConn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *wsConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }
