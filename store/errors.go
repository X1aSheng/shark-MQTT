package store

import "errors"

var (
	ErrSessionNotFound  = errors.New("session not found")
	ErrMessageNotFound  = errors.New("message not found")
	ErrRetainedNotFound = errors.New("retained message not found")
	ErrStoreUnavailable = errors.New("store unavailable")
)
