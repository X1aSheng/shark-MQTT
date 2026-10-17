//go:build !store_badger

package main

import (
	"fmt"

	"github.com/X1aSheng/shark-mqtt/config"
)

// openBadgerPersistence is the stub for a binary built without store_badger.
func openBadgerPersistence(cfg *config.Config) (*persistence, error) {
	return nil, fmt.Errorf(
		"storage_backend is %q but this binary was built without the store_badger tag; "+
			"rebuild with: go build -tags store_badger ./cmd", cfg.StorageBackend)
}
