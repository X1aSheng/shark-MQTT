package protocol

import (
	"bytes"
	"io"
)

func (c *Codec) decodeSubscribe(r io.Reader, fh FixedHeader) (*SubscribePacket, error) {
	// A SUBSCRIBE always carries a 2-byte packet identifier. The length check
	// has to come first: reading the identifier and only then noticing that
	// Remaining Length cannot hold it consumes bytes from the next packet (the
	// same guard decodeSubAck carries).
	if fh.RemainingLength < 2 {
		return nil, ErrMalformedPacket
	}
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
	reader := decodeReader(data)
	defer putDecodeReader(reader)

	var props *Properties
	if c.spec.Properties {
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
		// MQTT 3.1.1: bits 2-7 (NoLocal / RetainAsPublished / RetainHandling)
		// are reserved and must be zero (NEW-19).
		if !c.spec.Properties && optsByte&0xFC != 0 {
			return nil, ErrMalformedPacket
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
		FixedHeader: fh,
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

	// Properties (MQTT 5.0). The section is always present in a v5 SUBSCRIBE;
	// writePropertySection refuses a pre-v5 codec that was handed properties
	// instead of writing v5 bytes into a 3.1/3.1.1 frame.
	if err := c.writePropertySection(&buf, pkt.Properties); err != nil {
		return err
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

func (c *Codec) decodeSubAck(r io.Reader, fh FixedHeader) (*SubAckPacket, error) {
	// A SUBACK always carries a 2-byte packet identifier; without this guard
	// a Remaining Length < 2 would make the direct readUint16 below consume
	// bytes from the next packet and desynchronize the stream (audit).
	if fh.RemainingLength < 2 {
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
		reader := decodeReader(data)
		defer putDecodeReader(reader)

		if c.spec.Properties {
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
		FixedHeader: fh,
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

	// Properties (MQTT 5.0). A v5 SUBACK always carries the section (its
	// Remaining Length is at least 4). This block was the one acknowledgement
	// without a version gate: SUBACK has no reason-code check to fall back on,
	// because in 3.1.1 its payload *is* the return codes.
	if err := c.writePropertySection(&buf, pkt.Properties); err != nil {
		return err
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

func (c *Codec) decodeUnsubscribe(r io.Reader, fh FixedHeader) (*UnsubscribePacket, error) {
	// Same guard as decodeSubscribe: the 2-byte packet identifier must fit in
	// Remaining Length before it is read, or the rejection eats into the next
	// packet.
	if fh.RemainingLength < 2 {
		return nil, ErrMalformedPacket
	}
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
	reader := decodeReader(data)
	defer putDecodeReader(reader)

	var props *Properties
	if c.spec.Properties {
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
		FixedHeader: fh,
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

	// Properties (MQTT 5.0), always present in a v5 UNSUBSCRIBE. The pre-v5
	// refusal comes from writePropertySection itself: UNSUBSCRIBE has no reason
	// code to check, so nothing else would catch a properties-bearing v3.1.1
	// packet and the v5 bytes would be read as the topic list.
	if err := c.writePropertySection(&buf, pkt.Properties); err != nil {
		return err
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

func (c *Codec) decodeUnsubAck(r io.Reader, fh FixedHeader) (*UnsubAckPacket, error) {
	if !c.spec.ReasonCodes && fh.RemainingLength != 2 {
		return nil, ErrMalformedPacket
	}
	// A UNSUBACK always carries a 2-byte packet identifier; without this
	// guard a Remaining Length < 2 would make the direct readUint16 below
	// consume bytes from the next packet and desynchronize the stream (audit).
	if fh.RemainingLength < 2 {
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
		reader := decodeReader(data)
		defer putDecodeReader(reader)

		if c.spec.Properties {
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
		FixedHeader: fh,
		PacketID:    packetID,
		ReasonCodes: reasonCodes,
		Properties:  props,
	}, nil
}

func (c *Codec) encodeUnsubAck(w io.Writer, pkt *UnsubAckPacket) error {
	if !c.spec.ReasonCodes && (len(pkt.ReasonCodes) > 0 || pkt.Properties != nil) {
		return ErrMalformedPacket
	}

	var buf bytes.Buffer

	if pkt.PacketID == 0 {
		return ErrMalformedPacket
	}
	if err := writeUint16(&buf, pkt.PacketID); err != nil {
		return err
	}

	// Properties (MQTT 5.0), always present in a v5 UNSUBACK. The pre-v5 refusal
	// above already rejects a properties-bearing pre-v5 packet, so this call can
	// only fail on a malformed property set; it goes through the helper anyway so
	// that every mandatory property section is written the same way.
	if err := c.writePropertySection(&buf, pkt.Properties); err != nil {
		return err
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

func (c *Codec) decodeDisconnect(r io.Reader, fh FixedHeader) (*DisconnectPacket, error) {
	if !c.spec.ReasonCodes && fh.RemainingLength > 0 {
		return nil, ErrMalformedPacket
	}

	buf := make([]byte, fh.RemainingLength)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	reader := decodeReader(buf)
	defer putDecodeReader(reader)

	var reasonCode byte
	if fh.RemainingLength > 0 {
		var err error
		reasonCode, err = reader.ReadByte()
		if err != nil {
			return nil, err
		}
	}

	var props *Properties
	if fh.RemainingLength > 1 {
		var err error
		props, err = c.decodeProperties(reader)
		if err != nil {
			return nil, err
		}
		// All bytes after the properties must be consumed (NEW-18).
		if reader.Len() != 0 {
			return nil, ErrMalformedPacket
		}
	}

	return &DisconnectPacket{
		FixedHeader: fh,
		ReasonCode:  reasonCode,
		Properties:  props,
	}, nil
}

func (c *Codec) encodeDisconnect(w io.Writer, pkt *DisconnectPacket) error {
	if !c.spec.ReasonCodes && (pkt.ReasonCode != 0 || pkt.Properties != nil) {
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

func (c *Codec) decodeAuth(r io.Reader, fh FixedHeader) (*AuthPacket, error) {
	if !c.spec.AuthPacket {
		return nil, ErrInvalidPacket
	}

	buf := make([]byte, fh.RemainingLength)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	reader := decodeReader(buf)
	defer putDecodeReader(reader)

	var reasonCode byte
	if fh.RemainingLength > 0 {
		var err error
		reasonCode, err = reader.ReadByte()
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
		props, err = c.decodeProperties(reader)
		if err != nil {
			return nil, err
		}
		// All bytes after the properties must be consumed (NEW-18).
		if reader.Len() != 0 {
			return nil, ErrMalformedPacket
		}
	}

	return &AuthPacket{
		FixedHeader: fh,
		ReasonCode:  reasonCode,
		Properties:  props,
	}, nil
}

func (c *Codec) encodeAuth(w io.Writer, pkt *AuthPacket) error {
	if !c.spec.AuthPacket {
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
