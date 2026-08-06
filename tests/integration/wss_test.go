package integration

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/X1aSheng/shark-mqtt/api"
	"github.com/X1aSheng/shark-mqtt/broker"
	"github.com/X1aSheng/shark-mqtt/config"
	"github.com/X1aSheng/shark-mqtt/protocol"
	"github.com/gorilla/websocket"
)

// writeSelfSignedCert writes a self-signed server certificate and key to temp
// files and returns their paths.
func writeSelfSignedCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return certFile, keyFile
}

// TestWebSocket_TLS verifies MQTT-over-WebSocket over TLS (WSS): a subscriber
// connected via wss:// receives a message published by a wss:// publisher.
func TestWebSocket_TLS(t *testing.T) {
	certFile, keyFile := writeSelfSignedCert(t)

	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	cfg.WSSListenAddr = ":0"
	cfg.TLSEnabled = true
	cfg.TLSCertFile = certFile
	cfg.TLSKeyFile = keyFile

	b := api.NewBroker(
		api.WithConfig(cfg),
		api.WithAuth(broker.AllowAllAuth{}),
	)
	if err := b.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { b.Stop() })

	wssAddr := b.WSSAddr()
	if wssAddr == "" {
		t.Fatal("WSSAddr() empty, WSS not enabled")
	}

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // self-signed cert
	}
	wsDialWSS := func() *websocket.Conn {
		t.Helper()
		ws, _, err := dialer.Dial("wss://"+wssAddr+"/mqtt", nil)
		if err != nil {
			t.Fatalf("wss dial: %v", err)
		}
		t.Cleanup(func() { ws.Close() })
		return ws
	}

	// Subscriber
	subWS := wsDialWSS()
	subCodec := protocol.NewCodec(0)
	wsConnect(t, subWS, subCodec, "wss-sub")
	subPkt := &protocol.SubscribePacket{
		FixedHeader: protocol.FixedHeader{PacketType: protocol.PacketTypeSubscribe, QoS: 1},
		PacketID:    1,
		Topics:      []protocol.TopicFilter{{Topic: "wss/topic", QoS: 0}},
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
	pubWS := wsDialWSS()
	pubCodec := protocol.NewCodec(0)
	wsConnect(t, pubWS, pubCodec, "wss-pub")
	wsPublish(t, pubWS, pubCodec, "wss/topic", []byte("over-tls-ws"))
	wsMustReceivePublish(t, subWS, subCodec, "wss/topic", "over-tls-ws")
}

// TestWebSocket_TLSNotConfigured verifies WSSListenAddr without TLS fails Start.
func TestWebSocket_TLSNotConfigured(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ListenAddr = ":0"
	cfg.MetricsAddr = ":0"
	cfg.WSSListenAddr = ":0"
	// TLSEnabled stays false.

	b := api.NewBroker(api.WithConfig(cfg), api.WithAuth(broker.AllowAllAuth{}))
	if err := b.Start(); err == nil {
		b.Stop()
		t.Fatal("expected Start to fail when wss_listen_addr is set without TLS")
	}
}
