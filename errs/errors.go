// Package errs defines all error types used in the shark-mqtt project.
package errs

import "errors"

var (
	// 连接与会话错误
	ErrSessionClosed       = errors.New("session closed")
	ErrSessionNotFound     = errors.New("session not found")
	ErrWriteQueueFull      = errors.New("write queue full")
	ErrClientIDEmpty       = errors.New("client id is empty")
	ErrClientIDConflict    = errors.New("client id already connected")

	// 协议错误
	ErrInvalidPacket       = errors.New("invalid packet format")
	ErrUnsupportedVersion  = errors.New("unsupported protocol version")
	ErrIncomplete          = errors.New("incomplete packet")
	ErrPacketTooLarge      = errors.New("packet exceeds maximum size")

	// 认证错误
	ErrAuthFailed          = errors.New("authentication failed")
	ErrNotAuthorized       = errors.New("not authorized")

	// QoS 错误
	ErrInflightFull        = errors.New("inflight queue full")
	ErrDuplicatePacketID   = errors.New("duplicate packet id")
	ErrInvalidQoS          = errors.New("invalid qos level")

	// 存储错误
	ErrStoreUnavailable    = errors.New("store unavailable")
	ErrSessionExpired      = errors.New("session expired")

	// 服务错误
	ErrServerClosed        = errors.New("server closed")
	ErrAlreadyStarted      = errors.New("already started")
)
