# Shark-MQTT

> **简体中文** | [English](README.md)

用 Go 编写的高性能 MQTT Broker,同时支持 **MQTT 3.1.1** 和 **MQTT 5.0** 协议。

**项目版本基线:1.0.0**

## 特性

- **协议支持**:完整支持 MQTT 3.1.1 & 5.0,15 种报文类型,完整的属性编解码
- **QoS 等级**:QoS 0 / 1 / 2,含自动重试、inflight 跟踪与状态机
- **持久会话**:跨连接会话持久化(`CleanSession=false`),支持 MQTT 5.0 会话过期间隔
- **会话接管**:安全的 ClientID 接管——新连接踢掉旧连接,旧连接清理不破坏新状态
- **主题通配符**:完整支持 `+` 和 `#` 通配符,含符合规范的 `$SYS` 系统主题保护
- **保留消息**:按主题存储转发最后一条消息,支持通配符投递、QoS 降级与 MQTT 5.0 Retain Handling
- **遗嘱消息**:异常断开自动投递遗嘱,支持 MQTT 5.0 Will Delay Interval
- **可插拔认证**:链式认证——`AllowAll`、`DenyAll`、`StaticAuth`(凭据 + ACL)、`FileAuth`(YAML)、`ChainAuth` 或自定义 `Authenticator`/`Authorizer` 接口
- **插件系统**:可扩展钩子 `OnAccept`、`OnConnected`、`OnMessage`、`OnClose`——插件出错后继续分发
- **多种存储后端**:内存(默认)、Redis、BadgerDB,覆盖会话、消息与保留消息
- **连接限制**:可配置最大连接数,认证前强制
- **TLS 支持**:安全连接,可配置 TLS(最低 TLS 1.2)
- **MQTT 5.0 CONNACK 能力宣告**:服务器宣告 ReceiveMaximum、MaximumQoS、RetainAvailable、WildcardSubAvailable 等
- **MQTT 5.0 订阅选项**:支持 No Local 与 Retain Handling 语义,同时保留 MQTT 默认的自发投递
- **可观测性**:结构化日志(`slog`)+ Prometheus 指标(17+ 方法)+ `/healthz`/`/readyz` 端点
- **安全并发**:每连接写互斥锁、原子 ID 生成、线程安全会话管理、连接身份校验清理
- **配置校验**:所有配置字段内置 `Validate()`,支持 YAML/ENV/CLI 配置
- **独立运行**:作为独立 MQTT Broker 运行;跨系统互操作通过共享数据库、Redis/缓存与显式数据契约处理

## 架构

```
+----------------------------------------------------------+
|                       cmd/main.go                        |
|                   CLI 入口点                              |
+------------------------+---------------------------------+
                         |
                         v
+----------------------------------------------------------+
|                      api/api.go                          |
|             统一公共 API 与工厂                           |
|            (Start / Stop / Addr / ConnCount)             |
|            + 健康服务 (/healthz, /readyz)                |
+------------------------+---------------------------------+
                         |
                         v
+----------------------------------------------------------+
|                        broker/                           |
|              网络层 + 业务逻辑                           |
|  +-----------------+  +------------------------------+  |
|  |  MQTTServer      |  |  Broker                      |  |
|  |  TCP/TLS 接收    |<-+  TopicTree (通配符匹配)      |  |
|  |  连接管理        |  |  QoSEngine (重试 + inflight) |  |
|  |  每连接互斥锁    |  |  WillHandler (延迟支持)      |  |
|  +-----------------+  |  Manager (会话)               |  |
|                        |  Authenticator + Authorizer   |  |
|                        |  连接限流器                   |  |
|                        +------------------------------+  |
+----------------------------------------------------------+
              |              |              |
              v              v              v
+----------------+ +--------------+ +--------------+
|   protocol/    | |   store/     | |    pkg/      |
|  MQTT 编解码   | |  内存        | |  日志        |
|  15 种报文     | |  Redis       | |  指标        |
|  MQTT 5.0 属性 | |  BadgerDB    | |  缓冲池      |
+----------------+ +--------------+ +--------------+
```

### 目录结构

| 目录 | 说明 |
|-----------|-------------|
| `cmd/` | CLI 入口,含信号处理与参数解析 |
| `api/` | 统一公共 API、Broker 工厂、健康端点 |
| `broker/` | 核心:MQTTServer、Broker、TopicTree、QoSEngine、WillHandler、Session、Auth |
| `protocol/` | MQTT 3.1.1 & 5.0 编解码——15 种报文类型,含属性支持 |
| `store/` | 存储接口 + 内存 / redis / badger 实现 |
| `pkg/` | 基础设施:logger (slog)、metrics (Prometheus)、bufferpool |
| `config/` | 配置加载(YAML / ENV),含校验 |
| `plugin/` | 基于钩子架构的插件系统 |
| `client/` | MQTT 客户端实现 |
| `errs/` | 集中式错误定义 |
| `tests/integration/` | 96 个端到端集成测试,含 MQTT 流程与部署验证 |
| `tests/bench/` | 65 个在 Windows 上执行的基准测试(broker + E2E 数据校验 + 微基准 + store) |
| `examples/` | 可运行示例程序(standalone、TLS、自定义认证) |
| `deploy/` | Docker、docker-compose、k8s、Helm chart 部署资源 |
| `docs/` | 架构、部署、性能、测试与项目状态文档 |
| `testutils/` | 测试工具(模拟连接、模拟存储、辅助函数) |
| `scripts/` | 测试运行脚本(Windows / Linux / macOS) |

## 快速开始

### 安装

```bash
go get github.com/X1aSheng/shark-mqtt
```

### 独立 Broker

```go
package main

import (
    "context"
    "log"
    "os/signal"
    "syscall"

    "github.com/X1aSheng/shark-mqtt/api"
    "github.com/X1aSheng/shark-mqtt/broker"
    "github.com/X1aSheng/shark-mqtt/config"
)

func main() {
    cfg := config.DefaultConfig()
    cfg.ListenAddr = ":18983"

    b := api.NewBroker(
        api.WithConfig(cfg),
        api.WithAuth(broker.AllowAllAuth{}),
    )

    if err := b.Start(); err != nil {
        log.Fatal(err)
    }

    ctx, _ := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    <-ctx.Done()
    b.Stop()
}
```

### Docker

```bash
# 构建并运行
docker build -f deploy/docker/Dockerfile -t shark-mqtt .
docker run -d -p 18983:18983 -p 18999:18999 shark-mqtt -addr=:18983 -allow-all

# 验证
curl http://localhost:18999/healthz
```

Redis 与多服务配置请参见 `deploy/docker/docker-compose.yml`。

### Kubernetes

```bash
# 部署
kubectl apply -k deploy/k8s/app/

# 可选:Prometheus 监控
kubectl apply -k deploy/k8s/infra/prometheus/

# 检查
kubectl -n shark-mqtt get pods
```

Kubernetes 清单与 Helm chart 配置参见 `deploy/k8s/`。

### CLI

```bash
# 构建并运行
go build -o shark-mqtt ./cmd/
./shark-mqtt -addr :18983 -log-level info

# 启用 TLS
./shark-mqtt -addr :18993 -tls -tls-cert cert.pem -tls-key key.pem

# 设置连接数限制
./shark-mqtt -addr :18983 -max-conn 10000
```

### 认证

```go
auth := broker.NewStaticAuth()
auth.AddCredentials("admin", "secret")
auth.AddCredentials("device-1", "token-abc")
auth.AddPublishACL("admin", "sensor/#")   // admin 可发布到 sensor/*
auth.AddSubscribeACL("admin", "#")        // admin 可订阅所有主题

b := api.NewBroker(api.WithAuth(auth))
```

### TLS

```go
cfg := config.DefaultConfig()
cfg.ListenAddr = ":18993"
cfg.TLSEnabled = true
cfg.TLSCertFile = "cert.pem"
cfg.TLSKeyFile = "key.pem"

b := api.NewBroker(api.WithConfig(cfg))
```

### Redis 存储

```go
import (
    redisstore "github.com/X1aSheng/shark-mqtt/store/redis"
    "github.com/redis/go-redis/v9"
)

client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

b := api.NewBroker(
    api.WithSessionStore(redisstore.NewSessionStore(redisstore.SessionStoreConfig{
        Client:    client,
        KeyPrefix: "mqtt:session:",
    })),
)
```

### 插件

```go
type LogPlugin struct{}

func (p *LogPlugin) Name() string                          { return "log-plugin" }
func (p *LogPlugin) Hooks() []plugin.Hook                  { return []plugin.Hook{plugin.OnMessage} }
func (p *LogPlugin) Execute(hook plugin.Hook, data any) error {
    if msg, ok := data.(*protocol.PublishPacket); ok {
        log.Printf("message: topic=%s payload=%s", msg.Topic, msg.Payload)
    }
    return nil
}

pm := plugin.NewManager()
pm.Register(&LogPlugin{})
b := api.NewBroker(api.WithPluginManager(pm))
```

## 配置

### YAML

```yaml
listen_addr: ":18983"
keep_alive: 60
max_packet_size: 262144
max_connections: 10000
storage_backend: "memory"
log_level: "info"
log_format: "json"
qos_retry_interval: "10s"
qos_max_retries: 3
qos_max_inflight: 100
session_expiry_interval: 3600

# TLS
tls_enabled: false
tls_cert_file: ""
tls_key_file: ""

# Redis
redis_addr: "localhost:6379"
redis_password: ""
redis_db: 0

# BadgerDB
badger_path: "./data"

# Metrics
metrics_enabled: true
metrics_addr: ":18999"
```

### 环境变量

所有配置项均支持 `MQTT_` 前缀:

```bash
MQTT_LISTEN_ADDR=:18983 MQTT_MAX_CONNECTIONS=5000 ./shark-mqtt
```

### Options API

```go
b := api.NewBroker(
    api.WithConfig(cfg),
    api.WithAuth(myAuth),
    api.WithMaxConnections(5000),
    api.WithSessionStore(ss),
    api.WithMessageStore(ms),
    api.WithRetainedStore(rs),
    api.WithLogger(logger),
    api.WithMetrics(metrics),
    api.WithPluginManager(pm),
)
```

## 性能

最近一次基准测试运行于 **AMD Ryzen 7 8845HS / Windows 11 / Go 1.26.1**(`logs/20260506_123128_benchmark.log`):

| 基准测试 | ns/op | B/op | allocs/op |
|-----------|-------|------|-----------|
| 连接建立 | 305k | 4,079 | 65 |
| MQTT 连接 | 408k | 6,227 | 123 |
| 发布 QoS 0 | 24.0k | 1,760 | 27 |
| 发布 QoS 1 | 74.1k | 1,948 | 37 |
| 发布 QoS 2 | 201k | 2,548 | 52 |
| 并发发布 | 43.5k | 1,717 | 26 |
| 负载 128KB | 1.82M | 548,663 | 29 |
| TopicTree 订阅 | 132 | 51 | 0 |
| TopicTree 匹配(精确) | 244 | 88 | 2 |
| TopicTree 匹配(通配符 #) | 236 | 88 | 2 |
| TopicTree 匹配(通配符 +) | 354 | 136 | 3 |
| 编解码 编码发布 | 336 | 422 | 6 |
| 编解码 解码发布 | 536 | 736 | 10 |
| QoS 引擎 跟踪 QoS 1 | 19.2 | 0 | 0 |
| 缓冲池 Get/Put | 29.8 | 24 | 1 |
| 内存存储 会话获取 | 5.7 | 0 | 0 |

完整结果:`make bench` 或参见 `docs/performance.md`。

## 测试

| 类型 | 数量 | 状态 |
|------|-------|--------|
| 单元测试 | 344 个通过 / 13 个 Redis 跳过 | 全部通过 |
| 集成测试 | 96 个通过 | 全部通过 |
| 基准测试 | 65 个执行 | 全部通过 |
| **最近一次脚本运行** | `logs/20260806_140435_*` | **0 失败** |

> 未设置 `MQTT_REDIS_ADDR` 时,13 个 Redis 测试被跳过。
> 最近一次完整运行:`logs/20260806_*`;单元日志报告 344 个通过,Redis 未配置时 13 个测试跳过。将 `D:\Programs\w64devkit\bin` 加入 `PATH` 后 race 检测通过。

### 集成测试覆盖

| 类别 | 数量 | 详情 |
|----------|-------|---------|
| 连接与会话 | 6 | CONNECT 流程、持久会话、重连、踢连接、QoS1 ACK |
| 发布/订阅 | 6 | 基础、QoS 0/1/2、默认自发投递、MQTT 5.0 No Local 抑制 |
| 遗嘱消息 | 3 | 异常/正常断开、QoS 0/1 |
| 主题通配符 | 5 | `+`、`#`、根、混合、多订阅者 |
| 保留消息 | 7 | 新订阅者、更新、删除、通配符、QoS 降级、MQTT 5.0 Retain Handling 1/2 |
| 多订阅者 | 12 | 同主题、混合 QoS、顺序、突发、大/二进制/空/Unicode 负载、重叠、自发发布、结构化二进制 |
| 退订与 QoS | 8 | 停止投递、多主题、通配符、重订阅、系统主题、QoS 1 ACK、QoS 2 握手、无订阅者发布 |
| 边界情况 | 6 | 认证失败、重复 clientID、非法过滤器、空 clientID、最大连接数、系统主题隔离 |
| 部署验证 | 30 | Dockerfile、docker-compose、k8s 清单、Helm chart 结构、安全上下文、探针 |

所有 MQTT 集成测试(53 个 MQTT 数据/安全测试 + 36 个部署检查)都验证**端到端数据投递**、安全握手行为或部署产物正确性。

### 运行测试

所有测试运行会自动把带时间戳的日志保存到 `logs/` 目录,格式为 JSON(原始 `go test -json` 输出)和 `.log`(解析后的报告)。

#### 跨平台测试脚本

单个基于 Go 的测试运行器在所有平台提供一致功能,并附带精简的 shell 包装脚本。
底层 `go test` 或基准命令失败时,所有运行器都会返回非零退出码,同时仍写入解析后的日志报告。

| 平台 | 脚本 |
|----------|--------|
| 任意平台(Go 运行器) | `go run scripts/run_tests.go -mode <mode>` |
| Linux / macOS / Git Bash / WSL | `bash scripts/run_tests.sh [--unit\|--integration\|--benchmark\|--cover\|--all]` |
| Windows CMD | `scripts\run_tests.bat [--unit\|--integration\|--benchmark\|--cover\|--all]` |

**模式:**

| 模式 | 说明 | 日志文件 |
|------|-------------|-----------|
| `all`(默认) | 单元 + 集成 + 基准 | `{ts}_unit.{json,log}`, `{ts}_integration.{json,log}`, `{ts}_benchmark.{json,log}` |
| `unit` | 运行全部单元测试 | `{ts}_unit.{json,log}` |
| `integration` | 运行集成测试 | `{ts}_integration.{json,log}` |
| `benchmark` | 运行基准测试 | `{ts}_benchmark.{json,log}` |
| `cover` | 生成覆盖率报告 | `{ts}_cover.log` |

```bash
# 运行所有测试(默认)
bash scripts/run_tests.sh

# 单独模式
bash scripts/run_tests.sh --unit
bash scripts/run_tests.sh --integration
bash scripts/run_tests.sh --benchmark
bash scripts/run_tests.sh --cover

# Windows CMD
scripts\run_tests.bat --all
scripts\run_tests.bat --unit

# 或直接使用 Go 运行器(任意平台)
go run scripts/run_tests.go -mode unit
go run scripts/run_tests.go -mode cover -timeout 10m
```

`all` 模式产出:

```
logs/
+-- 20260428_190627_unit.json
+-- 20260428_190627_unit.log
+-- 20260428_190635_integration.json
+-- 20260428_190635_integration.log
+-- 20260428_190642_benchmark.json
+-- 20260428_190642_benchmark.log
+-- 20260426_194205_summary.log
```

#### Makefile 目标

```bash
make test              # 单元测试
make test-integration  # 集成测试
make test-race         # 带 race 检测器
make bench-quick       # 每个基准 1s
make bench             # 5s x 3 次
make test-coverage     # 覆盖率报告
make ci                # 完整 CI 流水线
```

## 示例

每个示例都在独立目录中,可单独运行:

```bash
# 独立 broker
go run ./examples/standalone

# TLS broker
go run ./examples/tls_broker

# 自定义认证
go run ./examples/custom_auth
```

## CI

GitHub Actions CI 在每次 push / PR 时运行:

- **单元测试**:Go 1.26 / stable x Ubuntu / macOS / Windows
- **插件测试**:专用插件管理器测试任务
- **脚本化测试**:`scripts/run_tests.go` 的单元与集成入口
- **Lint**:`go vet` + `gofmt` 格式检查
- **构建**:跨平台构建验证
- **覆盖率**:最低 55% 阈值,含 Codecov 上传(Ubuntu + Redis 上检查)

详见 `.github/workflows/ci.yml`。

## 项目状态

**总体:核心已生产就绪**

所有关键与高严重度问题均已解决。最近一次服务端审查完成于 2026-05-20。

### 已完成

- 完整 MQTT 3.1.1 & 5.0 协议支持(15 种报文类型 + 属性)
- MQTT 主题过滤器支持合法的零长度主题层,如 `/finance`、`finance/`、`finance//usd`
- MQTT 5.0 属性编码对超长 UTF-8 字符串返回错误,而非生成畸形报文
- QoS 0/1/2,含自动重试、inflight 跟踪与发送错误处理
- 符合规范的主题过滤器校验
- 保留消息与遗嘱消息(含延迟间隔)
- 持久会话管理,含优雅停机排空
- 会话接管安全清理(连接身份校验)
- MQTT 5.0 会话过期间隔,CONNACK 能力属性宣告
- 每连接写互斥锁(并发帧安全)
- 可配置连接数限制,认证前强制
- 可插拔认证/授权(AllowAll、DenyAll、StaticAuth、FileAuth、ChainAuth)
- 插件系统(插件失败后错误收集式派发仍继续)
- 内存 / Redis / BadgerDB 存储
- TLS 支持(最低 TLS 1.2)
- 健康端点(`/healthz`、`/readyz`)
- 配置校验(YAML/ENV/CLI)
- 集中式错误定义(`errs` 包)
- 完整测试套件,`tests/defects/` 含专项缺陷回归
- 静态分析基线:`go vet ./...` 与 `golangci-lint run ./...` 通过
- 保留消息指标在覆盖写/删除路径上精确
- MQTT 固定头标志与 CONNECT Will 标志组合畸形时被拒绝
- 可配置 QoS inflight 限制强制,含 MQTT 5.0 ReceiveMaximum 宣告
- 客户端侧 QoS 2 PUBLISH 重复检测
- 线程安全报文 ID 生成
- 非法 Will 主题的 CONNECT 报文在会话/连接注册前被拒绝,被拒客户端不会占用 broker 连接槽

### 剩余工作

| ID | 优先级 | 说明 |
|----|----------|-------------|
| M-002 | 中 | 实现离线消息队列 |
| M-005 | 中 | 文档化 StaticAuth ACL 行为 |
| M-006 | 中 | TopicTree 匹配缓存 |
| M-007 | 中 | 在更大的节点或托管集群上执行实际 Kubernetes 滚动发布 |
| M-008 | 中 | 将总覆盖率提升到文档化的 60% 目标 |
| L-005 | 低 | 修复客户端 Connect TOCTOU |
| L-007 | 低 | 在测试中使用命名超时常量 |
| L-008 | 低 | 增加协议 fuzz 测试 |

最新项目审查报告参见 `docs/PROJECT-REVIEW-260520-233509.md`。

## 文档

| 文档 | 说明 |
|----------|-------------|
| [架构](docs/Architecture.md) | 详细架构设计 |
| [API 参考](docs/API.md) | 公共 API 文档 |
| [配置](docs/configuration.md) | 完整配置指南 |
| [性能](docs/performance.md) | 基准测试与性能分析 |
| [部署](docs/DEPLOY.md) | 部署说明 |
| [安全](docs/SECURITY.md) | 安全考量 |
| [测试](docs/testing.md) | 测试指南 |
| [开发](docs/development.md) | 开发工作流 |
| [审查报告](docs/PROJECT-REVIEW-260520-233509.md) | 最新项目审查 |

## 许可证

MIT License
