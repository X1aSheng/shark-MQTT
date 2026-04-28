// Package protocol provides MQTT 3.1.1 and 5.0 packet definitions and codec.
package protocol

// FixedHeader represents the MQTT fixed header for all packet types.
type FixedHeader struct {
	PacketType      PacketType
	Dup             bool
	QoS             uint8
	Retain          bool
	RemainingLength int
}

// Packet is the base interface for all MQTT packets.
type Packet interface {
	GetFixedHeader() *FixedHeader
}

// ConnectPacket represents a CONNECT packet.
type ConnectPacket struct {
	FixedHeader
	ProtocolName    string
	ProtocolVersion uint8
	Flags           ConnectFlags
	KeepAlive       uint16
	ClientID        string
	WillTopic       string
	WillMessage     []byte
	Username        string
	Password        []byte
	Properties      *Properties // MQTT 5.0 only
	WillProperties  *Properties // MQTT 5.0 only
}

// ConnAckPacket represents a CONNACK packet.
type ConnAckPacket struct {
	FixedHeader
	ReasonCode     byte
	SessionPresent bool
	Properties     *Properties // MQTT 5.0 only
}

// PublishPacket represents a PUBLISH packet.
type PublishPacket struct {
	FixedHeader
	PacketID   uint16
	Topic      string
	Payload    []byte
	Properties *Properties // MQTT 5.0 only
}

// PubAckPacket represents a PUBACK packet.
type PubAckPacket struct {
	FixedHeader
	PacketID   uint16
	ReasonCode byte
	Properties *Properties // MQTT 5.0 only
}

// PubRecPacket represents a PUBREC packet.
type PubRecPacket struct {
	FixedHeader
	PacketID   uint16
	ReasonCode byte
	Properties *Properties // MQTT 5.0 only
}

// PubRelPacket represents a PUBREL packet.
type PubRelPacket struct {
	FixedHeader
	PacketID   uint16
	ReasonCode byte
	Properties *Properties // MQTT 5.0 only
}

// PubCompPacket represents a PUBCOMP packet.
type PubCompPacket struct {
	FixedHeader
	PacketID   uint16
	ReasonCode byte
	Properties *Properties // MQTT 5.0 only
}

// SubscribePacket represents a SUBSCRIBE packet.
type SubscribePacket struct {
	FixedHeader
	PacketID   uint16
	Topics     []TopicFilter
	Properties *Properties // MQTT 5.0 only
}

// TopicFilter represents a subscription topic with QoS and MQTT 5.0 options.
type TopicFilter struct {
	Topic             string
	QoS               uint8
	NoLocal           bool  // MQTT 5.0: don't forward to own subscriptions
	RetainAsPublished bool  // MQTT 5.0: preserve retain flag
	RetainHandling    uint8 // MQTT 5.0: 0=send retained, 1=send if new, 2=don't send
}

// SubAckPacket represents a SUBACK packet.
type SubAckPacket struct {
	FixedHeader
	PacketID    uint16
	ReasonCodes []byte
	Properties  *Properties // MQTT 5.0 only
}

// UnsubscribePacket represents an UNSUBSCRIBE packet.
type UnsubscribePacket struct {
	FixedHeader
	PacketID   uint16
	Topics     []string
	Properties *Properties // MQTT 5.0 only
}

// UnsubAckPacket represents an UNSUBACK packet.
type UnsubAckPacket struct {
	FixedHeader
	PacketID    uint16
	ReasonCodes []byte
	Properties  *Properties // MQTT 5.0 only
}

// PingReqPacket represents a PINGREQ packet.
type PingReqPacket struct {
	FixedHeader
}

// PingRespPacket represents a PINGRESP packet.
type PingRespPacket struct {
	FixedHeader
}

// DisconnectPacket represents a DISCONNECT packet.
type DisconnectPacket struct {
	FixedHeader
	ReasonCode byte
	Properties *Properties // MQTT 5.0 only
}

// AuthPacket represents an AUTH packet (MQTT 5.0 only).
type AuthPacket struct {
	FixedHeader
	ReasonCode byte
	Properties *Properties
}

// ConnectFlags holds the CONNECT flags byte.
type ConnectFlags struct {
	UsernameFlag  bool
	PasswordFlag  bool
	WillRetain    bool
	WillQoS       uint8
	WillFlag      bool
	WillTopicFlag bool
	CleanSession  bool // CleanStart in MQTT 5.0
	Reserved      bool
}

// GetFixedHeader returns the fixed header (satisfies Packet interface).
func (p *ConnectPacket) GetFixedHeader() *FixedHeader     { return &p.FixedHeader }
func (p *ConnAckPacket) GetFixedHeader() *FixedHeader     { return &p.FixedHeader }
func (p *PublishPacket) GetFixedHeader() *FixedHeader     { return &p.FixedHeader }
func (p *PubAckPacket) GetFixedHeader() *FixedHeader      { return &p.FixedHeader }
func (p *PubRecPacket) GetFixedHeader() *FixedHeader      { return &p.FixedHeader }
func (p *PubRelPacket) GetFixedHeader() *FixedHeader      { return &p.FixedHeader }
func (p *PubCompPacket) GetFixedHeader() *FixedHeader     { return &p.FixedHeader }
func (p *SubscribePacket) GetFixedHeader() *FixedHeader   { return &p.FixedHeader }
func (p *SubAckPacket) GetFixedHeader() *FixedHeader      { return &p.FixedHeader }
func (p *UnsubscribePacket) GetFixedHeader() *FixedHeader { return &p.FixedHeader }
func (p *UnsubAckPacket) GetFixedHeader() *FixedHeader    { return &p.FixedHeader }
func (p *PingReqPacket) GetFixedHeader() *FixedHeader     { return &p.FixedHeader }
func (p *PingRespPacket) GetFixedHeader() *FixedHeader    { return &p.FixedHeader }
func (p *DisconnectPacket) GetFixedHeader() *FixedHeader  { return &p.FixedHeader }
func (p *AuthPacket) GetFixedHeader() *FixedHeader        { return &p.FixedHeader }
