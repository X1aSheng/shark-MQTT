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

### Basic Usage

```bash
# Pull and run
docker run -d \
  --name mqtt-broker \
  -p 1883:1883 \
  x1asheng/shark-mqtt
```

### With Configuration

```bash
# Create config file
cat > config.yaml << EOF
listen_addr: ":1883"
max_connections: 1000
log_level: "info"
EOF

# Run with config
docker run -d \
  --name mqtt-broker \
  -p 1883:1883 \
  -v $(pwd)/config.yaml:/app/config.yaml \
  x1asheng/shark-mqtt
```

### With TLS

```bash
# Create certificates
mkdir -p certs
openssl req -new -x509 -days 365 -nodes \
  -out certs/cert.pem \
  -keyout certs/key.pem

# Run with TLS
docker run -d \
  --name mqtt-broker \
  -p 1883:1883 \
  -p 8883:8883 \
  -v $(pwd)/certs:/app/certs \
  -e MQTT_TLS_ENABLED=true \
  -e MQTT_TLS_CERT_FILE=/app/certs/cert.pem \
  -e MQTT_TLS_KEY_FILE=/app/certs/key.pem \
  x1asheng/shark-mqtt
```

### With Redis

```bash
docker run -d \
  --name mqtt-broker \
  -p 1883:1883 \
  -e MQTT_STORAGE_BACKEND=redis \
  -e MQTT_REDIS_ADDR=redis:6379 \
  --link redis \
  x1asheng/shark-mqtt
```

### Build from Source

```bash
# Clone and build
git clone https://github.com/X1aSheng/shark-mqtt.git
cd shark-mqtt
docker build -t shark-mqtt:latest .

# Run
docker run -d -p 1883:1883 shark-mqtt:latest
```

---

## Docker Compose

### Basic Setup

```yaml
# docker-compose.yaml
version: '3.8'

services:
  mqtt:
    image: x1asheng/shark-mqtt:latest
    container_name: mqtt-broker
    ports:
      - "1883:1883"
    volumes:
      - ./config.yaml:/app/config.yaml
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "nc", "-z", "localhost", "1883"]
      interval: 30s
      timeout: 10s
      retries: 3
```

### Full Stack with Redis

```yaml
version: '3.8'

services:
  redis:
    image: redis:7-alpine
    container_name: mqtt-redis
    ports:
      - "6379:6379"
    volumes:
      - redis-data:/data
    restart: unless-stopped

  mqtt:
    image: x1asheng/shark-mqtt:latest
    container_name: mqtt-broker
    ports:
      - "1883:1883"
    - "8883:8883"  # TLS
    depends_on:
      redis:
        condition: service_healthy
    environment:
      MQTT_STORAGE_BACKEND: redis
      MQTT_REDIS_ADDR: redis:6379
      MQTT_LOG_LEVEL: info
    volumes:
      - ./config.yaml:/app/config.yaml
      - ./certs:/app/certs
    restart: unless-stopped

volumes:
  redis-data:
```

### High Availability

```yaml
version: '3.8'

services:
  redis:
    image: redis:7-alpine
    command: redis-server --appendonly yes
    volumes:
      - redis-data:/data
    restart: unless-stopped

  mqtt:
    image: x1asheng/shark-mqtt:latest
    deploy:
      replicas: 3
    ports:
      - "1883:1883"
    depends_on:
      - redis
    environment:
      MQTT_STORAGE_BACKEND: redis
      MQTT_REDIS_ADDR: redis:6379
    restart: unless-stopped

  haproxy:
    image: haproxy:2.8
    ports:
      - "1883:1883"
    volumes:
      - ./haproxy.cfg:/usr/local/etc/haproxy/haproxy.cfg
    depends_on:
      - mqtt

volumes:
  redis-data:
```

---

## Kubernetes

### Deployment

```yaml
# mqtt-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: shark-mqtt
  labels:
    app: shark-mqtt
spec:
  replicas: 2
  selector:
    matchLabels:
      app: shark-mqtt
  template:
    metadata:
      labels:
        app: shark-mqtt
    spec:
      containers:
        - name: mqtt
          image: x1asheng/shark-mqtt:latest
          ports:
            - containerPort: 1883
          env:
            - name: MQTT_STORAGE_BACKEND
              value: redis
            - name: MQTT_REDIS_ADDR
              value: redis:6379
            - name: MQTT_LOG_LEVEL
              value: info
          resources:
            requests:
              memory: "128Mi"
              cpu: "100m"
            limits:
              memory: "512Mi"
              cpu: "500m"
          livenessProbe:
            tcpSocket:
              port: 1883
            initialDelaySeconds: 30
            periodSeconds: 10
          readinessProbe:
            tcpSocket:
              port: 1883
            initialDelaySeconds: 5
            periodSeconds: 5
```

### Service

```yaml
# mqtt-service.yaml
apiVersion: v1
kind: Service
metadata:
  name: shark-mqtt
spec:
  selector:
    app: shark-mqtt
  ports:
    - protocol: TCP
      port: 1883
      targetPort: 1883
  type: LoadBalancer
```

### ConfigMap

```yaml
# mqtt-configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: mqtt-config
data:
  config.yaml: |
    listen_addr: ":1883"
    max_connections: 10000
    keep_alive: 60
    log_level: "info"
    qos_retry_interval: "10s"
    qos_max_retries: 3
```

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