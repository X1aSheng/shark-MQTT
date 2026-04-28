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
ports: `1883` (MQTT), `8883` (MQTT+TLS), and `9090` (health/metrics).

### Build

```bash
# Build from source
docker build -t shark-mqtt:latest .

# Or via Make
make docker-build
```

### Basic Usage

```bash
docker run -d \
  --name mqtt-broker \
  -p 1883:1883 \
  -p 9090:9090 \
  shark-mqtt:latest

# Verify health
curl http://localhost:9090/healthz
```

### With Environment Variables

All `MQTT_*` environment variables are supported:

```bash
docker run -d \
  --name mqtt-broker \
  -p 1883:1883 \
  -p 8883:8883 \
  -p 9090:9090 \
  -e MQTT_LOG_LEVEL=debug \
  -e MQTT_TLS_ENABLED=true \
  -e MQTT_TLS_CERT_FILE=/certs/cert.pem \
  -e MQTT_TLS_KEY_FILE=/certs/key.pem \
  -v $(pwd)/certs:/certs \
  shark-mqtt:latest
```

### With Redis

```bash
docker run -d \
  --name mqtt-broker \
  -p 1883:1883 \
  -e MQTT_STORAGE_BACKEND=redis \
  -e MQTT_REDIS_ADDR=redis-host:6379 \
  shark-mqtt:latest
```

### Smoke Test

```bash
# Build image, start container, verify health + MQTT connectivity
bash scripts/docker-test.sh
```

---

## Docker Compose

A `docker-compose.yml` is provided at the project root with standalone and
Redis-backed configurations.

### Standalone

```bash
docker compose up -d

# Check status
docker compose ps
curl http://localhost:9090/healthz

# Stop
docker compose down
```

### Full Stack with Redis

Edit `docker-compose.yml` and uncomment the `redis` and `mqtt-redis` service blocks,
then:

```bash
docker compose up -d
```

The standalone `mqtt` and Redis-backed `mqtt-redis` services cannot run at the same
time on port 1883 — comment out the one you don't need.

### Smoke Test (CI)

```bash
# Build, start, run tests, cleanup
docker compose -f docker-compose.test.yml up --build --exit-code-from test
```

---

## Kubernetes

Kustomize-ready manifests are in `k8s/base/` with a production overlay in
`k8s/overlays/production/`.

### Directory Structure

```
k8s/
├── base/
│   ├── kustomization.yaml
│   ├── namespace.yaml        # shark-mqtt namespace
│   ├── configmap.yaml        # broker configuration
│   ├── deployment.yaml       # replicas: 2, health probes on :9090
│   └── service.yaml          # ClusterIP (MQTT + health ports)
└── overlays/
    └── production/
        └── kustomization.yaml # replicas: 3, LoadBalancer, more resources
```

### Deploy

```bash
# Deploy base
kubectl apply -k k8s/base/

# Or via Make
make k8s-deploy

# Production overlay
make k8s-deploy-prod
```

### Verify

```bash
kubectl -n shark-mqtt get pods
kubectl -n shark-mqtt get svc

# Health check (port-forward)
kubectl -n shark-mqtt port-forward svc/shark-mqtt 9090:9090
curl http://localhost:9090/healthz
```

### Delete

```bash
kubectl delete -k k8s/base/
# Or: make k8s-delete
```

### Liveness & Readiness

The deployment uses HTTP probes against the health server (port 9090):

| Probe | Path | Port | Initial Delay |
|-------|------|------|---------------|
| liveness | `/healthz` | 9090 | 10s |
| readiness | `/readyz` | 9090 | 5s |

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
    bind *:1883
    default_backend mqtt_backend

frontend mqtt_tls_front
    bind *:8883 ssl crt /etc/ssl/certs/mqtt.pem
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
        listen 1883;
        proxy_pass mqtt_cluster;
        proxy_timeout 300s;
        proxy_connect_timeout 1s;
    }
}
```

---

## Production Checklist

### Security

- [ ] Enable TLS on port 8883
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
| `MQTT_LISTEN_ADDR` | Listen address | `:1883` |
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
| `MQTT_METRICS_ADDR` | Metrics server address | `:9090` |

---

## See Also

- [Configuration Guide](configuration.md)
- [API Reference](API.md)
- [Examples](../examples/)
- [Project Status](PROJECT_STATUS.md)