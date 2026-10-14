package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sync"
	"unicode/utf8"

	"github.com/X1aSheng/shark-mqtt/pkg/bufferpool"
)

// decodeReaderPool reuses bytes.Reader instances for decoding packet bodies;
// the readers do not escape the decode call, so pooling avoids a per-packet
// allocation.
var decodeReaderPool = sync.Pool{
	New: func() any { return new(bytes.Reader) },
}

// decodeReader returns a pooled bytes.Reader over data.
func decodeReader(data []byte) *bytes.Reader {
	r := decodeReaderPool.Get().(*bytes.Reader)
	r.Reset(data)
	return r
}

// putDecodeReader returns a decode reader to the pool.
func putDecodeReader(r *bytes.Reader) {
	decodeReaderPool.Put(r)
}

// Codec handles encoding and decoding of MQTT packets.
type Codec struct {
	maxPacketSize   int
	protocolVersion uint8            // set after CONNECT is decoded
	spec            Spec             // capability table for protocolVersion
	pool            *bufferpool.Pool // optional buffer pool for decode buffers
}

// NewCodec creates a new Codec with the specified maximum packet size.
func NewCodec(maxPacketSize int) *Codec {
	if maxPacketSize <= 0 {
		maxPacketSize = 256 * 1024 // 256KB default
	}
	c := &Codec{maxPacketSize: maxPacketSize, pool: bufferpool.Default()}
	c.setVersion(Version311) // default MQTT 3.1.1
	return c
}

// NewCodecForVersion creates a Codec bound to an explicit protocol version.
//
// A codec obtained from NewCodec starts at MQTT 3.1.1 and only learns the
// negotiated version by decoding a CONNECT through the adapter handshake. That
// is the right path for a connection, but it leaves a caller that must encode a
// version-specific frame outside a handshake -- tooling, and tests that assert
// what a v5-only field does on the wire -- with no way to bind the version.
// Pass a version outside SpecOf's table and the codec falls back to 3.1.1.
func NewCodecForVersion(maxPacketSize int, version Version) *Codec {
	c := NewCodec(maxPacketSize)
	c.setVersion(version)
	return c
}

// setVersion records the protocol version and re-derives its capability table.
func (c *Codec) setVersion(v uint8) {
	c.protocolVersion = v
	c.syncSpec()
}

// syncSpec keeps the cached capability table in step with protocolVersion.
// protocolVersion is documented as set via setVersion, but white-box callers
// (and future code) may set the field directly, so Decode/Encode re-derive the
// table on the cheap when it is out of sync (single integer compare on the
// steady-state path).
func (c *Codec) syncSpec() {
	if c.spec.Version == c.protocolVersion {
		return
	}
	if sp, ok := SpecOf(c.protocolVersion); ok {
		c.spec = sp
		return
	}
	c.spec, _ = SpecOf(Version311)
}

// Spec returns the capability table for the codec's current protocol version.
// A codec that has not negotiated a known version falls back to the 3.1.1
// capabilities (the historical default).
func (c *Codec) Spec() Spec {
	c.syncSpec()
	return c.spec
}

// SetPool sets an optional buffer pool for decode operations.
func (c *Codec) SetPool(p *bufferpool.Pool) {
	c.pool = p
}

// Decode reads a packet from the reader and returns the appropriate Packet type.
func (c *Codec) Decode(r io.Reader) (Packet, error) {
	c.syncSpec()
	fh, err := c.decodeFixedHeader(r)
	if err != nil {
		return nil, err
	}
	if err := validateFixedHeaderFlags(fh); err != nil {
		return nil, err
	}

	// Sanity-check RemainingLength before arithmetic to avoid overflow.
	// MQTT 5.0 variable-length encoding allows up to 268,435,455 bytes (256 MiB - 1).
	const maxRemainingLength = 256*1024*1024 - 1
	if fh.RemainingLength < 0 || fh.RemainingLength > maxRemainingLength {
		return nil, ErrPacketTooLarge
	}
	// MQTT spec: max packet size includes the fixed header (1-5 bytes)
	// MQTT spec: max packet size includes the fixed header.
	// Use actual header size instead of hardcoded 5 (max) to avoid
	// rejecting valid short-header packets near the size boundary.
	if fh.RemainingLength+fh.HeaderSize > c.maxPacketSize {
		return nil, ErrPacketTooLarge
	}

	switch fh.PacketType {
	case PacketTypeConnect:
		return c.decodeConnect(r, fh)
	case PacketTypeConnAck:
		return c.decodeConnAck(r, fh)
	case PacketTypePublish:
		return c.decodePublish(r, fh)
	case PacketTypePubAck:
		return c.decodePubAck(r, fh)
	case PacketTypePubRec:
		return c.decodePubRec(r, fh)
	case PacketTypePubRel:
		return c.decodePubRel(r, fh)
	case PacketTypePubComp:
		return c.decodePubComp(r, fh)
	case PacketTypeSubscribe:
		return c.decodeSubscribe(r, fh)
	case PacketTypeSubAck:
		return c.decodeSubAck(r, fh)
	case PacketTypeUnsubscribe:
		return c.decodeUnsubscribe(r, fh)
	case PacketTypeUnsubAck:
		return c.decodeUnsubAck(r, fh)
	case PacketTypePingReq:
		// MQTT-2.2.1: PINGREQ must have Remaining Length 0; leftover bytes
		// would desynchronize the stream (audit).
		if fh.RemainingLength != 0 {
			return nil, ErrMalformedPacket
		}
		return &PingReqPacket{FixedHeader: fh}, nil
	case PacketTypePingResp:
		// MQTT-2.2.1: PINGRESP must have Remaining Length 0 (audit).
		if fh.RemainingLength != 0 {
			return nil, ErrMalformedPacket
		}
		return &PingRespPacket{FixedHeader: fh}, nil
	case PacketTypeDisconnect:
		return c.decodeDisconnect(r, fh)
	case PacketTypeAuth:
		return c.decodeAuth(r, fh)
	default:
		return nil, ErrInvalidPacket
	}
}

// validateFixedHeaderFlags enforces the reserved low nibble of the fixed
// header listed in MQTT 3.1.1 Table 2.2 / MQTT 5.0 Table 2-2 for every packet
// except PUBLISH ([MQTT-2.2.2-1]); a receiver that sees another value must
// close the network connection ([MQTT-2.2.2-2]).
func validateFixedHeaderFlags(fh FixedHeader) error {
	if fh.PacketType == PacketTypePublish {
		// PUBLISH is the only packet type whose flags carry meaning: DUP and
		// RETAIN are free, and only QoS 3 (both QoS bits set) is forbidden.
		if fh.QoS == 3 {
			return ErrInvalidPacket
		}
		return nil
	}

	switch fh.PacketType {
	case PacketTypePubRel, PacketTypeSubscribe, PacketTypeUnsubscribe:
		// Reserved nibble 0b0010: QoS 1, DUP 0, RETAIN 0. QoS and RETAIN are
		// checked, but the DUP bit deliberately is not: the reference
		// implementation retransmits PUBREL / SUBSCRIBE / UNSUBSCRIBE with DUP
		// set (smart-mqtt InflightQueue: PUB_REL_HEADER_DUP,
		// SUBSCRIBE_HEADER_DUP), so rejecting 0b1010 would tear down those
		// peers on every retransmission.
		if fh.QoS != 1 || fh.Retain {
			return ErrInvalidPacket
		}
	default:
		// Reserved nibble 0b0000: DUP, QoS and RETAIN must all be clear.
		// DUP has to be tested explicitly -- folding only QoS and RETAIN into
		// a bitmask would silently accept 0b1000 (e.g. a PINGREQ carrying the
		// DUP bit), which is exactly the hole this branch closes.
		if fh.Dup || fh.QoS != 0 || fh.Retain {
			return ErrInvalidPacket
		}
	}
	return nil
}

// Encode writes a packet to the writer.
func (c *Codec) Encode(w io.Writer, p Packet) error {
	c.syncSpec()
	// Mirror Decode's reserved-nibble check before writing anything, so a
	// malformed fixed header fails here instead of reaching the peer. Without
	// it a wrong nibble (the QoS 2 PUBREC bug: the QoS bit set on a packet
	// whose reserved nibble is 0b0000) compiles, passes the unit tests and is
	// only rejected by the real peer -- which tears the connection down.
	if fh := p.GetFixedHeader(); fh != nil {
		if err := validateFixedHeaderFlags(*fh); err != nil {
			return err
		}
	}
	switch pkt := p.(type) {
	case *ConnectPacket:
		return c.encodeConnect(w, pkt)
	case *ConnAckPacket:
		return c.encodeConnAck(w, pkt)
	case *PublishPacket:
		return c.encodePublish(w, pkt)
	case *PubAckPacket:
		return c.encodePubAck(w, pkt)
	case *PubRecPacket:
		return c.encodePubRec(w, pkt)
	case *PubRelPacket:
		return c.encodePubRel(w, pkt)
	case *PubCompPacket:
		return c.encodePubComp(w, pkt)
	case *SubscribePacket:
		return c.encodeSubscribe(w, pkt)
	case *SubAckPacket:
		return c.encodeSubAck(w, pkt)
	case *UnsubscribePacket:
		return c.encodeUnsubscribe(w, pkt)
	case *UnsubAckPacket:
		return c.encodeUnsubAck(w, pkt)
	case *PingReqPacket:
		return c.encodePingReq(w, pkt)
	case *PingRespPacket:
		return c.encodePingResp(w, pkt)
	case *DisconnectPacket:
		return c.encodeDisconnect(w, pkt)
	case *AuthPacket:
		return c.encodeAuth(w, pkt)
	default:
		return ErrInvalidPacket
	}
}

// --- FixedHeader encoding/decoding ---

func (c *Codec) decodeFixedHeader(r io.Reader) (FixedHeader, error) {
	var buf [5]byte
	if _, err := io.ReadFull(r, buf[:1]); err != nil {
		return FixedHeader{}, err
	}

	fh := FixedHeader{
		PacketType: PacketType((buf[0] >> 4) & 0x0F),
		Dup:        (buf[0] & 0x08) != 0,
		QoS:        (buf[0] >> 1) & 0x03,
		Retain:     (buf[0] & 0x01) != 0,
		HeaderSize: 1, // first byte always read
	}

	// Decode remaining length (variable length encoding). HeaderSize is set so
	// maxPacketSize checks account for the full wire size (P3-1).
	multiplier := 1
	remLenBytes := 0
	for {
		if _, err := io.ReadFull(r, buf[:1]); err != nil {
			return FixedHeader{}, err
		}
		encodedByte := buf[0]
		remLenBytes++
		fh.RemainingLength += int(encodedByte&0x7F) * multiplier
		multiplier *= 128
		fh.HeaderSize++
		if (encodedByte & 0x80) == 0 {
			// MQTT-1.5.5-1: the encoding must use the minimum number of bytes.
			// A multi-byte encoding whose final byte contributes zero is
			// non-minimal (e.g. [0x81, 0x00] encodes 1 in two bytes) (P3-2).
			if remLenBytes > 1 && (encodedByte&0x7F) == 0 {
				return FixedHeader{}, ErrInvalidPacket
			}
			break
		}
		if multiplier > 128*128*128 { // Max 4 bytes for remaining length
			return FixedHeader{}, ErrInvalidPacket
		}
	}

	return fh, nil
}

func (c *Codec) encodeFixedHeader(w io.Writer, fh *FixedHeader) error {
	// Encode remaining length
	var remLen []byte
	remainingLength := fh.RemainingLength
	for {
		encodedByte := byte(remainingLength % 128)
		remainingLength /= 128
		if remainingLength > 0 {
			encodedByte |= 0x80
		}
		remLen = append(remLen, encodedByte)
		if remainingLength == 0 {
			break
		}
	}

	firstByte := byte(fh.PacketType) << 4
	if fh.Dup {
		firstByte |= 0x08
	}
	firstByte |= (fh.QoS & 0x03) << 1
	if fh.Retain {
		firstByte |= 0x01
	}

	buf := make([]byte, 1+len(remLen))
	buf[0] = firstByte
	copy(buf[1:], remLen)
	_, err := w.Write(buf)
	return err
}

// --- UTF-8 string encoding/decoding ---

func readString(r io.Reader, pool *bufferpool.Pool) (string, error) {
	return readStringBounded(r, pool, math.MaxUint16)
}

// readStringBounded is readString with an upper bound on the length prefix.
// The bound lets a caller reject a prefix that cannot possibly fit in the bytes
// left in the current packet *before* any of the string is read off the wire:
// reading the prefix from an unbounded reader and only then comparing it with
// Remaining Length consumes bytes that belong to the next packet, which both
// desynchronizes the stream and makes the reader wait for (and allocate) a
// length the peer never sent. PUBLISH passes its remaining Remaining Length
// here for exactly that reason.
func readStringBounded(r io.Reader, pool *bufferpool.Pool, maxLen int) (string, error) {
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return "", err
	}
	length := int(binary.BigEndian.Uint16(lenBuf[:]))
	if length == 0 {
		return "", nil
	}
	if length > maxLen {
		return "", ErrMalformedPacket
	}
	var buf []byte
	if pool != nil {
		buf = pool.Get(length)
		defer pool.Put(buf)
	} else {
		buf = make([]byte, length)
	}
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	s := string(buf)
	if err := validateUTF8(s); err != nil {
		return "", err
	}
	return s, nil
}

func validateUTF8(s string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("protocol: invalid UTF-8 byte sequence")
	}
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		// MQTT prohibits control characters U+0000-U+001F and U+007F-U+009F
		if r <= 0x001F || (r >= 0x007F && r <= 0x009F) {
			return fmt.Errorf("protocol: control character U+%04X not allowed in MQTT string", r)
		}
		i += size
	}
	return nil
}

func writeString(w io.Writer, s string) error {
	if len(s) > math.MaxUint16 {
		return fmt.Errorf("protocol: string length %d exceeds MQTT max 65535", len(s))
	}
	if len(s) > 0 {
		if err := validateUTF8(s); err != nil {
			return fmt.Errorf("protocol: %w", err)
		}
	}
	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(s)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	if len(s) > 0 {
		_, err := w.Write([]byte(s))
		return err
	}
	return nil
}

// --- Two-byte integer encoding/decoding ---

func readUint16(r io.Reader) (uint16, error) {
	var buf [2]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(buf[:]), nil
}

func writeUint16(w io.Writer, v uint16) error {
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], v)
	_, err := w.Write(buf[:])
	return err
}

// --- Variable Byte Integer (MQTT 5.0) ---

func readVarInt(r io.Reader) (uint32, error) {
	var val uint32
	var multiplier uint32 = 1
	for i := 0; i < 4; i++ {
		var buf [1]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, err
		}
		val += uint32(buf[0]&0x7F) * multiplier
		if (buf[0] & 0x80) == 0 {
			return val, nil
		}
		multiplier *= 128
	}
	return 0, fmt.Errorf("protocol: varint exceeds 4 bytes")
}

func writeVarInt(w io.Writer, val uint32) error {
	for {
		encoded := byte(val % 128)
		val /= 128
		if val > 0 {
			encoded |= 0x80
		}
		if _, err := w.Write([]byte{encoded}); err != nil {
			return err
		}
		if val == 0 {
			break
		}
	}
	return nil
}
