# Shark-MQTT vs EMQX 功能差距分析

- **分析日期**：2026-09-10
- **分析对象**：`github.com/X1aSheng/shark-mqtt`（本地 `G:\shark-mqtt`）
- **对标产品**：EMQX（https://gitee.com/emqx/emqx，v5.9+ BSL 1.1）
- **方法**：源码静态清点（27,265 行非测试 Go 代码、71 个测试文件）+ EMQX 官方功能清单比对

---

## 一、结论速览

**Shark-MQTT 是一个"协议内核完成度很高、平台能力几乎为零"的单节点 MQTT Broker。**

- 在 **MQTT 3.1.1 / 5.0 协议语义**这一层，Shark-MQTT 的完成度可以打 85 分以上：15 种报文、属性编解码、QoS 状态机、会话过期、保留/遗嘱、主题别名、共享订阅、增强认证、流控都实现了，并且有 375 单测 + 111 集成测试 + fuzz 兜底。
- 但 EMQX 的护城河**不在协议层，而在平台层**：分布式集群、SQL 规则引擎、50+ 数据桥接、Dashboard/HTTP API、多协议网关、20+ 认证后端。这些在 Shark-MQTT 中**基本为 0**（关键词全量检索：cluster / bridge / rule / sql / kafka / gateway / dashboard / jwt / ldap / oauth / otel 命中均为 0 个文件）。

因此两者不是"同一个产品做得好不好"的差距，而是**「单机嵌入式 MQTT 库」与「云原生 IoT 数据平台」的品类差距**。

**建议策略**：不要试图复刻 EMQX（那是 10 万行 Erlang/OTP 的工作量）。应明确 Shark-MQTT 的差异化定位 —— **Go 生态、单二进制、零外部依赖、可嵌入式**，然后有选择地补齐 3 类能力：
1. **P0 平台基础**：HTTP 管理 API + 动态配置热重载（否则无法运维）；
2. **P0 横向扩展**：集群（至少 3 节点静态发现 + 跨节点路由）；
3. **P1 数据出站**：一个最小可用的规则引擎 + Webhook/Kafka 出口（否则消息进了 Broker 出不去）。

---

## 二、定位差异（这是理解全部差距的前提）

| 维度 | EMQX | Shark-MQTT |
|---|---|---|
| 语言/运行时 | Erlang/OTP（Actor + 热加载） | Go（goroutine + 静态编译） |
| 部署形态 | 分布式集群节点 | 单进程 Broker，可嵌入宿主程序 |
| 规模目标 | 单集群 1 亿+ 连接 | 单机 1 万连接（默认 `max_connections`） |
| 扩展方式 | 插件 + 规则 + 桥接 + 网关 | 编译期 Hook 插件（4 个钩子） |
| 外部依赖 | Erlang 运行时 + 集群中间件 | 默认零依赖（memory）；Redis/Badger 可选 |
| 管理面 | Dashboard + 全量 REST API | 仅 `/healthz` `/readyz` `/metrics` |
| License | BSL 1.1 | MIT |

> Shark-MQTT 的"零依赖 + 可嵌入 + MIT"本身就是 EMQX 给不了的差异化价值，规划时不应丢掉。

---

## 三、能力对比矩阵

图例：✅ 已实现　🟡 部分实现　❌ 缺失

### 3.1 协议层

| 能力 | EMQX | Shark-MQTT | 说明 |
|---|---|---|---|
| MQTT 3.1 / 3.1.1 / 5.0 | ✅ | 🟡 3.1.1 + 5.0 | **3.1 缺失**；`protocol/` 只覆盖 3.1.1 与 5.0 |
| 15 种报文 + 属性编解码 | ✅ | ✅ | 完成度高，含 `SubscriptionIdentifier` |
| 增强认证 §4.12 | ✅ | ✅ | `EnhancedAuthenticator` + AUTH 报文 |
| 主题别名 / ReceiveMaximum / 消息过期 | ✅ | ✅ | 已实现 |
| 遗嘱延迟 / 保留消息 TTL | ✅ | ✅ | 已实现 |
| 共享订阅 | ✅ | 🟡 | 仅 **round-robin**；EMQX 支持 `round_robin/random/sticky/hash` 多策略 |
| 排他订阅 `$exclusive` | ✅ | ❌ | 无 |
| MQTT over QUIC | ✅ | ❌ | Go 生态有 quic-go，但工作量中等偏大 |
| MQTT-SN / CoAP / LwM2M 网关 | ✅ | ❌ | 完全缺失 |
| MQTT Bridge（Broker 互通） | ✅ | ❌ | 无 |

### 3.2 集群与分布式

| 能力 | EMQX | Shark-MQTT | 说明 |
|---|---|---|---|
| 集群 / 无主架构 | ✅ | ❌ | **0 个文件的 cluster 代码，这是最大结构性缺口** |
| 节点发现（static/etcd/k8s DNS） | ✅ | ❌ | 无 |
| 跨节点路由表 | ✅ | ❌ | 无 |
| 会话迁移 / 分区容忍 | ✅ | ❌ | 无 |
| Cluster Linking（全球互联） | ✅ | ❌ | 无 |
| 集群级共享订阅路由 | ✅ | ❌ | 当前共享订阅是**单进程内**的 round-robin |

> `deploy/k8s/` 下已有 Deployment/HPA/Helm，但即使起了多个副本，各副本之间**互不通信**，无法构成集群 —— 这是一个容易误导部署者的隐患，README 应显式说明"当前不支持集群，多副本仅为无状态前置/独立实例"。

### 3.3 规则引擎与数据集成

| 能力 | EMQX | Shark-MQTT | 说明 |
|---|---|---|---|
| SQL 规则引擎 | ✅ | ❌ | 完全缺失 |
| Flow Designer（可视化编排） | ✅ | ❌ | 无 |
| Schema Registry / 校验 / 转换 | ✅ | ❌ | 无 |
| Kafka / RabbitMQ / Pulsar / RocketMQ | ✅ | ❌ | 无 |
| MySQL / PostgreSQL / MongoDB / ClickHouse / InfluxDB / Timescale | ✅ | ❌ | 无 |
| Redis 数据出口 | ✅ | ❌ | 有 Redis **存储**后端，但无 Redis **数据桥接** |
| 云服务（AWS Kinesis / GCP Pub/Sub / Azure Event Hubs） | ✅ | ❌ | 无 |
| Webhook（HTTP 出口） | ✅ | ❌ | **无，且实现成本最低、收益最高** |
| AI 处理节点 | ✅ | ❌ | 无 |

> 这一块是 EMQX 商业价值的核心，也是 Shark-MQTT 目前"消息进来后没有出路"的问题所在 —— 只有 Hook 插件能拦消息，但没有声明式的持久化/转发能力。

### 3.4 认证与安全

| 能力 | EMQX | Shark-MQTT | 说明 |
|---|---|---|---|
| 用户名/密码（内置） | ✅ | ✅ | `StaticAuth`（bcrypt）/ `FileAuth`（YAML/JSON） |
| ACL 授权 | ✅ | ✅ | `ACL` + `CanPublish` / `CanSubscribe`，支持通配 |
| 链式认证 | ✅ | ✅ | `ChainAuth` |
| JWT | ✅ | ❌ | 无 |
| HTTP 认证（Webhook 鉴权） | ✅ | ❌ | 无，实现成本低、可覆盖大量外部系统 |
| LDAP / SQL / Redis 认证后端 | ✅ | ❌ | 无 |
| X.509 证书认证（身份映射） | ✅ | 🟡 | 有 mTLS 传输层，但**无证书→用户名映射/授权** |
| PSK 认证 | ✅ | ❌ | 无 |
| OAuth 2.0 | ✅ | ❌ | 无 |
| 客户端黑名单 / 自动封禁（flapping 检测） | ✅ | ❌ | 无；只有连接/发布**速率限制**（`rate_limit.go`） |
| 授权结果缓存 | ✅ | ❌ | 无 |
| ACL 规则热重载 | ✅ | 🟡 | `auth_file.go` 有 `LoadFile`，但**无文件监听自动重载** |
| CRL / 证书吊销 | ✅ | ❌ | 无 |
| TLS 1.2+ / mTLS / 安全套件白名单 | ✅ | ✅ | `config.TLSConfig` 已限制安全套件 |

### 3.5 管理与运维接口

| 能力 | EMQX | Shark-MQTT | 说明 |
|---|---|---|---|
| Web Dashboard | ✅ | ❌ | 无 |
| REST 管理 API | ✅ | ❌ | 只有 `/healthz` `/readyz` `/metrics` |
| 在线客户端查询 / 强制下线 | ✅ | ❌ | Broker 内部有 `disconnect`，但**无对外 API** |
| REST 发布 / 订阅 | ✅ | ❌ | 无 |
| 配置动态热更新 | ✅ | ❌ | **全量配置仅启动时读取，无 reload 机制** |
| 多监听器（多端口多配置） | ✅ | ❌ | `config.Config` 是**单 listener 结构**，非数组 |
| Proxy Protocol | ✅ | ❌ | 无，K8s/LB 场景需要 |
| 集群概览 / 节点指标 | ✅ | ❌ | 无 |
| 系统告警（Alarm） | ✅ | ❌ | 无 |
| 慢订阅 / 消息追踪（Trace） | ✅ | ❌ | 无 |
| Banner / 统计页 | ✅ | ❌ | 无 |

### 3.6 可观测性

| 能力 | EMQX | Shark-MQTT | 说明 |
|---|---|---|---|
| Prometheus 指标 | ✅ | ✅ | `pkg/metrics` |
| 健康/就绪探针 | ✅ | ✅ | `/healthz` `/readyz`（就绪需 listener+broker 双子系统） |
| 结构化日志 | ✅ | ✅ | `slog` |
| OpenTelemetry / 分布式追踪 | ✅ | ❌ | 无 |
| 日志级别动态调整 / 日志轮转 | ✅ | ❌ | 无 |
| $SYS 主题丰富度 | ✅ 30+ | 🟡 5 个 | 仅 version/uptime/connections/retained/subscriptions |
| 审计日志 | ✅ | ❌ | 无 |

### 3.7 扩展机制

| 能力 | EMQX | Shark-MQTT | 说明 |
|---|---|---|---|
| 钩子（Hooks） | ✅ 30+ 个 | 🟡 4 个 | OnAccept / OnConnected / OnMessage / OnClose |
| 插件架构 | ✅ | 🟡 | 编译期注册；无**动态加载**（`.so` / 外部进程） |
| 运行时热加载 | ✅ | ❌ | Erlang 原生能力，Go 生态难复制 |
| 自定义存储/认证/指标实现 | ✅ | ✅ | 接口开放，做得不错 |

### 3.8 消息高级特性

| 能力 | EMQX | Shark-MQTT | 说明 |
|---|---|---|---|
| 消息优先级队列 | ✅ | ❌ | 无 |
| 延迟发布（delayed publish） | ✅ | ❌ | 无（仅遗嘱有 delay） |
| 主题重写（topic rewrite） | ✅ | ❌ | 无 |
| 自动订阅（auto subscribe） | ✅ | ❌ | 无 |
| 消息重投队列排除 | ✅ | ❌ | 无 |
| MQTT 5.0 能力协商（Retain/MaxQoS/Wildcard/Shared 可用性） | ✅ | 🟡 | `properties.go` 有相关字段，但**能力开关不完全** |

---

## 四、缺口清单（按优先级）

### P0 — 不补就无法作为"可运维的服务"使用

| # | 缺口 | 理由 | 预估量级 |
|---|---|---|---|
| 1 | **HTTP 管理 API**（客户端列表/踢下线/会话/订阅/保留消息/集群状态） | 没有它，运维只能看日志；这是 EMQX 日常使用最高频的接口 | 中 |
| 2 | **配置动态热更新**（SIGHUP / API 触发 reload） | 改一个限流阈值要重启进程，生产不可接受 | 中 |
| 3 | **集群（最小可用：静态/etcd 发现 + 跨节点路由 + 共享订阅跨节点）** | 单节点水平扩展不可能；当前 K8s 多副本会误导使用 | 大 |
| 4 | **规则引擎（最小可用：SQL 子集 + 条件过滤 + 动作）** | 消息"出不去"，平台价值无从谈起 | 大 |
| 5 | **Webhook / Kafka 数据出口** | 最低成本的集成能力，可复用规则引擎的执行框架 | 中 |

### P1 — 生产级安全与协议完整性

| # | 缺口 | 理由 |
|---|---|---|
| 6 | JWT 认证 | IoT 平台主流鉴权方式，实现成本低 |
| 7 | HTTP 认证（Webhook 鉴权） | 一次实现即可对接任意外部用户系统 |
| 8 | 客户端自动封禁 / flapping 检测 | 已有速率限制，缺"触发后惩罚"闭环 |
| 9 | ACL / 认证文件的自动重载 | 已有 `LoadFile`，只差文件监听 |
| 10 | X.509 证书 → 用户名/ACL 映射 | mTLS 已具备，缺身份落地 |
| 11 | 多监听器（listener 数组） | 一个端口跑 TLS、一个跑明文是常见需求 |
| 12 | Proxy Protocol | LB / K8s Ingress 后取真实 IP |
| 13 | OpenTelemetry 链路追踪 | 生产排障刚需 |
| 14 | 进程内 Trace 主题（订阅某客户端消息流） | 低成本高价值的调试工具 |
| 15 | MQTT 3.1 支持 | 老设备兼容 |

### P2 — 差异化与体验

| # | 缺口 | 理由 |
|---|---|---|
| 16 | 共享订阅多策略（random/sticky/hash） | 已有 round-robin，补齐策略成本低 |
| 17 | 排他订阅 `$exclusive` | 协议细节完备度 |
| 18 | 延迟发布 / 消息优先级 | 工业场景常见需求 |
| 19 | 主题重写 / 自动订阅 | 规则引擎的轻量替代 |
| 20 | Schema Registry（校验/转换） | 数据质量治理 |
| 21 | $SYS 主题扩展（30+ 指标） | 低成本，观感提升明显 |
| 22 | 日志轮转 / 审计日志 | 运维合规 |
| 23 | Alarm 系统 | 异常主动暴露 |
| 24 | 内置 Dashboard（可极简：只读指标页） | 或仅提供 Grafana 模板 |
| 25 | 插件动态加载（Go plugin / WASM / 外部进程） | 扩展生态 |

### P3 — 长尾

| # | 缺口 |
|---|---|
| 26 | MQTT over QUIC |
| 27 | MQTT-SN / CoAP / LwM2M 网关 |
| 28 | MQTT Bridge |
| 29 | Cluster Linking |
| 30 | Flow Designer 可视化编排 |
| 31 | 云服务桥接（Kinesis / Pub/Sub / Event Hubs） |
| 32 | 备份/恢复工具、CLI 管理工具 |

---

## 五、分阶段路线建议

**阶段一（平台基础，让 Broker 可运维）**
- HTTP 管理 API（`GET /clients`、`DELETE /clients/{id}`、`GET /subscriptions`、`GET /retained`、`POST /publish`）
- 配置热重载（SIGHUP + API）
- 多监听器改造（`Config.Listeners []Listener`）
- JWT + HTTP 认证
- 客户端封禁闭环、ACL 文件自动重载
- $SYS 指标扩展

**阶段二（水平扩展，解决单点）**
- 集群成员发现（静态列表 + etcd）
- 跨节点路由表（主题 → 节点映射，可先做广播式）
- 跨节点共享订阅
- 会话迁移 or 会话"粘性路由"

**阶段三（数据平台化，真正对标 EMQX 价值面）**
- 最小 SQL 规则引擎（`SELECT … FROM "topic" WHERE …` → 动作）
- 动作出口：Webhook → Kafka → Redis → 数据
- 规则/桥接的动态增删改（复用阶段一的管理 API）
- OpenTelemetry 追踪

**贯穿全程**
- 明确文档定位：写明"当前为单节点 Broker，不支持集群"，修正 `deploy/k8s/` 可能带来的误解
- 保持零依赖、可嵌入的差异化优势不被破坏

---

## 六、Shark-MQTT 值得保留的差异化优势

1. **单二进制、零外部依赖**：EMQX 必须带 Erlang 运行时，Shark-MQTT 可 `go build` 出一个文件，适合边缘/嵌入场景。
2. **Go 生态与可嵌入性**：`api.NewBroker(...)` 可直接嵌入宿主程序，EMQX 做不到。
3. **协议内核质量**：属性编解码、QoS 状态机、fuzz（840 万次无崩溃）、race detector 全绿 —— 这部分不比 EMQX 差。
4. **bounded concurrency 设计**：所有队列/缓冲区硬上限 + 分层 buffer pool + 对象池，工程纪律好。
5. **MIT 许可**：EMQX 5.9 起为 BSL 1.1，对商业集成有约束；MIT 是 Shark-MQTT 的天然卖点。

> **定位建议**：做"Go 世界的嵌入式/边缘 MQTT Broker"，而不是"EMQX 的 Go 复刻"。EMQX 的集群与数据集成面向的是大规模云平台，Shark-MQTT 补齐 **管理 API + 轻量规则引擎 + 单个数据出口** 后，即可覆盖绝大多数边缘与中等规模场景。

---

## 附：统计口径

| 项 | 数值 |
|---|---|
| 非测试 Go 代码 | 27,265 行 |
| 测试文件 | 71 个（单测 375 / 集成 111 / 基准 65） |
| 最大包 | `broker` 5,944 行 |
| 存储后端 | memory（默认）/ redis / badger |
| 插件钩子 | 4 个 |
| REST 端点 | 3 个（`/healthz` `/readyz` `/metrics`） |
| $SYS 主题 | 5 个 |
| 集群相关代码 | 0 |
| 规则/桥接相关代码 | 0 |
