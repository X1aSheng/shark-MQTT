//go:build !store_redis

package main

import (
	"fmt"

	"github.com/X1aSheng/shark-mqtt/config"
)

// openRedisPersistence is the stub for a binary built without store_redis.
//
// The configuration is valid and the operator asked for Redis, so the failure
// has to say exactly what to do about it. Returning an in-memory store here
// would hide the mismatch until the first restart lost every session.
func openRedisPersistence(cfg *config.Config) (*persistence, error) {
	return nil, fmt.Errorf(
		"storage_backend is %q but this binary was built without the store_redis tag; "+
			"rebuild with: go build -tags store_redis ./cmd", cfg.StorageBackend)
}
