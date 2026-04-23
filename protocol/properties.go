package protocol

import (
	"bytes"
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
			props.PayloadFormat = &v
		case PropMessageExpiryInterval:
			v, err := readVarIntFromReader(reader)
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
		case PropSubscriptionIdentifier, PropSessionExpiryInterval, PropWillDelayInterval, PropMaximumPacketSize:
			v, err := readVarIntFromReader(reader)
			if err != nil {
				return nil, err
			}
			// Assign to correct field based on type
			switch propType {
			case PropSubscriptionIdentifier:
				props.SubscriptionIdentifier = &v
			case PropSessionExpiryInterval:
				props.SessionExpiryInterval = &v
			case PropWillDelayInterval:
				props.WillDelayInterval = &v
			case PropMaximumPacketSize:
				props.MaximumPacketSize = &v
			}
		case PropAssignedClientID, PropAuthMethod, PropReasonString:
			v, err := readStringFromReader(reader)
			if err != nil {
				return nil, err
			}
			switch propType {
			case PropAssignedClientID:
				props.AssignedClientID = v
			case PropAuthMethod:
				props.AuthenticationMethod = v
			case PropReasonString:
				props.ReasonString = v
			}
		case PropServerKeepAlive:
			// Simplified: skip 2 bytes
			var v [2]byte
			if _, err := reader.Read(v[:]); err != nil {
				return nil, err
			}
			val := uint16(v[0])<<8 | uint16(v[1])
			props.ServerKeepAlive = &val
		case PropReceiveMaximum, PropTopicAliasMax, PropTopicAlias:
			// Simplified: skip 2 bytes
			var v [2]byte
			if _, err := reader.Read(v[:]); err != nil {
				return nil, err
			}
			val := uint16(v[0])<<8 | uint16(v[1])
			switch propType {
			case PropReceiveMaximum:
				props.ReceiveMaximum = &val
			case PropTopicAliasMax:
				props.TopicAliasMaximum = &val
			case PropTopicAlias:
				props.TopicAlias = &val
			}
		case PropRequestProblemInfo, PropMaximumQoS, PropRetainAvailable,
			PropWildcardSubAvailable, PropSubIDAvailable, PropSharedSubAvailable:
			v, err := reader.ReadByte()
			if err != nil {
				return nil, err
			}
			switch propType {
			case PropRequestProblemInfo:
				props.RequestProblemInfo = &v
			case PropMaximumQoS:
				props.MaximumQoS = &v
			case PropRetainAvailable:
				props.RetainAvailable = &v
			case PropWildcardSubAvailable:
				props.WildcardSubAvailable = &v
			case PropSubIDAvailable:
				props.SubIDAvailable = &v
			case PropSharedSubAvailable:
				props.SharedSubAvailable = &v
			}
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
		case PropAuthData:
			v, err := readBinaryDataFromReader(reader)
			if err != nil {
				return nil, err
			}
			props.AuthenticationData = v
		default:
			// Unknown property, skip
			return nil, nil
		}
	}
	return props, nil
}

func (c *Codec) encodeProperties(w io.Writer, props *Properties) error {
	var buf bytes.Buffer

	if props.PayloadFormat != nil {
		buf.WriteByte(PropPayloadFormat)
		buf.WriteByte(*props.PayloadFormat)
	}
	if props.MessageExpiryInterval != nil {
		buf.WriteByte(PropMessageExpiryInterval)
		writeVarInt(&buf, *props.MessageExpiryInterval)
	}
	if props.ContentType != "" {
		buf.WriteByte(PropContentType)
		writeString(&buf, props.ContentType)
	}
	if props.ResponseTopic != "" {
		buf.WriteByte(PropResponseTopic)
		writeString(&buf, props.ResponseTopic)
	}
	if props.CorrelationData != nil {
		buf.WriteByte(PropCorrelationData)
		writeBinaryData(&buf, props.CorrelationData)
	}
	if props.SessionExpiryInterval != nil {
		buf.WriteByte(PropSessionExpiryInterval)
		writeVarInt(&buf, *props.SessionExpiryInterval)
	}
	if props.AssignedClientID != "" {
		buf.WriteByte(PropAssignedClientID)
		writeString(&buf, props.AssignedClientID)
	}
	if props.ReasonString != "" {
		buf.WriteByte(PropReasonString)
		writeString(&buf, props.ReasonString)
	}
	for _, up := range props.UserProperties {
		buf.WriteByte(PropUserProperty)
		writeString(&buf, up.Key)
		writeString(&buf, up.Value)
	}

	// Write property length as varint first, then the buffer
	// This is a simplification; real implementation should prepend the length
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

// Helper functions for reading from io.Reader (bytes.Reader).
func readByte(r io.Reader) (byte, error) {
	var buf [1]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return 0, err
	}
	return buf[0], nil
}

func readByteFromReader(r *bytes.Reader) (byte, error) {
	return r.ReadByte()
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
	if _, err := r.Read(buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func readVarIntFromReader(r *bytes.Reader) (uint32, error) {
	var val uint32
	var multiplier uint32 = 1
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		val += uint32(b&0x7F) * multiplier
		if (b & 0x80) == 0 {
			break
		}
		multiplier *= 128
	}
	return val, nil
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
	if _, err := r.Read(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

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
