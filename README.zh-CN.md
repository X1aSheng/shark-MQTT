# Shark-MQTT

> [English](README.md) | **简体中文**

## 项目概述

Shark-MQTT 是一个使用 Go 编写的高性能 MQTT 代理（Broker），完整实现 **MQTT 3.1.1 与
MQTT 5.0** 协议，为可预测的高负载行为而设计：

- **零外部依赖即可运行** —— 默认内存存储；Redis 与 BadgerDB 为可选后端，通过构建标签
  编译启用。
- **并发有界** —— 所有队列与缓冲均有硬上限（写队列、流控缓冲、inflight、保留消息数），
  发布热路径经过分配优化（编解码池化、分档缓冲池）。
- **死链快速回收** —— MQTT 保活期限、可配置的 OS TCP 保活、写失败即断开，避免僵尸连接
  长期被计为"在线"。
- **可插拔、可观测** —— 认证/授权链、钩子式插件、自定义存储与指标、结构化日志、
  Prometheus 指标与健康/就绪探针。

在 AMD Ryzen 7 8845HS 上，端到端 QoS 0 往返约 **68 µs/条**（34 次分配）；128 KB 负载编码
约 **103 µs**（每条分配 135 KB）。

## 特性

**协议（MQTT 3.1.1 与 5.0）**
- 全部 15 种报文类型与完整属性编解码
- 增强认证（§4.12）、主题别名、消息/会话过期、RequestResponseInformation、
  ReceiveMaximum 流控、共享订阅、符合规范的 `$SYS` 主题保护

**QoS 与投递**
- QoS 0/1/2 状态机，自动重试与 inflight 跟踪
- 重叠订阅按**最大**匹配 QoS 投递一次（§3.3.5）
- 持久会话离线消息队列；消息过期在队列、流控缓冲与重试路径全程生效

**会话**
- 持久会话与 MQTT 5.0 会话过期（CONNECT 与 DISCONNECT 均生效）；基于连接身份校验的
  安全客户端接管

**消息**
- 保留消息（含 TTL）、遗嘱消息（含延迟）、`+`/`#` 通配符、MQTT 5.0 订阅选项
  （No Local、Retain Handling、订阅标识符）

**可靠性**
- 僵尸连接回收：保活期限、可配置 OS TCP 保活（`tcp_keepalive_period`）、写失败即关闭
- 认证前强制连接数上限；CONNECT 握手期限；每连接有界写队列

**安全**
- 可插拔认证链（AllowAll、DenyAll、带 ACL 的 StaticAuth、FileAuth、ChainAuth），
  **默认拒绝**（fail-closed）；TLS 1.2+ / mTLS；发布/订阅授权（含遗嘱消息授权）

**存储与扩展**
- `store` 接口 + memory（默认）/ Redis / BadgerDB 后端
- 钩子式插件系统（OnAccept/OnConnected/OnMessage/OnClose），panic 隔离；
  自定义认证器、授权器、存储与指标

**可观测性**
- 结构化日志（`slog`）、Prometheus 指标、`/healthz`/`/readyz`
  （就绪要求监听器与 broker 子系统均正常）

## 架构

```
cmd（CLI）→ api（公共门面 + 健康服务）
                 └─ broker：MQTTServer（TCP/TLS/WS）+ Broker
                      ├─ TopicTree（通配符匹配、$SYS 保护）
                      ├─ QoSEngine（重试 + inflight）
                      ├─ WillHandler（延迟遗嘱）
                      └─ Session Manager（持久化、接管）
                 ↓        ↓        ↓
           protocol/  store/   pkg/
           （编解码）   （memory/ （logger/
                      redis/   metrics/
                      badger）  bufferpool）
```

各层依赖单向；网络层与业务逻辑分离，连接与会话解耦。

### 目录结构

| 目录 | 职责 |
|------|------|
| `cmd/` `api/` | CLI 入口；公共 API/工厂与健康端点 |
| `broker/` | 核心：服务端、Broker、TopicTree、QoSEngine、WillHandler、会话、认证 |
| `protocol/` | MQTT 3.1.1 & 5.0 编解码（15 种报文、属性） |
| `store/` | 存储接口 + memory（默认）；redis/badger 位于构建标签之后 |
| `client/` | MQTT 3.1.1/5.0 客户端 |
| `plugin/` `config/` `errs/` | 插件系统；配置；错误哨兵 |
| `pkg/` | logger、metrics、分档缓冲池 |
| `tests/` | integration/、bench/、defects/ + 日志与产物（gitignored） |
| `examples/` `deploy/` `docs/` | 示例；Docker/K8s/Helm；文档 |

## 快速开始

```bash
# 运行 broker（仅开发用）
go run ./cmd -addr :18983 -allow-all
```

```go
cfg := config.DefaultConfig()
cfg.ListenAddr = ":18983"
b := api.NewBroker(
    api.WithConfig(cfg),
    api.WithAuth(broker.AllowAllAuth{}), // 生产环境请替换为真实认证
)
if err := b.Start(); err != nil {
    log.Fatal(err)
}
defer b.Stop()
```

> broker 默认**拒绝一切连接**（deny-all）；未显式配置认证器（或未加 `-allow-all`）时，
> 连接会被拒绝。

更多：[示例](examples/) · [配置](docs/guides/CONFIGURATION.md) ·
[Docker 与 K8s](docs/architecture/DEPLOY.md)

## 性能

环境：AMD Ryzen 7 8845HS / Windows 11 / Go 1.26.1（`go test -bench . ./tests/bench/`）。

| 基准 | 耗时 | B/op | allocs/op |
|---|---|---|---|
| 编解码 Encode Publish | 153 ns | 94 | 5 |
| 编解码 Decode Publish | 446 ns | 429 | 7 |
| 编解码 RoundTrip Publish | 565 ns | 528 | 12 |
| E2E QoS 0（完整往返） | 68 µs | 956 | 34 |
| E2E QoS 1 | 105 µs | 1,705 | 54 |
| E2E QoS 2 | 226 µs | 2,876 | 87 |
| E2E 64 KB 负载 | 226 µs | 181 KB | 36 |
| 编解码 128 KB 负载 | 103 µs | 135 KB | 24 |
| TopicTree 匹配（精确） | 280 ns | 160 | 2 |
| 缓冲池 Get/Put | 34 ns | 24 | 1 |

详情：[docs/guides/PERFORMANCE.md](docs/guides/PERFORMANCE.md)

## 测试

| 套件 | 数量 | 状态 |
|---|---|---|
| 单元测试（含缺陷回归） | 375 | 通过 |
| 集成测试（端到端，含部署校验） | 111 | 通过 |
| 基准 | 65 | 通过 |
| 竞态检测（全量） | — | 干净 |
| 协议 fuzz（2 个 fuzzer） | 840 万+ 次执行 | 无崩溃 |

跨平台运行器：`go run scripts/run_tests.go -mode all`（单元、集成、基准、覆盖率）。
日志输出至 `tests/logs/`，过程产物至 `tests/artifacts/`。文档链接由 CI 中的
`scripts/check_links.go` 校验。

## 项目状态

**核心已生产就绪。** MQTT 3.1.1/5.0 合规、QoS 状态机、持久会话、保留/遗嘱消息、
插件系统与可观测性均已实现并覆盖回归测试。近期可靠性工作：僵尸连接回收、
存储后端 fail-fast 校验、保活超时可观测化。

待办项（详见 [docs/reports/PROJECT-REVIEW-260806-143527.md](docs/reports/PROJECT-REVIEW-260806-143527.md)）：

| 优先级 | 事项 |
|---|---|
| 中 | TopicTree 匹配缓存（可选；当前规模下实测收益低） |
| 中 | 大规模集群 Kubernetes 上线验证 |
| 中 | 提升总覆盖率至 60% 目标 |
| 低 | 测试中使用命名超时常量 |

## 文档

| 文档 | 说明 |
|---|---|
| [架构](docs/architecture/ARCHITECTURE.md) | 分层设计与数据流 |
| [并发模型](docs/architecture/CONCURRENCY.md) | 锁清单、排序规则、僵尸连接检测 |
| [API 参考](docs/guides/API.md) | 公共 API |
| [配置](docs/guides/CONFIGURATION.md) | YAML/ENV/CLI 参考 |
| [性能](docs/guides/PERFORMANCE.md) | 基准与剖析 |
| [部署](docs/architecture/DEPLOY.md) | Docker、K8s、Helm |
| [安全](docs/architecture/SECURITY.md) | 威胁模型与加固 |
| [测试](docs/guides/TESTING.md) | 测试策略与工具 |
| [开发](docs/guides/DEVELOPMENT.md) | 工作流与约定 |

## 许可证

MIT License
