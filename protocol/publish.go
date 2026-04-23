package protocol

import (
	"bytes"
	"io"
)

func (c *Codec) decodePublish(r io.Reader, fh *FixedHeader) (*PublishPacket, error) {
	topic, err := readString(r)
	if err != nil {
		return nil, err
	}

	var packetID uint16
	if fh.QoS > 0 {
		packetID, err = readUint16(r)
		if err != nil {
			return nil, err
		}
	}

	// MQTT 5.0 properties
	var props *Properties
	if fh.RemainingLength > len(topic)+2 { // +2 for topic length
		if fh.QoS > 0 {
			// Already read packetID
		}
		// This is simplified; full implementation would track bytes read
	}

	payload := make([]byte, fh.RemainingLength-len(topic)-2)
	if fh.QoS > 0 {
		payload = make([]byte, fh.RemainingLength-len(topic)-4)
	}
	if len(payload) > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
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

	if err := writeString(&buf, pkt.Topic); err != nil {
		return err
	}

	if pkt.FixedHeader.QoS > 0 {
		if err := writeUint16(&buf, pkt.PacketID); err != nil {
			return err
		}
	}

	// Properties (MQTT 5.0)
	if pkt.Properties != nil {
		if err := c.encodeProperties(&buf, pkt.Properties); err != nil {
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
	packetID, err := readUint16(r)
	if err != nil {
		return nil, err
	}

	var reasonCode byte = ReasonCodeSuccess
	if fh.RemainingLength > 2 {
		reasonCode, err = readByte(r)
		if err != nil {
			return nil, err
		}
	}

	var props *Properties
	if fh.RemainingLength > 3 {
		props, err = c.decodeProperties(r)
		if err != nil {
			return nil, err
		}
	}

	return &PubAckPacket{
		FixedHeader: *fh,
		PacketID:    packetID,
		ReasonCode:  reasonCode,
		Properties:  props,
	}, nil
}

func (c *Codec) encodePubAck(w io.Writer, pkt *PubAckPacket) error {
	var buf bytes.Buffer

	if err := writeUint16(&buf, pkt.PacketID); err != nil {
		return err
	}

	if pkt.ReasonCode != ReasonCodeSuccess || pkt.Properties != nil {
		if err := buf.WriteByte(pkt.ReasonCode); err != nil {
			return err
		}
		if pkt.Properties != nil {
			if err := c.encodeProperties(&buf, pkt.Properties); err != nil {
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

// --- PubRec ---

func (c *Codec) decodePubRec(r io.Reader, fh *FixedHeader) (*PubRecPacket, error) {
	packetID, err := readUint16(r)
	if err != nil {
		return nil, err
	}

	var reasonCode byte = ReasonCodeSuccess
	if fh.RemainingLength > 2 {
		reasonCode, err = readByte(r)
		if err != nil {
			return nil, err
		}
	}

	var props *Properties
	if fh.RemainingLength > 3 {
		props, err = c.decodeProperties(r)
		if err != nil {
			return nil, err
		}
	}

	return &PubRecPacket{
		FixedHeader: *fh,
		PacketID:    packetID,
		ReasonCode:  reasonCode,
		Properties:  props,
	}, nil
}

func (c *Codec) encodePubRec(w io.Writer, pkt *PubRecPacket) error {
	var buf bytes.Buffer

	if err := writeUint16(&buf, pkt.PacketID); err != nil {
		return err
	}

	if pkt.ReasonCode != ReasonCodeSuccess || pkt.Properties != nil {
		if err := buf.WriteByte(pkt.ReasonCode); err != nil {
			return err
		}
		if pkt.Properties != nil {
			if err := c.encodeProperties(&buf, pkt.Properties); err != nil {
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

// --- PubRel ---

func (c *Codec) decodePubRel(r io.Reader, fh *FixedHeader) (*PubRelPacket, error) {
	packetID, err := readUint16(r)
	if err != nil {
		return nil, err
	}

	var reasonCode byte = ReasonCodeSuccess
	if fh.RemainingLength > 2 {
		reasonCode, err = readByte(r)
		if err != nil {
			return nil, err
		}
	}

	var props *Properties
	if fh.RemainingLength > 3 {
		props, err = c.decodeProperties(r)
		if err != nil {
			return nil, err
		}
	}

	return &PubRelPacket{
		FixedHeader: *fh,
		PacketID:    packetID,
		ReasonCode:  reasonCode,
		Properties:  props,
	}, nil
}

func (c *Codec) encodePubRel(w io.Writer, pkt *PubRelPacket) error {
	var buf bytes.Buffer

	if err := writeUint16(&buf, pkt.PacketID); err != nil {
		return err
	}

	if pkt.ReasonCode != ReasonCodeSuccess || pkt.Properties != nil {
		if err := buf.WriteByte(pkt.ReasonCode); err != nil {
			return err
		}
		if pkt.Properties != nil {
			if err := c.encodeProperties(&buf, pkt.Properties); err != nil {
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

// --- PubComp ---

func (c *Codec) decodePubComp(r io.Reader, fh *FixedHeader) (*PubCompPacket, error) {
	packetID, err := readUint16(r)
	if err != nil {
		return nil, err
	}

	var reasonCode byte = ReasonCodeSuccess
	if fh.RemainingLength > 2 {
		reasonCode, err = readByte(r)
		if err != nil {
			return nil, err
		}
	}

	var props *Properties
	if fh.RemainingLength > 3 {
		props, err = c.decodeProperties(r)
		if err != nil {
			return nil, err
		}
	}

	return &PubCompPacket{
		FixedHeader: *fh,
		PacketID:    packetID,
		ReasonCode:  reasonCode,
		Properties:  props,
	}, nil
}

func (c *Codec) encodePubComp(w io.Writer, pkt *PubCompPacket) error {
	var buf bytes.Buffer

	if err := writeUint16(&buf, pkt.PacketID); err != nil {
		return err
	}

	if pkt.ReasonCode != ReasonCodeSuccess || pkt.Properties != nil {
		if err := buf.WriteByte(pkt.ReasonCode); err != nil {
			return err
		}
		if pkt.Properties != nil {
			if err := c.encodeProperties(&buf, pkt.Properties); err != nil {
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
