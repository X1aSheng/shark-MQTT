package protocol

import "errors"

// Protocol errors.
var (
	ErrInvalidPacket    = errors.New("invalid MQTT packet")
	ErrInvalidProtocol  = errors.New("invalid protocol name or version")
	ErrMalformedPacket    = errors.New("malformed packet")
	ErrPacketTooLarge   = errors.New("packet exceeds maximum size")
	ErrUnsupportedVersion = errors.New("unsupported MQTT version")
)
