package protocol

import (
	"bytes"
	"fmt"
	"io"
)

func (c *Codec) decodePublish(r io.Reader, fh *FixedHeader) (*PublishPacket, error) {
	topic, err := readString(r, c.pool)
	if err != nil {
		return nil, err
	}
	if !ValidatePublishTopic(topic) {
		return nil, ErrMalformedPacket
	}

	var packetID uint16
	if fh.QoS > 0 {
		packetID, err = readUint16(r)
		if err != nil {
			return nil, err
		}
		if packetID == 0 {
			return nil, ErrMalformedPacket
		}
	}

	// Read remaining bytes into a buffer so both protocol versions use the
	// same parsing path. For 5.0: properties + payload. For 3.1.1: payload only.
	headerBytes := 2 + len(topic) // topic length prefix + topic string
	if fh.QoS > 0 {
		headerBytes += 2 // packet ID
	}
	remaining := fh.RemainingLength - headerBytes
	if remaining < 0 {
		return nil, ErrMalformedPacket
	}

	data := make([]byte, remaining)
	if remaining > 0 {
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, err
		}
	}
	reader := bytes.NewReader(data)

	var props *Properties
	if c.protocolVersion == Version50 {
		props, err = c.decodeProperties(reader)
		if err != nil {
			return nil, fmt.Errorf("decode properties: %w", err)
		}
	}

	// Whatever remains is the payload
	payloadLen := reader.Len()
	var payload []byte
	if payloadLen > 0 {
		payload = make([]byte, payloadLen)
		if _, err := reader.Read(payload); err != nil {
			return nil, err
		}
	}

	return &PublishPacket{
		FixedHeader: *fh,
		PacketID:    packetID,
		Topic:       topic,
		Payload:     payload,
		Properties:  props,
	}, nil
}

func (c *Codec) encodePublish(w io.Writer, pkt *PublishPacket) error {
	var buf bytes.Buffer

	if !ValidatePublishTopic(pkt.Topic) {
		return ErrMalformedPacket
	}
	if pkt.QoS == 0 && pkt.PacketID != 0 {
		return ErrMalformedPacket
	}
	if pkt.QoS > 0 && pkt.PacketID == 0 {
		return ErrMalformedPacket
	}

	if err := writeString(&buf, pkt.Topic); err != nil {
		return err
	}

	if pkt.QoS > 0 {
		if err := writeUint16(&buf, pkt.PacketID); err != nil {
			return err
		}
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

	if len(pkt.Payload) > 0 {
		if _, err := buf.Write(pkt.Payload); err != nil {
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

// --- PubAck ---

func (c *Codec) decodePubAck(r io.Reader, fh *FixedHeader) (*PubAckPacket, error) {
	packetID, reasonCode, props, err := c.decodeAckFields(r, fh)
	if err != nil {
		return nil, err
	}

	return &PubAckPacket{
		FixedHeader: *fh,
		PacketID:    packetID,
		ReasonCode:  reasonCode,
		Properties:  props,
	}, nil
}

func (c *Codec) encodePubAck(w io.Writer, pkt *PubAckPacket) error {
	buf, err := c.encodeAckFields(pkt.PacketID, pkt.ReasonCode, pkt.Properties)
	if err != nil {
		return err
	}
	pkt.RemainingLength = len(buf)
	if err := c.encodeFixedHeader(w, &pkt.FixedHeader); err != nil {
		return err
	}
	_, err = w.Write(buf)
	return err
}

// --- PubRec ---

func (c *Codec) decodePubRec(r io.Reader, fh *FixedHeader) (*PubRecPacket, error) {
	packetID, reasonCode, props, err := c.decodeAckFields(r, fh)
	if err != nil {
		return nil, err
	}

	return &PubRecPacket{
		FixedHeader: *fh,
		PacketID:    packetID,
		ReasonCode:  reasonCode,
		Properties:  props,
	}, nil
}

func (c *Codec) encodePubRec(w io.Writer, pkt *PubRecPacket) error {
	buf, err := c.encodeAckFields(pkt.PacketID, pkt.ReasonCode, pkt.Properties)
	if err != nil {
		return err
	}
	pkt.RemainingLength = len(buf)
	if err := c.encodeFixedHeader(w, &pkt.FixedHeader); err != nil {
		return err
	}
	_, err = w.Write(buf)
	return err
}

// --- PubRel ---

func (c *Codec) decodePubRel(r io.Reader, fh *FixedHeader) (*PubRelPacket, error) {
	packetID, reasonCode, props, err := c.decodeAckFields(r, fh)
	if err != nil {
		return nil, err
	}

	return &PubRelPacket{
		FixedHeader: *fh,
		PacketID:    packetID,
		ReasonCode:  reasonCode,
		Properties:  props,
	}, nil
}

func (c *Codec) encodePubRel(w io.Writer, pkt *PubRelPacket) error {
	buf, err := c.encodeAckFields(pkt.PacketID, pkt.ReasonCode, pkt.Properties)
	if err != nil {
		return err
	}

	pkt.QoS = 1
	pkt.FixedHeader.Retain = false
	pkt.RemainingLength = len(buf)
	if err := c.encodeFixedHeader(w, &pkt.FixedHeader); err != nil {
		return err
	}
	_, err = w.Write(buf)
	return err
}

// --- PubComp ---

func (c *Codec) decodePubComp(r io.Reader, fh *FixedHeader) (*PubCompPacket, error) {
	packetID, reasonCode, props, err := c.decodeAckFields(r, fh)
	if err != nil {
		return nil, err
	}

	return &PubCompPacket{
		FixedHeader: *fh,
		PacketID:    packetID,
		ReasonCode:  reasonCode,
		Properties:  props,
	}, nil
}

func (c *Codec) encodePubComp(w io.Writer, pkt *PubCompPacket) error {
	buf, err := c.encodeAckFields(pkt.PacketID, pkt.ReasonCode, pkt.Properties)
	if err != nil {
		return err
	}
	pkt.RemainingLength = len(buf)
	if err := c.encodeFixedHeader(w, &pkt.FixedHeader); err != nil {
		return err
	}
	_, err = w.Write(buf)
	return err
}

func (c *Codec) decodeAckFields(r io.Reader, fh *FixedHeader) (uint16, byte, *Properties, error) {
	if fh.RemainingLength < 2 {
		return 0, 0, nil, ErrMalformedPacket
	}
	if c.protocolVersion != Version50 && fh.RemainingLength != 2 {
		return 0, 0, nil, ErrMalformedPacket
	}

	data := make([]byte, fh.RemainingLength)
	if _, err := io.ReadFull(r, data); err != nil {
		return 0, 0, nil, err
	}
	reader := bytes.NewReader(data)

	packetID, err := readUint16FromReader(reader)
	if err != nil {
		return 0, 0, nil, err
	}
	if packetID == 0 {
		return 0, 0, nil, ErrMalformedPacket
	}

	reasonCode := byte(ReasonCodeSuccess)
	if reader.Len() > 0 {
		reasonCode, err = readByte(reader)
		if err != nil {
			return 0, 0, nil, err
		}
	}

	var props *Properties
	if reader.Len() > 0 {
		props, err = c.decodeProperties(reader)
		if err != nil {
			return 0, 0, nil, err
		}
		if reader.Len() != 0 {
			return 0, 0, nil, ErrMalformedPacket
		}
	}

	return packetID, reasonCode, props, nil
}

func (c *Codec) encodeAckFields(packetID uint16, reasonCode byte, props *Properties) ([]byte, error) {
	if packetID == 0 {
		return nil, ErrMalformedPacket
	}
	if c.protocolVersion != Version50 && (reasonCode != ReasonCodeSuccess || props != nil) {
		return nil, ErrMalformedPacket
	}

	var buf bytes.Buffer
	if err := writeUint16(&buf, packetID); err != nil {
		return nil, err
	}
	if reasonCode != ReasonCodeSuccess || props != nil {
		if err := buf.WriteByte(reasonCode); err != nil {
			return nil, err
		}
		if props != nil {
			if err := c.encodeProperties(&buf, props); err != nil {
				return nil, err
			}
		}
	}
	return buf.Bytes(), nil
}
