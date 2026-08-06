# 部署验证报告 (云服务器 120.76.44.233)

- 日期: 2026-08-06 12:49
- 服务器: 120.76.44.233 (Ubuntu 26.04, 8核/16G 实测 hostname iZwz93b9fvc8lttmuewj0cZ)
- 访问: SSH key (免密), root 用户
- 项目: shark-mqtt (Go 1.26 单模块 MQTT Broker)

## 1. 云服务器清理 (用户明确要求)

清理前状态 (实测):
- 运行中进程: /usr/local/bin/shark-socket (PID 1372905, 容器 docker-shark-socket-1 内, 已运行 5 周)
- 监听端口: 18000/18080/18081 (shark-socket), 1883 (mosquitto)
- Docker: 2 个运行容器 (docker-shark-socket-1, docker-mosquitto-1), 9 个镜像 (含 docker-mqtt-test 675MB, kindest/node 1.35GB), 1 个 compose 项目 (docker)
- 旧配置/应用目录: /opt/shark-socket.tar.gz, /opt/shark-MQTT, /opt/shark-socket, /root/shark-mqtt, /root/shark-socket-new-cloud, /usr/local/bin/shark-socket

清理动作 (全部执行):
1. kind 检查: 无 kind 集群 (kindest/node 镜像仅为残留)
2. `docker compose down --remove-orphans`: 停止并移除 2 个容器 + docker_default 网络
3. `docker ps -aq | xargs docker rm -f`: 无剩余容器
4. `docker images -q | xargs docker rmi -f`: 删除全部 9 个镜像
5. `docker system prune -af --volumes`: 清理 kind 网络, 1 个匿名卷, 全部 build cache, **回收 7.325GB**
6. `docker volume rm docker_mosquitto-data`: 删除残留命名卷
7. `rm -rf`: 删除上述旧配置/应用目录与 shark 二进制
8. 最终状态: 无 shark/mqtt 进程, 无相关端口监听, `docker ps -a`/`docker images` 全空, 磁盘 19G -> 9.9G 已用 (27%)

## 2. 源码部署与原生编译测试

- 本地打包 (exclude .git/logs/bin/coverage) -> scp -> 解压至 /opt/shark-mqtt
- Go: go1.26.5 linux/amd64 (系统已装)
- 结果:

| 检查项 | 结果 |
| --- | --- |
| go build ./... | PASS |
| go build ./examples/... | PASS |
| go vet ./... | PASS |
| go test -count=1 ./... | 全部 PASS (api/broker/client/config/errs/pkg/plugin/protocol/store/*/tests/defects/tests/integration) |

## 3. Docker 部署验证

- 构建: `docker build -f deploy/docker/Dockerfile -t shark-mqtt:cloud .` -> PASS (多阶段 golang:1.26-alpine -> alpine:3.21, CGO_ENABLED=0, 非 root shark 用户)
- 运行: `docker run -d --name shark-mqtt-test -p 18983:18983 -p 18999:18999 shark-mqtt:cloud -addr=:18983 -allow-all`
- 健康检查: 1 秒内 /healthz=ok, /readyz=ok (HTTP 200)
- MQTT 冒烟: `go run scripts/mqtt_smoke.go -addr localhost:18983` -> connect/subscribe/publish/disconnect 全 PASS
- QoS 端到端 (自写临时客户端, 通过容器):
  - QoS1: PUBACK 收到 + 消息投递正确 (qos/verify:hello-qos1) -> PASS
  - QoS2: PUBCOMP 收到 + 消息投递正确 (qos/verify:hello-qos2) -> PASS
- 发现缺陷: `curl :18999/metrics` 返回 **404**. 原因: 默认 metrics 实现为 noop (metrics.Default()), 无 HTTPHandler; api.startHealthServer 仅在 metrics 实现带 Handler() 时挂载 /metrics. 即默认部署 (cmd/main.go, 未显式 WithMetrics) 无 Prometheus 端点, 与 prometheus.yml/DEPLOY 文档冲突. 记为审查报告 NEW-20 (P2).
- 验证后清理: 移除测试容器与镜像 (保持服务器整洁).

## 4. Kubernetes 部署验证

- kubectl v1.35.0 (client), helm 已装; **无可用集群** (kind 集群已清理), 按可达级别验证:

| 检查项 | 结果 |
| --- | --- |
| kubectl kustomize deploy/k8s/app/ | PASS (渲染 250 行 YAML) |
| kubectl kustomize deploy/k8s/infra/prometheus/ | PASS (渲染 108 行) |
| helm template shark-mqtt deploy/k8s/helm/shark-mqtt/ | PASS (渲染 5 个 kind) |
| kubectl apply --dry-run=client | 不可用 (需 API server, localhost:8080 拒绝连接) - 无集群属预期 |

说明: k8s 达到"清单渲染 + helm 模板"验证级别; 真实集群部署需在有控制面的集群执行 `kubectl apply -k deploy/k8s/app/`。

## 5. V6 Fix Round 云服务器复验 (2026-08-06, 修复后)

修复完成后再部署到同一云服务器复验 (120.76.44.233):

| 检查项 | 结果 |
| --- | --- |
| 源码替换为修复后版本, go build ./... | PASS |
| go build ./examples/... + go vet | PASS |
| go test -count=1 ./... (全部包) | 全部 PASS |
| docker build -f deploy/docker/Dockerfile | PASS |
| 容器启动 + /healthz | 1 秒内 ok |
| **/metrics (NEW-20 修复验证)** | **HTTP 200** (修复前 404) |
| MQTT 冒烟 (scripts/mqtt_smoke.go) | connect/subscribe/publish/disconnect 全 PASS |
| QoS1/QoS2 端到端 round-trip (临时客户端) | QoS1 PUBACK+投递 PASS, QoS2 PUBCOMP+投递 PASS |
| 复验后清理 | 移除测试容器与镜像, 服务器保持整洁 |

验证后 344 单元 + 96 集成全绿, CI 全矩阵 (3 OS x 2 Go) 通过。

## 6. 结论

- 云服务器清理完成: 所有 shark-socket/mqtt 残留进程、旧配置、旧镜像、docker 进程全清, 回收 7.3GB.
- 源码原生编译 + vet + 全量测试在云服务器全部通过.
- Docker 镜像构建/运行/健康检查/MQTT 冒烟/QoS1+QoS2 端到端全部通过.
- 实测发现默认部署 /metrics 404 (Prometheus 监控失效), 记为 NEW-20 缺陷.
- V6 Fix Round 后复验: /metrics 修复为 200, QoS 端到端验证通过.
- K8s 清单 + Helm chart 渲染验证通过; 真实集群部署留待具备控制面的环境.
