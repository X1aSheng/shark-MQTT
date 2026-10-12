package protocol

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
)

// Compile-time guarantees: the MQTT adapter implements the pluggable Adapter
// contract and the concrete codec implements PacketCodec.
var (
	_ Adapter     = (*MQTTAdapter)(nil)
	_ PacketCodec = (*Codec)(nil)
)

// peekPair returns a net.Pipe pair whose server side is wrapped in a PeekConn
// (the same wrapper MQTTServer uses in production), plus the client side for
// feeding bytes. Both ends are closed on cleanup.
func peekPair(t *testing.T) (PeekConn, net.Conn) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	return NewPeekConn(server), client
}

// writeAsync writes b on conn from a goroutine because net.Pipe is unbuffered:
// a synchronous Write would block until the reader consumes the bytes.
func writeAsync(conn net.Conn, b []byte) {
	go func() {
		_, _ = conn.Write(b)
	}()
}

// encodeConnectBytes encodes a CONNECT packet to raw wire bytes.
func encodeConnectBytes(t *testing.T, protoName string, version byte, clientID string) []byte {
	t.Helper()
	var buf bytes.Buffer
	pkt := &ConnectPacket{
		FixedHeader:     FixedHeader{PacketType: PacketTypeConnect},
		ProtocolName:    protoName,
		ProtocolVersion: version,
		Flags:           ConnectFlags{CleanSession: true},
		KeepAlive:       30,
		ClientID:        clientID,
	}
	if err := NewCodec(0).Encode(&buf, pkt); err != nil {
		t.Fatalf("encode CONNECT: %v", err)
	}
	return buf.Bytes()
}

func TestMQTTAdapterIdentify(t *testing.T) {
	a := NewMQTTAdapter(0)

	t.Run("connect first byte is mqtt", func(t *testing.T) {
		pc, client := peekPair(t)
		writeAsync(client, encodeConnectBytes(t, ProtocolNameMQTT, Version311, "c1"))
		ok, err := a.Identify(pc)
		if err != nil {
			t.Fatalf("Identify: %v", err)
		}
		if !ok {
			t.Fatal("Identify = false for a CONNECT first byte, want true")
		}
	})

	t.Run("publish first byte is not mqtt connect", func(t *testing.T) {
		pc, client := peekPair(t)
		writeAsync(client, []byte{0x30, 0x00}) // PUBLISH header
		ok, err := a.Identify(pc)
		if err != nil {
			t.Fatalf("Identify: %v", err)
		}
		if ok {
			t.Fatal("Identify = true for a PUBLISH first byte, want false")
		}
	})
}

// TestMQTTAdapterIdentifyDoesNotConsume verifies that Identify only Peek()s:
// the bytes must still be decodable by a subsequent Handshake on the same
// PeekConn.
func TestMQTTAdapterIdentifyDoesNotConsume(t *testing.T) {
	a := NewMQTTAdapter(0)
	pc, client := peekPair(t)
	writeAsync(client, encodeConnectBytes(t, ProtocolNameMQTT, Version50, "peek-ok"))

	ok, err := a.Identify(pc)
	if err != nil || !ok {
		t.Fatalf("Identify = %v, %v; want true, nil", ok, err)
	}
	info, _, err := a.Handshake(context.Background(), pc)
	if err != nil {
		t.Fatalf("Handshake after Identify: %v", err)
	}
	if info.ClientID != "peek-ok" {
		t.Errorf("ClientID = %q, want peek-ok (Peek must not consume the stream)", info.ClientID)
	}
}

func TestMQTTAdapterHandshake(t *testing.T) {
	a := NewMQTTAdapter(0)
	pc, client := peekPair(t)
	writeAsync(client, encodeConnectBytes(t, ProtocolNameMQTT, Version311, "hs-client"))

	info, codec, err := a.Handshake(context.Background(), pc)
	if err != nil {
		t.Fatalf("Handshake: %v", err)
	}
	if info.Protocol != "mqtt" {
		t.Errorf("Protocol = %q, want mqtt", info.Protocol)
	}
	if info.Version != Version311 {
		t.Errorf("Version = %d, want %d", info.Version, Version311)
	}
	if info.ClientID != "hs-client" {
		t.Errorf("ClientID = %q, want hs-client", info.ClientID)
	}
	if info.KeepAlive != 30 {
		t.Errorf("KeepAlive = %d, want 30", info.KeepAlive)
	}
	if !info.CleanSession {
		t.Errorf("CleanSession = false, want true")
	}
	if _, ok := info.Extensions.(*ConnectPacket); !ok {
		t.Errorf("Extensions = %T, want *ConnectPacket", info.Extensions)
	}
	if codec == nil {
		t.Fatal("Handshake returned a nil codec")
	}
	if got := codec.Spec(); got.Version != Version311 {
		t.Errorf("codec Spec().Version = %d, want %d (version-bound by the handshake)", got.Version, Version311)
	}
	if sp, ok := a.Spec(info.Version); !ok || sp.Version != Version311 {
		t.Errorf("Spec(%d) = %+v, ok=%v; want Version311", info.Version, sp, ok)
	}
}

// TestMQTTAdapterHandshakeMQTT31 verifies the handshake negotiates the 3.1
// version and derives the 3.1 capability set (23-byte ClientID, no empty ID).
func TestMQTTAdapterHandshakeMQTT31(t *testing.T) {
	a := NewMQTTAdapter(0)
	pc, client := peekPair(t)
	writeAsync(client, encodeConnectBytes(t, ProtocolNameMQIsdp, Version31, "legacy"))

	info, codec, err := a.Handshake(context.Background(), pc)
	if err != nil {
		t.Fatalf("Handshake: %v", err)
	}
	if info.Version != Version31 {
		t.Fatalf("Version = %d, want %d", info.Version, Version31)
	}
	if info.ClientID != "legacy" {
		t.Errorf("ClientID = %q, want legacy", info.ClientID)
	}
	sp := codec.Spec()
	if sp.Version != Version31 || sp.MaxClientIDLen != 23 || sp.AllowEmptyID {
		t.Errorf("codec Spec() = %+v, want Version31 with MaxClientIDLen=23 and AllowEmptyID=false", sp)
	}
}

func TestRegistrySelectExplicitBinding(t *testing.T) {
	reg := NewRegistry(NewMQTTAdapter(0))
	// Explicit binding skips sniffing entirely: no bytes need be present.
	pc, _ := peekPair(t)
	got, err := reg.Select(pc, "mqtt")
	if err != nil {
		t.Fatalf("Select(bound mqtt): %v", err)
	}
	if got.Name() != "mqtt" {
		t.Fatalf("Select(bound mqtt) = %q, want mqtt", got.Name())
	}
}

func TestRegistrySelectUnknownBinding(t *testing.T) {
	reg := NewRegistry(NewMQTTAdapter(0))
	pc, _ := peekPair(t)
	if _, err := reg.Select(pc, "coap"); err == nil {
		t.Fatal("Select(unknown bound) error = nil, want error")
	}
}

func TestRegistrySelectBySniffing(t *testing.T) {
	reg := NewRegistry(NewMQTTAdapter(0))
	pc, client := peekPair(t)
	writeAsync(client, encodeConnectBytes(t, ProtocolNameMQTT, Version311, "sniff"))

	got, err := reg.Select(pc, "")
	if err != nil {
		t.Fatalf("Select(sniff): %v", err)
	}
	if got.Name() != "mqtt" {
		t.Fatalf("Select(sniff) = %q, want mqtt", got.Name())
	}
}

func TestRegistrySelectNoMatch(t *testing.T) {
	reg := NewRegistry(NewMQTTAdapter(0))
	pc, client := peekPair(t)
	writeAsync(client, []byte{0x30, 0x00}) // PUBLISH header, not CONNECT
	_, err := reg.Select(pc, "")
	if !errors.Is(err, ErrNoMatchingAdapter) {
		t.Fatalf("Select(non-mqtt) error = %v, want ErrNoMatchingAdapter", err)
	}
}

func TestRegistryLookupAdaptersRegister(t *testing.T) {
	reg := NewRegistry(NewMQTTAdapter(0))

	if got, ok := reg.Lookup("mqtt"); !ok || got.Name() != "mqtt" {
		t.Fatalf("Lookup(mqtt) = %v, %v; want mqtt, true", got, ok)
	}
	if _, ok := reg.Lookup("missing"); ok {
		t.Fatal("Lookup(missing) ok = true, want false")
	}
	if adapters := reg.Adapters(); len(adapters) != 1 || adapters[0].Name() != "mqtt" {
		t.Fatalf("Adapters() = %v, want exactly [mqtt]", adapters)
	}

	// Re-registering the same name replaces rather than duplicates.
	reg.Register(NewMQTTAdapter(0))
	if adapters := reg.Adapters(); len(adapters) != 1 {
		t.Fatalf("Adapters() after re-register = %d entries, want 1", len(adapters))
	}
	// A nil adapter is ignored.
	reg.Register(nil)
	if adapters := reg.Adapters(); len(adapters) != 1 {
		t.Fatalf("Adapters() after nil register = %d entries, want 1", len(adapters))
	}
}
