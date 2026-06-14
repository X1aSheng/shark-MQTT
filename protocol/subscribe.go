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
	if packetID == 0 {
		return nil, ErrMalformedPacket
	}

	remaining := fh.RemainingLength - 2
	if remaining < 0 {
		return nil, ErrMalformedPacket
	}

	data := make([]byte, remaining)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	reader := bytes.NewReader(data)

	var props *Properties
	if c.protocolVersion == Version50 {
		props, err = c.decodeProperties(reader)
		if err != nil {
			return nil, err
		}
	}

	// Extract SubscriptionIdentifier from packet-level properties (MQTT 5.0 3.8.2.1.2).
	// When present, it applies to ALL topic filters in this SUBSCRIBE.
	var subID *uint32
	if props != nil && props.SubscriptionIdentifier != nil {
		subID = props.SubscriptionIdentifier
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
		if optsByte&0xC0 != 0 || optsByte&0x03 == 3 || (optsByte>>4)&0x03 == 3 {
			return nil, ErrMalformedPacket
		}
		topics = append(topics, TopicFilter{
			Topic:                  topic,
			QoS:                    optsByte & 0x03,
			NoLocal:                (optsByte & 0x04) != 0,
			RetainAsPublished:      (optsByte & 0x08) != 0,
			RetainHandling:         (optsByte >> 4) & 0x03,
			SubscriptionIdentifier: subID,
		})
	}
	if len(topics) == 0 {
		return nil, ErrMalformedPacket
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

	if pkt.PacketID == 0 {
		return ErrMalformedPacket
	}
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
		if topic.QoS > 2 || topic.RetainHandling > 2 {
			return ErrMalformedPacket
		}
		if err := writeString(&buf, topic.Topic); err != nil {
			return err
		}
		// Subscription Options byte
		opts := topic.QoS & 0x03
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
	if len(pkt.Topics) == 0 {
		return ErrMalformedPacket
	}

	pkt.FixedHeader.QoS = 1
	pkt.FixedHeader.Retain = false
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
	if packetID == 0 {
		return nil, ErrMalformedPacket
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
				return nil, err
			}
		}

		reasonLen := reader.Len()
		if reasonLen > 0 {
			reasonCodes = make([]byte, reasonLen)
			if _, err := io.ReadFull(reader, reasonCodes); err != nil {
				return nil, err
			}
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

	if pkt.PacketID == 0 {
		return ErrMalformedPacket
	}
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
	if packetID == 0 {
		return nil, ErrMalformedPacket
	}

	remaining := fh.RemainingLength - 2
	if remaining < 0 {
		return nil, ErrMalformedPacket
	}

	data := make([]byte, remaining)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	reader := bytes.NewReader(data)

	var props *Properties
	if c.protocolVersion == Version50 {
		props, err = c.decodeProperties(reader)
		if err != nil {
			return nil, err
		}
	}

	var topics []string
	for reader.Len() > 0 {
		topic, err := readStringFromReader(reader)
		if err != nil {
			return nil, err
		}
		topics = append(topics, topic)
	}
	if len(topics) == 0 {
		return nil, ErrMalformedPacket
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

	if pkt.PacketID == 0 {
		return ErrMalformedPacket
	}
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
	if len(pkt.Topics) == 0 {
		return ErrMalformedPacket
	}

	pkt.FixedHeader.QoS = 1
	pkt.FixedHeader.Retain = false
	pkt.RemainingLength = buf.Len()
	if err := c.encodeFixedHeader(w, &pkt.FixedHeader); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

func (c *Codec) decodeUnsubAck(r io.Reader, fh *FixedHeader) (*UnsubAckPacket, error) {
	if c.protocolVersion != Version50 && fh.RemainingLength != 2 {
		return nil, ErrMalformedPacket
	}

	packetID, err := readUint16(r)
	if err != nil {
		return nil, err
	}
	if packetID == 0 {
		return nil, ErrMalformedPacket
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
				return nil, err
			}
		}

		reasonLen := reader.Len()
		if reasonLen > 0 {
			reasonCodes = make([]byte, reasonLen)
			if _, err := io.ReadFull(reader, reasonCodes); err != nil {
				return nil, err
			}
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
	if c.protocolVersion != Version50 && (len(pkt.ReasonCodes) > 0 || pkt.Properties != nil) {
		return ErrMalformedPacket
	}

	var buf bytes.Buffer

	if pkt.PacketID == 0 {
		return ErrMalformedPacket
	}
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
	if c.protocolVersion != Version50 && fh.RemainingLength > 0 {
		return nil, ErrMalformedPacket
	}

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
	if c.protocolVersion != Version50 && (pkt.ReasonCode != 0 || pkt.Properties != nil) {
		return ErrMalformedPacket
	}

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
	if c.protocolVersion != Version50 {
		return nil, ErrInvalidPacket
	}

	var reasonCode byte
	if fh.RemainingLength > 0 {
		var err error
		reasonCode, err = readByte(r)
		if err != nil {
			return nil, err
		}
	}
	if !validAuthReasonCode(reasonCode) {
		return nil, ErrMalformedPacket
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
	if c.protocolVersion != Version50 {
		return ErrInvalidPacket
	}
	if !validAuthReasonCode(pkt.ReasonCode) {
		return ErrMalformedPacket
	}

	var buf bytes.Buffer

	if pkt.ReasonCode != AuthSuccess || pkt.Properties != nil {
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

func validAuthReasonCode(reasonCode byte) bool {
	return reasonCode == AuthSuccess || reasonCode == AuthContinueAuth || reasonCode == AuthReAuth
}
