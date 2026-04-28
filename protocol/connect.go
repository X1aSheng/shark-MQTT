package protocol

import (
	"bytes"
	"io"
)

func (c *Codec) decodeConnect(r io.Reader, fh *FixedHeader) (*ConnectPacket, error) {
	buf := make([]byte, fh.RemainingLength)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	reader := bytes.NewReader(buf)

	protoName, err := readString(reader)
	if err != nil {
		return nil, err
	}

	protoVer, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	c.protocolVersion = protoVer

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
	if protoVer == Version50 {
		props, err = c.decodeProperties(reader)
		if err != nil {
			return nil, err
		}
	}

	clientID, err := readString(reader)
	if err != nil {
		return nil, err
	}

	var willTopic string
	var willMessage []byte
	if flags.WillFlag {
		if protoVer == Version50 {
			willProps, err = c.decodeProperties(reader)
			if err != nil {
				return nil, err
			}
		}
		willTopic, err = readString(reader)
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
		username, err = readString(reader)
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

	pkt := &ConnectPacket{
		FixedHeader:     *fh,
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

func (c *Codec) encodeConnect(w io.Writer, pkt *ConnectPacket) error {
	var buf bytes.Buffer

	// Track protocol version for subsequent encode/decode
	c.protocolVersion = pkt.ProtocolVersion

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

	// MQTT 5.0 properties
	if pkt.ProtocolVersion == Version50 {
		if pkt.Properties != nil {
			if err := c.encodeProperties(&buf, pkt.Properties); err != nil {
				return err
			}
		} else {
			// MQTT 5.0 requires Property Length field (0 if no properties)
			if err := writeVarInt(&buf, 0); err != nil {
				return err
			}
		}
	}

	// Client ID
	if err := writeString(&buf, pkt.ClientID); err != nil {
		return err
	}

	// Will
	if pkt.Flags.WillFlag {
		if pkt.ProtocolVersion == Version50 {
			if pkt.WillProperties != nil {
				if err := c.encodeProperties(&buf, pkt.WillProperties); err != nil {
					return err
				}
			} else {
				// MQTT 5.0 requires Property Length field for will properties (0 if none)
				if err := writeVarInt(&buf, 0); err != nil {
					return err
				}
			}
		}
		if err := writeString(&buf, pkt.WillTopic); err != nil {
			return err
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
		if err := writeUint16(&buf, uint16(len(pkt.Password))); err != nil {
			return err
		}
		if len(pkt.Password) > 0 {
			if _, err := buf.Write(pkt.Password); err != nil {
				return err
			}
		}
	}

	pkt.RemainingLength = buf.Len()
	if err := c.encodeFixedHeader(w, &pkt.FixedHeader); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

func (c *Codec) decodeConnAck(r io.Reader, fh *FixedHeader) (*ConnAckPacket, error) {
	buf := make([]byte, fh.RemainingLength)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	reader := bytes.NewReader(buf)

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
	// If remaining length > 2, there are properties
	if fh.RemainingLength > 2 {
		props, err = c.decodeProperties(reader)
		if err != nil {
			return nil, err
		}
	}

	return &ConnAckPacket{
		FixedHeader:    *fh,
		ReasonCode:     reasonCode,
		SessionPresent: sessionPresent,
		Properties:     props,
	}, nil
}

func (c *Codec) encodeConnAck(w io.Writer, pkt *ConnAckPacket) error {
	var buf bytes.Buffer

	// Session present + reserved
	spByte := byte(0)
	if pkt.SessionPresent {
		spByte |= 0x01
	}
	if err := buf.WriteByte(spByte); err != nil {
		return err
	}

	// Reason code
	if err := buf.WriteByte(pkt.ReasonCode); err != nil {
		return err
	}

	// Properties (MQTT 5.0)
	if pkt.Properties != nil {
		if err := c.encodeProperties(&buf, pkt.Properties); err != nil {
			return err
		}
	} else if c.protocolVersion == Version50 {
		if err := writeVarInt(&buf, 0); err != nil {
			return err
		}
	}

	pkt.RemainingLength = buf.Len()
	if err := c.encodeFixedHeader(w, &pkt.FixedHeader); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}
