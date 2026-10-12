# shark-mqtt Helm Chart

## 多副本 != 集群（重要）

**shark-mqtt 是单机可嵌入 Broker，不是集群。** 每个副本都把会话表、订阅树和保留消息
存储放在自己的进程内存里，副本之间**没有任何通信**。因此把 `replicaCount` 设为大于 1、
或开启 HPA 让副本数上涨，会静默地造成数据错误：

| 现象 | 原因 |
|------|------|
| **漏投** | 订阅者在 pod A、发布者落到 pod B 时，pod B 的订阅树里没有这个订阅者，消息被丢弃。Service 只做连接级负载均衡，不按主题路由。 |
| **重复投递** | `$share` 共享订阅只在单个 pod 内轮询，多副本之间不协调。 |
| **保留消息分裂** | 保留消息存储是每 pod 一份，新订阅者接到的保留消息取决于它连到哪个 pod。 |
| **水平扩容不迁移存量连接** | MQTT 是长连接；扩副本只会分摊**新**连接，已建立的会话不会被重新平衡。 |

**结论：** `replicaCount` 保持 1，`autoscaling.enabled` 保持 `false`。要做到多实例可
用，必须先在前面放一层真正的集群 / 主题路由（见 `docs/architecture/CLUSTER-FEASIBILITY.md`）。
MQTT 层面上的高可用来自"客户端重连到存活 pod + 会话持久化在 Redis"
（`config.storageBackend=redis`），而**不是**来自 Service 后面的多副本。

## 安装

```bash
helm install shark-mqtt ./deploy/k8s/helm/shark-mqtt/ \
  --namespace shark-mqtt --create-namespace
```

## 配置

见 `values.yaml` 了解所有可配置参数。生产环境推荐叠加 `values-prod.yaml`：

```bash
helm install shark-mqtt ./deploy/k8s/helm/shark-mqtt/ \
  --namespace shark-mqtt --create-namespace \
  --values ./deploy/k8s/helm/shark-mqtt/values.yaml \
  --values ./deploy/k8s/helm/shark-mqtt/values-prod.yaml
```

## 主要参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `replicaCount` | `1` | Pod 副本数。**大于 1 不是集群**，会导致漏投 / 重复投 / 保留消息分裂，见上节 |
| `image.repository` | `x1asheng/shark-mqtt` | 镜像仓库 |
| `image.tag` | `1.0.0` | 镜像标签 |
| `service.type` | `ClusterIP` | Service 类型 |
| `service.mqttPort` | `18983` | MQTT 默认端口 |
| `service.metricsPort` | `18999` | Metrics/Health 端口 |
| `autoscaling.enabled` | `false` | 默认关闭；扩容无法迁移存量长连接且会造成漏投，仅在集群层就绪后开启 |
| `config.listenAddr` | `:18983` | Broker 监听地址 |
| `config.allowAllAuth` | `true` | 示例部署启用开发模式免认证；生产配置覆盖为 `false` |
| `config.logLevel` | `info` | 日志级别 |
| `config.storageBackend` | `memory` | 存储后端 |

默认 `values.yaml` 面向本地验证和快速部署，会传入 `-allow-all` 以便冒烟测试可直接连接。
生产环境叠加 `values-prod.yaml` 后会关闭该选项，应接入真实认证和 ACL。
