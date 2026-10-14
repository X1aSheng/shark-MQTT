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
	RequestResponseInfo    *byte  // MQTT 5.0: 0=don't send ResponseInfo, 1=request ResponseInfo
	ResponseInfo           string // MQTT 5.0: used as basis for creating response topic
	ServerReference        string // MQTT 5.0: redirect target for reason 0x9C/0x9D
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
	PropRequestResponseInfo    byte = 0x19 // MQTT 5.0 §3.1.2.11.6
	PropResponseInfo           byte = 0x1A // MQTT 5.0 §3.2.2.3.3
	PropServerReference        byte = 0x1C // MQTT 5.0: CONNACK / DISCONNECT redirect
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

// propertyLocusSet is a bit set over the locus values, recording which property
// sections one Identifier is permitted in.
type propertyLocusSet uint16

func locusBit(l propertyLocus) propertyLocusSet { return 1 << l }

// allLoci is every locus. Table 2-4 lists User Property for all fourteen packet
// types that carry a property section; the eleven loci are those fourteen with
// the four-member acknowledgement family collapsed into locusAck.
const allLoci = propertyLocusSet(1)<<(locusAuth+1) - 1

// propertyLoci records, per Table 2-4, which property sections each Identifier
// is valid in.
//
// This is the rule §2.2.2.2 states in one sentence for the whole protocol: "A
// Control Packet which contains an Identifier which is not valid for its packet
// type, or contains a value not of the specified data type, is a Malformed
// Packet. If received, use a CONNACK or DISCONNECT packet with Reason Code 0x81
// (Malformed Packet)". An Identifier absent from this table is not valid for any
// packet type and takes the same answer, which is why the table doubles as the
// "is this Identifier defined at all" test: MQTT 5.0 defines no property a peer
// may send in any packet type, so an unknown Identifier is not something to skip
// for forward compatibility.
var propertyLoci = map[byte]propertyLocusSet{
	PropPayloadFormat:          locusBit(locusPublish) | locusBit(locusWill),
	PropMessageExpiryInterval:  locusBit(locusPublish) | locusBit(locusWill),
	PropContentType:            locusBit(locusPublish) | locusBit(locusWill),
	PropResponseTopic:          locusBit(locusPublish) | locusBit(locusWill),
	PropCorrelationData:        locusBit(locusPublish) | locusBit(locusWill),
	PropSubscriptionIdentifier: locusBit(locusPublish) | locusBit(locusSubscribe),
	PropSessionExpiryInterval:  locusBit(locusConnect) | locusBit(locusConnAck) | locusBit(locusDisconnect),
	PropAssignedClientID:       locusBit(locusConnAck),
	PropServerKeepAlive:        locusBit(locusConnAck),
	PropAuthMethod:             locusBit(locusConnect) | locusBit(locusConnAck) | locusBit(locusAuth),
	PropAuthData:               locusBit(locusConnect) | locusBit(locusConnAck) | locusBit(locusAuth),
	PropRequestProblemInfo:     locusBit(locusConnect),
	PropWillDelayInterval:      locusBit(locusWill),
	PropRequestResponseInfo:    locusBit(locusConnect),
	PropResponseInfo:           locusBit(locusConnAck),
	PropServerReference:        locusBit(locusConnAck) | locusBit(locusDisconnect),
	PropReasonString: locusBit(locusConnAck) | locusBit(locusAck) | locusBit(locusSubAck) |
		locusBit(locusUnsubAck) | locusBit(locusDisconnect) | locusBit(locusAuth),
	PropReceiveMaximum:       locusBit(locusConnect) | locusBit(locusConnAck),
	PropTopicAliasMax:        locusBit(locusConnect) | locusBit(locusConnAck),
	PropTopicAlias:           locusBit(locusPublish),
	PropMaximumQoS:           locusBit(locusConnAck),
	PropRetainAvailable:      locusBit(locusConnAck),
	PropUserProperty:         allLoci,
	PropMaximumPacketSize:    locusBit(locusConnect) | locusBit(locusConnAck),
	PropWildcardSubAvailable: locusBit(locusConnAck),
	PropSubIDAvailable:       locusBit(locusConnAck),
	PropSharedSubAvailable:   locusBit(locusConnAck),
}

// propertyProbe reports whether a property set carries the Identifier id, that
// is, whether encodeProperties would put it on the wire.
type propertyProbe struct {
	id  byte
	set func(*Properties) bool
}

// propertyProbes names the property set the encoder can emit, one entry per
// Identifier in propertyLoci.
//
// validatePropertyLoci walks this table rather than the encodeProperties block
// list, so the rule lives in one place and the check runs before a single byte
// is written. The two must not drift: TestPropertyProbesCoverPropertyLoci fails
// when an Identifier in propertyLoci has no probe, so a property added to
// Properties without teaching the guard about it is caught rather than silently
// escaping the check.
var propertyProbes = []propertyProbe{
	{PropPayloadFormat, func(p *Properties) bool { return p.PayloadFormat != nil }},
	{PropMessageExpiryInterval, func(p *Properties) bool { return p.MessageExpiryInterval != nil }},
	{PropContentType, func(p *Properties) bool { return p.ContentType != "" }},
	{PropResponseTopic, func(p *Properties) bool { return p.ResponseTopic != "" }},
	{PropCorrelationData, func(p *Properties) bool { return p.CorrelationData != nil }},
	{PropSubscriptionIdentifier, func(p *Properties) bool { return p.SubscriptionIdentifier != nil }},
	{PropSessionExpiryInterval, func(p *Properties) bool { return p.SessionExpiryInterval != nil }},
	{PropAssignedClientID, func(p *Properties) bool { return p.AssignedClientID != "" }},
	{PropServerKeepAlive, func(p *Properties) bool { return p.ServerKeepAlive != nil }},
	{PropAuthMethod, func(p *Properties) bool { return p.AuthenticationMethod != "" }},
	{PropAuthData, func(p *Properties) bool { return p.AuthenticationData != nil }},
	{PropRequestProblemInfo, func(p *Properties) bool { return p.RequestProblemInfo != nil }},
	{PropWillDelayInterval, func(p *Properties) bool { return p.WillDelayInterval != nil }},
	{PropRequestResponseInfo, func(p *Properties) bool { return p.RequestResponseInfo != nil }},
	{PropResponseInfo, func(p *Properties) bool { return p.ResponseInfo != "" }},
	{PropServerReference, func(p *Properties) bool { return p.ServerReference != "" }},
	{PropReceiveMaximum, func(p *Properties) bool { return p.ReceiveMaximum != nil }},
	{PropTopicAliasMax, func(p *Properties) bool { return p.TopicAliasMaximum != nil }},
	{PropTopicAlias, func(p *Properties) bool { return p.TopicAlias != nil }},
	{PropMaximumQoS, func(p *Properties) bool { return p.MaximumQoS != nil }},
	{PropRetainAvailable, func(p *Properties) bool { return p.RetainAvailable != nil }},
	{PropUserProperty, func(p *Properties) bool { return len(p.UserProperties) > 0 }},
	{PropMaximumPacketSize, func(p *Properties) bool { return p.MaximumPacketSize != nil }},
	{PropWildcardSubAvailable, func(p *Properties) bool { return p.WildcardSubAvailable != nil }},
	{PropSubIDAvailable, func(p *Properties) bool { return p.SubIDAvailable != nil }},
	{PropSharedSubAvailable, func(p *Properties) bool { return p.SharedSubAvailable != nil }},
	{PropReasonString, func(p *Properties) bool { return p.ReasonString != "" }},
}

// validatePropertyLoci rejects a property set that carries an Identifier the
// packet type does not define (MQTT 5.0 Table 2-4).
//
// This is the encoding-side half of the rule decodeProperties enforces through
// propertyLoci. §2.2.2.2 makes "a Control Packet which contains an Identifier
// which is not valid for its packet type" a Malformed Packet, so a peer that
// applies the rule has to reject the packet: producing one is a defect even when
// the local decoder is happy with it. Decoding alone cannot close this -- the
// broker's own encoder emitted a CONNACK carrying the CONNECT-only Request
// Response Information for as long as nothing checked this side. The section is
// buffered before it is written, so a rejection never reaches the wire.
func validatePropertyLoci(props *Properties, locus propertyLocus) error {
	if props == nil {
		return nil
	}
	for _, probe := range propertyProbes {
		if !probe.set(props) {
			continue
		}
		if propertyLoci[probe.id]&locusBit(locus) == 0 {
			return fmt.Errorf("%w: property 0x%02X is not valid in this packet type",
				ErrMalformedPacket, probe.id)
		}
	}
	return nil
}

// propertyLocus names the packet (or the CONNECT Will) whose property section
// is being decoded.
//
// It exists because the two MQTT 5.0 rules that govern a property section --
// which Identifiers Table 2-4 allows in it, and which of those may appear more
// than once -- are functions of the packet type, not of the Identifier alone.
// A flat Properties struct cannot express either rule on its own, so the caller
// has to say where it is decoding from.
type propertyLocus uint8

const (
	locusConnect propertyLocus = iota
	locusWill
	locusConnAck
	locusPublish
	// locusAck covers PUBACK, PUBREC, PUBREL and PUBCOMP: Table 2-4 gives all
	// four the same property set (Reason String and User Property only).
	locusAck
	locusSubscribe
	locusSubAck
	locusUnsubscribe
	locusUnsubAck
	locusDisconnect
	locusAuth
)

// maxPropertyID bounds every Identifier Table 2-4 defines, which is also the
// largest Identifier this version of the protocol uses: "all of the Property
// Identifiers are one byte long" (non-normative comment on §2.2.2.2). It sizes
// the occurrence bitmap in decodeProperties; the propertyLoci lookup rejects an
// Identifier outside the table before that bitmap is indexed.
const maxPropertyID = 0x2A

// propertyMayRepeat reports whether propID may appear more than once in the
// property section identified by locus.
//
// Table 2-4 marks exactly two properties repeatable, and neither is repeatable
// everywhere:
//
//   - User Property may be repeated in every packet type that defines it, and
//     the relative order of the pairs is significant (§3.3.2.3.7).
//   - Subscription Identifier may be repeated in a PUBLISH, where §3.3.2.3.8
//     uses the repetition to report that one publication matched more than one
//     subscription. It may NOT be repeated in a SUBSCRIBE: §3.8.2.1.2 states
//     that it is a Protocol Error to include it more than once, because a
//     SUBSCRIBE carries a single identifier that applies to every Topic Filter
//     in its list.
//
// That second case is why the answer needs the locus rather than a fixed set of
// Identifiers: the same Identifier is repeatable in one packet type and a
// Protocol Error in another.
func propertyMayRepeat(locus propertyLocus, propID byte) bool {
	if propID == PropUserProperty {
		return true
	}
	return propID == PropSubscriptionIdentifier && locus == locusPublish
}

func (c *Codec) decodeProperties(r io.Reader, locus propertyLocus) (*Properties, error) {
	propLen, err := readVarInt(r)
	if err != nil {
		return nil, err
	}
	if propLen == 0 {
		return nil, nil
	}

	// Bound the property-length allocation BEFORE make(): propLen is a wire
	// varint independent of the remaining packet bytes (up to 256 MiB - 1),
	// so an unvalidated claim previously forced a ~256 MB transient
	// allocation per crafted packet (audit H1). Every in-package decode path
	// parses properties from a *bytes.Reader over the already-bounded packet
	// body, so require propLen <= remaining bytes there; for any future
	// streaming caller, additionally cap at the codec's max packet size.
	if br, ok := r.(*bytes.Reader); ok {
		if propLen > uint32(br.Len()) {
			return nil, ErrMalformedPacket
		}
	} else if propLen > uint32(c.maxPacketSize) {
		return nil, ErrMalformedPacket
	}

	buf := make([]byte, propLen)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	reader := decodeReader(buf)
	defer putDecodeReader(reader)

	props := &Properties{}
	// seen records the Identifiers already consumed in this section so a repeat
	// can be rejected. A plain array indexed by Identifier is enough: the
	// propertyLoci lookup below is what makes the index safe, and it keeps the
	// whole check allocation-free and local to one decodeProperties call.
	var seen [maxPropertyID + 1]bool
	for reader.Len() > 0 {
		propType, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		// §2.2.2.2: an Identifier that is not valid for its packet type is a
		// Malformed Packet, answered with 0x81. An Identifier outside Table 2-4
		// is not valid for any packet type and gets the same answer -- MQTT 5.0
		// defines no property a peer may send in every packet type, so there is
		// nothing that has to be skipped for forward compatibility.
		allowed, defined := propertyLoci[propType]
		if !defined {
			return nil, fmt.Errorf("%w: unknown property ID 0x%02X", ErrMalformedPacket, propType)
		}
		if allowed&locusBit(locus) == 0 {
			return nil, fmt.Errorf("%w: property 0x%02X is not valid in this packet type", ErrMalformedPacket, propType)
		}
		// Repeating a property Table 2-4 allows only once is a Protocol Error,
		// not a malformed packet: the bytes parse and the field is well typed,
		// the peer simply sent a combination the specification forbids. It is
		// worth rejecting because the alternative is a silent "last one wins",
		// which changes meaning without leaving a trace -- a second
		// Subscription Identifier on a SUBSCRIBE would otherwise be copied onto
		// every Topic Filter in the list (see decodeSubscribe), so a filter the
		// client tagged with one identifier would quietly acquire another's.
		if seen[propType] && !propertyMayRepeat(locus, propType) {
			return nil, fmt.Errorf("%w: property 0x%02X appears more than once", ErrProtocolError, propType)
		}
		seen[propType] = true
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
		case PropRequestResponseInfo:
			v, err := reader.ReadByte()
			if err != nil {
				return nil, err
			}
			if v > 1 {
				return nil, ErrMalformedPacket
			}
			props.RequestResponseInfo = &v
		case PropResponseInfo:
			v, err := readStringFromReader(reader)
			if err != nil {
				return nil, err
			}
			props.ResponseInfo = v
		case PropServerReference:
			v, err := readStringFromReader(reader)
			if err != nil {
				return nil, err
			}
			props.ServerReference = v
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
			// Limit user properties to prevent memory exhaustion (MQTT recommends capping)
			const maxUserProperties = 100
			if len(props.UserProperties) >= maxUserProperties {
				return nil, ErrMalformedPacket
			}
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
			// Unreachable: propertyLoci lists exactly the Identifiers the cases
			// above handle, and an Identifier outside that table is rejected
			// before the switch is reached. Kept as an error rather than a
			// silent skip so that adding an Identifier to the table without a
			// matching case fails loudly instead of dropping its value.
			return nil, fmt.Errorf("%w: property 0x%02X has no decoder", ErrMalformedPacket, propType)
		}
	}
	return props, nil
}

// writePropertySection writes the MQTT 5 property section of a packet whose
// variable header always carries one in v5 -- PUBLISH, CONNECT, CONNACK,
// SUBSCRIBE, UNSUBSCRIBE and SUBACK: the Property Length followed by the encoded
// properties, or a zero length when the packet has none.
//
// A codec bound to a pre-v5 version cannot represent the section at all. It
// writes nothing when the caller set no properties, and reports
// ErrUnsupportedVersion when it did: the property bytes would otherwise be read
// as payload by the peer (stream corruption), and dropping them silently would
// hide the mismatch from the caller. The acknowledgement family that carries a
// reason code (PUBACK/PUBREC/PUBREL/PUBCOMP, UNSUBACK, DISCONNECT) does not use
// this helper: those encoders already refuse properties on a pre-v5 codec
// through their ReasonCodes check.
//
// locus is the packet type the section belongs to. It is required rather than
// optional because encodeProperties applies Table 2-4 to it: a property the
// packet type does not define is refused instead of written, which is the only
// place that rule can be enforced on this codec's own output.
func (c *Codec) writePropertySection(w io.Writer, props *Properties, locus propertyLocus) error {
	if !c.spec.Properties {
		return c.rejectPreV5Properties(props)
	}
	if props == nil {
		return writeVarInt(w, 0)
	}
	return c.encodeProperties(w, props, locus)
}

// rejectPreV5Properties is the guard that both writePropertySection and the
// acknowledgement encoders' own reason-code check rely on: a pre-v5 codec has no
// way to put properties on the wire, so a caller that set them is told rather
// than quietly served a packet without them.
func (c *Codec) rejectPreV5Properties(props *Properties) error {
	if props == nil {
		return nil
	}
	return fmt.Errorf("%w: packet properties require MQTT 5, codec is bound to version %d",
		ErrUnsupportedVersion, c.protocolVersion)
}

// encodeProperties writes the properties to w, prefixed by their length.
//
// locus is the packet type whose property section this is, and it is validated
// before anything is written: a property Table 2-4 does not allow here fails the
// call rather than producing a packet a conformant peer would reject.
func (c *Codec) encodeProperties(w io.Writer, props *Properties, locus propertyLocus) error {
	if err := validateProperties(props, locus); err != nil {
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
	if props.RequestResponseInfo != nil {
		if err := buf.WriteByte(PropRequestResponseInfo); err != nil {
			return err
		}
		if err := buf.WriteByte(*props.RequestResponseInfo); err != nil {
			return err
		}
	}
	if props.ResponseInfo != "" {
		if err := buf.WriteByte(PropResponseInfo); err != nil {
			return err
		}
		if err := writeString(&buf, props.ResponseInfo); err != nil {
			return err
		}
	}
	if props.ServerReference != "" {
		if err := buf.WriteByte(PropServerReference); err != nil {
			return err
		}
		if err := writeString(&buf, props.ServerReference); err != nil {
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

// validateProperties applies every constraint a property set has to satisfy to
// be put on the wire: the value rules of each Identifier, and Table 2-4's rule
// that the Identifier is defined for this packet type at all.
//
// The value rules run first so a property that is both out of range and out of
// place is reported as the value error it is.
func validateProperties(props *Properties, locus propertyLocus) error {
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
	if props.RequestResponseInfo != nil && *props.RequestResponseInfo > 1 {
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
	return validatePropertyLoci(props, locus)
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
