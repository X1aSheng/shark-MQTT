package broker

import (
	"time"
)

// State represents the current state of a session.
type State int

const (
	StateDisconnected State = iota
	StateConnecting
	StateConnected
	StateDisconnecting
)

func (s State) String() string {
	switch s {
	case StateDisconnected:
		return "disconnected"
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateDisconnecting:
		return "disconnecting"
	default:
		return "unknown"
	}
}

// CloseReason represents why a session was closed.
type CloseReason int

const (
	ReasonClientDisconnect CloseReason = iota
	ReasonServerKick
	ReasonServerShut
	ReasonKeepAliveExpired
	ReasonProtocolError
	ReasonReplacedByNewConnection
)

func (r CloseReason) String() string {
	switch r {
	case ReasonClientDisconnect:
		return "client_disconnect"
	case ReasonServerKick:
		return "server_kick"
	case ReasonServerShut:
		return "server_shutdown"
	case ReasonKeepAliveExpired:
		return "keepalive_expired"
	case ReasonProtocolError:
		return "protocol_error"
	case ReasonReplacedByNewConnection:
		return "replaced"
	default:
		return "unknown"
	}
}

// Stats holds session statistics.
type Stats struct {
	MessagesReceived  uint64
	MessagesSent      uint64
	BytesReceived     uint64
	BytesSent         uint64
	ConnectCount      uint64
	LastConnectedAt   time.Time
	Subscriptions     int
	InflightCount     int
}
