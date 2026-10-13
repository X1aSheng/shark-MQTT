package protocol

import (
	"bytes"
	"fmt"
	"io"
	"math"
)

func (c *Codec) decodeConnect(r io.Reader, fh FixedHeader) (*ConnectPacket, error) {
	buf := make([]byte, fh.RemainingLength)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	reader := decodeReader(buf)
	defer putDecodeReader(reader)

	protoName, err := readString(reader, c.pool)
	if err != nil {
		return nil, err
	}

	protoVer, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	c.setVersion(protoVer)

	connectFlags, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}

	flags := ConnectFlags{
		UsernameFlag: (connectFlags & 0x80) != 0,
		PasswordFlag: (connectFlags & 0x40) != 0,
		WillRetain:   (connectFlags & 0x20) != 0,
		WillQoS:      (connectFlags >> 3) & 0x03,
		WillFlag:     (connectFlags & 0x04) != 0,
		CleanSession: (connectFlags & 0x02) != 0,
		Reserved:     (connectFlags & 0x01) != 0,
	}
	flags.WillTopicFlag = flags.WillFlag

	keepAlive, err := readUint16(reader)
	if err != nil {
		return nil, err
	}

	// MQTT 5.0 properties
	var props, willProps *Properties
	if c.spec.Properties {
		props, err = c.decodeProperties(reader)
		if err != nil {
			return nil, err
		}
	}

	clientID, err := readString(reader, c.pool)
	if err != nil {
		return nil, err
	}

	var willTopic string
	var willMessage []byte
	if flags.WillFlag {
		if c.spec.Properties {
			willProps, err = c.decodeProperties(reader)
			if err != nil {
				return nil, err
			}
		}
		willTopic, err = readString(reader, c.pool)
		if err != nil {
			return nil, err
		}
		willLen, err := readUint16(reader)
		if err != nil {
			return nil, err
		}
		if willLen > 0 {
			willMessage = make([]byte, willLen)
			if _, err := io.ReadFull(reader, willMessage); err != nil {
				return nil, err
			}
		}
	}

	var username string
	var password []byte
	if flags.UsernameFlag {
		username, err = readString(reader, c.pool)
		if err != nil {
			return nil, err
		}
	}
	if flags.PasswordFlag {
		pwLen, err := readUint16(reader)
		if err != nil {
			return nil, err
		}
		if pwLen > 0 {
			password = make([]byte, pwLen)
			if _, err := io.ReadFull(reader, password); err != nil {
				return nil, err
			}
		}
	}

	// MQTT-2.2.1: every CONNECT payload byte must belong to a parsed field;
	// silently accepting trailing garbage lets malformed frames through
	// (audit).
	if reader.Len() != 0 {
		return nil, ErrMalformedPacket
	}

	pkt := &ConnectPacket{
		FixedHeader:     fh,
		ProtocolName:    protoName,
		ProtocolVersion: protoVer,
		Flags:           flags,
		KeepAlive:       keepAlive,
		ClientID:        clientID,
		WillTopic:       willTopic,
		WillMessage:     willMessage,
		Username:        username,
		Password:        password,
		Properties:      props,
		WillProperties:  willProps,
	}
	return pkt, nil
}

// ValidateConnect validates the CONNECT packet. Most rules are shared across
// versions (protocol name/version pairing, reserved flag, will flags, password
// flag); the version-specific ClientID rules are applied through the Spec
// capability table: MQTT 3.1 rejects a zero-length ClientID and caps the ID at
// 23 characters, while MQTT 3.1.1 / 5.0 allow an empty ID only with Clean
// Session/Clean Start = 1. Returns an error describing the first violation, or
// nil if valid.
func ValidateConnect(pkt *ConnectPacket) error {
	// Check protocol name (MQTT 3.1.1 §3.1.2.1)
	if pkt.ProtocolName != ProtocolNameMQTT && pkt.ProtocolName != ProtocolNameMQIsdp {
		return fmt.Errorf("protocol: invalid protocol name %q", pkt.ProtocolName)
	}

	// Check protocol version (MQTT 3.1.1 §3.1.2.2)
	if pkt.ProtocolVersion != Version31 && pkt.ProtocolVersion != Version311 && pkt.ProtocolVersion != Version50 {
		return fmt.Errorf("protocol: unsupported protocol version %d", pkt.ProtocolVersion)
	}
	switch pkt.ProtocolVersion {
	case Version31:
		if pkt.ProtocolName != ProtocolNameMQIsdp {
			return fmt.Errorf("protocol: MQTT 3.1 requires protocol name %q", ProtocolNameMQIsdp)
		}
	case Version311, Version50:
		if pkt.ProtocolName != ProtocolNameMQTT {
			return fmt.Errorf("protocol: MQTT %d requires protocol name %q", pkt.ProtocolVersion, ProtocolNameMQTT)
		}
	}

	// Reserved flag must be 0 (MQTT 3.1.1 §3.1.2.3)
	if pkt.Flags.Reserved {
		return fmt.Errorf("protocol: reserved flag must be 0")
	}

	// If WillFlag is set, WillQoS must not be 3 (MQTT 3.1.1 §3.1.2.6)
	if pkt.Flags.WillFlag && pkt.Flags.WillQoS > 2 {
		return fmt.Errorf("protocol: invalid will QoS %d (must be 0, 1, or 2)", pkt.Flags.WillQoS)
	}
	if !pkt.Flags.WillFlag {
		if pkt.Flags.WillQoS != 0 {
			return fmt.Errorf("protocol: will QoS must be 0 when will flag is not set")
		}
		if pkt.Flags.WillRetain {
			return fmt.Errorf("protocol: will retain must be 0 when will flag is not set")
		}
	}

	// If PasswordFlag is set, UsernameFlag must also be set (MQTT 3.1.1 §3.1.2.5)
	if pkt.Flags.PasswordFlag && !pkt.Flags.UsernameFlag {
		return fmt.Errorf("protocol: password flag set but username flag not set")
	}

	// ClientID rules are version-specific (Spec):
	//   - MQTT 3.1 forbids a zero-length ClientID entirely, and caps the
	//     ClientID at 23 characters (MQTT 3.1 §3.1.3.1).
	//   - MQTT 3.1.1 / 5.0 allow a zero-length ClientID only when Clean
	//     Session / Clean Start is 1; the server then assigns an identifier
	//     (MQTT 3.1.1 §3.1.3.1, MQTT 5.0 §3.1.3.1).
	// The protocol version has already been validated against the Spec table
	// above, so sp is non-zero here.
	sp, _ := SpecOf(pkt.ProtocolVersion)
	if len(pkt.ClientID) == 0 {
		if !sp.AllowEmptyID {
			return fmt.Errorf("protocol: MQTT %d does not allow a zero-length client ID", pkt.ProtocolVersion)
		}
		if !pkt.Flags.CleanSession {
			return fmt.Errorf("protocol: zero-length client ID requires clean session")
		}
	}
	if sp.MaxClientIDLen > 0 && len(pkt.ClientID) > sp.MaxClientIDLen {
		return fmt.Errorf("protocol: client ID length %d exceeds MQTT %d maximum %d",
			len(pkt.ClientID), pkt.ProtocolVersion, sp.MaxClientIDLen)
	}

	// WillFlag: if set, WillTopic must not be empty (spec recommends but doesn't require)
	if pkt.Flags.WillFlag && len(pkt.WillTopic) == 0 {
		return fmt.Errorf("protocol: will flag set but will topic is empty")
	}

	return nil
}

func (c *Codec) encodeConnect(w io.Writer, pkt *ConnectPacket) error {
	var buf bytes.Buffer

	// Track protocol version for subsequent encode/decode
	c.setVersion(pkt.ProtocolVersion)

	// Protocol name
	if err := writeString(&buf, pkt.ProtocolName); err != nil {
		return err
	}

	// Protocol version
	if err := buf.WriteByte(pkt.ProtocolVersion); err != nil {
		return err
	}

	// Connect flags
	var flags byte
	if pkt.Flags.UsernameFlag {
		flags |= 0x80
	}
	if pkt.Flags.PasswordFlag {
		flags |= 0x40
	}
	if pkt.Flags.WillRetain {
		flags |= 0x20
	}
	if pkt.Flags.WillFlag {
		flags |= 0x04
		flags |= (pkt.Flags.WillQoS & 0x03) << 3
	}
	if pkt.Flags.CleanSession {
		flags |= 0x02
	}
	if err := buf.WriteByte(flags); err != nil {
		return err
	}

	// Keep alive
	if err := writeUint16(&buf, pkt.KeepAlive); err != nil {
		return err
	}

	// MQTT 5.0 properties. The section is always present in a v5 CONNECT, so a
	// zero length is written when the client sent none.
	if err := c.writePropertySection(&buf, pkt.Properties); err != nil {
		return err
	}

	// Client ID
	if err := writeString(&buf, pkt.ClientID); err != nil {
		return err
	}

	// Will
	if pkt.Flags.WillFlag {
		// The will carries a property section of its own, also always present
		// in a v5 CONNECT.
		if err := c.writePropertySection(&buf, pkt.WillProperties); err != nil {
			return err
		}
		if err := writeString(&buf, pkt.WillTopic); err != nil {
			return err
		}
		if len(pkt.WillMessage) > math.MaxUint16 {
			// Previously the length was silently truncated to uint16 (audit).
			return fmt.Errorf("protocol: will message length %d exceeds MQTT max 65535", len(pkt.WillMessage))
		}
		if err := writeUint16(&buf, uint16(len(pkt.WillMessage))); err != nil {
			return err
		}
		if len(pkt.WillMessage) > 0 {
			if _, err := buf.Write(pkt.WillMessage); err != nil {
				return err
			}
		}
	}

	// Username
	if pkt.Flags.UsernameFlag {
		if err := writeString(&buf, pkt.Username); err != nil {
			return err
		}
	}

	// Password
	if pkt.Flags.PasswordFlag {
		if len(pkt.Password) > math.MaxUint16 {
			// Previously the length was silently truncated to uint16 (audit).
			return fmt.Errorf("protocol: password length %d exceeds MQTT max 65535", len(pkt.Password))
		}
		if err := writeUint16(&buf, uint16(len(pkt.Password))); err != nil {
			return err
		}
		if len(pkt.Password) > 0 {
			if _, err := buf.Write(pkt.Password); err != nil {
				return err
			}
		}
	}

	pkt.PacketType = PacketTypeConnect
	pkt.RemainingLength = buf.Len()
	if err := c.encodeFixedHeader(w, &pkt.FixedHeader); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

func (c *Codec) decodeConnAck(r io.Reader, fh FixedHeader) (*ConnAckPacket, error) {
	buf := make([]byte, fh.RemainingLength)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	reader := decodeReader(buf)
	defer putDecodeReader(reader)

	// Session present flag (only first bit of first byte)
	spFlag, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	sessionPresent := (spFlag & 0x01) != 0

	reasonCode, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}

	var props *Properties
	// MQTT 3.1.1 CONNACK has exactly 2 payload bytes (session present + reason
	// code); anything else is malformed (P3-3).
	if !c.spec.Properties && fh.RemainingLength != 2 {
		return nil, ErrMalformedPacket
	}
	// MQTT 5.0: remaining bytes (if any) are properties, and all of them must
	// be consumed (NEW-18).
	if c.spec.Properties && fh.RemainingLength > 2 {
		props, err = c.decodeProperties(reader)
		if err != nil {
			return nil, err
		}
		if reader.Len() != 0 {
			return nil, ErrMalformedPacket
		}
	}

	return &ConnAckPacket{
		FixedHeader:    fh,
		ReasonCode:     reasonCode,
		SessionPresent: sessionPresent,
		Properties:     props,
	}, nil
}

func (c *Codec) encodeConnAck(w io.Writer, pkt *ConnAckPacket) error {
	var buf bytes.Buffer

	// Session present + reserved. MQTT 3.1 has no Session Present flag: the
	// first CONNACK byte is reserved and MUST be 0 (Spec.ConnAckSessionPresent
	// is false for 3.1, true for 3.1.1 / 5.0).
	spByte := byte(0)
	if pkt.SessionPresent && c.spec.ConnAckSessionPresent {
		spByte |= 0x01
	}
	if err := buf.WriteByte(spByte); err != nil {
		return err
	}

	// Reason code
	if err := buf.WriteByte(pkt.ReasonCode); err != nil {
		return err
	}

	// Properties (MQTT 5.0). The section is always present in a v5 CONNACK and
	// never in a 3.1/3.1.1 one -- the decoder enforces that by requiring
	// Remaining Length 2 there -- so this must be version-gated like every
	// other property encoder. It previously wrote whatever the caller set,
	// which put v5 property bytes into a v3.1.1 CONNACK.
	if err := c.writePropertySection(&buf, pkt.Properties); err != nil {
		return err
	}

	pkt.RemainingLength = buf.Len()
	if err := c.encodeFixedHeader(w, &pkt.FixedHeader); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}
