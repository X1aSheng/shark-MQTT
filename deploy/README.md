# shark-mqtt 部署指南

## 快速开始

### Docker Compose

```bash
# 生产模式
docker compose -f deploy/docker/docker-compose.yml up -d

# 带 Prometheus 监控
docker compose -f deploy/docker/docker-compose.yml --profile prod up -d

# 测试模式
docker compose -f deploy/docker/docker-compose.test.yml up --abort-on-container-exit
```

### Docker

```bash
# 构建镜像
docker build -f deploy/docker/Dockerfile -t shark-mqtt:latest .

# 运行容器
docker run -d \
  -p 1883:1883 \
  -p 8883:8883 \
  -p 9090:9090 \
  -e MQTT_LOG_LEVEL=info \
  shark-mqtt:latest
```

---

## 端口参考

| 端口 | 协议 | 说明 |
|------|------|------|
| 1883 | TCP | MQTT 标准端口 |
| 8883 | TCP | MQTT TLS 端口 |
| 9090 | TCP | Metrics/Health/Ready（/metrics, /healthz, /readyz） |

---

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `MQTT_LOG_LEVEL` | `info` | 日志级别: debug, info, warn, error |
| `MQTT_LOG_FORMAT` | `json` | 日志格式: json, text |
| `MQTT_METRICS_ADDR` | `:9090` | Metrics 监听地址 |
| `MQTT_METRICS_ENABLED` | `true` | 启用 Prometheus 指标 |

---

## Kubernetes 部署

### 使用 kubectl + Kustomize

```bash
kubectl apply -k deploy/k8s/app/
```

### 使用 Helm

```bash
# 默认配置
helm install shark-mqtt deploy/k8s/helm/shark-mqtt/ \
  --namespace shark-mqtt --create-namespace

# 生产配置
helm install shark-mqtt deploy/k8s/helm/shark-mqtt/ \
  --namespace shark-mqtt --create-namespace \
  --values deploy/k8s/helm/shark-mqtt/values.yaml \
  --values deploy/k8s/helm/shark-mqtt/values-prod.yaml

# 升级
helm upgrade shark-mqtt deploy/k8s/helm/shark-mqtt/ \
  --namespace shark-mqtt
```

---

## 监控

### Prometheus 指标

指标地址: `http://<host>:9090/metrics`

主要指标:
- `shark_mqtt_connections_total` — 连接总数
- `shark_mqtt_messages_total` — 消息总数
- `shark_mqtt_subscriptions_total` — 订阅总数
- `shark_mqtt_bytes_total` — 传输字节总数
- `shark_mqtt_errors_total` — 错误总数

### 健康检查

- `/healthz` — 存活检查（返回 200 表示服务运行中）
- `/readyz` — 就绪检查（返回 200 表示可接收请求）

---

## 生产检查清单

- [ ] 配置 TLS 证书（MQTT over TLS 端口 8883）
- [ ] 设置资源限制（CPU/Memory）
- [ ] 配置 HPA 自动扩缩
- [ ] 启用 NetworkPolicy 网络隔离
- [ ] 配置 PodDisruptionBudget
- [ ] 启用 Prometheus 监控
- [ ] 配置日志聚合（JSON 格式输出到 stdout）
- [ ] 设置 terminationGracePeriodSeconds >= 60
- [ ] 配置 Ingress（用于 MQTT 外部访问）
- [ ] 使用非 root 用户运行容器
- [ ] 配置 Redis 持久化（生产环境）

---

## 故障排查

### 端口冲突

确保 1883（MQTT）、9090（Metrics）端口未被占用。

### Redis 连接

使用 Redis 作为存储后端时，确保 Redis 服务可达且健康。

### 优雅关闭

默认关闭超时 30 秒。在 K8s 中设为 60 秒需同步调整 `terminationGracePeriodSeconds`。

### MQTT 客户端连接

```bash
# 使用 mosquitto 客户端测试
mosquitto_pub -h localhost -p 1883 -t "test/topic" -m "hello"
mosquitto_sub -h localhost -p 1883 -t "test/topic"
```
