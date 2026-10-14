# Shark-MQTT 与 Smart-MQTT 对比梳理（2026-10-14 重做）

> **参照方**：`G:/smart-mqtt`（Java / AGPL-3.0，276 个 `.java`，非 git 仓库的静态快照）
> **被评估方**：`G:/shark-mqtt`（Go / MIT，HEAD `22c16b83b`）
> **上一版**：`docs/reports/SMART-MQTT-GAP-ANALYSIS-261012.md`（基线 `ebe4aa52d`）
> **口径**：以**代码为准**，不以 README / 文档声明为准；凡结论均给出 `文件:行`。
> 复核某属性是否"未完成"时，必须先确认当前实现**是否已经满足规范**，不能仅因
> "字段被解析但没有后续处理代码"就判为缺口 —— 上一版有 3 处栽在这个口径上，见第 5 节。
>
> **结论性质**：两者同属 MQTT Broker 品类，但 smart-mqtt 的终点是**企业级平台**
> （插件市场 + 控制台 + 集群 + 数据出口），shark-mqtt 的终点是**单机可嵌入 Broker 库**。
> 因此差距表既列功能缺口，也标注"是否属 shark-mqtt 的既定定位"。

---

## 0. TL;DR

| 维度 | 结论 |
|---|---|
| **规模** | shark-mqtt：159 个 `.go`（67 非测试 / 92 测试），非测试 **16,009** 行，测试 25,813 行；对照 **440 单测 / 147 集成 / 61 基准**（全绿） |
| **MQTT 协议内核** | **本轮反超**：MQTT 5.0 逐消息属性中继（§3.3.2.3）已落地，**smart-mqtt 至今丢弃**（见 4.1） |
| **协议合规深度** | 本轮再补 4 处规范边界：重连 PUBREL 重传 / PUBREC 理由码 / 按版本门控重传 / 过期边界 |
| **平台化能力（主要差距）** | 4 项 P0 结构性缺口（插件体系 / 管理面 / 集群 / 数据出口）**本轮零推进** |
| **21 项缺口状态** | 本轮**无一项被关闭**（P0 4 / P1 9 / P2 8）；但 21 项之外的 MQTT 5.0 属性项被本轮新增实现关闭 |
| **最危险的一项** | 不变：`deploy/k8s/` 多副本 ≠ 集群（默认值已修正并补告警，`f229539dd`） |
| **与上一版的差别** | 上一版有 **3 处误判**（2 处其自身已修，1 处本轮新发现），本版全部更正 |

**一句话**：这 17 个提交全部落在**协议内核正确性与工程债**上，没有一项落在平台化缺口上。
结果是 shark-mqtt 在"单机 Broker 的规范符合度"这条线上进一步拉开，而在"平台化"这条线上
与上一版完全一致。

---

## 1. 本轮增量（`ebe4aa52d..22c16b83b`，17 个提交）

| 类别 | 提交数 | 提交 |
|---|---|---|
| **A. MQTT 5.0 逐消息属性中继** | 8 | `b1e92323d` `89fa5c21e` `c13ce7a97` `8ebf3b676` `75bb9ca3a` `df98f4a73` `f8ccd6952` `2460554a4` |
| **B. 协议合规边界修补** | 4 | `df7791891`（重连重传 PUBREL）`5d4e8916e`（PUBREC 理由码）`a090ced29`（版本门控重传）`bde0f36ae`（过期边界） |
| **C. 工程债 / CI** | 5 | `36c53c878`（后台循环竞态）`3f13e617a` `22c16b83b`（健康服务端竞态与格式）`6d6d048df` `63a67e1a0`（文档） |

### 1.1 A 类：逐消息属性中继（本轮唯一的功能性新增）

`broker/publish_properties.go:34` `forwardedPublishProperties` 把发布者的**逐消息属性**
原样带到每一条出站 PUBLISH：

| 属性 | 处理 | 依据 |
|---|---|---|
| Payload Format Indicator | 原样转发 | §3.3.2.3.1 |
| Content Type | 原样转发 | §3.3.2.3.2 |
| Response Topic | 原样转发 | §3.3.2.3.4 |
| Correlation Data | 原样转发 | §3.3.2.3.5 |
| **User Property** | 原样转发且**保持相对顺序** | §3.3.2.3.7 |
| Topic Alias | **有意不转发**（属发布者入站别名空间） | §3.3.2.3.4 |
| Message Expiry Interval | 由绝对截止时间反推**递减后**的值 | §3.3.2.3.3 / [MQTT-3.3.2-6] |
| Subscription Identifier | 服务端追加（匹配订阅的） | §3.3.4 |

同时 `broker/publish_properties.go:100` `storeProperties` / `:127` `protocolProperties`
把这份中继数据**落盘**（`store.MessageProperties`），使 QoS 2 中转（`broker/broker.go:1655`）、
会话重启后重传（`broker/session.go:667,684`）、保留消息重放（`2460554a4`）三条路径都能带上它。

> 这一项**关闭了上一版 §3.4 里被判为「两者皆缺」的两行**（User Property 转发、
> Content Type / Response Topic / Correlation Data / Payload Format），并且因为 smart-mqtt
> 至今不转发，该维度上 shark-mqtt **反超**（证据见 4.1）。

### 1.2 B 类：协议合规边界（4 处）

| 提交 | 修正内容 | 规范依据 |
|---|---|---|
| `df7791891` | 重连时按握手阶段重传：已收到 PUBREC 的条目重发 **PUBREL** 而非 PUBLISH | §4.4、[MQTT-4.3.3-4] |
| `5d4e8916e` | 收到 PUBREC 后按其 Reason Code 决定收尾（< 0x80 发 PUBREL，≥ 0x80 直接释放） | §4.3.3、[MQTT-4.4.0-2] |
| `a090ced29` | 连接内重传按**协议版本**门控：3.1/3.1.1 允许，5.0 禁止 | 5.0 §4.4 [MQTT-4.4.0-1] |
| `bde0f36ae` | Message Expiry 只作**首次转发前**的过滤器，禁止丢弃「已上过线」的投递 | [MQTT-3.3.2-5]、[MQTT-4.3.3-7] |

`protocol/spec.go:57` 新增能力位 `RetransmitUnacked`（3.1/3.1.1 = `true`、5.0 = `false`，见
`spec.go:67,80,102`）；门控由 broker 在 CONNECT 时**推入**引擎
（`broker/qos_engine.go:154` `SetClientVersion`、`:171` `retransmitPermitted`），
而不是在重试回调里回查会话 —— 后者会构成 `会话锁 → q.mu` 的反向环。

### 1.3 C 类：工程债（2 个真竞态，1 个被顺带暴露）

| 提交 | 问题 | 性质 |
|---|---|---|
| `36c53c878` | `sessionCleanupLoop` / `retainedCleanupLoop` / `sysStatusLoop` 三个循环未被追踪，`Stop` 只 cancel 就返回，下一次 `Start` 重写 `b.ctx` / `b.startedAt` 与仍在 drain 的循环**并发** | 真竞态（自 `784b18a6f2` 潜伏），WaitGroup 在 `Stop` 顶部 join |
| `3f13e617a` | `api.Broker` 的健康监听器：goroutine 读 `healthSrv` **字段**而 `Start → Stop → Start` 重赋它；且 `Stop` 只调 `Shutdown`（只关已注册的监听器）导致**端口泄漏** | 真竞态 + 端口泄漏（自 `b1b011794`） |
| `22c16b83b` | 上一条的结构体注释对齐不合 `gofmt` | 格式 |

> **注意**：`36c53c878` **不是**"优雅停机 drain"。它与上一版 §2.3 里那条
> `[ ] 优雅停机 drain（滚动升级不触发遗嘱风暴）` 是两件事，后者**仍然开放**。
> 该提交顺带说明：这两个 `Stop → Start` 竞态是被 `b1b011794` 新增的优雅停机测试**照出来的**，
> 在此之前 `-race` 从未走到该路径。

---

## 2. 差距总览（21 项，本轮复核后）

状态口径：`未动` = 本轮无任何代码变化；`不适用` = 与 MIT 定位冲突，不建议补。

| # | 缺口 | 优先级 | smart-mqtt 证据（已复核路径） | shark-mqtt 现状（HEAD） | 状态 | 属既定定位 |
|---|---|---|---|---|---|---|
| 1 | 插件生命周期与热插拔 | P0 | `plugin/spec/Plugin.java:114,190,237,241,296,363`；`PluginRegistryImpl.java` URLClassLoader | 仅 4 钩子（`plugin/manager.go:15-20`）；`Register`/`Unregister` 进程内 | 未动 | 否（应补） |
| 2 | 事件总线 / 消息总线 / Provider 扩展点 | P0 | `plugin/spec/bus/EventType.java`；`provider/Providers.java` | 无 | 未动 | 否（应补） |
| 3 | HTTP 管理 API + Web 控制台 | P0 | enterprise-plugin 9 个 Controller | 仅 `/healthz` `/readyz` `/metrics`（`api/api.go:410,414,432`） | 未动 | 部分（可选） |
| 4 | 集群（跨节点消息路由） | P0 | `cluster-plugin/.../Coordinator.java` | `cluster` 生产代码 **0 命中** | 未动 | 是（已明确暂不做） |
| 5 | 数据出口：Redis 桥接 | P1 | `redis-bridge-plugin/.../BridgeService.java` | `bridge` 0 命中 | 未动 | 是（定位为不做） |
| 6 | 数据出口：Kafka 协议 + 存储引擎 | P1 | `kafka-storage-plugin/.../KafkaServer.java`、`.../store/PartitionLog.java` | `kafka` 0 命中 | 未动 | 是 |
| 7 | 认证：Redis / HTTP 认证器 + 异步认证链 | P1 | `advanced-auth-plugin/.../AdvancedAuthPlugin.java:104-129,176-181`；`HttpAuthenticator.java` | 仅 `Static`/`File`/`Chain`/`Noop`（`broker/auth*.go`） | 未动 | 部分（应补 Redis/HTTP） |
| 8 | 连接防抖（Flapping 自动封禁） | P1 | `plugin/flapping/FlappingDetector.java:35,50,67,105` | 仅常量 `ConnAckBanned = 0x8A`（`protocol/constants.go:98`），无逻辑 | 未动 | 否（应补） |
| 9 | 多 listener 数组 / 插件化端口 | P1 | `Plugin.java:296` `addUsagePort`，每插件独立端口 | 单 `listener net.Listener`（`broker/server.go:27`）；WS/WSS 为数组（`:40`） | 未动 | 否（应补） |
| 10 | 配置 UI 元数据（Schema） | P1 | `plugin/spec/schema/Item.java`；`Plugin.java:363` | `config/` 内无 schema | 未动 | 否 |
| 11 | 插件市场 / 本地 JAR 上传 | P1 | `PluginManagerController.java`；`PluginRepositoryController.java` | 无 | 未动 | 否（可选） |
| 12 | 插件运行时启停 / 配置热改 / 日志 SSE | P1 | `PluginManagerController.java` | 无 | 未动 | 否 |
| 13 | 智能推送模型（高负载自适应延迟） | P2 | changelog v1.5.3 | 无 | 未动 | 否（可选） |
| 14 | 插件自愈定时器 / 柔性注销 | P2 | `Plugin.java:60,190` `selfRescueTimer` | 无 | 未动 | 否 |
| 15 | 版本检查 / 更新提示 | P2 | `VersionController.java` | 无 | 未动 | 否 |
| 16 | 指标项丰富度（MqttMetricEnum + Histogram） | P2 | `MetricController.java` | 12 项（`pkg/metrics/prometheus.go:67,73,79,85,91,97,103,109,115,121,127,133`） | 未动 | 部分 |
| 17 | AI Agent / MCP 接口 | P2 | `AiController.java` | 无 | 未动 | 否（不适用） |
| 18 | License 授权与连接数限制 | P2 | `LicenseController.java` | 无 | 不适用 | **不适用**（MIT） |
| 19 | 内置压测插件（控制台启停 / 压三方） | P2 | bench-plugin | 仅 `tests/bench`（离线） | 未动 | 部分 |
| 20 | 连接管理（列表查询 / 强制断开） | P2 | `ConnectionsController.java` | 无 | 未动 | 否 |
| 21 | Topic 统计 / 订阅关系查询 | P2 | `TopicController.java` | 无 | 未动 | 否 |

**读法**：`未动` 出现 20 次不是"分析偷懒"，而是本轮 17 个提交的真实分布 —— 见第 1 节。
若把两侧的投入方向画出来，smart-mqtt 的增量在**插件与平台**，shark-mqtt 的增量在**协议内核**。

---

## 3. P0 结构性缺口的详细现状（4 项）

### 3.1 插件体系：4 个钩子 vs 完整生命周期 + 双总线

**smart-mqtt**（`plugin/spec/Plugin.java`，均已复核行号）

| 能力 | 行 |
|---|---|
| `install()` / `uninstall()` | `:114` |
| 定时器 `timer()` / 自愈 `selfRescueTimer()` | `:201` / `:190`（内部 `HashedWheelTimer`，`:192`） |
| 加载优先级 `order()` | `:237` |
| 插件存储目录 `storage()` | `:241` |
| 端口占用声明 `addUsagePort()` | `:296` |
| YAML 配置加载 + 下划线转驼峰 | `:345` |
| 配置 UI 元数据 `schema()` | `:363` |
| 事件总线 15+ 事件类型 | `plugin/spec/bus/EventType.java` |
| Provider 扩展点（会话状态 / 订阅关系） | `plugin/spec/provider/Providers.java` |
| **运行时热插拔**（每插件独立 `URLClassLoader`） | `plugin/spec/PluginRegistryImpl.java` |

**shark-mqtt**（`plugin/manager.go`）
- 钩子仅 4 个：`OnAccept` / `OnConnected` / `OnMessage` / `OnClose` —— `:15-20`
- 仅 `Register` / `Unregister` 进程内注册，**无动态加载**
- 分发带**超时 + panic 隔离**（`hookTimeout` 默认 10s）—— 这点比 smart-mqtt 更硬

> **上一版此处有一处误判，已更正**：上一版写"HookContext 未携带 Topic/Payload
> （`plugin/manager.go:22-32`）"。复核上一版基线 `ebe4aa52d` 的同一文件，
> **8 个字段（`ClientID` `Username` `Topic` `Payload` `QoS` `Retain` `RemoteAddr` `Err`）
> 当时就已全部存在**（字段自 `178078566`，2026-04-23 引入）。
> **真正的缺口不是结构体缺字段，而是分发点不填充**：

| 钩子 | 分发点 | 实际填充 |
|---|---|---|
| `OnAccept` | `broker/broker.go:229` | 只有 `RemoteAddr` |
| `OnConnected` | `broker/broker.go:568` | `ClientID` + `Username` |
| `OnClose` | `broker/broker.go:1202` | 只有 `ClientID` |
| **`OnMessage`** | `broker/broker.go:1241` | **只有 `ClientID`**（Topic/QoS/Retain/Payload 全部为空） |

**未完成清单**
- [ ] 插件生命周期抽象（版本 / 厂商 / 优先级 / 初始化 / 销毁 / 卸载回调）
- [ ] 事件总线（连接 / 断开 / 收发消息 / 会话 / 主题 / 订阅全生命周期事件）
- [ ] 消息总线（可消费的发布流，替代单一 `OnMessage` 钩子）
- [ ] Provider 扩展点（可替换会话状态存储、订阅关系存储）
- [ ] 插件配置模型（每插件独立配置 + schema 元数据）
- [ ] 插件热插拔（动态加载 / 卸载，或至少运行时启停）
- [ ] **钩子上下文补全**：`OnMessage` 需带 `Topic` / `Payload` / `QoS` / `Retain`
      （结构体已就绪，改的是 `broker.go:1241` 这一个调用点）

### 3.2 HTTP 管理 API 与 Web 控制台

smart-mqtt enterprise-plugin 的 9 个 Controller 路径均已复核存在：
`ConnectionsController` · `TopicController` · `PluginManagerController` ·
`PluginRepositoryController` · `SystemController` · `MetricController` ·
`LicenseController` · `VersionController` · `AiController`
（均在 `plugins/enterprise-plugin/.../plugin/openapi/controller/`）。

**shark-mqtt 现状**：只有 3 个端点 —— `/healthz`（`api/api.go:410`）、
`/readyz`（`:414`）、`/metrics`（`:432`，仅当 `metrics_enabled`）。

### 3.3 集群

**shark-mqtt 现状**：`cluster` 在生产代码中 **0 命中**；仅
`docs/architecture/CLUSTER-FEASIBILITY.md` 设计文档。本轮**无变化**。
已识别的硬约束不变（`broker/broker.go` 的 `clientState.conn net.Conn` 不可迁移、
`connections` map 与 `Manager.sessions` 进程私有、每实例一份全量订阅树）。

已关闭 / 仍开放的相邻项：

| 项 | 状态 |
|---|---|
| 遗嘱风暴（滚动重启不触发遗嘱洪泛） | **已修复**（`b1b011794`） |
| `DISCONNECT 0x8B` 通知 MQTT 5 客户端 | **已修复**（`b1b011794`） |
| `deploy/k8s/` 多副本默认值与告警 | **已修复**（`f229539dd`） |
| **优雅停机 drain** | **仍开放**（本轮 `36c53c878` 修的是循环泄漏竞态，不是 drain） |
| 节点发现 / 订阅 delta 广播 / 跨节点路由 / 全局唯一 msgID | 仍开放 |

### 3.4 数据出口

`bridge` / `kafka` / `exporter` / `webhook` 在生产代码中全部 **0 命中**。
`store/` 三个后端（memory / redis / badger）**只做持久化，不做出站桥接**。
本轮**无变化**。

---

## 4. MQTT 5.0 属性语义 —— 本轮唯一被关闭的项，且反超

### 4.1 smart-mqtt 丢弃逐消息属性（新取证）

上一版写"smart-mqtt 同样不转发"，本轮复核到**确切的构造点**：smart-mqtt 的 broker 在
**每一条出站 PUBLISH 上构造一个空的 `PublishProperties()`**，发布者的属性从未被拷入：

| 路径 | 证据 |
|---|---|
| 普通投递 | `broker/topic/AdvancedMessageDeliver.java:79-81` → `publishBuilder.publishProperties(new PublishProperties())` |
| QoS 0 投递 | `broker/topic/BaseMessageDeliver.java:147` → `PublishProperties properties = new PublishProperties();` |
| 共享订阅投递 | `broker/topic/SharedOrderedMessageDeliver.java:84-86` |
| 保留消息重放 | `broker/MqttSessionImpl.java:271-273` |
| 遗嘱发布 | `broker/processor/ConnectProcessor.java:190-193` |

`ContentType` / `ResponseTopic` / `CorrelationData` / `PayloadFormat` / `UserProperty`
在 Java 侧**只存在于编解码层**（`common/.../variable/properties/MqttProperties.java:602-630`、
`PublishProperties.java:35-75`），broker 包内**零读取**。

**后果**：MQTT 5.0 §4.10 的请求/响应模式在 smart-mqtt 上不可用 —— 响应方收不到
Response Topic 与 Correlation Data；应用层标签（User Property）也被剥离。

### 4.2 逐项对照（当前状态）

| 属性 | shark-mqtt（HEAD） | smart-mqtt | 判定 |
|---|---|---|---|
| **User Property 转发** | **转发且保序**（`publish_properties.go:34`） | 丢弃 | **shark-mqtt 反超** |
| **Content Type** | **转发** | 丢弃 | **shark-mqtt 反超** |
| **Response Topic** | **转发** | 丢弃 | **shark-mqtt 反超** |
| **Correlation Data** | **转发** | 丢弃 | **shark-mqtt 反超** |
| **Payload Format Indicator** | **转发** | 丢弃 | **shark-mqtt 反超** |
| Topic Alias | 有意不转发（正确） | 不转发 | 相当 |
| Message Expiry Interval | 按剩余时间递减（`remainingExpiry`，`:75`） | — | **shark-mqtt 独有** |
| Subscription Identifier | 服务端追加 | — | 相当 |
| **属性落盘（跨重传/重放保持）** | **有**（`storeProperties`，`:100`） | 无 | **shark-mqtt 独有** |
| Retain As Published | **已生效**（`be8021a91`） | — | **shark-mqtt 独有** |
| Maximum QoS 通告 | 省略（合规，见下） | — | 非缺口 |
| Request Problem Information | 天然满足（见下） | — | 非缺口 |
| 共享订阅策略 | 仅 `$share` + round-robin | 仅 `$share` + round-robin | 两者皆有限 |

---

## 5. 上一版 3 处误判的更正记录

| # | 上一版结论 | 更正 | 更正者 |
|---|---|---|---|
| 1 | "CONNACK 未通告 Maximum QoS" 为缺口 | §3.2.2.3.4：Maximum QoS **只允许 0 或 1，省略即表示支持 QoS 2**。shark-mqtt 支持 QoS 2，省略是唯一正确编码 | 上一版自身（`63a67e1a0`） |
| 2 | "Request Problem Information 仅解析未裁剪" 为缺口 | §3.1.2.11.7 只要求"置 0 时不得发送 Reason String / User Property"；broker 侧对这两类属性**零引用**，天然满足 | 上一版自身（`63a67e1a0`） |
| 3 | "HookContext 未携带 Topic/Payload，`OnMessage` 只有 ClientID" | **字段自 2026-04-23 起就在**（`178078566`）。真缺口是**分发点未填充**（`broker.go:1241`） | **本轮新发现** |

> 三处误判同源：**读了类型定义或字段解析代码，就推断运行时行为**。
> 更正方法一致：回到**实际调用点**取证。这也是本版把"复核口径"写进抬头的原因。

**另修正 2 处证据路径**（上一版附录 A 写错，现按实际路径给出）：

| 文件 | 上一版路径 | 实际路径 |
|---|---|---|
| `FlappingDetector.java` | `.../plugin/enterprise/flapping/` | `plugins/enterprise-plugin/src/main/java/tech/smartboot/mqtt/plugin/flapping/` |
| `BridgeService.java` | `.../plugin/redis/bridge/` | `plugins/redis-bridge-plugin/src/main/java/tech/smartboot/mqtt/bridge/redis/` |

---

## 6. Shark-MQTT 已超越 Smart-MQTT 的点（反向清单）

**本轮新增 3 项**（标 ★）：

| 能力 | 证据 | 说明 |
|---|---|---|
| ★ **MQTT 5.0 逐消息属性中继** | `broker/publish_properties.go:34,100,127`；`broker/broker.go:2135` | smart-mqtt 每条投递都构造空 `PublishProperties()`（4.1），§4.10 请求/响应在其上不可用 |
| ★ **属性持久化 + 跨重传/重放保持** | `store.MessageProperties`；`broker/session.go:667,684`；`2460554a4` | 重传与保留消息重放都带原属性，不丢标签 |
| ★ **重传语义按协议版本分流** | `protocol/spec.go:57,67,80,102`；`broker/qos_engine.go:154,171` | 5.0 遵守 [MQTT-4.4.0-1] 的"MUST NOT"，3.1/3.1.1 保留弱网兼容重传 |
| 协议适配层抽象（Adapter/Spec/Registry） | `protocol/spec.go`、`adapter.go`、`registry.go`、`broker/conn.go` | smart-mqtt 无此抽象层，多协议扩展需改核心 |
| MQTT 3.1 完整语义 | `spec.go` 能力表 + 专项集成测试 | 有独立 Spec 能力表与回归用例 |
| 增强认证框架（AUTH 报文 + 握手 + 重认证） | `broker/broker.go` AUTH 处理路径 | smart-mqtt 仅有属性常量，Broker 无 AUTH 处理 |
| 协议 fuzz（840 万+ 次执行无崩溃） | `protocol/fuzz_test.go` | smart-mqtt 无 |
| 单二进制 / 零外部依赖 | `go.mod`（badger/redis 为可选构建标签） | smart-mqtt 需 JVM |
| 批量写队列有界 + 分档缓冲池 | `broker/write_queue_test.go`、`pkg/bufferpool/pool.go` | — |
| 存储后端 fail-fast 校验 | `store/errors.go`、`store/interfaces.go` | — |
| 文件型账户认证器 | `broker/auth_file.go` | smart-mqtt 无 |
| 指标标签基数收敛（防时间线爆炸） | `broker/metrics_cardinality_test.go` | — |

---

## 7. 建议补齐顺序（若决定向平台化演进）

与上一版**一致**（本轮无一项被关闭，故顺序不变），仅标注状态：

```
阶段 0（接口地基，低风险）
  T01 listener 数组化 + 插件化端口                       [未动]
  T02 插件生命周期抽象（版本/优先级/初始化/销毁 + 配置模型）  [未动]
  T03 事件总线（扩展为全事件）                            [未动]
  T03b 钩子分发点补全（OnMessage 带 Topic/QoS/Retain/Payload）  ← 最低成本，单点改动
  T04 k8s "多副本 ≠ 集群"告警                            [已完成 f229539dd]

阶段 1（管理面）
  T05 HTTP 管理 API（只读：连接/订阅/主题统计）             [未动]
  T06 连接管理（强制断开）+ 运行时可观测端点                [未动]
  T07 连接防抖（Flapping）                                [未动]

阶段 2（出口与集群）
  T08 消息出站抽象 + Redis/Webhook 出口                    [未动]
  T09 集群：节点发现 + 订阅 delta 广播 + 粘性路由            [未动]
  T10 优雅停机 drain（先行，独立于集群）                    [未动；遗嘱风暴部分已完成 b1b011794]
```

> 新建议：**T03b 是整张表里性价比最高的一项** —— 结构体与钩子签名都已就绪，
> 只需在 `broker/broker.go:1241` 的 `OnMessage` 分发点填充
> `Topic` / `QoS` / `Retain` / `Payload`（`handlePublish` 处可拿到完整 `*protocol.PublishPacket`）。
> 在此之前，任何依赖 `OnMessage` 的插件都拿不到消息内容，插件生态无法起步。

---

## 附录 A — 规模与计数基线（2026-10-14 实测）

| 指标 | 值 |
|---|---|
| `.go` 文件总数 | 159（67 非测试 / 92 测试） |
| 非测试 Go 代码行数 | 16,009 |
| 测试 Go 代码行数 | 25,813 |
| 单元测试 / 集成测试 / 基准 | 440 / 147 / 61（全绿，`-race` 全量通过） |
| 对照：smart-mqtt `.java` | 276 |

分模块非测试行数：`broker` 6,791（17 个文件）· `protocol` 3,479 · `store` 1,447 ·
`client` 884 · `api` 532 · `pkg` 466 · `config` 366 · `plugin` 179 · `cmd` 160。

**口径复现**：测试计数 = 源码中声明的顶层 `Test*` / `Benchmark*` 函数数，
单目录非递归统计，`store/redis` 与 `store/badger`（构建标签）不计入。

### 文档中已失效的数字（待单独提交刷新）

以下位置仍在引用**上一版之前的**规模数字，与本附录不符：

| 位置 | 现文（失效） |
|---|---|
| `docs/architecture/PROTOCOL-ADAPTER-DESIGN.md:6` | `27,265 行非测试代码，375 单测 + 111 集成测试 + 65 基准` |
| `docs/architecture/CLUSTER-FEASIBILITY.md:9` | `非测试 Go 代码 27,265 行；broker 包 5,944 行` |
| `docs/reports/EMQX-GAP-ANALYSIS-260910.md:6,259` | 同上（**历史快照，按约定不维护**） |

> `docs/reports/*` 与 `CHANGELOG.md` 是当日快照，保留原始数字；前两处是**活文档**，应当刷新。

---

## 附录 B — 证据索引

**smart-mqtt（路径均已按实际目录复核）**
- 插件规范：`smart-mqtt-plugin-spec/src/main/java/tech/smartboot/mqtt/plugin/spec/`
  `Plugin.java:60,114,190,237,241,296,345,363` · `BrokerContext.java` ·
  `PluginRegistryImpl.java` · `bus/EventType.java` · `provider/Providers.java` · `schema/Item.java`
- Broker 核心：`smart-mqtt-broker/src/main/java/tech/smartboot/mqtt/broker/`
  `BrokerContextImpl.java` · `processor/ConnectProcessor.java:190-193` ·
  `processor/PublishProcessor.java:33-100` · `topic/AdvancedMessageDeliver.java:79-81` ·
  `topic/BaseMessageDeliver.java:147` · `topic/SharedOrderedMessageDeliver.java:84-86` ·
  `MqttSessionImpl.java:271-273`
- 协议层：`smart-mqtt-common/src/main/java/tech/smartboot/mqtt/common/`
  `enums/MqttVersion.java` · `enums/MqttMessageType.java` · `util/MqttPropertyConstant.java` ·
  `util/TopicByteTree.java` · `InflightQueue.java` ·
  `message/variable/properties/MqttProperties.java:602-630` · `.../properties/PublishProperties.java:35-75`
- 插件：`plugins/`
  `enterprise-plugin/.../plugin/openapi/controller/`（9 个）·
  `enterprise-plugin/.../plugin/flapping/FlappingDetector.java:35,50,67,105` ·
  `cluster-plugin/.../plugin/cluster/Coordinator.java` ·
  `kafka-storage-plugin/.../plugin/kafka/storage/kafka/server/KafkaServer.java` ·
  `kafka-storage-plugin/.../plugin/kafka/storage/store/PartitionLog.java` ·
  `advanced-auth-plugin/.../auth/advanced/AdvancedAuthPlugin.java:104-129,176-181` ·
  `advanced-auth-plugin/.../auth/advanced/provider/HttpAuthenticator.java` ·
  `redis-bridge-plugin/.../bridge/redis/BridgeService.java` ·
  `simple-auth-plugin/.../auth/SimpleAuthPlugin.java`

**shark-mqtt**
- 协议：`protocol/spec.go:40,57,67,80,102` · `protocol/constants.go:98` ·
  `protocol/properties.go` · `protocol/topic.go` · `protocol/packets.go`
- Broker：`broker/broker.go:229,568,1202,1241,1655,2135` ·
  `broker/publish_properties.go:34,75,100,127` ·
  `broker/session.go:138,147,558,623,636,667,684` ·
  `broker/qos_engine.go:154,171` · `broker/topic_tree.go` · `broker/server.go:27,40,185-209`
- 扩展 / 配置：`plugin/manager.go:15-20,24-34` · `config/config.go` · `config/loader.go` · `cmd/main.go`
- 存储 / 指标：`store/interfaces.go` · `store/types.go` · `pkg/metrics/prometheus.go:67-133`
- 既有分析：`docs/reports/SMART-MQTT-GAP-ANALYSIS-261012.md` ·
  `docs/reports/EMQX-GAP-ANALYSIS-260910.md` ·
  `docs/architecture/CLUSTER-FEASIBILITY.md` ·
  `docs/architecture/PROTOCOL-ADAPTER-DESIGN.md` ·
  `docs/architecture/CONCURRENCY.md`

---

*生成时间：2026-10-14 · 参照方：smart-mqtt（静态快照，276 个 .java）· 被评估方：shark-mqtt `22c16b83b`*
*相对 261012 版的变化：新增第 1 节（本轮增量）、第 4 节（MQTT 5.0 属性重定性）、*
*第 5 节（3 处误判更正）；刷新附录 A 规模基线；修正 2 处 Java 证据路径。*
