package protocol

// Independent QA verification of the protocol-adapter layer, written by the QA
// engineer (Yan). These tests deliberately re-derive behavior from the wire
// format / interface contract rather than re-using the implementation's own
// helpers, so they can catch a self-consistent but wrong implementation.

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"
)

// qaFakeAdapter is a controllable Adapter used to prove Registry selection
// precedence without relying on MQTT byte sniffing.
type qaFakeAdapter struct {
	name     string
	identify bool
}

func (f *qaFakeAdapter) Name() string                    { return f.name }
func (f *qaFakeAdapter) Identify(PeekConn) (bool, error) { return f.identify, nil }
func (f *qaFakeAdapter) Spec(v Version) (Spec, bool)     { return SpecOf(v) }
func (f *qaFakeAdapter) Handshake(context.Context, net.Conn) (*ConnectInfo, PacketCodec, error) {
	return nil, nil, ErrNoMatchingAdapter
}

// TestQAIdentifyFirstByteMatrix pins the fingerprint predicate (b[0]>>4 == 0x1)
// across every packet-type nibble plus non-MQTT probe bytes.
func TestQAIdentifyFirstByteMatrix(t *testing.T) {
	a := NewMQTTAdapter(0)
	cases := []struct {
		b    byte
		want bool
	}{
		{0x10, true},  // CONNECT
		{0x1F, true},  // CONNECT nibble with low bits set (still type 1)
		{0x00, false}, // RESERVED
		{0x20, false}, // CONNACK
		{0x30, false}, // PUBLISH
		{0x40, false}, // PUBACK
		{0x80, false}, // SUBSCRIBE
		{0xE0, false}, // DISCONNECT
		{0xF0, false}, // reserved
		{'G', false},  // 0x47: an HTTP "GET" probe
		{'P', false},  // 0x50: an HTTP POST probe
	}
	for _, tc := range cases {
		pc, client := peekPair(t)
		writeAsync(client, []byte{tc.b, 0x00})
		got, err := a.Identify(pc)
		if err != nil {
			t.Fatalf("Identify(0x%02X): %v", tc.b, err)
		}
		if got != tc.want {
			t.Errorf("Identify(0x%02X) = %v, want %v", tc.b, got, tc.want)
		}
	}
}

// TestQARegistryExplicitBindingBeatsSniffer proves the "explicit binding first,
// fingerprint otherwise" policy: even when a registered sniffer claims every
// connection, a bound protocol name must win; when unbound the first match in
// registration order wins.
func TestQARegistryExplicitBindingBeatsSniffer(t *testing.T) {
	sniffer := &qaFakeAdapter{name: "sniffer", identify: true}
	reg := NewRegistry(sniffer, NewMQTTAdapter(0))

	// Explicit binding wins even though the sniffer is first and would claim it.
	pc, client := peekPair(t)
	writeAsync(client, []byte{0x30, 0x00}) // not MQTT at all
	got, err := reg.Select(pc, "mqtt")
	if err != nil {
		t.Fatalf("Select(bound=mqtt): %v", err)
	}
	if got.Name() != "mqtt" {
		t.Fatalf("Select(bound=mqtt) = %q, want mqtt (binding must beat sniffing)", got.Name())
	}

	// Unbound: first registered adapter that identifies wins.
	pc2, client2 := peekPair(t)
	writeAsync(client2, []byte{0x30, 0x00})
	got2, err := reg.Select(pc2, "")
	if err != nil {
		t.Fatalf("Select(unbound): %v", err)
	}
	if got2.Name() != "sniffer" {
		t.Fatalf("Select(unbound) = %q, want sniffer (first match in order)", got2.Name())
	}
}

// TestQARegistryNoMatchReturnsSentinel checks the unbound no-match error is the
// documented sentinel (the server keys its metrics off it).
func TestQARegistryNoMatchReturnsSentinel(t *testing.T) {
	reg := NewRegistry(NewMQTTAdapter(0))
	pc, client := peekPair(t)
	writeAsync(client, []byte{0x40, 0x00}) // PUBACK header: not a CONNECT
	if _, err := reg.Select(pc, ""); !errors.Is(err, ErrNoMatchingAdapter) {
		t.Fatalf("Select(non-mqtt) error = %v, want ErrNoMatchingAdapter", err)
	}
}

// TestQAHandshakeConnectInfoAllFields asserts every ConnectInfo field is
// projected correctly from the decoded CONNECT (not just ClientID/KeepAlive).
func TestQAHandshakeConnectInfoAllFields(t *testing.T) {
	a := NewMQTTAdapter(0)
	pc, client := peekPair(t)

	var buf bytes.Buffer
	pkt := &ConnectPacket{
		FixedHeader:     FixedHeader{PacketType: PacketTypeConnect},
		ProtocolName:    ProtocolNameMQTT,
		ProtocolVersion: Version311,
		Flags: ConnectFlags{
			UsernameFlag: true,
			PasswordFlag: true,
			WillFlag:     true,
			WillQoS:      1,
			WillRetain:   true,
			CleanSession: false,
		},
		KeepAlive:   45,
		ClientID:    "qa-full-fields",
		WillTopic:   "will/topic",
		WillMessage: []byte("goodbye"),
		Username:    "alice",
		Password:    []byte("s3cret"),
	}
	if err := NewCodec(0).Encode(&buf, pkt); err != nil {
		t.Fatalf("encode CONNECT: %v", err)
	}
	writeAsync(client, buf.Bytes())

	info, codec, err := a.Handshake(context.Background(), pc)
	if err != nil {
		t.Fatalf("Handshake: %v", err)
	}

	type want struct {
		name string
		got  any
		exp  any
	}
	for _, c := range []want{
		{"Protocol", info.Protocol, "mqtt"},
		{"Version", info.Version, byte(Version311)},
		{"ClientID", info.ClientID, "qa-full-fields"},
		{"Username", info.Username, "alice"},
		{"Password", string(info.Password), "s3cret"},
		{"CleanSession", info.CleanSession, false},
		{"KeepAlive", info.KeepAlive, uint16(45)},
		{"WillTopic", info.WillTopic, "will/topic"},
		{"WillPayload", string(info.WillPayload), "goodbye"},
		{"WillQoS", info.WillQoS, uint8(1)},
		{"WillRetain", info.WillRetain, true},
	} {
		if c.got != c.exp {
			t.Errorf("ConnectInfo.%s = %v, want %v", c.name, c.got, c.exp)
		}
	}
	if _, ok := info.Extensions.(*ConnectPacket); !ok {
		t.Errorf("Extensions = %T, want *ConnectPacket", info.Extensions)
	}
	if codec == nil {
		t.Fatal("Handshake returned a nil codec")
	}
	if sp := codec.Spec(); sp.Version != Version311 {
		t.Errorf("codec.Spec().Version = %d, want %d (version-bound)", sp.Version, Version311)
	}
}

// TestQAHandshakeUnknownVersion verifies an unrecognized version byte still
// decodes (wire legality) but yields no Spec, and the codec self-heals to the
// documented 3.1.1 fallback rather than pretending to be 5.0.
func TestQAHandshakeUnknownVersion(t *testing.T) {
	a := NewMQTTAdapter(0)
	pc, client := peekPair(t)
	writeAsync(client, encodeConnectBytes(t, ProtocolNameMQTT, 6, "badver"))

	info, codec, err := a.Handshake(context.Background(), pc)
	if err != nil {
		t.Fatalf("Handshake on a syntactically valid CONNECT with version 6: %v", err)
	}
	if info.Version != 6 {
		t.Errorf("Version = %d, want 6", info.Version)
	}
	if sp, ok := a.Spec(6); ok {
		t.Errorf("MQTTAdapter.Spec(6) = %+v, ok = true; want ok = false", sp)
	}
	if sp := codec.Spec(); sp.Version != Version311 {
		t.Errorf("unknown-version codec.Spec().Version = %d, want %d fallback", sp.Version, Version311)
	}
	if err := ValidateConnect(&ConnectPacket{
		ProtocolName:    ProtocolNameMQTT,
		ProtocolVersion: 6,
		ClientID:        "badver",
	}); err == nil {
		t.Error("ValidateConnect(version 6) = nil, want rejection")
	}
}

// TestQAIdentifyThenHandshake31And50 confirms Identify never consumes bytes for
// 3.1 and 5.0 either (the existing suite only covers 5.0).
func TestQAIdentifyThenHandshake31And50(t *testing.T) {
	a := NewMQTTAdapter(0)
	for _, tc := range []struct {
		name      string
		protoName string
		version   byte
		id        string
	}{
		{"3.1", ProtocolNameMQIsdp, Version31, "id-31"},
		{"3.1.1", ProtocolNameMQTT, Version311, "id-311"},
		{"5.0", ProtocolNameMQTT, Version50, "id-50"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pc, client := peekPair(t)
			writeAsync(client, encodeConnectBytes(t, tc.protoName, tc.version, tc.id))
			ok, err := a.Identify(pc)
			if err != nil || !ok {
				t.Fatalf("Identify = %v, %v; want true, nil", ok, err)
			}
			info, _, err := a.Handshake(context.Background(), pc)
			if err != nil {
				t.Fatalf("Handshake after Identify: %v", err)
			}
			if info.ClientID != tc.id {
				t.Errorf("ClientID = %q, want %q (Peek must not consume)", info.ClientID, tc.id)
			}
			if info.Version != tc.version {
				t.Errorf("Version = %d, want %d", info.Version, tc.version)
			}
		})
	}
}
