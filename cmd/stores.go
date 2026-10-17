package main

import (
	"fmt"

	"github.com/X1aSheng/shark-mqtt/config"
	mqttstore "github.com/X1aSheng/shark-mqtt/store"
)

// persistence bundles the three stores the broker needs, plus the release hook
// for whatever they opened (a Redis pool, a Badger directory lock).
type persistence struct {
	session  mqttstore.SessionStore
	message  mqttstore.MessageStore
	retained mqttstore.RetainedStore
	close    func()
}

// openPersistence returns the stores selected by cfg.StorageBackend.
//
// The redis and badger implementations are build-tagged (store_redis /
// store_badger) because each pulls in a client library. A binary can therefore
// be asked for a backend it was not built with, and the honest answer is an
// error naming the tag. Falling back to memory would start cleanly, look
// healthy and lose every session on restart, which is the worst possible
// outcome for a persistence setting.
//
// The backend names come from config.Validate, which has already rejected
// anything outside this set, so the default branch is a bug rather than user
// input.
func openPersistence(cfg *config.Config) (*persistence, error) {
	switch cfg.StorageBackend {
	case "", "memory":
		return &persistence{close: func() {}}, nil
	case "redis":
		return openRedisPersistence(cfg)
	case "badger":
		return openBadgerPersistence(cfg)
	default:
		return nil, fmt.Errorf("unknown storage_backend %q (want memory, redis or badger)", cfg.StorageBackend)
	}
}
