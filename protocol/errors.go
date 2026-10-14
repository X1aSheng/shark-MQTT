package protocol

import "github.com/X1aSheng/shark-mqtt/errs"

// Protocol errors are re-exported from the canonical errs package.
var (
	ErrInvalidPacket      = errs.ErrInvalidPacket
	ErrPacketTooLarge     = errs.ErrPacketTooLarge
	ErrUnsupportedVersion = errs.ErrUnsupportedVersion
)

// Protocol-specific errors not in the shared errs package.
var (
	ErrInvalidProtocol = errs.ErrInvalidProtocol
	ErrMalformedPacket = errs.ErrMalformedPacket
	// ErrProtocolError is the sentinel for the failures MQTT 5.0 spells out as
	// a "Protocol Error" rather than a malformed packet: the packet parses
	// cleanly and its fields are well typed, but a combination the peer is not
	// allowed to send was used. §4.13.2 answers it with a DISCONNECT carrying
	// reason code 0x82, where a Malformed Packet gets 0x81.
	ErrProtocolError = errs.ErrProtocolError
)
