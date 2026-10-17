//go:build store_badger

package main

import (
	"fmt"

	badger "github.com/dgraph-io/badger/v4"

	"github.com/X1aSheng/shark-mqtt/config"
	badgerstore "github.com/X1aSheng/shark-mqtt/store/badger"
)

// openBadgerPersistence wires the BadgerDB-backed stores for
// storage_backend=badger. Build with: go build -tags store_badger ./cmd
func openBadgerPersistence(cfg *config.Config) (*persistence, error) {
	// Badger writes its own INFO lines through the std logger by default, which
	// would bypass log_level entirely; the broker's structured logger covers
	// every store failure that matters.
	db, err := badger.Open(badger.DefaultOptions(cfg.BadgerPath).WithLogger(nil))
	if err != nil {
		return nil, fmt.Errorf("open badger at %q: %w", cfg.BadgerPath, err)
	}

	return &persistence{
		session:  badgerstore.NewSessionStore(badgerstore.SessionStoreConfig{DB: db}),
		message:  badgerstore.NewMessageStore(badgerstore.MessageStoreConfig{DB: db}),
		retained: badgerstore.NewRetainedStore(badgerstore.RetainedStoreConfig{DB: db}),
		close:    func() { _ = db.Close() },
	}, nil
}
