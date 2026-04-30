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
)
