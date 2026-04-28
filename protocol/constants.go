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

	// Failure reason codes (MQTT 5.0)
	ReasonCodeUnspecifiedError         = 0x80
	ReasonCodeMalformedPacket          = 0x81
	ReasonCodeProtocolError            = 0x82
	ReasonCodeImplementationSpecific   = 0x83
	ReasonCodeNotAuthorized            = 0x87
	ReasonCodeServerBusy               = 0x89
	ReasonCodeBadAuthMethod             = 0x8C
	ReasonCodeTopicFilterInvalid       = 0x8F
	ReasonCodePacketIdentifierInUse    = 0x91
	ReasonCodePacketIdentifierNotFound = 0x92
	ReasonCodeReceiveMaxExceeded       = 0x93
	ReasonCodeTopicAliasInvalid        = 0x94
	ReasonCodePacketTooLarge           = 0x95
	ReasonCodeMessageRateTooHigh       = 0x96
	ReasonCodeQuotaExceeded            = 0x97
	ReasonCodeAdministrativeAction     = 0x98
	ReasonCodePayloadFormatInvalid     = 0x99
	ReasonCodeRetainNotSupported       = 0xA0
	ReasonCodeQoSNotSupported          = 0xA1
	ReasonCodeUseAnotherServer         = 0x9C
	ReasonCodeServerMoved              = 0x9D
	ReasonCodeTopicNameInvalid        = 0x90
	ReasonCodeSharedSubNotSupported    = 0xA2
	ReasonCodeConnectionRateExceeded   = 0x9F
	ReasonCodeMaxConnectTime           = 0xA3
	ReasonCodeSubscriptionIDsNotSupported = 0xA4
	ReasonCodeWildcardSubNotSupported  = 0xA5
)

// SubAck failure codes.
const (
	SubAckFailure = 0x80
)

// DISCONNECT reason codes (MQTT 5.0).
const (
	DisconnectNormalDisconnection     = 0x00
	DisconnectClientReasonUnspecified  = 0x04
	DisconnectProtocolError            = 0x82
	DisconnectImplementationError      = 0x83
	DisconnectKeepAliveTimeout         = 0x8D
	DisconnectSessionTakenOver         = 0x8E
	DisconnectTopicFilterInvalid       = 0x8F
	DisconnectTopicNameInvalid         = 0x90
	DisconnectReceiveMaximumExceeded   = 0x93
	DisconnectTopicAliasInvalid        = 0x94
	DisconnectPacketTooLarge           = 0x95
	DisconnectMessageRateTooHigh       = 0x96
	DisconnectQuotaExceeded            = 0x97
	DisconnectAdministrativeAction     = 0x98
	DisconnectPayloadFormatInvalid     = 0x99
	DisconnectRetainNotSupported       = 0xA0
	DisconnectQoSNotSupported          = 0xA1
	DisconnectUseAnotherServer         = 0x9C
	DisconnectServerMoved              = 0x9D
	DisconnectSharedSubNotSupported    = 0xA2
	DisconnectConnectionRateExceeded   = 0x9F
	DisconnectMaxConnectTime           = 0xA3
	DisconnectSubscriptionIDsNotSupported = 0xA4
	DisconnectWildcardSubNotSupported  = 0xA5
)

// AUTH reason codes (MQTT 5.0).
const (
	AuthSuccess            = 0x18
	AuthContinueAuth       = 0x19
	AuthReAuth             = 0x1A
)
