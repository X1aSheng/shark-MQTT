# Shark-MQTT 相对 Smart-MQTT 未完成细节清单

> 对比基线：`G:/smart-mqtt`（Java / AGPL-3.0，v1.8.0 代码，276 个 .java）
> 被评估方：`G:/shark-mqtt`（Go / MIT，145 个 .go 含测试）
> 口径说明：以**代码为准**，不以文档/README 声明为准。凡引用均给出 `file:line`。
> 结论性质：两者同属"MQTT Broker"品类，但 smart-mqtt 的终点是**企业级平台**（插件市场 + 控制台 + 集群 + 数据出口），shark-mqtt 的终点是**单机可嵌入 Broker 库**。因此下表既包含"功能缺口"，也标注"是否属 shark-mqtt 的既定定位"。

---

## 0. TL;DR

| 维度                            | 结论                                                                                                                                                                                                                                                                                |
| ------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **MQTT 协议内核**               | shark-mqtt **已基本对齐，局部超越**（协议适配层抽象、MQTT 3.1 完整语义、AUTH 报文与重认证、fuzz、-race）                                                                                                                                                                            |
| **单机 Broker 语义**            | QoS 0/1/2、持久会话、会话过期、接管、遗嘱（含延迟）、保留、共享订阅、主题别名、流控、$SYS -- 均已实现，**无实质缺口**                                                                                                                                                               |
| **平台化能力（主要差距）**      | 插件体系深度、HTTP 管理面、集群、数据出口 4 项为**结构性缺失**                                                                                                                                                                                                                      |
| **优先级分布**                  | P0 结构性 4 项 / P1 功能 9 项 / P2 次要 8 项                                                                                                                                                                                                                                        |
| **最危险的一项**                | `deploy/k8s/` 曾默认给出多副本模板（`deploy/k8s/app/deployment.yaml` `replicas: 2`、`hpa.yaml` `minReplicas: 2`、Helm `values-prod.yaml` `replicaCount: 3`），但节点间**零通信**--多副本 != 集群，会导致漏投 / 重复投 / 保留消息分裂。**已修正默认值并补告警**（commit `f229539dd`） |
| **本轮已修复（合规 + 健壮性）** | Retain As Published 投递（`be8021a91`）,  优雅停机遗嘱风暴 + DISCONNECT 0x8B（`b1b011794`）,  k8s 多副本告警（`f229539dd`）,  拒绝连接审计日志 + 指标标签基数收敛（`ebe4aa52d`）                                                                                                       |

> 修订说明：第 3.4 节原有的两条"真缺口"（Maximum QoS 通告、Request Problem Information）
> 经复核为**误判**，已在该节标注并给出反证；同时把本轮已修复项就地标记。其余结论不变。

---

## 1. 差距总览（按优先级）

| #  | 缺口                                       | 优先级 | smart-mqtt 证据                                                                                  | shark-mqtt 现状                    | 是否属既定定位                  |
| -- | ------------------------------------------ | ------ | ------------------------------------------------------------------------------------------------ | ---------------------------------- | ------------------------------- |
| 1  | 插件生命周期与热插拔                       | **P0** | `Plugin.java`（install/uninstall/order/schema）；`PluginRegistryImpl.java:61,145` URLClassLoader | 仅 4 钩子，编译期注册              | 否（应补）                      |
| 2  | 事件总线 / 消息总线 / Provider 扩展点      | **P0** | `EventType.java:33-100`（15+ 事件）；`Providers.java:18-55`                                      | 无                                 | 否（应补）                      |
| 3  | HTTP 管理 API + Web 控制台                 | **P0** | enterprise-plugin 9 个 Controller；`api.go` 仅 3 端点                                            | 仅 `/healthz` `/readyz` `/metrics` | 部分（可选）                    |
| 4  | 集群（跨节点消息路由）                     | **P0** | cluster-plugin：Coordinator / SSE / ClusterMessage                                               | 0 代码（仅设计文档）               | 是（已明确暂不做）              |
| 5  | 数据出口：Redis 桥接                       | **P1** | redis-bridge-plugin `BridgeService.java:31-38` ZADD                                              | 0 代码                             | 是（定位为不做）                |
| 6  | 数据出口：Kafka 协议 + 存储引擎            | **P1** | kafka-storage-plugin 9 类 Kafka API + 自研分段日志                                               | 0 代码                             | 是                              |
| 7  | 认证：Redis / HTTP 认证器 + 异步认证链     | **P1** | advanced-auth-plugin（`AdvancedAuthPlugin.java:104-112`）                                        | 仅 Static/File/Chain               | 部分（应补 Redis/HTTP）         |
| 8  | 连接防抖（Flapping 自动封禁）              | **P1** | `FlappingDetector.java:35,67,88`                                                                 | 0（`ConnAckBanned` 常量未用）      | 否（应补）                      |
| 9  | 多 listener 数组 / 插件化端口              | **P1** | 每插件 `addUsagePort` 各自端口                                                                   | 单 listener 结构                   | 否（应补）                      |
| 10 | 配置 UI 元数据（Schema）                   | **P1** | `Item.java:42-103`；`Schema.java`                                                                | 无                                 | 否                              |
| 11 | 插件市场 / 本地 JAR 上传                   | **P1** | `PluginManagerController.java:186,460`；`PluginRepositoryController.java`                        | 无                                 | 否（可选）                      |
| 12 | 插件运行时启停 / 配置热改 / 日志 SSE       | **P1** | `PluginManagerController.java:72,143,388,402`                                                    | 无                                 | 否                              |
| 13 | 智能推送模型（高负载自适应延迟）           | **P2** | changelog v1.5.3                                                                                 | 无                                 | 否（可选）                      |
| 14 | 插件自愈定时器 / 柔性注销                  | **P2** | `Plugin.java:190` selfRescueTimer                                                                | 无                                 | 否                              |
| 15 | 版本检查 / 更新提示                        | **P2** | `VersionController.java:34,66`                                                                   | 无                                 | 否                              |
| 16 | 指标项丰富度（MqttMetricEnum + Histogram） | **P2** | `MetricController.java:67,117-122`                                                               | 12 项 Prometheus                   | 部分                            |
| 17 | AI Agent / MCP 接口                        | **P2** | `AiController.java:36,57` SSE                                                                    | 无                                 | 否（不适用）                    |
| 18 | License 授权与连接数限制                   | **P2** | `LicenseController.java:74-127`                                                                  | 无                                 | **不适用**（shark-mqtt 为 MIT） |
| 19 | 内置压测插件（控制台启停 / 压三方）        | **P2** | bench-plugin                                                                                     | 仅有 `tests/bench`                 | 部分                            |
| 20 | 连接管理（列表查询 / 强制断开）            | **P2** | `ConnectionsController.java:276,301`                                                             | 无                                 | 否                              |
| 21 | Topic 统计 / 订阅关系查询                  | **P2** | `TopicController.java:120,132`                                                                   | 无                                 | 否                              |

---

## 2. P0 - 结构性缺失（4 项）

### 2.1 插件体系：从"4 个钩子"到"完整生命周期 + 双总线"

**smart-mqtt（对照物）**

| 能力                                                                                                                            | 证据                                                                                               |
| ------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| 插件生命周期抽象类：`pluginName()/getVersion()/getVendor()/order()/install()/uninstall()/initPlugin()/destroyPlugin()/schema()` | `smart-mqtt-plugin-spec/.../Plugin.java`                                                           |
| 事件订阅（并支持销毁后自动失效）                                                                                                | `Plugin.java:137-153`                                                                              |
| 消息总线消费（`consumer()`）                                                                                                    | `Plugin.java:176-188`                                                                              |
| 定时器 `timer()` / 自愈定时器 `selfRescueTimer()`                                                                               | `Plugin.java:201`、`:190`                                                                          |
| 插件存储目录 `storage()`                                                                                                        | `Plugin.java:241`                                                                                  |
| YAML 配置加载 + 下划线转驼峰                                                                                                    | `Plugin.java:281-288`、`:310-339`                                                                  |
| 端口占用声明 `addUsagePort()`                                                                                                   | `Plugin.java:296-301`                                                                              |
| 配置 UI 元数据 `schema()`                                                                                                       | `Plugin.java:363`                                                                                  |
| 加载优先级 `order()`（小值优先）                                                                                                | `Plugin.java:237`                                                                                  |
| **编译期**加载：JDK ServiceLoader                                                                                               | `BrokerContextImpl.java:476`                                                                       |
| **运行时热插拔**：每插件独立 `URLClassLoader`，目录由 `SMART_MQTT_PLUGINS` 决定                                                 | `PluginRegistryImpl.java:35-48`、`:61`、`:145`                                                     |
| 事件总线 15+ 事件类型（含一次性事件 once）                                                                                      | `bus/EventType.java:33-100`、`:106-119`                                                            |
| 消息总线（发布/消费分离）                                                                                                       | `bus/MessageBus.java`、`MessageBusConsumer.java`                                                   |
| 异步事件（`AsyncEventObject`，认证等 IO 不阻塞）                                                                                | `bus/AsyncEventObject.java`                                                                        |
| Provider 扩展点：会话状态 / 订阅管理                                                                                            | `provider/Providers.java:18-55`、`SessionStateProvider.java:20-35`、`SubscribeProvider.java:40-56` |

**shark-mqtt（现状）**
- 钩子仅 4 个：`OnAccept` / `OnConnected` / `OnMessage` / `OnClose` -- `plugin/manager.go:15-20`
- HookContext 未携带 Topic/Payload（OnMessage 只有 ClientID）-- `plugin/manager.go:22-32`
- 仅 `Register`/`Unregister` 进程内注册，**无动态加载** -- `plugin/manager.go:67-96`
- 分发有超时与 panic 隔离（这点做得比 smart-mqtt 更硬） -- `plugin/manager.go:98-161`

**未完成清单**
- [ ] 插件生命周期抽象（版本/厂商/优先级/初始化/销毁/卸载回调）
- [ ] 事件总线（连接/断开/收发消息/会话/主题/订阅全生命周期事件，含一次性事件）
- [ ] 消息总线（可消费的发布流，替代单一 `OnMessage` 钩子）
- [ ] Provider 扩展点（可替换会话状态存储、订阅关系存储）
- [ ] 插件配置模型（每插件独立配置 + schema 元数据）
- [ ] 插件热插拔（动态加载/卸载，或至少运行时启停）
- [ ] 钩子上下文补全（OnMessage 需带 topic/payload/qos/retain）

### 2.2 HTTP 管理 API 与 Web 控制台

**smart-mqtt 端点清单（enterprise-plugin，9 个 Controller）**

| Controller                   | 端点                                                                                                                                                            | 证据                                                     |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------- |
| `ConnectionsController`      | `/api/connections`（分页+ClientID 模糊搜索）、`/api/disconnection`（强制断开）、`/internal/clients/report`                                                      | `:276`、`:301`、`:77`                                    |
| `TopicController`            | 订阅关系查询、Topic 统计                                                                                                                                        | `:49`、`:120`、`:132`                                    |
| `PluginManagerController`    | `/current`、`/:id/schema`、`/:id/config`、`/:id/config/save`、`/market`、`/list`、`/download`、`/uninstall`、`/enable`、`/:id/logs`(SSE)、`/disable`、`/upload` | `:72`...`:477`                                             |
| `PluginRepositoryController` | 插件市场仓库 `/`、`/:plugin/download`                                                                                                                           | `:46`、`:51`、`:85`                                      |
| `SystemController`           | 登录、用户增删改查、系统设置读写                                                                                                                                | `:49`...`:180`                                             |
| `MetricController`           | `/api/cluster/metricCodes`、`/metrics`(Prometheus)                                                                                                              | `:62`、`:197`、`:207`                                    |
| `LicenseController`          | License 导入 / 查询                                                                                                                                             | `:54`、`:146`、`:176`                                    |
| `VersionController`          | 版本检查                                                                                                                                                        | `:34`、`:66`                                             |
| `AiController`               | `/api/chat/completions`（SSE + MCP）                                                                                                                            | `:36`、`:57`                                             |
| 持久化                       | MyBatis + H2/H2-mem/MySQL，4 张表（subscriptions/connection/user_info/system_config）                                                                           | `resources/mybatis/ddl/schema.sql:1,14,38,51`            |
| 前端                         | Shadcn UI Web 控制台（Dashboard/连接/插件/用户/系统设置）                                                                                                       | `pages/src/content/docs/product/introduction.mdx:49-168` |

**shark-mqtt 现状**：`api/api.go` 仅 `/healthz:369`、`/readyz:373`、`/metrics:389`。无任何管理端点。

**未完成清单**（若定位为可嵌入库可整体裁剪）
- [ ] 连接管理 API（列表/分页/搜索/强制断开）
- [ ] 订阅与 Topic 统计 API
- [ ] 保留消息管理 API
- [ ] 服务端主动发布 / 订阅 API
- [ ] 运行时指标查询 API
- [ ] 用户与鉴权体系（Session 登录）
- [ ] Web 控制台 / Dashboard

### 2.3 集群（跨节点消息路由）

**smart-mqtt 实现**：核心/工作节点模型 + HTTP 短连投递 + SSE 长连订阅。
- `ClusterPlugin.java:15-41`（核心起 HTTP 服务，边缘走反向连接）
- `Coordinator.java:22,127-137,154,235`（Distributor/Receiver 双线程、ArrayBlockingQueue、丢弃策略）
- `Coordinator.java:195`（SSE `/cluster/subscribe/core|worker/:token`）；`:274,287`（`/cluster/put/core|worker`）
- `ClusterMessageStream.java`（二进制帧 `t:`topic / `p:`payload / `r:`retain）
- `VirtualMqttSession.java`（虚拟会话，跨节点投递的会话抽象）
- 高可用实测验证：3 Broker + HAProxy 拔节点业务不中断（`development/ha_cluster.mdx`）

**shark-mqtt 现状**：`.go` 0 命中；仅 `docs/architecture/CLUSTER-FEASIBILITY.md` 设计文档。已识别的硬约束（该文档自述）：
- `broker/broker.go:26` clientState 含 `conn net.Conn`（套接字不可迁移）
- `broker/broker.go:67` `connections map` 进程私有
- `broker/session.go:129-134` `Manager.sessions` 进程私有
- `broker/topic_tree.go` 每实例一份全量订阅树，无跨节点传播
- ~~`broker/broker.go:905-939` Stop() 立即关连接、无 drain、**不抑制遗嘱 -> 滚动重启会引发遗嘱风暴**~~
  **已修复**（commit `b1b011794`）：`Stop()` 先置 `shuttingDown` 再关连接，抑制遗嘱；并对 MQTT 5.0 连接发送
  `DISCONNECT 0x8B`。注意根因在 API 层顺序（`api.Broker.Stop()` 原先先 `srv.Stop()` 关 socket、后 `broker.Stop()`），
  仅改 broker 层是死代码。

**未完成清单**
- [ ] 节点发现（静态列表 / memberlist / etcd）
- [ ] 订阅关系 delta 广播 + anti-entropy
- [ ] 跨节点消息路由（含 retained 与共享订阅的集群语义）
- [ ] 全局唯一 msgID（当前为墙钟 + 进程内原子计数，无节点前缀）
- [x] ~~优雅停机 drain（滚动升级不触发遗嘱风暴）~~ -- 遗嘱风暴已消除（`b1b011794`）；跨节点 drain 仍随集群一并搁置
- [x] ~~**`deploy/k8s/` 文档修正**：`deployment.yaml` replicas/HPA 多副本模板必须标注"多副本 != 集群，会导致漏投/重复投/retained 分裂"~~
  **已完成**（commit `f229539dd`）：默认值改为 `replicas: 1` / Helm `replicaCount: 1` / `autoscaling.enabled: false`，
  `hpa.yaml` 从默认 kustomization 注释掉，并在 manifest、Helm 模板、NOTES.txt、两个 README 中落地"多副本 != 集群"告警

### 2.4 数据出口（Redis 桥接 / Kafka）

**smart-mqtt**
| 能力                                                                                                                                                | 证据                                                                  |
| --------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| Redis 桥接：消费消息总线 -> `ZADD {topic}` score=timestamp，多实例并行，编码 bytes/string/base64                                                     | `BridgeService.java:16,31-38,52-71`；`PayloadEncodeEnum`              |
| Kafka：**服务端实现 9 类 Kafka 协议 API**（ApiVersions/Metadata/InitProducerId/Produce/Fetch/ListOffsets/FindCoordinator/OffsetCommit/OffsetFetch） | `KafkaServer.java:66,106-131,145-385`                                 |
| 自研分段日志存储引擎：`PartitionLog`(TreeMap 分段/按时间与大小 cleanup/roll) / `LogSegment` / `TopicLog` / `ConsumerOffsetStore`                    | `PartitionLog.java:16,84,129-143,185,194`；`ConsumerOffsetStore.java` |
| 分区策略 round_robin / topic_hash                                                                                                                   | `PartitionStrategy.java`、`TopicLog.java`                             |

**shark-mqtt 现状**：`.go` 中 `bridge` / `kafka` 0 命中。`store/` 三个后端（memory/redis/badger）**只做持久化，不做出站桥接** -- `store/interfaces.go:9-38`。

**未完成清单**（按定位取舍）
- [ ] 消息出站抽象（Bridge/Exporter 接口 + 主题过滤器）
- [ ] Redis 出口（SortedSet 时序存储）
- [ ] 任意 HTTP/Webhook 出口
- [ ] Kafka 出口（或 Kafka 协议兼容存储层）

---

## 3. P1 - 功能缺失（9 项）

### 3.1 认证体系

| 能力                                                                                    | smart-mqtt                                                        | shark-mqtt                                                                         | 判定                 |
| --------------------------------------------------------------------------------------- | ----------------------------------------------------------------- | ---------------------------------------------------------------------------------- | -------------------- |
| 用户名/密码（配置内）                                                                   | `SimpleAuthPlugin.java:19,25-46`                                  | `StaticAuth`（`auth.go:52-251`，含 bcrypt）                                        | shark-mqtt **更优**  |
| 文件型账户                                                                              | 无                                                                | `FileAuth`（`auth_file.go:16-139`）                                                | shark-mqtt **独有**  |
| **Redis 认证器**（HMGET pwd_hash/salt/encoder，plain/md5/sha256）                       | `RedisAuthenticator.java`（`advanced-auth-plugin.mdx:101-118`）   | **缺失**                                                                           | **缺口**             |
| **HTTP/Webhook 认证器**（POST JSON -> 200/非 200）                                       | `HttpAuthenticator.java:58`                                       | **缺失**                                                                           | **缺口**             |
| **全异步认证链**（不占用工作线程，抗集中重连）                                          | `AdvancedAuthPlugin.java:104-112`（`thenCompose`）                | `ChainAuth` 为同步 fail-closed（`auth_chain.go:10-59`）                            | **缺口**             |
| **认证结果三态**（SUCCESS/FAILURE/**CONTINUE 降级**）+ `stopOnError` / `allowAnonymous` | `AdvancedAuthPlugin.java:176-181`                                 | 无 CONTINUE 语义                                                                   | **缺口**             |
| 增强认证（AUTH 报文）                                                                   | 仅属性常量（`MqttPropertyConstant.java:69`），Broker 无 AUTH 处理 | AUTH 报文 + 握手 + 会话内重认证（`broker.go:581-670,1145-1180`），**无内置认证器** | shark-mqtt **更优**  |
| JWT / LDAP / OAuth / PSK / X.509 身份映射                                               | 无                                                                | 无                                                                                 | 两者皆缺（非对齐项） |
| ACL                                                                                     | 订阅/发布 Provider 拦截                                           | `StaticAuth` 内建 ACL（`auth.go:265-275`）                                         | 相当                 |
| ACL 文件热重载                                                                          | 无                                                                | 无（`auth_file.go:49` 仅手动 LoadFile）                                            | 两者皆缺             |

**未完成清单**：[ ] Redis 认证器 ,  [ ] HTTP 认证器 ,  [ ] 异步认证链 + CONTINUE 降级 ,  [ ] 增强认证内置实现（可选）

### 3.2 运维与可观测

| 能力                              | smart-mqtt 证据                                                                                             | shark-mqtt 现状                                                       |
| --------------------------------- | ----------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| **连接防抖（Flapping 自动封禁）** | `FlappingDetector.java:35,67,88,120`（滑动窗口 + 阈值封禁 + 惰性解封）；`EnterprisePlugin.java:77-107` 装配 | 无（`protocol/constants.go:98` 仅 `ConnAckBanned=0x8A` 常量，无逻辑） |
| 指标项                            | `MqttMetricEnum` 全量 + `HistogramMetric`；`MetricController.java:67,117-122`                               | 12 项（`pkg/metrics/prometheus.go:67-138`）                           |
| 实时日志推送                      | 插件日志 SSE（`PluginManagerController.java:388`，changelog v1.5.3）                                        | 无                                                                    |
| 故障自愈                          | `Plugin.java:190` selfRescueTimer                                                                           | 无                                                                    |
| 智能推送模型（高负载自适应延迟）  | changelog v1.5.3                                                                                            | 无                                                                    |
| 版本检查/更新提示                 | `VersionController.java:34,66`                                                                              | 无                                                                    |
| 日志轮转                          | 有                                                                                                          | 无                                                                    |
| 结构化日志 / 健康探针             | 有                                                                                                          | **有**（`slog` + `/healthz` `/readyz`，`api.go:369-383`）             |
| 僵尸连接回收                      | 有                                                                                                          | **有且更强**（`tcp_keepalive_period` 可配，写失败即断）               |

**未完成清单**：[ ] 连接防抖闭环 ,  [ ] 日志轮转 ,  [ ] 指标项扩充 ,  [ ] 运行时日志 SSE ,  [ ] 版本检查

**本轮已加固（commit `ebe4aa52d`）**：
- 安全相关拒绝（认证失败 / 不支持的认证方法 / 会话归属冲突）原先**只计数不出日志**--默认 `info` 级别下
  运维看不到任何记录。现补 `warn` 级审计日志（clientID + username + remote，**不含口令**）。
- 指标字符串标签（`IncRejections` / `IncMessagesDropped` / `IncErrors` 的 `reason`、`component`）此前无
  约束，调用方一旦传入用户可控值（clientID / topic）即造成时间线爆炸。现由
  `broker/metrics_cardinality_test.go` 锁定为固定 allowlist token。
- `pkg/logger` 不再在 `init()` 里改标准库全局 logger（可嵌入库不应污染宿主进程），改由 `cmd/main.go` 设置。

### 3.3 传输与配置

| 能力                     | smart-mqtt                                                                                       | shark-mqtt                                                                                                              | 判定                  |
| ------------------------ | ------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------- | --------------------- |
| WebSocket                | websocket-plugin，独立端口，`Sec-WebSocket-Protocol: mqtt`（`WebSocketPlugin.java:37,45,50-64`） | **内建**（`broker/server.go:415-492`、`ws_conn.go`）                                                                    | shark-mqtt **更集成** |
| TLS                      | mqtts-plugin，PEM 证书独立端口（`TlsPlugin.java:24,43,46`）                                      | **内建** + **mTLS** + 版本/套件白名单（`config/config.go:183-221`）                                                     | shark-mqtt **更强**   |
| **多 listener / 多端口** | 插件各自 `addUsagePort()` 独立端口，天然多监听                                                   | **单 listener 结构**：`listener net.Listener` 单字段（`server.go:27`）；配置单 `ListenAddr`（`config/config.go:21-23`） | **缺口**              |
| 配置形态                 | 系统属性 + 环境变量（v1.4.0 废弃 YAML）+ 插件 `plugin.yaml`                                      | YAML + ENV + CLI（`config/loader.go:50-100`）                                                                           | 相当                  |
| **配置热重载**           | 无（文档明示"修改配置后需重启"）                                                                 | 无（`cmd/main.go:96-101` 仅 SIGINT/SIGTERM）                                                                            | 两者皆缺              |
| 配置 schema（UI 元数据） | `Item.java:42-103`（string/int/password/textarea/object/array/enum/switch）                      | 无                                                                                                                      | **缺口**              |
| Proxy Protocol           | 无                                                                                               | 无                                                                                                                      | 两者皆缺              |

**未完成清单**：[ ] listener 数组化（多端口/多协议） ,  [ ] 插件化端口声明 ,  [ ] 插件配置 schema

### 3.4 MQTT 5.0 语义细节（**注：多数为"两者皆缺"，非对齐项**）

| 属性                                                              | shark-mqtt                                                                                                          | smart-mqtt                                                             | 性质                                        |
| ----------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------- | ------------------------------------------- |
| User Property 转发                                                | 协议层可编解码（`properties.go:32,301-315`），Broker 投递时不转发（`broker.go:1929-1960` 仅带 SubID+MessageExpiry） | **同样不转发**（broker 内无 getU*Property 引用）                       | 两者皆缺                                    |
| Content Type / Response Topic / Correlation Data / Payload Format | 可编解码，Broker 不处理                                                                                             | 同样不处理                                                             | 两者皆缺                                    |
| Retain As Published                                               | ~~选项已解析存储（`subscribe.go:66`、`session.go:318`），投递未生效（`broker.go:1929-1937`）~~                      | -                                                                      | ~~真缺口~~ **已修复**（commit `be8021a91`） |
| Maximum QoS 通告                                                  | CONNACK 未通告（`broker.go:2224-2260`）                                                                             | -                                                                      | ~~真缺口~~ **非缺口**（误判，见 3.4.1）     |
| Request Problem Information                                       | 仅解析，未裁剪 Reason String                                                                                        | -                                                                      | ~~真缺口~~ **非缺口**（误判，见 3.4.2）     |
| 共享订阅策略                                                      | 仅 round-robin，无 `$queue`（`topic_tree.go:228-267,304-415`）                                                      | 仅 `$share` + round-robin（`SharedOrderedMessageDeliver.java:29-108`） | 两者皆有限                                  |

#### 3.4.1 Maximum QoS 通告 -- 误判，实为合规

初判"CONNACK 未通告 Maximum QoS"为缺口，**该结论错误**。MQTT 5.0 Section 3.2.2.3.4 明确规定：
Maximum QoS 只允许取值 0 或 1，**省略即表示支持 QoS 2**。shark-mqtt 支持 QoS 2，因此"省略"是
唯一正确的编码；若强行通告 2 反而违反属性取值范围。

反证（仓库内已有回归用例）：`broker/broker_test.go:207`
`TestBroker_ConnAckPropertiesOmitMaximumQoSWhenQoS2Supported` 断言 `props.MaximumQoS == nil`，
注释亦写明 "omission means QoS 2 supported"。此行为是**有意为之**，非遗漏。

#### 3.4.2 Request Problem Information -- 误判，实为天然合规

初判"仅解析、未裁剪 Reason String"为缺口，**该结论同样错误**。MQTT 5.0 Section 3.1.2.11.7 要求：
客户端将 Request Problem Information 置 0 时，服务端**不得**发送 Reason String / User Property。

复核生产代码：`broker/` 中 `ReasonString`、`UserProperties` **零引用**（仅在 `protocol/properties.go`
的编解码表中存在），即 Broker 在任何路径下都不会主动发出这两类属性。因此"置 0 时不得发送"这一
约束**天然满足**，无需裁剪逻辑，不构成缺口。

> 结论修正口径：判断某属性是否"未完成"，必须先确认**当前实现是否已经满足规范**，而不能仅因
> "字段被解析但没有后续处理代码"就判定为缺口。上述两条均属此类误判。

---

## 4. P2 - 次要项

| # | 缺口                                          | smart-mqtt 证据                                                       | shark-mqtt                                                       |
| - | --------------------------------------------- | --------------------------------------------------------------------- | ---------------------------------------------------------------- |
| 1 | 插件市场 + 本地 JAR 上传                      | `PluginManagerController.java:186,238,460,477`                        | 无                                                               |
| 2 | 插件运行时启停 / 配置热改                     | `PluginManagerController.java:72,143,402`                             | 无                                                               |
| 3 | Broker 节点列表（集群视图）                   | `BrokerNodeDO`、`BrokerNodeTO`                                        | 无                                                               |
| 4 | AI Agent / MCP 对话接口                       | `AiController.java:36,57`                                             | 无                                                               |
| 5 | License 授权与连接数上限                      | `LicenseController.java:74-127`                                       | **不适用**（MIT）                                                |
| 6 | 内置压测插件（控制台启停、可压第三方 Broker） | bench-plugin（`BenchPlugin.java:42,149`）；`bench-plugin.mdx:207-315` | 仅 `tests/bench`（离线跑）                                       |
| 7 | Maven 插件（`mvn` 一键起 Broker）             | `smart-mqtt-maven-plugin/RunMojo.java`                                | 无（`cmd/main.go` + Makefile）                                   |
| 8 | 客户端自动重连 / subAck 回调                  | `MqttClient.java:546-547`（`maxReconnectDelay`）、`:300-314`          | `client/client.go` 有 KeepAlive 与读超时；**需核对自动重连实现** |

---

## 5. Shark-MQTT 已超越 Smart-MQTT 的点（反向清单）

| 能力                                      | 证据                                                                                       | 说明                                                            |
| ----------------------------------------- | ------------------------------------------------------------------------------------------ | --------------------------------------------------------------- |
| 协议适配层抽象（Adapter/Spec/Registry）   | `protocol/spec.go:40-92`、`adapter.go`、`mqtt_adapter.go`、`registry.go`、`broker/conn.go` | smart-mqtt 无此抽象层，多协议扩展需改核心                       |
| MQTT 3.1 完整语义                         | `spec.go:39-47`、`connect.go:148-205`、`broker.go:1509-1514,2193-2206` + 专项集成测试      | smart-mqtt 亦有 3.1，但 shark-mqtt 有独立 Spec 能力表与回归用例 |
| 增强认证框架（AUTH 报文 + 握手 + 重认证） | `broker.go:262-295,581-670,1145-1180`                                                      | smart-mqtt 仅有属性常量                                         |
| 协议 fuzz（840 万+ 次执行无崩溃）         | `protocol/fuzz_test.go`                                                                    | smart-mqtt 无                                                   |
| 单二进制 / 零外部依赖                     | `go.mod`（仅 badger/redis 可选构建标签）                                                   | smart-mqtt 需 JVM                                               |
| 批量写队列有界 + 分档缓冲池               | `broker/write_queue_test.go`、`pkg/bufferpool/pool.go`                                     | -                                                               |
| 存储后端 fail-fast 校验                   | `store/errors.go`、`store/interfaces.go`                                                   | -                                                               |

---

## 6. 建议补齐顺序（若决定向平台化演进）

```
阶段 0（接口地基，低风险）
  T01 listener 数组化 + 插件化端口
  T02 插件生命周期抽象（版本/优先级/初始化/销毁 + 配置模型）
  T03 事件总线（复用现有 OnAccept/OnConnected/OnMessage/OnClose 语义，扩展为全事件）
  T04 k8s 模板"多副本 != 集群"告警补丁   <- 已完成（f229539dd）

阶段 1（管理面）
  T05 HTTP 管理 API（只读：连接/订阅/主题统计）
  T06 连接管理（强制断开）+ 运行时可观测端点
  T07 连接防抖（Flapping）

阶段 2（出口与集群）
  T08 消息出站抽象 + Redis/Webhook 出口
  T09 集群：节点发现 + 订阅 delta 广播 + 粘性路由
  T10 优雅停机 drain（先行，独立于集群，解决遗嘱风暴）   <- 遗嘱风暴部分已完成（b1b011794）
```

---

## 附录 A - 证据索引速查

**smart-mqtt**
- 插件规范：`smart-mqtt-plugin-spec/src/main/java/tech/smartboot/mqtt/plugin/spec/`
  `Plugin.java` ,  `BrokerContext.java` ,  `PluginRegistryImpl.java:35-48,61,145` ,  `bus/EventType.java:33-100` ,  `bus/AsyncEventObject.java` ,  `provider/Providers.java:18-55` ,  `schema/Item.java:42-103`
- Broker 核心：`smart-mqtt-broker/src/main/java/tech/smartboot/mqtt/broker/`
  `BrokerContextImpl.java:275-322,476` ,  `processor/ConnectProcessor.java:113-197` ,  `processor/PublishProcessor.java:33-100` ,  `topic/SharedOrderedMessageDeliver.java:29-108` ,  `bus/event/KeepAliveMonitorSubscriber.java:36-63`
- 协议层：`smart-mqtt-common/src/main/java/tech/smartboot/mqtt/common/`
  `enums/MqttVersion.java:14` ,  `enums/MqttMessageType.java:18` ,  `util/MqttPropertyConstant.java:21-153` ,  `util/TopicByteTree.java:42,83,229` ,  `InflightQueue.java:46-237`
- 插件：`plugins/`
  `enterprise-plugin/.../openapi/controller/`（9 个）,  `enterprise-plugin/.../flapping/FlappingDetector.java` ,  `cluster-plugin/.../Coordinator.java:22,195,274` ,  `kafka-storage-plugin/.../kafka/server/KafkaServer.java:106-385` ,  `kafka-storage-plugin/.../store/PartitionLog.java:84,129-143` ,  `advanced-auth-plugin/.../AdvancedAuthPlugin.java:104-112,176-181` ,  `redis-bridge-plugin/.../BridgeService.java:31-38`

**shark-mqtt**
- 协议：`protocol/spec.go:40-92` ,  `protocol/constants.go:10-27,67-71` ,  `protocol/properties.go:11-74,119-629` ,  `protocol/topic.go:26-102` ,  `protocol/packets.go:20-150`
- Broker：`broker/broker.go:26,67,179-568,905-939,1929-1960,2224-2260` ,  `broker/session.go:129-134,178-237,512-679` ,  `broker/qos_engine.go:54-109,318-414` ,  `broker/will_handler.go:19,66-157` ,  `broker/topic_tree.go:228-267,304-415` ,  `broker/server.go:27,251-307,415-492`
- 扩展/配置：`plugin/manager.go:15-20,22-32,67-161` ,  `config/config.go:19-83` ,  `config/loader.go:50-100` ,  `cmd/main.go:96-101`
- 存储/指标：`store/interfaces.go:9-38` ,  `pkg/metrics/prometheus.go:67-138`
- 既有分析：`docs/reports/EMQX-GAP-ANALYSIS-260910.md` ,  `docs/architecture/CLUSTER-FEASIBILITY.md` ,  `docs/architecture/PROTOCOL-ADAPTER-DESIGN.md`

---

*生成时间：2026-10-12 ,  对比基线：smart-mqtt v1.8.0 代码树 / shark-mqtt HEAD*
*复核与修订：2026-10-12 第二轮 -- 修正 3.4 节两处误判（Maximum QoS、Request Problem Information），
并标注本轮已修复项（RAP `be8021a91`、遗嘱风暴 `b1b011794`、k8s 多副本告警 `f229539dd`、
可观测加固 `ebe4aa52d`）。被评估的 shark-mqtt 基线推进到 `ebe4aa52d`。*
