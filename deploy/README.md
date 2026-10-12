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
  -p 18983:18983 \
  -p 18993:18993 \
  -p 18999:18999 \
  -e MQTT_LOG_LEVEL=info \
  shark-mqtt:latest -addr=:18983 -allow-all
```

`-allow-all` is intended for local smoke tests and demo deployments only. Use
real authentication and ACLs before exposing the broker in production.

---

## 端口参考

| 端口 | 协议 | 说明 |
|------|------|------|
| 18983 | TCP | MQTT 默认端口 |
| 18984 | WS | MQTT-over-WebSocket（需配置 `ws_listen_addr` / `MQTT_WS_LISTEN_ADDR`） |
| 18993 | TCP | MQTT TLS 端口 |
| 18999 | TCP | Metrics/Health/Ready（/metrics, /healthz, /readyz） |

---

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `MQTT_LOG_LEVEL` | `info` | 日志级别: debug, info, warn, error |
| `MQTT_LOG_FORMAT` | `json` | 日志格式: json, text |
| `MQTT_METRICS_ADDR` | `:18999` | Metrics 监听地址 |
| `MQTT_METRICS_ENABLED` | `true` | 启用 Prometheus 指标 |

---

## Kubernetes 部署

### 使用 kubectl + Kustomize

```bash
kubectl apply -k deploy/k8s/app/

# 可选：部署 Prometheus 监控
kubectl apply -k deploy/k8s/infra/prometheus/
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

### 多副本 != 集群（重要）

**shark-mqtt 是单机可嵌入 Broker，不是集群。** 会话表、订阅树、保留消息存储都在进程内存里，
副本之间零通信。因此 `replicas` / `replicaCount` > 1，或用 HPA 把副本数拉上去，会**静默地**造成：

- **漏投**：订阅者在 pod A、发布者落到 pod B，pod B 的订阅树里没有该订阅者 → 消息丢弃。
  Service 只做连接级负载均衡，不按主题路由。
- **重复投递**：`$share` 共享订阅只在 pod 内轮询，多副本间不协调。
- **保留消息分裂**：保留存储每 pod 一份，新订阅者看到的保留消息取决于它连到哪个 pod。
- **扩容不迁移存量连接**：MQTT 是长连接，扩副本只分摊**新**连接，已有会话不会被重新平衡。

模板层面的处理：

| 位置 | 默认值 | 说明 |
|------|--------|------|
| `deploy/k8s/app/deployment.yaml` | `replicas: 1` | 顶部有醒目告警注释 |
| `deploy/k8s/app/hpa.yaml` | 文件保留，`minReplicas: 1` | **已从 `kustomization.yaml` 默认资源中注释掉**，避免默认部署自动扩容 |
| Helm `values.yaml` | `replicaCount: 1` / `autoscaling.enabled: false` | 默认不扩容 |
| Helm `values-prod.yaml` | `replicaCount: 1` / `autoscaling.enabled: false` | 生产同样单副本；HA 靠客户端重连 + Redis 持久化会话 |

要做到真正的多实例可用，必须先在前面引入集群 / 主题路由层
（见 `docs/architecture/CLUSTER-FEASIBILITY.md`）。

---

## 监控

### Prometheus 指标

指标地址: `http://<host>:18999/metrics`

主要指标:
- `shark_mqtt_connections_total` - 连接总数
- `shark_mqtt_messages_total` - 消息总数
- `shark_mqtt_subscriptions_total` - 订阅总数
- `shark_mqtt_bytes_total` - 传输字节总数
- `shark_mqtt_errors_total` - 错误总数

### 健康检查

- `/healthz` - 存活检查（返回 200 表示服务运行中）
- `/readyz` - 就绪检查（返回 200 表示可接收请求）

---

## 生产检查清单

- [ ] 配置 TLS 证书（MQTT over TLS 端口 18993）
- [ ] 设置资源限制（CPU/Memory）
- [ ] **保持副本数为 1**（多副本 != 集群，见上文），不要启用 HPA
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

确保 18983（MQTT）、18999（Metrics/Health）端口未被占用。

### Redis 连接

使用 Redis 作为存储后端时，确保 Redis 服务可达且健康。

### 优雅关闭

收到 SIGTERM 后，Broker 先标记"正在关闭"，再关闭连接。这样关闭动作不会被当成客户端
异常断开，**不会触发遗嘱风暴**（滚动重启时若每个客户端的遗嘱都发一遍，会瞬间刷满所有
遗嘱主题）。同时，受影响的 MQTT 5.0 客户端会先收到 `DISCONNECT` reason `0x8B`
（Server shutting down）。MQTT 3.1.1 没有服务端 DISCONNECT 报文，只会看到连接关闭。

默认关闭超时 30 秒。在 K8s 中设为 60 秒需同步调整 `terminationGracePeriodSeconds`。

### MQTT 客户端连接

```bash
# 使用 mosquitto 客户端测试
mosquitto_pub -h localhost -p 18983 -t "test/topic" -m "hello"
mosquitto_sub -h localhost -p 18983 -t "test/topic"
```
