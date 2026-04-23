package protocol

import (
	"fmt"
)

// PacketType represents the MQTT packet type.
type PacketType byte

const (
	PacketTypeReserved    PacketType = 0
	PacketTypeConnect     PacketType = 1
	PacketTypeConnAck      PacketType = 2
	PacketTypePublish      PacketType = 3
	PacketTypePubAck       PacketType = 4
	PacketTypePubRec       PacketType = 5
	PacketTypePubRel       PacketType = 6
	PacketTypePubComp      PacketType = 7
	PacketTypeSubscribe    PacketType = 8
	PacketTypeSubAck       PacketType = 9
	PacketTypeUnsubscribe  PacketType = 10
	PacketTypeUnsubAck     PacketType = 11
	PacketTypePingReq      PacketType = 12
	PacketTypePingResp     PacketType = 13
	PacketTypeDisconnect   PacketType = 14
	PacketTypeAuth         PacketType = 15
)

func (t PacketType) String() string {
	switch t {
	case PacketTypeConnect:
		return "CONNECT"
	case PacketTypeConnAck:
		return "CONNACK"
	case PacketTypePublish:
		return "PUBLISH"
	case PacketTypePubAck:
		return "PUBACK"
	case PacketTypePubRec:
		return "PUBREC"
	case PacketTypePubRel:
		return "PUBREL"
	case PacketTypePubComp:
		return "PUBCOMP"
	case PacketTypeSubscribe:
		return "SUBSCRIBE"
	case PacketTypeSubAck:
		return "SUBACK"
	case PacketTypeUnsubscribe:
		return "UNSUBSCRIBE"
	case PacketTypeUnsubAck:
		return "UNSUBACK"
	case PacketTypePingReq:
		return "PINGREQ"
	case PacketTypePingResp:
		return "PINGRESP"
	case PacketTypeDisconnect:
		return "DISCONNECT"
	case PacketTypeAuth:
		return "AUTH"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", byte(t))
	}
}

// MQTT protocol version constants.
const (
	Version31   = 3
	Version311  = 4
	Version50   = 5
)

// Protocol name strings.
const (
	ProtocolNameMQTT      = "MQTT"
	ProtocolNameMQIsdp    = "MQIsdp"
)

// ConnAck reason codes.
const (
	ConnAckAccepted                     = 0x00
	ConnAckUnacceptableProtocol         = 0x01
	ConnAckIdentifierRejected           = 0x02
	ConnAckServerUnavailable            = 0x03
	ConnAckBadUsernameOrPassword        = 0x04
	ConnAckNotAuthorized                = 0x05
	// MQTT 5.0 extended reason codes
	ConnAckUnspecifiedError             = 0x80
	ConnAckMalformedPacket              = 0x81
	ConnAckProtocolError                = 0x82
	ConnAckImplementationSpecificError  = 0x83
	ConnAckUnsupportedProtocolVersion   = 0x84
	ConnAckClientIdentifierNotValid     = 0x85
	ConnAckBadUsernameOrPassword5       = 0x86
	ConnAckNotAuthorized5               = 0x87
	ConnAckServerUnavailable5           = 0x88
	ConnAckServerBusy                   = 0x89
	ConnAckBanned                       = 0x8A
)

// PubAck/SubAck reason codes.
const (
	ReasonCodeSuccess                  = 0x00
	ReasonCodeGrantedQoS0              = 0x00
	ReasonCodeGrantedQoS1              = 0x01
	ReasonCodeGrantedQoS2              = 0x02
	ReasonCodeUnspecifiedError         = 0x80
	ReasonCodeInvalidTopic             = 0x8F
	ReasonCodePacketIdentifierInUse    = 0x91
	ReasonCodeQuotaExceeded            = 0x97
	ReasonCodeNotAuthorized            = 0x87
)

// SubAck failure codes.
const (
	SubAckFailure = 0x80
)
