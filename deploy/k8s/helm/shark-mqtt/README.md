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
| `image.tag` | `latest` | 镜像标签 |
| `service.type` | `ClusterIP` | Service 类型 |
| `autoscaling.enabled` | `true` | 启用 HPA |
| `config.logLevel` | `info` | 日志级别 |
| `config.storageBackend` | `memory` | 存储后端 |
