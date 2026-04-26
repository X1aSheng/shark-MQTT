package protocol

import (
	"bytes"
	"io"
)

func (c *Codec) decodeSubscribe(r io.Reader, fh *FixedHeader) (*SubscribePacket, error) {
	packetID, err := readUint16(r)
	if err != nil {
		return nil, err
	}

	bytesRead := 2 // packetID

	// MQTT 5.0 properties
	var props *Properties
	if c.protocolVersion == Version50 && fh.RemainingLength > 2 {
		// Read remaining into buffer for proper property + topic parsing
		remaining := fh.RemainingLength - bytesRead
		data := make([]byte, remaining)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, err
		}
		reader := bytes.NewReader(data)

		props, err = c.decodeProperties(reader)
		if err != nil {
			reader.Reset(data)
		}

		var topics []TopicFilter
		for reader.Len() > 0 {
			topic, err := readStringFromReader(reader)
			if err != nil {
				return nil, err
			}
			optsByte, err := reader.ReadByte()
			if err != nil {
				return nil, err
			}
			topics = append(topics, TopicFilter{
				Topic:             topic,
				QoS:               optsByte & 0x03,
				NoLocal:           (optsByte & 0x04) != 0,
				RetainAsPublished: (optsByte & 0x08) != 0,
				RetainHandling:    (optsByte >> 4) & 0x03,
			})
		}

		return &SubscribePacket{
			FixedHeader: *fh,
			PacketID:    packetID,
			Topics:      topics,
			Properties:  props,
		}, nil
	}

	// MQTT 3.1.1: no properties, just topic filters
	var topics []TopicFilter
	for bytesRead < fh.RemainingLength {
		topic, err := readString(r)
		if err != nil {
			return nil, err
		}
		bytesRead += len(topic) + 2

		qosByte, err := readByte(r)
		if err != nil {
			return nil, err
		}
		bytesRead++
		topics = append(topics, TopicFilter{
			Topic: topic,
			QoS:   qosByte & 0x03,
		})
	}

	return &SubscribePacket{
		FixedHeader: *fh,
		PacketID:    packetID,
		Topics:      topics,
		Properties:  props,
	}, nil
}

func (c *Codec) encodeSubscribe(w io.Writer, pkt *SubscribePacket) error {
	var buf bytes.Buffer

	if err := writeUint16(&buf, pkt.PacketID); err != nil {
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

	for _, topic := range pkt.Topics {
		if err := writeString(&buf, topic.Topic); err != nil {
			return err
		}
		// Subscription Options byte
		var opts byte = topic.QoS & 0x03
		if topic.NoLocal {
			opts |= 0x04
		}
		if topic.RetainAsPublished {
			opts |= 0x08
		}
		opts |= (topic.RetainHandling & 0x03) << 4
		if err := buf.WriteByte(opts); err != nil {
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

func (c *Codec) decodeSubAck(r io.Reader, fh *FixedHeader) (*SubAckPacket, error) {
	packetID, err := readUint16(r)
	if err != nil {
		return nil, err
	}

	var props *Properties
	var reasonCodes []byte

	remaining := fh.RemainingLength - 2
	if remaining > 0 {
		data := make([]byte, remaining)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, err
		}
		reader := bytes.NewReader(data)

		if c.protocolVersion == Version50 {
			props, err = c.decodeProperties(reader)
			if err != nil {
				reader.Reset(data)
			}
		}

		reasonLen := reader.Len()
		if reasonLen > 0 {
			reasonCodes = make([]byte, reasonLen)
			reader.Read(reasonCodes)
		}
	}

	return &SubAckPacket{
		FixedHeader: *fh,
		PacketID:    packetID,
		ReasonCodes: reasonCodes,
		Properties:  props,
	}, nil
}

func (c *Codec) encodeSubAck(w io.Writer, pkt *SubAckPacket) error {
	var buf bytes.Buffer

	if err := writeUint16(&buf, pkt.PacketID); err != nil {
		return err
	}

	if pkt.Properties != nil {
		if err := c.encodeProperties(&buf, pkt.Properties); err != nil {
			return err
		}
	} else if c.protocolVersion == Version50 {
		if err := writeVarInt(&buf, 0); err != nil {
			return err
		}
	}

	if len(pkt.ReasonCodes) > 0 {
		if _, err := buf.Write(pkt.ReasonCodes); err != nil {
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

func (c *Codec) decodeUnsubscribe(r io.Reader, fh *FixedHeader) (*UnsubscribePacket, error) {
	packetID, err := readUint16(r)
	if err != nil {
		return nil, err
	}

	var props *Properties
	var topics []string

	// MQTT 5.0: read properties first
	bytesRead := 2
	if c.protocolVersion == Version50 && fh.RemainingLength > 2 {
		remaining := fh.RemainingLength - bytesRead
		data := make([]byte, remaining)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, err
		}
		reader := bytes.NewReader(data)

		props, err = c.decodeProperties(reader)
		if err != nil {
			reader.Reset(data)
		}

		for reader.Len() > 0 {
			topic, err := readStringFromReader(reader)
			if err != nil {
				break
			}
			topics = append(topics, topic)
		}

		return &UnsubscribePacket{
			FixedHeader: *fh,
			PacketID:    packetID,
			Topics:      topics,
			Properties:  props,
		}, nil
	}

	// MQTT 3.1.1: read topics directly
	for bytesRead < fh.RemainingLength {
		topic, err := readString(r)
		if err != nil {
			return nil, err
		}
		bytesRead += len(topic) + 2
		topics = append(topics, topic)
	}

	return &UnsubscribePacket{
		FixedHeader: *fh,
		PacketID:    packetID,
		Topics:      topics,
		Properties:  props,
	}, nil
}

func (c *Codec) encodeUnsubscribe(w io.Writer, pkt *UnsubscribePacket) error {
	var buf bytes.Buffer

	if err := writeUint16(&buf, pkt.PacketID); err != nil {
		return err
	}

	if pkt.Properties != nil {
		if err := c.encodeProperties(&buf, pkt.Properties); err != nil {
			return err
		}
	} else if c.protocolVersion == Version50 {
		if err := writeVarInt(&buf, 0); err != nil {
			return err
		}
	}

	for _, topic := range pkt.Topics {
		if err := writeString(&buf, topic); err != nil {
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

func (c *Codec) decodeUnsubAck(r io.Reader, fh *FixedHeader) (*UnsubAckPacket, error) {
	packetID, err := readUint16(r)
	if err != nil {
		return nil, err
	}

	var props *Properties
	var reasonCodes []byte

	remaining := fh.RemainingLength - 2
	if remaining > 0 {
		data := make([]byte, remaining)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, err
		}
		reader := bytes.NewReader(data)

		if c.protocolVersion == Version50 {
			props, err = c.decodeProperties(reader)
			if err != nil {
				reader.Reset(data)
			}
		}

		reasonLen := reader.Len()
		if reasonLen > 0 {
			reasonCodes = make([]byte, reasonLen)
			reader.Read(reasonCodes)
		}
	}

	return &UnsubAckPacket{
		FixedHeader: *fh,
		PacketID:    packetID,
		ReasonCodes: reasonCodes,
		Properties:  props,
	}, nil
}

func (c *Codec) encodeUnsubAck(w io.Writer, pkt *UnsubAckPacket) error {
	var buf bytes.Buffer

	if err := writeUint16(&buf, pkt.PacketID); err != nil {
		return err
	}

	if pkt.Properties != nil {
		if err := c.encodeProperties(&buf, pkt.Properties); err != nil {
			return err
		}
	} else if c.protocolVersion == Version50 {
		if err := writeVarInt(&buf, 0); err != nil {
			return err
		}
	}

	if len(pkt.ReasonCodes) > 0 {
		if _, err := buf.Write(pkt.ReasonCodes); err != nil {
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

// --- PingReq ---

func (c *Codec) encodePingReq(w io.Writer, pkt *PingReqPacket) error {
	pkt.RemainingLength = 0
	return c.encodeFixedHeader(w, &pkt.FixedHeader)
}

// --- PingResp ---

func (c *Codec) encodePingResp(w io.Writer, pkt *PingRespPacket) error {
	pkt.RemainingLength = 0
	return c.encodeFixedHeader(w, &pkt.FixedHeader)
}

// --- Disconnect ---

func (c *Codec) decodeDisconnect(r io.Reader, fh *FixedHeader) (*DisconnectPacket, error) {
	var reasonCode byte
	if fh.RemainingLength > 0 {
		var err error
		reasonCode, err = readByte(r)
		if err != nil {
			return nil, err
		}
	}

	var props *Properties
	if fh.RemainingLength > 1 {
		var err error
		props, err = c.decodeProperties(r)
		if err != nil {
			return nil, err
		}
	}

	return &DisconnectPacket{
		FixedHeader: *fh,
		ReasonCode:  reasonCode,
		Properties:  props,
	}, nil
}

func (c *Codec) encodeDisconnect(w io.Writer, pkt *DisconnectPacket) error {
	var buf bytes.Buffer

	if pkt.ReasonCode != 0 || pkt.Properties != nil {
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

// --- Auth ---

func (c *Codec) decodeAuth(r io.Reader, fh *FixedHeader) (*AuthPacket, error) {
	var reasonCode byte
	if fh.RemainingLength > 0 {
		var err error
		reasonCode, err = readByte(r)
		if err != nil {
			return nil, err
		}
	}

	var props *Properties
	if fh.RemainingLength > 1 {
		var err error
		props, err = c.decodeProperties(r)
		if err != nil {
			return nil, err
		}
	}

	return &AuthPacket{
		FixedHeader: *fh,
		ReasonCode:  reasonCode,
		Properties:  props,
	}, nil
}

func (c *Codec) encodeAuth(w io.Writer, pkt *AuthPacket) error {
	var buf bytes.Buffer

	if err := buf.WriteByte(pkt.ReasonCode); err != nil {
		return err
	}

	if pkt.Properties != nil {
		if err := c.encodeProperties(&buf, pkt.Properties); err != nil {
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
