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
| `listen_addr` | string | `:1883` | Address to listen on |
| `tls_enabled` | bool | `false` | Enable TLS |
| `tls_cert_file` | string | - | TLS certificate file path |
| `tls_key_file` | string | - | TLS private key file path |
| `connect_timeout` | duration | `10s` | Connection timeout |

**Examples:**
```yaml
listen_addr: "0.0.0.0:1883"
connect_timeout: 15s
```

```bash
export MQTT_LISTEN_ADDR=":1883"
export MQTT_CONNECT_TIMEOUT="15s"
```

### Connection Limits

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `max_connections` | int | `10000` | Maximum concurrent connections |
| `max_packet_size` | int | `262144` | Maximum packet size in bytes |
| `keep_alive` | uint16 | `60` | Keep-alive interval in seconds |
| `write_queue_size` | int | `256` | Per-connection write queue size |

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

#### Redis Backend

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `redis_addr` | string | `localhost:6379` | Redis server address |
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
| `badger_path` | string | `badger-data` | Path to BadgerDB data directory |

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
| `metrics_enabled` | bool | `false` | Enable Prometheus metrics |
| `metrics_addr` | string | `:9090` | Metrics server address |

**Examples:**
```yaml
metrics_enabled: true
metrics_addr: ":9090"
```

---

## YAML Configuration

### Complete Example

```yaml
# Network
listen_addr: ":1883"
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
metrics_addr: ":9090"
```

### TLS Configuration

```yaml
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
| `MQTT_MAX_CONNECTIONS` | `max_connections` |
| `MQTT_MAX_PACKET_SIZE` | `max_packet_size` |
| `MQTT_KEEP_ALIVE` | `keep_alive` |
| `MQTT_QOS_RETRY_INTERVAL` | `qos_retry_interval` |
| `MQTT_QOS_MAX_RETRIES` | `qos_max_retries` |
| `MQTT_QOS_MAX_INFLIGHT` | `qos_max_inflight` |
| `MQTT_STORAGE_BACKEND` | `storage_backend` |
| `MQTT_SESSION_EXPIRY_INTERVAL` | `session_expiry_interval` |
| `MQTT_REDIS_ADDR` | `redis_addr` |
| `MQTT_REDIS_PASSWORD` | `redis_password` |
| `MQTT_REDIS_DB` | `redis_db` |
| `MQTT_BADGER_PATH` | `badger_path` |
| `MQTT_LOG_LEVEL` | `log_level` |
| `MQTT_LOG_FORMAT` | `log_format` |
| `MQTT_METRICS_ENABLED` | `metrics_enabled` |
| `MQTT_METRICS_ADDR` | `metrics_addr` |

### Example Environment Configuration

```bash
# Basic configuration
export MQTT_LISTEN_ADDR=":1883"
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
export MQTT_METRICS_ADDR=":9090"
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
        ListenAddr:      ":1883",
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
metrics_addr: ":9090"
```

### Security-First Configuration

```yaml
# Production TLS
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

- [Development Guide](development.md)
- [Testing Guide](testing.md)
- [API Reference](API.md)
- [SECURITY.md](../SECURITY.md)