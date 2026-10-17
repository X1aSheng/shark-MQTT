# Configuration Guide

This guide documents all configuration options for Shark-MQTT.

---

## Table of Contents

- [Configuration Sources](#configuration-sources)
- [Configuration Options](#configuration-options)
- [YAML Configuration](#yaml-configuration)
- [Environment Variables](#environment-variables)
- [Programmatic Configuration](#programmatic-configuration)
- [Production Recommendations](#production-recommendations)

---

## Configuration Sources

Shark-MQTT supports configuration from multiple sources with the following priority:

```
1. Programmatic options (highest priority)
2. YAML configuration file
3. Environment variables
4. Default values (lowest priority)
```

---

## Configuration Options

### Network Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `listen_addr` | string | `:18983` | Address to listen on |
| `listen_addrs` | []string | - | Additional MQTT-over-TCP addresses served by the same process. **Takes precedence over `listen_addr`** and does not merge with it: when set, `listen_addr` is ignored entirely. Duplicates and empty entries are dropped |
| `tls_enabled` | bool | `false` | Enable TLS |
| `tls_cert_file` | string | - | TLS certificate file path |
| `tls_key_file` | string | - | TLS private key file path |
| `tls_min_version` | uint16 | `0` | Min TLS version (0=default 1.2, 772=TLS 1.3) |
| `tls_max_version` | uint16 | `0` | Max TLS version (0=no limit) |
| `tls_mutual` | bool | `false` | Enable mTLS client cert verification |
| `tls_ca_cert_file` | string | - | CA cert file for mTLS |
| `connect_timeout` | duration | `10s` | Connection timeout |
| `tcp_keepalive_period` | duration | `60s` | OS-level TCP keep-alive period applied to accepted sockets; 0 disables |
| `drain_timeout` | duration | `5s` | How long Stop waits for in-flight QoS 1/2 handshakes before closing sockets |
| `ws_listen_addr` | string | - | MQTT over WebSocket address; empty disables |
| `ws_listen_addrs` | []string | - | Additional WebSocket addresses; takes precedence over `ws_listen_addr` |
| `wss_listen_addr` | string | - | MQTT over TLS WebSocket address; requires `tls_enabled` |
| `wss_listen_addrs` | []string | - | Additional WSS addresses; takes precedence over `wss_listen_addr` |

**Examples:**
```yaml
listen_addr: "0.0.0.0:18983"
connect_timeout: 15s
```

```bash
export MQTT_LISTEN_ADDR=":18983"
export MQTT_CONNECT_TIMEOUT="15s"
```

### Connection Limits

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `max_connections` | int | `10000` | Maximum concurrent connections |
| `max_packet_size` | int | `262144` | Maximum packet size in bytes |
| `keep_alive` | uint16 | `60` | Keep-alive interval in seconds |
| `write_queue_size` | int | `256` | Per-connection write queue size |
| `drain_timeout` | duration | `5s` | How long shutdown waits for in-flight QoS 1/2 messages to be acknowledged before closing sockets anyway. `0` closes immediately |
| `ws_listen_addr` | string | - | MQTT-over-WebSocket listen address (empty disables, R5) |
| `ws_listen_addrs` | []string | - | Array form of `ws_listen_addr`; takes precedence over it, same rule |
| `wss_listen_addr` | string | - | MQTT-over-TLS-WebSocket (WSS) listen address; requires `tls_enabled` |
| `wss_listen_addrs` | []string | - | Array form of `wss_listen_addr`; takes precedence over it, same rule |

**Examples:**
```yaml
max_connections: 50000
max_packet_size: 1048576  # 1MB
keep_alive: 120
```

### QoS Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `qos_retry_interval` | duration | `10s` | Retry interval for QoS 1/2 |
| `qos_max_retries` | int | `3` | Maximum retry attempts |
| `qos_max_inflight` | int | `100` | Maximum in-flight messages |

**Examples:**
```yaml
qos_retry_interval: 30s
qos_max_retries: 5
qos_max_inflight: 200
```

### Storage Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `storage_backend` | string | `memory` | Storage backend: memory, redis, badger |
| `session_expiry_interval` | duration | `24h` | Session expiration time |
| `sys_interval` | duration | `30s` | $SYS status topic publish interval (0 disables) |

> **The redis and badger backends must be compiled in.**
>
> The CLI wires the stores itself (`cmd/stores*.go`), but each backend lives
> behind a build tag because it pulls in its own client library:
>
> ```bash
> go build -tags store_redis  -o shark ./cmd   # storage_backend: redis
> go build -tags store_badger -o shark ./cmd   # storage_backend: badger
> go build                     -o shark ./cmd   # memory only
> ```
>
> Asking a binary for a backend it was not built with is a startup error, not a
> silent fallback:
>
> ```
> Failed to open storage backend: storage_backend is "redis" but this binary was
> built without the store_redis tag; rebuild with: go build -tags store_redis ./cmd
> ```
>
> A fallback to memory would start cleanly, look healthy and lose every session
> on the next restart, which is the worst outcome for a persistence setting.
>
> Embedders that build their own stores keep using `api.WithSessionStore` /
> `WithMessageStore` / `WithRetainedStore` instead of the CLI path.

#### Redis Backend

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `redis_addr` | string | (empty) | Redis server address; required when `storage_backend: redis` |
| `redis_password` | string | - | Redis password |
| `redis_db` | int | `0` | Redis database number |

**Examples:**
```yaml
storage_backend: redis
redis_addr: "10.0.0.1:6379"
redis_password: "secret"
redis_db: 1
```

```bash
export MQTT_REDIS_ADDR="10.0.0.1:6379"
export MQTT_REDIS_PASSWORD="secret"
```

#### BadgerDB Backend

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `badger_path` | string | (empty) | Path to BadgerDB data directory; required when `storage_backend: badger` |

**Examples:**
```yaml
storage_backend: badger
badger_path: "/var/lib/shark-mqtt/badger"
```

### Logging Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `log_level` | string | `info` | Log level: debug, info, warn, error |
| `log_format` | string | `text` | Log format: text, json |

**Examples:**
```yaml
log_level: debug
log_format: json
```

### Metrics Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `metrics_enabled` | bool | `true` | Serve the Prometheus /metrics endpoint (healthz/readyz always served) |
| `metrics_addr` | string | `:18999` | Metrics server address |
| `pprof_enabled` | bool | `false` | Serve `/debug/pprof/` on the metrics listener. **Validate rejects it unless `metrics_addr` is loopback**: a heap profile contains message payloads and usernames |

**Examples:**
```yaml
metrics_enabled: true
metrics_addr: ":18999"
```

### Authentication Configuration

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `auth_file` | string | - | Path to a users file (`name: bcrypt-hash`). When neither `auth_file` nor `-allow-all` is given, the broker refuses to start: an open broker must be an explicit decision |

The CLI also accepts `-allow-all`, which installs an authenticator that accepts
every client and prints a warning at startup. Use it for local testing only.

**Examples:**
```yaml
auth_file: "/etc/shark-mqtt/users.yaml"
```

```bash
export MQTT_AUTH_FILE="/etc/shark-mqtt/users.yaml"
```

### Management Endpoint Configuration

The management endpoints (`/clients`, `/topics`) expose client IDs, usernames
and topic names, so they are **off by default**. Enabling them without a token is
rejected unless the listener is loopback-only, and the token is compared with
`subtle.ConstantTimeCompare`.

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `management_enabled` | bool | `false` | Serve the read-only management endpoints |
| `management_token` | string | - | Bearer token required by every management request |

**Examples:**
```yaml
management_enabled: true
management_token: "change-me"
```

---

## YAML Configuration

### Complete Example

### Multiple Listen Ports

One process can serve several MQTT ports. `listen_addrs` **replaces** `listen_addr`
rather than adding to it -- when it is present, `listen_addr` is ignored. That is
deliberate: merging would silently open the default port next to the operator's
explicit list, which is a surprise nobody asked for.

```yaml
# Serves :18983 and :28983; listen_addr below is NOT used.
listen_addrs:
  - ":18983"
  - ":28983"
  - "192.168.1.10:3883"   # bind a specific interface
```

Repeat an address and only one socket is opened. Use `broker.Addrs()` to read back
every bound address (`broker.Addr()` still returns the first, for compatibility).

```yaml
# WebSocket works the same way.
ws_listen_addrs:
  - ":18984"
  - ":28984"
```

```yaml
# Network
listen_addr: ":18993"
connect_timeout: 15s
max_connections: 10000
max_packet_size: 262144
keep_alive: 60
write_queue_size: 256

# TLS
tls_enabled: true
tls_cert_file: "/etc/shark-mqtt/cert.pem"
tls_key_file: "/etc/shark-mqtt/key.pem"

# QoS
qos_retry_interval: 10s
qos_max_retries: 3
qos_max_inflight: 100

# Storage
storage_backend: redis
session_expiry_interval: 24h
redis_addr: "localhost:6379"
redis_password: ""
redis_db: 0
# badger_path: "/var/lib/shark-mqtt/badger"

# Logging
log_level: info
log_format: json

# Metrics
metrics_enabled: true
metrics_addr: ":18999"
```

### TLS Configuration

```yaml
listen_addr: ":18993"
tls_enabled: true
tls_cert_file: "/path/to/cert.pem"
tls_key_file: "/path/to/key.pem"
```

### Redis Storage

```yaml
storage_backend: redis
redis_addr: "10.0.0.1:6379"
redis_password: "your-password"
redis_db: 0
```

### BadgerDB Storage

```yaml
storage_backend: badger
badger_path: "/data/shark-mqtt"
```

---

## Connection Flapping Protection

A client that reconnects in a tight loop never holds many concurrent
connections, so `max_connections` does not catch it. Each attempt still costs a
goroutine, a file descriptor and a codec buffer, so a reconnect storm is a
resource-exhaustion vector.

| Key | Env | Default | Meaning |
| --- | --- | --- | --- |
| `flapping_enabled` | `MQTT_FLAPPING_ENABLED` | `false` | Turn detection on |
| `flapping_max_connects` | `MQTT_FLAPPING_MAX_CONNECTS` | `10` | Connections allowed per window per client |
| `flapping_window` | `MQTT_FLAPPING_WINDOW` | `10s` | Sliding window |
| `flapping_penalty_duration` | `MQTT_FLAPPING_PENALTY_DURATION` | `60s` | How long a client stays blocked |
| `flapping_max_tracked_clients` | `MQTT_FLAPPING_MAX_TRACKED_CLIENTS` | `10000` | Memory bound; oldest entries evicted |
| `flapping_cleanup_interval` | `MQTT_FLAPPING_CLEANUP_INTERVAL` | window | How often expired entries are swept |

```yaml
flapping_enabled: true
flapping_max_connects: 5
flapping_window: 10s
flapping_penalty_duration: 300s
```

**Off by default.** A limit that silently rejects clients is worse than no
limit when the limit is wrong. Turn it on once you know your clients'
reconnection behaviour -- a broker whose clients legitimately reconnect on a
schedule can throttle healthy traffic.

Two details worth knowing:

- Clients are tracked by **both** remote IP and client ID. Either key alone is
  evadable: a client that reconnects from a new ephemeral port defeats an
  IP-only check, and one that changes its Client ID defeats an ID-only check.
- The penalty does **not** extend on further attempts. Extending it would mean a
  client stuck in a reconnect storm could never recover, since every attempt
  would postpone its own release.

A rejected client gets MQTT 5.0 `0x8A` (Banned) or MQTT 3.1.1 `0x03` (Server
Unavailable), which is the closest code 3.1.1 has. Rejections are counted in
`errors_total{component="flapping"}`.

## Environment Variables

All configuration options can be set via environment variables with the `MQTT_` prefix.

### Environment Variable Mapping

| Environment Variable | YAML Equivalent |
|---------------------|-----------------|
| `MQTT_LISTEN_ADDR` | `listen_addr` |
| `MQTT_TLS_ENABLED` | `tls_enabled` |
| `MQTT_TLS_CERT_FILE` | `tls_cert_file` |
| `MQTT_TLS_KEY_FILE` | `tls_key_file` |
| `MQTT_CONNECT_TIMEOUT` | `connect_timeout` |
| `MQTT_DRAIN_TIMEOUT` | `drain_timeout` |
| `MQTT_MAX_CONNECTIONS` | `max_connections` |
| `MQTT_MAX_PACKET_SIZE` | `max_packet_size` |
| `MQTT_KEEP_ALIVE` | `keep_alive` |
| `MQTT_QOS_RETRY_INTERVAL` | `qos_retry_interval` |
| `MQTT_QOS_MAX_RETRIES` | `qos_max_retries` |
| `MQTT_QOS_MAX_INFLIGHT` | `qos_max_inflight` |
| `MQTT_STORAGE_BACKEND` | `storage_backend` |
| `MQTT_SESSION_EXPIRY` | `session_expiry_interval` |
| `MQTT_SYS_INTERVAL` | `sys_interval` |
| `MQTT_WS_LISTEN_ADDR` | `ws_listen_addr` |
| `MQTT_WSS_LISTEN_ADDR` | `wss_listen_addr` |
| `MQTT_REDIS_ADDR` | `redis_addr` |
| `MQTT_REDIS_PASSWORD` | `redis_password` |
| `MQTT_REDIS_DB` | `redis_db` |
| `MQTT_BADGER_PATH` | `badger_path` |
| `MQTT_LOG_LEVEL` | `log_level` |
| `MQTT_LOG_FORMAT` | `log_format` |
| `MQTT_METRICS_ENABLED` | `metrics_enabled` |
| `MQTT_METRICS_ADDR` | `metrics_addr` |
| `MQTT_WS_LISTEN_ADDR` | `ws_listen_addr` |
| `MQTT_WS_LISTEN_ADDRS` | `ws_listen_addrs` |
| `MQTT_WSS_LISTEN_ADDR` | `wss_listen_addr` |
| `MQTT_WSS_LISTEN_ADDRS` | `wss_listen_addrs` |
| `MQTT_TCP_KEEPALIVE_PERIOD` | `tcp_keepalive_period` |
| `MQTT_DRAIN_TIMEOUT` | `drain_timeout` |
| `MQTT_TLS_MIN_VERSION` | `tls_min_version` |
| `MQTT_TLS_MAX_VERSION` | `tls_max_version` |
| `MQTT_TLS_MUTUAL` | `tls_mutual` |
| `MQTT_TLS_CA_CERT_FILE` | `tls_ca_cert_file` |
| `MQTT_AUTH_FILE` | `auth_file` |
| `MQTT_MANAGEMENT_ENABLED` | `management_enabled` |
| `MQTT_MANAGEMENT_TOKEN` | `management_token` |
| `MQTT_FLAPPING_ENABLED` | `flapping_enabled` |
| `MQTT_FLAPPING_MAX_CONNECTS` | `flapping_max_connects` |
| `MQTT_FLAPPING_WINDOW` | `flapping_window` |
| `MQTT_FLAPPING_PENALTY_DURATION` | `flapping_penalty_duration` |
| `MQTT_FLAPPING_MAX_TRACKED_CLIENTS` | `flapping_max_tracked_clients` |
| `MQTT_FLAPPING_CLEANUP_INTERVAL` | `flapping_cleanup_interval` |

### Example Environment Configuration

```bash
# Basic configuration
export MQTT_LISTEN_ADDR=":18983"
export MQTT_LOG_LEVEL="info"

# TLS configuration
export MQTT_TLS_ENABLED="true"
export MQTT_TLS_CERT_FILE="/etc/shark-mqtt/cert.pem"
export MQTT_TLS_KEY_FILE="/etc/shark-mqtt/key.pem"

# Redis configuration
export MQTT_STORAGE_BACKEND="redis"
export MQTT_REDIS_ADDR="10.0.0.1:6379"
export MQTT_REDIS_PASSWORD="secret"

# Metrics
export MQTT_METRICS_ENABLED="true"
export MQTT_METRICS_ADDR=":18999"
```

---

## Programmatic Configuration

```go
package main

import (
    "github.com/X1aSheng/shark-mqtt/api"
    "github.com/X1aSheng/shark-mqtt/broker"
    "github.com/X1aSheng/shark-mqtt/config"
)

func main() {
    // Create custom config
    cfg := &config.Config{
        ListenAddr:      ":18983",
        KeepAlive:       60,
        MaxConnections:  10000,
        QoSRetryInterval: 10 * time.Second,
        // ... other options
    }

    // Create custom authenticator
    type MyAuth struct{}
    func (a *MyAuth) Authenticate(ctx context.Context, clientID, username, password string) error {
        if username == "admin" && password == "secret" {
            return nil
        }
        return errors.New("invalid credentials")
    }

    // Create broker with options
    broker := api.NewBroker(
        api.WithConfig(cfg),
        api.WithAuth(&MyAuth{}),
    )

    // Start broker
    if err := broker.Start(); err != nil {
        log.Fatal(err)
    }
}
```

### Option Functions

| Option | Description |
|--------|-------------|
| `WithConfig(cfg)` | Set custom config |
| `WithAuth(a)` | Set authenticator |
| `WithAuthorizer(z)` | Set authorizer |
| `WithSessionStore(s)` | Set session store |
| `WithMessageStore(m)` | Set message store |
| `WithRetainedStore(r)` | Set retained message store |
| `WithLogger(l)` | Set logger |
| `WithMetrics(m)` | Set metrics collector |
| `WithPluginManager(p)` | Set plugin manager |

---

## Production Recommendations

### High-Volume Configuration

```yaml
# For 50,000+ concurrent connections
max_connections: 75000
max_packet_size: 262144
write_queue_size: 512
qos_max_inflight: 200
qos_retry_interval: 5s
qos_max_retries: 5
```

### Low-Latency Configuration

```yaml
# For minimal latency
qos_retry_interval: 3s
qos_max_retries: 2
write_queue_size: 128
```

### High-Availability Configuration

```yaml
# With Redis for distributed sessions
storage_backend: redis
redis_addr: "redis-cluster:6379"
redis_password: "secret"
redis_db: 0
session_expiry_interval: 1h

# Enable metrics for monitoring
metrics_enabled: true
metrics_addr: ":18999"
```

### Security-First Configuration

```yaml
# Production TLS
listen_addr: ":18993"
tls_enabled: true
tls_cert_file: "/etc/shark-mqtt/fullchain.pem"
tls_key_file: "/etc/shark-mqtt/privkey.pem"

# Restrict connections
max_connections: 10000
max_packet_size: 65536

# Debug logging disabled
log_level: warn
```

---

## Configuration Validation

Shark-MQTT validates configuration on startup:

| Validation | Error |
|------------|-------|
| `listen_addr` required | `listen address is required` |
| `tls_enabled` requires cert/key | `TLS enabled but certificate/key not configured` |
| `max_connections` > 0 | `max_connections must be positive` |
| `qos_retry_interval` > 0 | `qos_retry_interval must be positive` |
| `redis_addr` required for redis backend | `redis_addr required for redis storage` |
| `badger_path` required for badger backend | `badger_path required for badger storage` |

---

## See Also

- [Development Guide](DEVELOPMENT.md)
- [Testing Guide](TESTING.md)
- [API Reference](API.md)
- [SECURITY.md](../architecture/SECURITY.md)




