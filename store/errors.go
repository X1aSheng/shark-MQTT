package store

import "github.com/X1aSheng/shark-mqtt/errs"

// Re-export errs sentinels for backward compatibility.
var (
	ErrSessionNotFound  = errs.ErrSessionNotFound
	ErrMessageNotFound  = errs.ErrMessageNotFound
	ErrRetainedNotFound = errs.ErrRetainedNotFound
	ErrStoreUnavailable = errs.ErrStoreUnavailable
)
