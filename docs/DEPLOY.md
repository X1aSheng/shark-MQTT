# Shark-MQTT Deployment Guide

This guide covers various deployment options for Shark-MQTT.

---

## Table of Contents

- [Docker](#docker)
- [Docker Compose](#docker-compose)
- [Kubernetes](#kubernetes)
- [Systemd](#systemd)
- [Reverse Proxy](#reverse-proxy)
- [Production Checklist](#production-checklist)

---

## Docker

A multi-stage `Dockerfile` is provided at the project root. The image exposes three
ports: `18983` (MQTT), `18993` (MQTT+TLS), and `18999` (health/metrics).

### Build

```bash
# Build from source
docker build -f deploy/docker/Dockerfile -t shark-mqtt:latest .

# Or via Make
make docker-build
```

### Basic Usage

```bash
docker run -d \
  --name mqtt-broker \
  -p 18983:18983 \
  -p 18999:18999 \
  shark-mqtt:latest -addr=:18983 -allow-all

# Verify health
curl http://localhost:18999/healthz
```

### With Environment Variables

All `MQTT_*` environment variables are supported:

```bash
docker run -d \
  --name mqtt-broker \
  -p 18983:18983 \
  -p 18993:18993 \
  -p 18999:18999 \
  -e MQTT_LOG_LEVEL=debug \
  -e MQTT_TLS_ENABLED=true \
  -e MQTT_TLS_CERT_FILE=/certs/cert.pem \
  -e MQTT_TLS_KEY_FILE=/certs/key.pem \
  -v $(pwd)/certs:/certs \
  shark-mqtt:latest -addr=:18983 -allow-all
```

### With Redis

```bash
docker run -d \
  --name mqtt-broker \
  -p 18983:18983 \
  -e MQTT_STORAGE_BACKEND=redis \
  -e MQTT_REDIS_ADDR=redis-host:6379 \
  shark-mqtt:latest -addr=:18983 -allow-all
```

`-allow-all` is for smoke tests and development-only deployments. Production
deployments must replace it with a real authenticator/authorizer integration.

### Smoke Test

```bash
# Build image, start container, verify health + MQTT connectivity
bash scripts/docker-test.sh
```

---

## Docker Compose

A Docker Compose file is provided at `deploy/docker/docker-compose.yml` with standalone and
Redis-backed configurations.

### Standalone

```bash
docker compose -f deploy/docker/docker-compose.yml up -d

# Check status
docker compose -f deploy/docker/docker-compose.yml ps
curl http://localhost:18999/healthz

# Stop
docker compose -f deploy/docker/docker-compose.yml down
```

### Full Stack with Redis

Edit `deploy/docker/docker-compose.yml` and uncomment the `redis` and `mqtt-redis` service blocks,
then:

```bash
docker compose -f deploy/docker/docker-compose.yml up -d
```

The standalone `mqtt` and Redis-backed `mqtt-redis` services cannot run at the same
time on port 18983 — comment out the one you don't need.

### Smoke Test (CI)

```bash
# Build, start, run tests, cleanup
docker compose -f deploy/docker/docker-compose.test.yml up --build --exit-code-from test-runner
```

---

## Kubernetes

Kustomize-ready manifests are in `deploy/k8s/app/`. Optional Prometheus
infrastructure lives in `deploy/k8s/infra/prometheus/`.

### Directory Structure

```
deploy/k8s/
├── app/
│   ├── kustomization.yaml
│   ├── namespace.yaml         # shark-mqtt namespace
│   ├── configmap.yaml         # broker configuration
│   ├── deployment.yaml        # replicas: 2, health probes on :18999
│   └── service.yaml           # ClusterIP (MQTT + health ports)
└── infra/
    └── prometheus/
        ├── kustomization.yaml
        ├── configmap.yaml
        ├── deployment.yaml
        └── service.yaml
```

### Deploy

```bash
# Deploy base
kubectl apply -k deploy/k8s/app/

# Optional Prometheus monitoring
kubectl apply -k deploy/k8s/infra/prometheus/
```

### Verify

```bash
kubectl -n shark-mqtt get pods
kubectl -n shark-mqtt get svc

# Health check (port-forward)
kubectl -n shark-mqtt port-forward svc/shark-mqtt 18999:18999
curl http://localhost:18999/healthz
```

### Delete

```bash
kubectl delete -k k8s/base/
# Or: make k8s-delete
```

### Liveness & Readiness

The deployment uses HTTP probes against the health server (port 18999):

| Probe | Path | Port | Initial Delay |
|-------|------|------|---------------|
| liveness | `/healthz` | 18999 | 10s |
| readiness | `/readyz` | 18999 | 5s |

`/healthz` always returns 200 while the process is running.
`/readyz` returns 200 once the MQTT server is accepting connections, 503 otherwise.

---

## Systemd

### Service File

```ini
# /etc/systemd/system/shark-mqtt.service
[Unit]
Description=Shark-MQTT Broker
After=network.target

[Service]
Type=simple
User=mqtt
Group=mqtt
ExecStart=/usr/local/bin/shark-mqtt --config /etc/shark-mqtt/config.yaml
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

# Security hardening
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/shark-mqtt /var/log/shark-mqtt
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

### Installation

```bash
# Create user
sudo useradd -r -s /bin/false mqtt

# Create directories
sudo mkdir -p /var/lib/shark-mqtt /var/log/shark-mqtt /etc/shark-mqtt
sudo chown mqtt:mqtt /var/lib/shark-mqtt /var/log/shark-mqtt /etc/shark-mqtt

# Copy binary
sudo cp shark-mqtt /usr/local/bin/

# Install service
sudo cp shark-mqtt.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable shark-mqtt
sudo systemctl start shark-mqtt
```

---

## Reverse Proxy

### HAProxy

```haproxy
# /etc/haproxy/haproxy.cfg
global
    log stdout format raw local0
    maxconn 4096

defaults
    log     global
    mode    tcp
    option  tcplog

frontend mqtt_front
    bind *:18983
    default_backend mqtt_backend

frontend mqtt_tls_front
    bind *:18993 ssl crt /etc/ssl/certs/mqtt.pem
    default_backend mqtt_backend

backend mqtt_backend
    balance roundrobin
    server mqtt1 127.0.0.1:1884 check
    server mqtt2 127.0.0.1:1885 check
```

### Nginx (TCP Stream)

```nginx
stream {
    upstream mqtt_cluster {
        server 127.0.0.1:1884;
        server 127.0.0.1:1885;
    }

    server {
        listen 18983;
        proxy_pass mqtt_cluster;
        proxy_timeout 300s;
        proxy_connect_timeout 1s;
    }
}
```

---

## Production Checklist

### Security

- [ ] Enable TLS on port 18993
- [ ] Use valid SSL certificates
- [ ] Implement authentication (never use AllowAllAuth in production)
- [ ] Configure ACLs for topic access control
- [ ] Enable firewall rules
- [ ] Use network segmentation
- [ ] Regular security updates

### Performance

- [ ] Set appropriate `max_connections`
- [ ] Configure `max_packet_size` based on use case
- [ ] Use Redis for multi-instance deployments
- [ ] Monitor memory usage
- [ ] Enable metrics endpoint
- [ ] Configure keep-alive appropriately

### Reliability

- [ ] Use Redis for session persistence
- [ ] Configure proper `keep_alive` values
- [ ] Set up monitoring and alerting
- [ ] Configure log rotation
- [ ] Regular backups of configuration
- [ ] Test disaster recovery

### Monitoring

Recommended metrics to monitor:
- `mqtt_connections_active`
- `mqtt_messages_published_total`
- `mqtt_messages_delivered_total`
- `mqtt_messages_dropped_total`
- `mqtt_inflight_count`
- `mqtt_session_count`

---

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MQTT_LISTEN_ADDR` | Listen address | `:18983` |
| `MQTT_TLS_ENABLED` | Enable TLS | `false` |
| `MQTT_TLS_CERT_FILE` | TLS certificate file | - |
| `MQTT_TLS_KEY_FILE` | TLS key file | - |
| `MQTT_CONNECT_TIMEOUT` | Connection timeout | `10s` |
| `MQTT_KEEP_ALIVE` | Keep alive timeout (seconds) | `60` |
| `MQTT_MAX_CONNECTIONS` | Max concurrent connections | `10000` |
| `MQTT_MAX_PACKET_SIZE` | Max packet size | `262144` |
| `MQTT_WRITE_QUEUE_SIZE` | Per-connection write queue size | `256` |
| `MQTT_QOS_RETRY_INTERVAL` | QoS retry interval | `10s` |
| `MQTT_QOS_MAX_RETRIES` | Max QoS retry attempts | `3` |
| `MQTT_QOS_MAX_INFLIGHT` | Max in-flight messages | `100` |
| `MQTT_STORAGE_BACKEND` | Storage type (memory/redis/badger) | `memory` |
| `MQTT_SESSION_EXPIRY_INTERVAL` | Session expiration time | `24h` |
| `MQTT_REDIS_ADDR` | Redis address | `localhost:6379` |
| `MQTT_REDIS_PASSWORD` | Redis password | - |
| `MQTT_REDIS_DB` | Redis database number | `0` |
| `MQTT_BADGER_PATH` | BadgerDB data directory | `badger-data` |
| `MQTT_LOG_LEVEL` | Log level (debug/info/warn/error) | `info` |
| `MQTT_LOG_FORMAT` | Log format (text/json) | `text` |
| `MQTT_METRICS_ENABLED` | Enable Prometheus metrics | `false` |
| `MQTT_METRICS_ADDR` | Metrics server address | `:18999` |

---

## See Also

- [Configuration Guide](configuration.md)
- [API Reference](API.md)
- [Examples](../examples/)
- [Latest Review](PROJECT-REVIEW-260518-233900.md)
