//go:build store_redis

package main

import (
	goredis "github.com/redis/go-redis/v9"

	"github.com/X1aSheng/shark-mqtt/config"
	redisstore "github.com/X1aSheng/shark-mqtt/store/redis"
)

// openRedisPersistence wires the Redis-backed stores for storage_backend=redis.
// Build with: go build -tags store_redis ./cmd
func openRedisPersistence(cfg *config.Config) (*persistence, error) {
	client := goredis.NewClient(&goredis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	// Session TTL follows session_expiry_interval so Redis keys do not outlive
	// the session state they hold; the stores fall back to 24h when this is 0.
	ttl := cfg.SessionExpiryInterval.Duration()

	return &persistence{
		session:  redisstore.NewSessionStore(redisstore.SessionStoreConfig{Client: client, TTL: ttl}),
		message:  redisstore.NewMessageStore(redisstore.MessageStoreConfig{Client: client, TTL: ttl}),
		retained: redisstore.NewRetainedStore(redisstore.RetainedStoreConfig{Client: client}),
		close:    func() { _ = client.Close() },
	}, nil
}
