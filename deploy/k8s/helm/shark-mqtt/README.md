# shark-mqtt Helm Chart

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
| `replicaCount` | `2` | Pod 副本数 |
| `image.repository` | `x1asheng/shark-mqtt` | 镜像仓库 |
| `image.tag` | `1.0.0` | 镜像标签 |
| `service.type` | `ClusterIP` | Service 类型 |
| `service.mqttPort` | `18983` | MQTT 默认端口 |
| `service.metricsPort` | `18999` | Metrics/Health 端口 |
| `autoscaling.enabled` | `true` | 启用 HPA |
| `config.listenAddr` | `:18983` | Broker 监听地址 |
| `config.allowAllAuth` | `true` | 示例部署启用开发模式免认证；生产配置覆盖为 `false` |
| `config.logLevel` | `info` | 日志级别 |
| `config.storageBackend` | `memory` | 存储后端 |

默认 `values.yaml` 面向本地验证和快速部署，会传入 `-allow-all` 以便冒烟测试可直接连接。
生产环境叠加 `values-prod.yaml` 后会关闭该选项，应接入真实认证和 ACL。
