package integration

import (
	"bytes"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/gorilla/websocket"
)

// wsDial connects to the broker's MQTT-over-WebSocket endpoint.
func wsDial(t *testing.T, addr string) *websocket.Conn {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/mqtt", nil)
	if err != nil {
		t.Fatalf("ws dial %s: %v", addr, err)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}

// wsConnect performs the MQTT CONNECT handshake over WebSocket.
func wsConnect(t *testing.T, ws *websocket.Conn, codec *protocol.Codec, clientID string) {
	t.Helper()
	var buf bytes.Buffer
	connectPkt := &protocol.ConnectPacket{
		FixedHeader:     protocol.FixedHeader{PacketType: protocol.PacketTypeConnect},
		ProtocolName:    protocol.ProtocolNameMQTT,
		ProtocolVersion: protocol.Version50,
		Flags: protocol.ConnectFlags{
			CleanSession: true,
		},
		KeepAlive: 30,
		ClientID:  clientID,
	}
	if err := codec.Encode(&buf, connectPkt); err != nil {
		t.Fatalf("CONNECT encode: %v", err)
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, buf.Bytes()); err != nil {
		t.Fatalf("CONNECT write: %v", err)
	}
	_, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("CONNACK read: %v", err)
	}
	pkt, err := codec.Decode(bytes.NewReader(msg))
	if err != nil {
		t.Fatalf("CONNACK decode: %v", err)
	}
	if _, ok := pkt.(*protocol.ConnAckPacket); !ok {
		t.Fatalf("expected CONNACK, got %T", pkt)
	}
}

// wsPublish sends a QoS 0 PUBLISH over WebSocket.
func wsPublish(t *testing.T, ws *websocket.Conn, codec *protocol.Codec, topic string, payload []byte) {
	t.Helper()
	var buf bytes.Buffer
	pubPkt := &protocol.PublishPacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypePublish},
		Topic:       topic,
		Payload:     payload,
	}
	if err := codec.Encode(&buf, pubPkt); err != nil {
		t.Fatalf("PUBLISH encode: %v", err)
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, buf.Bytes()); err != nil {
		t.Fatalf("PUBLISH write: %v", err)
	}
}

// wsMustReceivePublish reads the next MQTT packet and asserts it is a PUBLISH
// for the expected topic/payload.
func wsMustReceivePublish(t *testing.T, ws *websocket.Conn, codec *protocol.Codec, expectTopic, expectPayload string) {
	t.Helper()
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("PUBLISH read: %v", err)
	}
	pkt, err := codec.Decode(bytes.NewReader(msg))
	if err != nil {
		t.Fatalf("PUBLISH decode: %v", err)
	}
	p, ok := pkt.(*protocol.PublishPacket)
	if !ok {
		t.Fatalf("expected PUBLISH, got %T", pkt)
	}
	if p.Topic != expectTopic {
		t.Errorf("expected topic %q, got %q", expectTopic, p.Topic)
	}
	if string(p.Payload) != expectPayload {
		t.Errorf("expected payload %q, got %q", expectPayload, p.Payload)
	}
}

// TestWebSocket_MQTTTransport verifies MQTT over WebSocket end to end (R5):
// a WS subscriber receives a message published by a WS publisher.
func TestWebSocket_MQTTTransport(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	cfg.WSListenAddr = ":0"

	b := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { b.Stop() })

	wsAddr := b.WSAddr()
	if wsAddr == "" {
		t.Fatal("WSAddr() empty, WS not enabled")
	}

	// Subscriber
	subWS := wsDial(t, wsAddr)
	subCodec := protocol.NewCodec(0)
	wsConnect(t, subWS, subCodec, "ws-sub")
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "ws/topic", QoS: 0}},
	}
	var subBuf bytes.Buffer
	if err := subCodec.Encode(&subBuf, subPkt); err != nil {
		t.Fatalf("SUBSCRIBE encode: %v", err)
	}
	if err := subWS.WriteMessage(websocket.BinaryMessage, subBuf.Bytes()); err != nil {
		t.Fatalf("SUBSCRIBE write: %v", err)
	}
	subWS.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, subAckMsg, err := subWS.ReadMessage()
	if err != nil {
		t.Fatalf("SUBACK read: %v", err)
	}
	if pkt, err := subCodec.Decode(bytes.NewReader(subAckMsg)); err != nil || !isSubAck(pkt) {
		t.Fatalf("expected SUBACK (err=%v), got %T", err, pkt)
	}

	// Publisher
	pubWS := wsDial(t, wsAddr)
	pubCodec := protocol.NewCodec(0)
	wsConnect(t, pubWS, pubCodec, "ws-pub")

	wsPublish(t, pubWS, pubCodec, "ws/topic", []byte("hello-ws"))
	wsMustReceivePublish(t, subWS, subCodec, "ws/topic", "hello-ws")
}

func isSubAck(pkt protocol.Packet) bool {
	_, ok := pkt.(*protocol.SubAckPacket)
	return ok
}

// TestWebSocket_TCPStillWorks guards against the WS transport breaking the TCP
// path: both share the broker handler.
func TestWebSocket_TCPStillWorks(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	cfg.WSListenAddr = ":0"

	b := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { b.Stop() })

	// TCP subscriber + publisher using the standard helpers.
	subConn := dialTestBroker(t, b)
	subCodec := protocol.NewCodec(0)
	connectAndSubscribe(t, subConn, subCodec, "tcp-sub", "tcp/topic", 0)

	pubConn := dialTestBroker(t, b)
	pubCodec := protocol.NewCodec(0)
	connectClient(t, pubConn, pubCodec, "tcp-pub")

	publishQoS0(t, pubConn, pubCodec, "tcp/topic", []byte("over-tcp"))
	mustReceivePublish(t, subConn, subCodec, "tcp/topic", "over-tcp")
}
