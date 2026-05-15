package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// Properties represents MQTT 5.0 properties.
type Properties struct {
	PayloadFormat          *byte
	MessageExpiryInterval  *uint32
	ContentType            string
	ResponseTopic          string
	CorrelationData        []byte
	SubscriptionIdentifier *uint32
	SessionExpiryInterval  *uint32
	AssignedClientID       string
	ServerKeepAlive        *uint16
	AuthenticationMethod   string
	AuthenticationData     []byte
	RequestProblemInfo     *byte
	WillDelayInterval      *uint32
	ReceiveMaximum         *uint16
	TopicAliasMaximum      *uint16
	TopicAlias             *uint16
	MaximumQoS             *byte
	RetainAvailable        *byte
	UserProperties         []UserProperty
	MaximumPacketSize      *uint32
	WildcardSubAvailable   *byte
	SubIDAvailable         *byte
	SharedSubAvailable     *byte
	ReasonString           string
}

// UserProperty represents a user-defined key-value pair.
type UserProperty struct {
	Key   string
	Value string
}

// Property type constants.
const (
	PropPayloadFormat          byte = 0x01
	PropMessageExpiryInterval  byte = 0x02
	PropContentType            byte = 0x03
	PropResponseTopic          byte = 0x08
	PropCorrelationData        byte = 0x09
	PropSubscriptionIdentifier byte = 0x0B
	PropSessionExpiryInterval  byte = 0x11
	PropAssignedClientID       byte = 0x12
	PropServerKeepAlive        byte = 0x13
	PropAuthMethod             byte = 0x15
	PropAuthData               byte = 0x16
	PropRequestProblemInfo     byte = 0x17
	PropWillDelayInterval      byte = 0x18
	PropReceiveMaximum         byte = 0x21
	PropTopicAliasMax          byte = 0x22
	PropTopicAlias             byte = 0x23
	PropMaximumQoS             byte = 0x24
	PropRetainAvailable        byte = 0x25
	PropUserProperty           byte = 0x26
	PropMaximumPacketSize      byte = 0x27
	PropWildcardSubAvailable   byte = 0x28
	PropSubIDAvailable         byte = 0x29
	PropSharedSubAvailable     byte = 0x2A
	PropReasonString           byte = 0x1F
)

// propertyType maps property ID to its data type size for skipping unknowns.
// MQTT 5.0 spec Table 2-6: Property data types.
type propType int

const (
	propTypeByte           propType = iota // 1 byte
	propTypeUInt16                         // 2 bytes big-endian
	propTypeUInt32                         // 4 bytes big-endian
	propTypeVarInt                         // variable byte integer
	propTypeUTF8String                     // 2-byte length + string
	propTypeBinaryData                     // 2-byte length + data
	propTypeUTF8StringPair                 // 2-byte length + string, twice
)

var propTypeMap = map[byte]propType{
	PropPayloadFormat:          propTypeByte,
	PropMessageExpiryInterval:  propTypeUInt32,
	PropContentType:            propTypeUTF8String,
	PropResponseTopic:          propTypeUTF8String,
	PropCorrelationData:        propTypeBinaryData,
	PropSubscriptionIdentifier: propTypeVarInt,
	PropSessionExpiryInterval:  propTypeUInt32,
	PropAssignedClientID:       propTypeUTF8String,
	PropServerKeepAlive:        propTypeUInt16,
	PropAuthMethod:             propTypeUTF8String,
	PropAuthData:               propTypeBinaryData,
	PropRequestProblemInfo:     propTypeByte,
	PropWillDelayInterval:      propTypeUInt32,
	PropReceiveMaximum:         propTypeUInt16,
	PropTopicAliasMax:          propTypeUInt16,
	PropTopicAlias:             propTypeUInt16,
	PropMaximumQoS:             propTypeByte,
	PropRetainAvailable:        propTypeByte,
	PropUserProperty:           propTypeUTF8StringPair,
	PropMaximumPacketSize:      propTypeUInt32,
	PropWildcardSubAvailable:   propTypeByte,
	PropSubIDAvailable:         propTypeByte,
	PropSharedSubAvailable:     propTypeByte,
	PropReasonString:           propTypeUTF8String,
}

func (c *Codec) decodeProperties(r io.Reader) (*Properties, error) {
	propLen, err := readVarInt(r)
	if err != nil {
		return nil, err
	}
	if propLen == 0 {
		return nil, nil
	}

	buf := make([]byte, propLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	reader := bytes.NewReader(buf)

	props := &Properties{}
	for reader.Len() > 0 {
		propType, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		switch propType {
		case PropPayloadFormat:
			v, err := reader.ReadByte()
			if err != nil {
				return nil, err
			}
			if v > 1 {
				return nil, ErrMalformedPacket
			}
			props.PayloadFormat = &v
		case PropMessageExpiryInterval:
			v, err := readUint32FromReader(reader)
			if err != nil {
				return nil, err
			}
			props.MessageExpiryInterval = &v
		case PropContentType:
			v, err := readStringFromReader(reader)
			if err != nil {
				return nil, err
			}
			props.ContentType = v
		case PropResponseTopic:
			v, err := readStringFromReader(reader)
			if err != nil {
				return nil, err
			}
			props.ResponseTopic = v
		case PropCorrelationData:
			v, err := readBinaryDataFromReader(reader)
			if err != nil {
				return nil, err
			}
			props.CorrelationData = v
		case PropSubscriptionIdentifier:
			v, err := readVarIntFromReader(reader)
			if err != nil {
				return nil, err
			}
			if v == 0 {
				return nil, ErrMalformedPacket
			}
			props.SubscriptionIdentifier = &v
		case PropSessionExpiryInterval:
			v, err := readUint32FromReader(reader)
			if err != nil {
				return nil, err
			}
			props.SessionExpiryInterval = &v
		case PropAssignedClientID:
			v, err := readStringFromReader(reader)
			if err != nil {
				return nil, err
			}
			props.AssignedClientID = v
		case PropServerKeepAlive:
			v, err := readUint16FromReader(reader)
			if err != nil {
				return nil, err
			}
			props.ServerKeepAlive = &v
		case PropAuthMethod:
			v, err := readStringFromReader(reader)
			if err != nil {
				return nil, err
			}
			props.AuthenticationMethod = v
		case PropAuthData:
			v, err := readBinaryDataFromReader(reader)
			if err != nil {
				return nil, err
			}
			props.AuthenticationData = v
		case PropRequestProblemInfo:
			v, err := reader.ReadByte()
			if err != nil {
				return nil, err
			}
			if v > 1 {
				return nil, ErrMalformedPacket
			}
			props.RequestProblemInfo = &v
		case PropWillDelayInterval:
			v, err := readUint32FromReader(reader)
			if err != nil {
				return nil, err
			}
			props.WillDelayInterval = &v
		case PropReceiveMaximum:
			v, err := readUint16FromReader(reader)
			if err != nil {
				return nil, err
			}
			if v == 0 {
				return nil, ErrMalformedPacket
			}
			props.ReceiveMaximum = &v
		case PropTopicAliasMax:
			v, err := readUint16FromReader(reader)
			if err != nil {
				return nil, err
			}
			props.TopicAliasMaximum = &v
		case PropTopicAlias:
			v, err := readUint16FromReader(reader)
			if err != nil {
				return nil, err
			}
			if v == 0 {
				return nil, ErrMalformedPacket
			}
			props.TopicAlias = &v
		case PropMaximumQoS:
			v, err := reader.ReadByte()
			if err != nil {
				return nil, err
			}
			if v > 1 {
				return nil, ErrMalformedPacket
			}
			props.MaximumQoS = &v
		case PropRetainAvailable:
			v, err := reader.ReadByte()
			if err != nil {
				return nil, err
			}
			if v > 1 {
				return nil, ErrMalformedPacket
			}
			props.RetainAvailable = &v
		case PropUserProperty:
			k, err := readStringFromReader(reader)
			if err != nil {
				return nil, err
			}
			v, err := readStringFromReader(reader)
			if err != nil {
				return nil, err
			}
			props.UserProperties = append(props.UserProperties, UserProperty{Key: k, Value: v})
		case PropMaximumPacketSize:
			v, err := readUint32FromReader(reader)
			if err != nil {
				return nil, err
			}
			if v == 0 {
				return nil, ErrMalformedPacket
			}
			props.MaximumPacketSize = &v
		case PropWildcardSubAvailable:
			v, err := reader.ReadByte()
			if err != nil {
				return nil, err
			}
			if v > 1 {
				return nil, ErrMalformedPacket
			}
			props.WildcardSubAvailable = &v
		case PropSubIDAvailable:
			v, err := reader.ReadByte()
			if err != nil {
				return nil, err
			}
			if v > 1 {
				return nil, ErrMalformedPacket
			}
			props.SubIDAvailable = &v
		case PropSharedSubAvailable:
			v, err := reader.ReadByte()
			if err != nil {
				return nil, err
			}
			if v > 1 {
				return nil, ErrMalformedPacket
			}
			props.SharedSubAvailable = &v
		case PropReasonString:
			v, err := readStringFromReader(reader)
			if err != nil {
				return nil, err
			}
			props.ReasonString = v
		default:
			// Unknown property — skip per MQTT-1.5.5-1
			if err := skipPropertyValue(reader, propType); err != nil {
				return nil, err
			}
		}
	}
	return props, nil
}

// skipPropertyValue skips an unknown property's value based on its type.
// Returns an error for completely unrecognized property IDs.
func skipPropertyValue(r *bytes.Reader, propID byte) error {
	pt, ok := propTypeMap[propID]
	if !ok {
		return fmt.Errorf("protocol: unknown property ID 0x%02X with unrecognized data type", propID)
	}
	switch pt {
	case propTypeByte:
		_, err := r.ReadByte()
		return err
	case propTypeUInt16:
		_, err := r.Seek(2, 1)
		return err
	case propTypeUInt32:
		_, err := r.Seek(4, 1)
		return err
	case propTypeVarInt:
		_, err := readVarIntFromReader(r)
		return err
	case propTypeUTF8String, propTypeBinaryData:
		_, err := readBinaryDataFromReader(r) // same format: 2-byte len + data
		return err
	case propTypeUTF8StringPair:
		if _, err := readBinaryDataFromReader(r); err != nil {
			return err
		}
		_, err := readBinaryDataFromReader(r)
		return err
	}
	return nil
}

func (c *Codec) encodeProperties(w io.Writer, props *Properties) error {
	if err := validateProperties(props); err != nil {
		return err
	}

	var buf bytes.Buffer

	if props.PayloadFormat != nil {
		if err := buf.WriteByte(PropPayloadFormat); err != nil {
			return err
		}
		if err := buf.WriteByte(*props.PayloadFormat); err != nil {
			return err
		}
	}
	if props.MessageExpiryInterval != nil {
		if err := buf.WriteByte(PropMessageExpiryInterval); err != nil {
			return err
		}
		if err := writeUint32ToWriter(&buf, *props.MessageExpiryInterval); err != nil {
			return err
		}
	}
	if props.ContentType != "" {
		if err := buf.WriteByte(PropContentType); err != nil {
			return err
		}
		if err := writeString(&buf, props.ContentType); err != nil {
			return err
		}
	}
	if props.ResponseTopic != "" {
		if err := buf.WriteByte(PropResponseTopic); err != nil {
			return err
		}
		if err := writeString(&buf, props.ResponseTopic); err != nil {
			return err
		}
	}
	if props.CorrelationData != nil {
		if err := buf.WriteByte(PropCorrelationData); err != nil {
			return err
		}
		if err := writeBinaryData(&buf, props.CorrelationData); err != nil {
			return err
		}
	}
	if props.SubscriptionIdentifier != nil {
		if err := buf.WriteByte(PropSubscriptionIdentifier); err != nil {
			return err
		}
		if err := writeVarInt(&buf, *props.SubscriptionIdentifier); err != nil {
			return err
		}
	}
	if props.SessionExpiryInterval != nil {
		if err := buf.WriteByte(PropSessionExpiryInterval); err != nil {
			return err
		}
		if err := writeUint32ToWriter(&buf, *props.SessionExpiryInterval); err != nil {
			return err
		}
	}
	if props.AssignedClientID != "" {
		if err := buf.WriteByte(PropAssignedClientID); err != nil {
			return err
		}
		if err := writeString(&buf, props.AssignedClientID); err != nil {
			return err
		}
	}
	if props.ServerKeepAlive != nil {
		if err := buf.WriteByte(PropServerKeepAlive); err != nil {
			return err
		}
		if err := writeUint16(&buf, *props.ServerKeepAlive); err != nil {
			return err
		}
	}
	if props.AuthenticationMethod != "" {
		if err := buf.WriteByte(PropAuthMethod); err != nil {
			return err
		}
		if err := writeString(&buf, props.AuthenticationMethod); err != nil {
			return err
		}
	}
	if props.AuthenticationData != nil {
		if err := buf.WriteByte(PropAuthData); err != nil {
			return err
		}
		if err := writeBinaryData(&buf, props.AuthenticationData); err != nil {
			return err
		}
	}
	if props.RequestProblemInfo != nil {
		if err := buf.WriteByte(PropRequestProblemInfo); err != nil {
			return err
		}
		if err := buf.WriteByte(*props.RequestProblemInfo); err != nil {
			return err
		}
	}
	if props.WillDelayInterval != nil {
		if err := buf.WriteByte(PropWillDelayInterval); err != nil {
			return err
		}
		if err := writeUint32ToWriter(&buf, *props.WillDelayInterval); err != nil {
			return err
		}
	}
	if props.ReceiveMaximum != nil {
		if err := buf.WriteByte(PropReceiveMaximum); err != nil {
			return err
		}
		if err := writeUint16(&buf, *props.ReceiveMaximum); err != nil {
			return err
		}
	}
	if props.TopicAliasMaximum != nil {
		if err := buf.WriteByte(PropTopicAliasMax); err != nil {
			return err
		}
		if err := writeUint16(&buf, *props.TopicAliasMaximum); err != nil {
			return err
		}
	}
	if props.TopicAlias != nil {
		if err := buf.WriteByte(PropTopicAlias); err != nil {
			return err
		}
		if err := writeUint16(&buf, *props.TopicAlias); err != nil {
			return err
		}
	}
	if props.MaximumQoS != nil {
		if err := buf.WriteByte(PropMaximumQoS); err != nil {
			return err
		}
		if err := buf.WriteByte(*props.MaximumQoS); err != nil {
			return err
		}
	}
	if props.RetainAvailable != nil {
		if err := buf.WriteByte(PropRetainAvailable); err != nil {
			return err
		}
		if err := buf.WriteByte(*props.RetainAvailable); err != nil {
			return err
		}
	}
	for _, up := range props.UserProperties {
		if err := buf.WriteByte(PropUserProperty); err != nil {
			return err
		}
		if err := writeString(&buf, up.Key); err != nil {
			return err
		}
		if err := writeString(&buf, up.Value); err != nil {
			return err
		}
	}
	if props.MaximumPacketSize != nil {
		if err := buf.WriteByte(PropMaximumPacketSize); err != nil {
			return err
		}
		if err := writeUint32ToWriter(&buf, *props.MaximumPacketSize); err != nil {
			return err
		}
	}
	if props.WildcardSubAvailable != nil {
		if err := buf.WriteByte(PropWildcardSubAvailable); err != nil {
			return err
		}
		if err := buf.WriteByte(*props.WildcardSubAvailable); err != nil {
			return err
		}
	}
	if props.SubIDAvailable != nil {
		if err := buf.WriteByte(PropSubIDAvailable); err != nil {
			return err
		}
		if err := buf.WriteByte(*props.SubIDAvailable); err != nil {
			return err
		}
	}
	if props.SharedSubAvailable != nil {
		if err := buf.WriteByte(PropSharedSubAvailable); err != nil {
			return err
		}
		if err := buf.WriteByte(*props.SharedSubAvailable); err != nil {
			return err
		}
	}
	if props.ReasonString != "" {
		if err := buf.WriteByte(PropReasonString); err != nil {
			return err
		}
		if err := writeString(&buf, props.ReasonString); err != nil {
			return err
		}
	}

	data := buf.Bytes()
	if len(data) == 0 {
		return writeVarInt(w, 0)
	}
	if err := writeVarInt(w, uint32(len(data))); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func validateProperties(props *Properties) error {
	if props == nil {
		return nil
	}
	if props.PayloadFormat != nil && *props.PayloadFormat > 1 {
		return ErrMalformedPacket
	}
	if props.SubscriptionIdentifier != nil && *props.SubscriptionIdentifier == 0 {
		return ErrMalformedPacket
	}
	if props.RequestProblemInfo != nil && *props.RequestProblemInfo > 1 {
		return ErrMalformedPacket
	}
	if props.ReceiveMaximum != nil && *props.ReceiveMaximum == 0 {
		return ErrMalformedPacket
	}
	if props.TopicAlias != nil && *props.TopicAlias == 0 {
		return ErrMalformedPacket
	}
	if props.MaximumQoS != nil && *props.MaximumQoS > 1 {
		return ErrMalformedPacket
	}
	if props.RetainAvailable != nil && *props.RetainAvailable > 1 {
		return ErrMalformedPacket
	}
	if props.MaximumPacketSize != nil && *props.MaximumPacketSize == 0 {
		return ErrMalformedPacket
	}
	if props.WildcardSubAvailable != nil && *props.WildcardSubAvailable > 1 {
		return ErrMalformedPacket
	}
	if props.SubIDAvailable != nil && *props.SubIDAvailable > 1 {
		return ErrMalformedPacket
	}
	if props.SharedSubAvailable != nil && *props.SharedSubAvailable > 1 {
		return ErrMalformedPacket
	}
	return nil
}

// --- Helper functions for reading from bytes.Reader ---

func readByte(r io.Reader) (byte, error) {
	var buf [1]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return buf[0], nil
}

func readStringFromReader(r *bytes.Reader) (string, error) {
	var lenBuf [2]byte
	for i := range lenBuf {
		b, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		lenBuf[i] = b
	}
	length := int(lenBuf[0])<<8 | int(lenBuf[1])
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

func readUint16FromReader(r *bytes.Reader) (uint16, error) {
	var buf [2]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(buf[:]), nil
}

func readUint32FromReader(r *bytes.Reader) (uint32, error) {
	var buf [4]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(buf[:]), nil
}

func readVarIntFromReader(r *bytes.Reader) (uint32, error) {
	var val uint32
	var multiplier uint32 = 1
	for i := 0; i < 4; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		val += uint32(b&0x7F) * multiplier
		if (b & 0x80) == 0 {
			return val, nil
		}
		multiplier *= 128
	}
	return 0, fmt.Errorf("protocol: varint exceeds 4 bytes")
}

func readBinaryDataFromReader(r *bytes.Reader) ([]byte, error) {
	var lenBuf [2]byte
	for i := range lenBuf {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		lenBuf[i] = b
	}
	length := int(lenBuf[0])<<8 | int(lenBuf[1])
	if length == 0 {
		return nil, nil
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// --- Helper functions for writing ---

func writeBinaryData(w io.Writer, data []byte) error {
	var lenBuf [2]byte
	lenBuf[0] = byte(len(data) >> 8)
	lenBuf[1] = byte(len(data))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	if len(data) > 0 {
		_, err := w.Write(data)
		return err
	}
	return nil
}

func writeUint32ToWriter(w io.Writer, v uint32) error {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	_, err := w.Write(buf[:])
	return err
}
