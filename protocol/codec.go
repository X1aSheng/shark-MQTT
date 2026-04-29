package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"unicode/utf8"
)

// Codec handles encoding and decoding of MQTT packets.
type Codec struct {
	maxPacketSize   int
	protocolVersion uint8 // set after CONNECT is decoded
}

// NewCodec creates a new Codec with the specified maximum packet size.
func NewCodec(maxPacketSize int) *Codec {
	if maxPacketSize <= 0 {
		maxPacketSize = 256 * 1024 // 256KB default
	}
	return &Codec{maxPacketSize: maxPacketSize, protocolVersion: 4} // default MQTT 3.1.1
}

// Decode reads a packet from the reader and returns the appropriate Packet type.
func (c *Codec) Decode(r io.Reader) (Packet, error) {
	fh, err := c.decodeFixedHeader(r)
	if err != nil {
		return nil, err
	}

	// Sanity-check RemainingLength before arithmetic to avoid overflow.
	// MQTT 5.0 variable-length encoding allows up to 268,435,455 bytes (256 MiB - 1).
	const maxRemainingLength = 256*1024*1024 - 1
	if fh.RemainingLength < 0 || fh.RemainingLength > maxRemainingLength {
		return nil, ErrPacketTooLarge
	}
	// MQTT spec: max packet size includes the fixed header (1-5 bytes)
	if fh.RemainingLength+5 > c.maxPacketSize {
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
		return &PingReqPacket{FixedHeader: *fh}, nil
	case PacketTypePingResp:
		return &PingRespPacket{FixedHeader: *fh}, nil
	case PacketTypeDisconnect:
		return c.decodeDisconnect(r, fh)
	case PacketTypeAuth:
		return c.decodeAuth(r, fh)
	default:
		return nil, ErrInvalidPacket
	}
}

// Encode writes a packet to the writer.
func (c *Codec) Encode(w io.Writer, p Packet) error {
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

func (c *Codec) decodeFixedHeader(r io.Reader) (*FixedHeader, error) {
	var buf [5]byte
	if _, err := io.ReadFull(r, buf[:1]); err != nil {
		return nil, err
	}

	fh := &FixedHeader{
		PacketType: PacketType((buf[0] >> 4) & 0x0F),
		Dup:        (buf[0] & 0x08) != 0,
		QoS:        (buf[0] >> 1) & 0x03,
		Retain:     (buf[0] & 0x01) != 0,
	}

	// Decode remaining length (variable length encoding)
	multiplier := 1
	for {
		if _, err := io.ReadFull(r, buf[:1]); err != nil {
			return nil, err
		}
		encodedByte := buf[0]
		fh.RemainingLength += int(encodedByte&0x7F) * multiplier
		multiplier *= 128
		if (encodedByte & 0x80) == 0 {
			break
		}
		if multiplier > 128*128*128 { // Max 4 bytes for remaining length
			return nil, ErrInvalidPacket
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

func readString(r io.Reader) (string, error) {
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return "", err
	}
	length := int(binary.BigEndian.Uint16(lenBuf[:]))
	if length == 0 {
		return "", nil
	}
	buf := make([]byte, length)
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
	for i := 0; i < len(s); i++ {
		if s[i] == 0x00 {
			return fmt.Errorf("protocol: null character U+0000 not allowed in MQTT UTF-8 string")
		}
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
