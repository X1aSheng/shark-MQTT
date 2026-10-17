# Shark-MQTT 未完成细节清单（对比 smart-mqtt，2026-10-14 21:25 重做）

> **分析对象**：`G:/smart-mqtt`（Java / AGPL-3.0，276 个 `.java` / 28,465 行，非 git 仓库的静态快照）
> **被评估方**：`G:/shark-mqtt`（Go / MIT，HEAD `2395d63e1`）
> **上一版**：`SMART-MQTT-GAP-ANALYSIS-261014.md`（基线 `22c16b83b`）<- `...-261012.md`（基线 `ebe4aa52d`）
> **口径**：以**代码为准**，不以 README / 文档声明为准；凡结论均给出 `文件:行号`。
> 复核某属性是否"未完成"时，必须先确认当前实现**是否已经满足规范**，不能仅因"字段被解析但
> 没有后续处理代码"就判为缺口 -- 这个口径已经栽过 3 次，本版第 3 节又逮到第 4 次（自己）。
>
> **结论性质**：两者同属 MQTT Broker 品类，但 smart-mqtt 的终点是**企业级平台**
> （插件市场 + 控制台 + 集群 + 数据出口），shark-mqtt 的终点是**单机可嵌入 Broker 库**。
> 因此清单既列功能缺口，也标注"是否属 shark-mqtt 的既定定位"。

---

## 0. TL;DR

| 维度               | 结论                                                                                                                                                                                             |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **规模**           | shark-mqtt：168 个 `.go`（67 非测试 / 101 测试），非测试 **16,567** 行、测试 **27,868** 行；对照 **461 单测 / 159 集成 / 61 基准**                                                               |
| **MQTT 协议内核**  | 继续**反超**：本轮又补 5 处规范边界（属性归属校验对称化、被拒报文不越界消费流、DUP 位、拆链回执原因码、CONNECT 失败回 CONNACK）                                                                  |
| **平台化能力**     | 4 项 P0 结构性缺口（插件体系 / 管理面 / 集群 / 数据出口）**本轮零推进**                                                                                                                          |
| **未完成项总数**   | **26 项** = 上一版 21 项（P0 4 / P1 9 / P2 8，逐项复核后**全部仍开放**）+ 本轮新发现 **5 项协议细节缺口**（Section 2.4，其中 2 项是 MUST 违反）；本轮**关闭 1 项**（OnMessage 钩子上下文，`33c728a0e`） |
| **最危险的一项**   | 不变：`deploy/k8s/` 多副本 != 集群                                                                                                                                                                |
| **本轮新发现**     | 5 项协议细节缺口全部是 **MUST 级**或规范明确描述的语义缺失，见 2.4 -- 这是本版相对上一版最实质的增量                                                                                             |
| **与上一版的差别** | 上一版 Section 3.1 推荐的"最高性价比项 T03b"**已经完成**（`33c728a0e`），上一版未记录；另更正 1 处 Java 证据路径、1 处上一版记忆的错误定位                                                              |

**一句话**：`22c16b83b -> 2395d63e1` 这 20 个提交**没有一项**落在平台化缺口上，全部落在协议内核
正确性与工程债；同时本轮由"共同盘点两侧代码"发现了 5 项此前从未被记录的**协议细节缺口**
（客户端收包上限未约束出站、Subscription Identifier 未按 MUST 合并、Will 属性丢失、
Will Delay 未与会话结束联动、Topic Alias 非法值未按 Protocol Error 断开）。

---

## 1. 本轮增量（`22c16b83b..2395d63e1`，20 个提交）

| 类别                       | 提交数 | 提交                                                                                                                                                                                                                                                                                                                                               |
| -------------------------- | ------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **A. 协议合规边界**        | 8      | `1e3f7b67e`（属性归属校验补编码端）`e96611613`（Table 2-4 属性所属报文类型 + Server Reference）`2991ef451`（重复属性按报文类型拒绝 + Protocol Error 哨兵）`b4075f039`（被拒报文不得越界消费流）`3a784d983`（DUP 位校验 + Encode/Decode 对称）`6b5cbf32e`（client 不得把对端拒绝当成功）`bc3d94268`（client 入站 PUBREC flags）`df7791891` 之前系列 |
| **B. 拆链回执（Section 4.13.1）** | 3      | `71018449e`（拆链前确定性发 DISCONNECT）`75501e0e2`（解码失败先发 DISCONNECT）`f477c6b40`（CONNECT 失败回 CONNACK、keepalive 补 0x8D）                                                                                                                                                                                                             |
| **C. 测试确定性 / CI**     | 1      | `d7d48f812`（保留消息用例 QoS 1 + PUBACK 串行化，修 CI run `38531226449`）                                                                                                                                                                                                                                                                         |
| **D. 扩展点**              | 1      | `33c728a0e`（**OnMessage 钩子上下文补全** -- 上一版 T03b）                                                                                                                                                                                                                                                                                         |
| **E. 文档**                | 5      | `f8569e6bc` `0e44bbb60` `99b4c4c4c` `438c38493` `fde7b4fc4` `f7cc01cea` `2395d63e1`（计数与代码规模刷新、对比报告重排）                                                                                                                                                                                                                            |

### 1.1 D 类：本版唯一被关闭的缺口

`33c728a0e` 修掉了上一版 Section 3.1 里被标为"整张表里性价比最高的一项"（T03b）：`OnMessage`
钩子的分发点此前只填 `ClientID`，`Topic`/`Payload`/`QoS`/`Retain` 全部为空。

| 证据     | 内容                                                                                    |
| -------- | --------------------------------------------------------------------------------------- |
| 分发点   | `broker/broker.go:1352-1361`（`if b.pluginMgr != nil` 守卫，无插件路径零分配）          |
| 填充契约 | `plugin/manager.go:34-43` 的钩子-字段对照表（文档化，明示"别因为字段存在就假定被填充"） |
| 触发语义 | `OnMessage` 对**每个解码报文**触发（非仅 PUBLISH），命中等 PUBLISH 才填那四个字段       |
| 回归     | `broker/plugin_hook_context_test.go`                                                    |

> 这是"结构体早就齐备、缺的是调用点填充"的第二例（第一例见上一版 Section 5 更正 3）。
> 上一版报告未记录这一关闭，**本版据此更正**。

---

## 2. 未完成清单

状态口径：`未动` = 自上一版基线以来无任何代码变化；`不适用` = 与 MIT 定位冲突，不建议补。

### 2.1 P0 结构性缺口（4 项）

| #   | 缺口                                  | smart-mqtt 证据（路径已复核）                                                                                                  | shark-mqtt 现状（HEAD）                                                      | 状态 | 属既定定位         |
| --- | ------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------- | ---- | ------------------ |
| 1   | 插件生命周期与热插拔                  | `plugin/spec/Plugin.java:67,75,107,114,137,190,201,212,224,237,241,296,344,363`；`PluginRegistryImpl.java:35-43,57-71,111-160` | 仅 4 钩子（`plugin/manager.go:15-25`）+ `Register`/`Unregister`（`:88-117`） | 未动 | 否（应补）         |
| 2   | 事件总线 / 消息总线 / Provider 扩展点 | `plugin/spec/bus/EventType.java:33-100`（15 类事件）；`provider/Providers.java:19,36,43,52`                                    | 无（仅同步钩子）                                                             | 未动 | 否（应补）         |
| 3   | HTTP 管理 API + Web 控制台            | `enterprise-plugin/.../openapi/controller/` 9 个 Controller                                                                    | 仅 `/healthz` `api/api.go:410`、`/readyz` `:414`、`/metrics` `:432`          | 未动 | 部分（可选）       |
| 4   | 集群（跨节点消息路由）                | `cluster-plugin/.../cluster/Coordinator.java:22-56,63-111,195-219`                                                             | `cluster` 生产代码 **0 命中**                                                | 未动 | 是（已明确暂不做） |

#### 2.1.1 插件体系逐项差异（缺口 1 细分）

**smart-mqtt `Plugin.java` 生命周期方法全集**（逐条已复核行号）：
`pluginName()` `:67` ,  `install()` `:75` ,  `initPlugin()` `:107` ,  `uninstall()` `:114` , 
`subscribe(EventType,...)` `:137` ,  `log()` `:155` ,  `subLog()` `:159` ,  `consumer(MessageBusConsumer)` `:176` , 
`selfRescueTimer()` `:190` ,  `timer()` `:201` ,  `addPlugin()` `:212` ,  `destroyPlugin()` `:224` , 
`order()` `:237` ,  `storage()` `:241` ,  `getVersion()`/`getVendor()` `:252`/`:254` , 
`loadPluginConfig(Class)` `:281` ,  `addUsagePort(int,String)` `:296` ,  `schema()` `:363`。

**未完成子项**：

- [ ] 插件生命周期抽象（版本 / 厂商 / 优先级 `order()` / 初始化 / 销毁 / 卸载回调）
- [ ] 事件总线（连接 / 断开 / 收发消息 / 会话 / 主题 / 订阅全生命周期）
- [ ] 消息总线（可消费的发布流，替代单一 `OnMessage` 钩子）
- [ ] Provider 扩展点（可替换会话状态存储、订阅关系存储）
- [ ] 每插件独立配置模型 + `plugin.yaml` 加载
- [ ] 配置 UI 元数据 schema（`schema/Item.java:42,46,55,64,73,80,87,96` 共 8 种控件）
- [ ] 插件热插拔（`URLClassLoader` 目录扫描，环境变量 `SMART_MQTT_PLUGINS`，`PluginRegistryImpl.java:35-43,57-71`）
- [x] ~~钩子上下文补全（`OnMessage` 带 Topic/Payload/QoS/Retain）~~ -- **`33c728a0e` 已完成**

#### 2.1.2 集群（缺口 4 细分）

| 子项                                   | 状态                      |
| -------------------------------------- | ------------------------- |
| 节点发现（注册中心 / 静态列表 + 探活） | 未动                      |
| 订阅 delta 广播                        | 未动                      |
| 跨节点消息路由                         | 未动                      |
| 全局唯一 msgID                         | 未动                      |
| 遗嘱风暴（滚动重启不触发遗嘱洪泛）     | **已修复**（`b1b011794`） |
| `DISCONNECT 0x8B` 通知 MQTT 5 客户端   | **已修复**（`b1b011794`） |
| `deploy/k8s/` 多副本默认值与告警       | **已修复**（`f229539dd`） |
| **优雅停机 drain**                     | **仍开放**                |

> smart-mqtt 的集群是 **core/worker + HTTP + SSE**：`Coordinator.java:98-110` HTTP 探活、
> `:274,287` POST 推消息、`ClusterController.java:158-208` SSE 回推；节点来自 `clusters`
> 静态列表（`PluginConfig.java:23,49-55`），**无注册中心、非自动发现**。
> 且集群内消息一律以 `MqttQoS.AT_MOST_ONCE` 重建（`ClusterController.java:126`、`Coordinator.java:210`），
> **QoS 与 MQTT 5 属性在跨节点路径上全部丢失**。

### 2.2 P1 缺口（9 项）

| #   | 缺口                                 | smart-mqtt 证据（路径已复核）                                                                                                                   | shark-mqtt 现状（HEAD）                                                                                                     | 状态 | 属既定定位       |
| --- | ------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------- | ---- | ---------------- |
| 5   | 数据出口：Redis 桥接                 | `redis-bridge-plugin/.../bridge/redis/BridgeService.java:28-50,52-75`                                                                           | `bridge` 0 命中；Redis 仅作 `store` 后端                                                                                    | 未动 | 是（定位为不做） |
| 6   | 数据出口：Kafka 协议 + 存储引擎      | `kafka-storage-plugin/.../kafka/storage/kafka/server/KafkaServer.java:103-445`；`store/PartitionLog.java`、`LogSegment.java`                    | `kafka` 0 命中                                                                                                              | 未动 | 是               |
| 7   | 认证：Redis / HTTP 认证器            | `advanced-auth-plugin/.../auth/advanced/AdvancedAuthPlugin.java:104-135`；`provider/HttpAuthenticator.java`、`provider/RedisAuthenticator.java` | 6 种认证器（`broker/auth.go:52` `:258` `:281`、`auth_chain.go:13`、`auth_file.go:16`、`auth_noop.go:6`），**无 Redis/HTTP** | 未动 | 部分             |
| 8   | 连接防抖（Flapping 自动封禁）        | `plugin/flapping/FlappingDetector.java:35,67-81,88-114,120-146`；参数 `60s/5 次/封禁 300s`（`PluginConfig$FlappingConfig`）                     | 仅常量 `ConnAckBanned = 0x8A`（`protocol/constants.go:98`），**全仓无第二处引用**                                           | 未动 | 否（应补）       |
| 9   | 多 listener 数组 / 插件化端口        | `Plugin.java:296` `addUsagePort`；五个插件各起端口：MQTTS 8883 / WS 8083 / Cluster 8884 / Kafka 9092 / HTTP 18083                               | **单** `listener net.Listener`（`broker/server.go:27`）+ WS/WSS 数组（`:40`）                                               | 未动 | 否（应补）       |
| 10  | 配置 UI 元数据（Schema）             | `plugin/spec/schema/Item.java:42-96`；`Plugin.java:363`                                                                                         | `config/` 内无 schema（仅 struct tag + `Validate()` `config/config.go:110-169`）                                            | 未动 | 否               |
| 11  | 插件市场 / 本地 JAR 上传             | `PluginManagerController.java:238,270,306,477`；`PluginRepositoryController.java:51,85`                                                         | 无                                                                                                                          | 未动 | 否（可选）       |
| 12  | 插件运行时启停 / 配置热改 / 日志 SSE | `PluginManagerController.java:160,186,388,402,419,460`                                                                                          | 无；broker 配置亦无热重载（`cmd/main.go:103-107` 仅处理 SIGINT/SIGTERM）                                                    | 未动 | 否               |
| 14  | 插件自愈定时器 / 柔性注销            | `Plugin.java:190,201,224`（`HashedWheelTimer` `:192`）                                                                                          | 无                                                                                                                          | 未动 | 否               |

> **缺口 7 的缓解说明**（避免误判）：shark-mqtt 提供 `EnhancedAuthenticator` 接口
> （`broker/auth.go:37-44`，Method/Initial/Continue 三阶段）与 `ChainAuth`（`auth_chain.go:13-59`），
> 宿主可以据此自行实现 Redis/HTTP 认证器；缺的是**内置实现**，不是**扩展能力**。
> 另注：smart-mqtt 的 `Authenticator.java:26-28` 声明了 `mysql` 常量但**无实现类**，
> 实际可装配的只有 `http` 与 `redis`（`AdvancedAuthPlugin.java:59-67`）。

### 2.3 P2 平台 / 运维缺口（8 项）

| #   | 缺口                                | smart-mqtt 证据                                                                                                 | shark-mqtt 现状                                               | 状态       | 属既定定位                  |
| --- | ----------------------------------- | --------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------- | ---------- | --------------------------- |
| 13  | 智能推送模型（高负载自适应延迟）    | changelog v1.5.3                                                                                                | 无                                                            | 未动       | 否（可选）                  |
| 15  | 版本检查 / 更新提示                 | `VersionController.java:40-73`                                                                                  | 无                                                            | 未动       | 否                          |
| 16  | 指标项丰富度                        | `MetricController.java:88-195,207-280`；`MqttMetricEnum.java`；JVM/CPU gauge `:233-256`；`node_info` `:263-280` | 12 项（`pkg/metrics/prometheus.go:67-138`）**已含 Histogram** | 未动       | 部分（JVM gauge 不适用 Go） |
| 17  | AI Agent / MCP 接口                 | `AiController.java:57`；`MetricController.java:61` `@McpEndpoint`                                               | 无                                                            | 未动       | **不适用**                  |
| 18  | License 授权与连接数限制            | `LicenseController.java:73-84,103-131`                                                                          | 无                                                            | **不适用** | **不适用**（MIT）           |
| 19  | 内置压测插件（控制台启停 / 压三方） | `bench-plugin/.../BenchPlugin.java:42-317`                                                                      | 仅 `tests/bench`（离线）                                      | 未动       | 部分                        |
| 20  | 连接管理（列表查询 / 强制断开）     | `ConnectionsController.java:289,301`                                                                            | 无                                                            | 未动       | 否                          |
| 21  | Topic 统计 / 订阅关系查询           | `TopicController.java:120,132`                                                                                  | 无                                                            | 未动       | 否                          |

### 2.4 协议细节缺口（5 项 -- 本轮新发现）*

这 5 项**上一版 21 项清单里一项都没有**，是本次"两侧同时做代码级盘点、再做差集"才浮出来的。
每项均已落到实际调用点，并比对规范原文。**前两项是 MUST 违反，后三项是规范明确描述的语义缺失。**

| #   | 缺口                                                      | 规范依据（原文）                                                                                                                                                                                                                                                 | shark-mqtt 现状                                                                                                                                                                                                                                                                                                                                     | 级别     |
| --- | --------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- |
| 22  | **客户端声明的 Maximum Packet Size 未约束出站**           | Section 3.1.2.11.4：「The Server **MUST NOT** send packets exceeding Maximum Packet Size to the Client **[MQTT-3.1.2-24]**」「Where a Packet is too large to send, the Server **MUST discard it without sending it** **[MQTT-3.1.2-25]**」                              | 属性在 `protocol/properties.go:495-503` 被解析（`props.MaximumPacketSize`），但 `broker/` 全包**只有 1 处** `MaximumPacketSize` 引用，即 `broker.go:2641` -- 那是**服务端自己**在 CONNACK 里声明的值。客户端值**从不被消费**                                                                                                                        | **MUST** |
| 23  | **重叠订阅的 Subscription Identifier 未按 MUST 全部带回** | Section 3.3.4：「If the Server sends **a single copy** of the message it **MUST** include in the PUBLISH packet the Subscription Identifiers for **all matching subscriptions** which have a Subscription Identifier **[MQTT-3.3.4-4]**」                               | `Match` 按 ClientID 去重（`broker/topic_tree.go:104-117`，注释自述"single delivery at the maximum QoS"）=> 服务端**发单份**，因此适用 [MQTT-3.3.4-4]；但 `SubscriptionIdentifier` 是**单值** `*uint32`（`broker/session.go:98`），`matchSubscriptions` 只取"最高 QoS 的那个匹配"（`session.go:398-410`），`doDeliver` 只传这一个（`broker.go:2256`） | **MUST** |
| 24  | **Will 的非 Delay 属性全部丢失**                          | Section 3.1.3.2 / Section 3.3.2.3：Will Properties 中除 Will Delay Interval 外的属性即遗嘱 PUBLISH 的属性，须按 Section 3.3.2.3 转发                                                                                                                                                  | `broker.go:574-575` 是 `WillProperties` 的**唯一**消费点，只取 `WillDelayInterval`；`publishWill` 签名 `(username, topic, payload, qos, retain)`（`broker.go:3001`）、`publishWillMessage`（`will_handler.go:159-161`）**不传属性** => PayloadFormat / ContentType / ResponseTopic / CorrelationData / UserProperty / MessageExpiry 全丢             | 语义     |
| 25  | **Will Delay 未与会话结束联动**                           | Section 3.1.3.2 Will Delay Interval：「The Server delays publishing the Client's Will Message until the Will Delay Interval has passed **or the Session ends, whichever happens first**」                                                                               | `will_handler.go:108-125` 只做 `select { case <-time.After(will.Delay): ... case <-ctx.Done(): }`，`ctx` 仅由 `CancelWill`（重连）取消，**不感知 Session Expiry**；`broker.go:579-583` 只把 delay 封顶到 `opts.maxWillDelay`（默认 24h）                                                                                                              | 语义     |
| 26  | **Topic Alias 非法值未按 Protocol Error 断开**            | Section 3.3.2.3.4：「A sender **MUST NOT** send a PUBLISH packet containing a Topic Alias which has the value 0 **[MQTT-3.3.2-8]**」；值超过对端声明的 Topic Alias Maximum 同为 Protocol Error（0x94 *Topic Alias invalid*），Section 4.13 要求关闭连接并 SHOULD 发 DISCONNECT | `broker.go:1497-1504`：`alias == 0` -> 丢弃并回 ack（QoS 1 时用 0x97 Quota Exceeded），**未断开**；`broker.go:1517-1522`：`RegisterTopicAlias` 越界返回错误，但只写 Debug 日志后**继续投递**。对照：alias 无映射时**已经**正确走 `closeProtocolViolation`（`broker.go:1510-1516`）                                                                   | 语义     |

**缺口 22 的连锁影响**：同一属性契约还牵连 Section 3.2.2.3.9 / Section 3.2.2.3.10 --
「The Server MUST NOT send **[Reason String / User Property]** if it would increase the size of the
CONNACK packet beyond the Maximum Packet Size specified by the Client **[MQTT-3.2.2-19/20]**」。
shark-mqtt 恰好**从不发送** Reason String（`broker/` 内 `ReasonString` **0 命中**），
该两条因此天然满足；但这正说明"缺口 22"不是孤立的形式问题，而是出站限包这一整块能力的缺失。

**缺口 23 的复现路径**：客户端订阅 `a/+`（SubscriptionIdentifier=1）与 `a/b`（=2），
向 `a/b` 发布 -> 客户端只收到**一份** PUBLISH，且只带 identifier 1 或 2 中的一个（取决于会话内 QoS 比较结果）。

---

## 3. 更正记录（4 处）

### 3.1 上一版失效项：T03b 已关闭

| 项                         | 上一版写法                                                    | 实际                                               |
| -------------------------- | ------------------------------------------------------------- | -------------------------------------------------- |
| Section 3.1 "未完成清单" 最后一条 | `[ ] 钩子上下文补全：OnMessage 需带 Topic/Payload/QoS/Retain` | **已完成**（`33c728a0e`，2026-10-14 08:44）        |
| Section 7 建议顺序 T03b           | `T03b 钩子分发点补全（...） <- 最低成本，单点改动`               | 同上                                               |
| Section 3.1 表格                  | `OnMessage` 分发点 `broker/broker.go:1241` 只填 ClientID      | 现为 `:1352-1361`，命中等 PUBLISH 时四个字段均填充 |

**根因**：`33c728a0e` 的提交时间（08:44）**晚于**上一版报告基线 `22c16b83b`（10-13 16:00），
但报告未被回写。已用 `git merge-base --is-ancestor 33c728a0e 22c16b83b` 判定为"否"确认。

### 3.2 证据路径更正：enterprise-plugin 没有 `PublishProcessor`

上一版 Section 4.1 与附录 B 把"enterprise-plugin 的 `PublishProcessor` 不转发属性"写得像是该插件内的类。
实际：

| 类                                                              | 真实路径                                                                                     |
| --------------------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| `PublishProcessor.java`（broker 侧，处理入站 PUBLISH 并回 ack） | `smart-mqtt-broker/src/main/java/tech/smartboot/mqtt/broker/processor/PublishProcessor.java` |
| `PublishProcessor.java`（内置客户端侧）                         | `smart-mqtt-client/src/main/java/tech/smartboot/mqtt/client/processor/PublishProcessor.java` |
| enterprise-plugin 下的 `*Processor`                             | **不存在**（该插件无任何 `*Processor`，`PublishProperties` 亦零引用）                        |

结论不变（出站属性确实被丢弃），但**归因必须挂在 broker 的 5 个构造点上**，不能挂到插件上。

### 3.3 本轮自我更正：Assigned Client Identifier **已实现**，不是缺口

我一度据 `grep -rn "AssignedClientID" --include='*.go' .` 的输出判定"broker 侧零消费"，
差点把它写成第 6 项新缺口。复核后被推翻：

| 环节       | 证据                                                                                                         |
| ---------- | ------------------------------------------------------------------------------------------------------------ |
| 生成       | `broker/broker.go:380-384`（`fmt.Sprintf("shark-%x", time.Now().UnixNano())`，注释引 Section 3.1.3.6）              |
| 落到会话   | `broker/broker.go:449-450`（`sess.AssignedClientID = assignedClientID`），字段定义 `broker/session.go:53-55` |
| 回 CONNACK | `broker/broker.go:2672-2674`（`if sess.AssignedClientID != "" { props.AssignedClientID = ... }`）              |
| 回归       | `broker/broker_test.go:605-623`                                                                              |

=> 满足 Section 3.2.2.3.7 **「If the Client connects using a zero length Client Identifier, the Server
**MUST** respond with a CONNACK containing an Assigned Client Identifier **[MQTT-3.2.2-16]**」**。

**误判根因**：那条 grep 用了 `| head`，前 10 行全被 `protocol/properties.go` 的编解码命中占满，
`broker/` 的两条真命中被截掉。**教训：断言"零命中"时绝不能用 `head` 截断输出。**

### 3.4 复核方法（本次实际执行的）

1. 两侧**独立**做代码级能力盘点（不看 README）。
2. 对每条"未实现"断言，在 `broker/`（或 `smart-mqtt-broker/src/`）内做**忽略大小写**的消费点检索：
   `for k in topicalias messageexpiry sessionexpiry subscriptionidentifier retainhandling nolocal retainaspublished maximumpacketsize willdelay; do grep -rin "$k" smart-mqtt-broker/src/ | wc -l; done` -> **全部 0**。
3. 引用 Java 路径前先 `find` 复核（本次据此修正了 `MqttHeaderDecoder.java` 的真实位置：
   在 `smart-mqtt-common/src/main/java/tech/smartboot/mqtt/common/`，**不在** `.../common/message/`）。
4. 涉及规范级的结论，**取规范原文**再落笔（Section 3.1.2.11.4 / Section 3.2.2.3.7 / Section 3.3.2.3.4 / Section 3.3.4）。

---

## 4. smart-mqtt 侧可确证的缺陷（本仓的反超点）

以下均为**本轮独立复核**、可给出确切 `文件:行号` 的问题，可作为 shark-mqtt 的对比卖点。

| #   | 问题                                                                                                                                                                                  | 证据                                                                                                                                                                                                                                                                                                  | 性质                                                              |
| --- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------- |
| 1   | **出站 PUBLISH 一律构造空 `PublishProperties()`** => UserProperty / ContentType / ResponseTopic / CorrelationData / PayloadFormat **全不转发**，MQTT 5 Section 4.10 请求/响应模式在其上不可用 | 5 个构造点：`topic/AdvancedMessageDeliver.java:81`、`topic/BaseMessageDeliver.java:147`、`topic/SharedOrderedMessageDeliver.java:86`、`MqttSessionImpl.java:273`、`processor/ConnectProcessor.java:193`（**逐行已复核**）                                                                             | 违反 Section 3.3.2.3 的 MUST 转发                                        |
| 2   | **不处理 AUTH(0x0F) 报文**                                                                                                                                                            | `enums/MqttMessageType.java:18` 把 15 号类型命名为 `Reserved2(15,"保留")`；`common/MqttHeaderDecoder.java:89-128` 的 `newMessage` switch **无 case 15**，落入 `:128 default:` -> `throw new IllegalArgumentException("unknown message type: ...")`；`MqttBrokerMessageProcessor.java:72-83` 无 AUTH 注册 | 收到 AUTH 即抛异常断连；MQTT 5 Section 3.1.2.11.9 / Section 4.12 增强认证不可用 |
| 3   | **入站 `Message` 结构根本不携带属性**                                                                                                                                                 | `MqttSessionImpl.java:119-123` -> `common/.../Message.java:40-48`（构造只取 qos/payload/retained）                                                                                                                                                                                                       | 属性在 broker 内部第一跳即蒸发，是缺陷 1 的上游原因               |
| 4   | **Subscription Identifier 未实现**                                                                                                                                                    | `getSubscriptionIdentifier` 在 broker 包内 0 命中                                                                                                                                                                                                                                                     | 违反 Section 3.8.2.1.2 / Section 3.3.4                                          |
| 5   | **Topic Alias 未实现**（含 CONNACK 的 Topic Alias Maximum）                                                                                                                           | `topicalias` 在 broker 包内 0 命中                                                                                                                                                                                                                                                                    | Section 3.3.2.3.4                                                        |
| 6   | **Message Expiry Interval 未实现**                                                                                                                                                    | `messageexpiry` 在 broker 包内 0 命中                                                                                                                                                                                                                                                                 | Section 3.3.2.3.3                                                        |
| 7   | **Session Expiry Interval 未实现**                                                                                                                                                    | `sessionexpiry` 在 broker 包内 0 命中                                                                                                                                                                                                                                                                 | Section 3.1.2.11.2；会话不按声明过期                                     |
| 8   | **订阅选项 No Local / Retain As Published / Retain Handling 全被忽略**（只取 QoS 2 bit）                                                                                              | `MqttSubscribeMessage.java:66`；三个属性在 broker 包内 0 命中                                                                                                                                                                                                                                         | Section 3.8.3.1                                                          |
| 9   | **Will Delay Interval 未实现**，且 will 属性丢弃                                                                                                                                      | `willdelay` 在 broker 包内 0 命中；`processor/ConnectProcessor.java:193` 建空属性                                                                                                                                                                                                                     | Section 3.1.3.2                                                          |
| 10  | **客户端声明的 Maximum Packet Size 不用于出站限包**                                                                                                                                   | `maximumpacketsize` 在 broker 包内 0 命中                                                                                                                                                                                                                                                             | Section 3.1.2.11.4                                                       |
| 11  | **`$SYS` 系统主题无任何发布实现**                                                                                                                                                     | 全仓 `$SYS` 仅出现在 `plugin/spec/provider/SubscribeProvider.java:24,48,55`（**注释 + 默认禁止订阅**）                                                                                                                                                                                                | 缺标准运维主题                                                    |
| 12  | **会话恢复不重发 inflight 未确认 PUBLISH/PUBREL**                                                                                                                                     | `ConnectProcessor.java:154-179`（重连只还原订阅 `:169`）；`provider/SessionState.java:23-34` 的 `responseConsumers` 未被用于恢复                                                                                                                                                                      | 违反 Section 4.4.0-1                                                     |
| 13  | **集群路径丢 QoS 与属性**（一律以 `AT_MOST_ONCE` 重建）                                                                                                                               | `cluster-plugin/.../ClusterController.java:126`、`Coordinator.java:210`                                                                                                                                                                                                                               | 集群场景降级                                                      |
| 14  | **`docker/docker-compose.yml` 与 `start.sh` 的 `BROKER_*` 环境变量无读取点**                                                                                                          | `docker/docker-compose.yml:14,37`、`start.sh:4-12` vs 全仓检索（仅 `BROKER_LOWMEMORY` 被 `smart-mqtt-bench/.../AbstractBench.java:40` 读）                                                                                                                                                            | 配置文档/脚本与代码不一致                                         |
| 15  | **broker 主配置依赖企业版插件才有 `@Value` 注入**                                                                                                                                     | `BrokerContextImpl.java:275-322` 不读任何配置；`BrokerConfig.java` 在 enterprise-plugin 内                                                                                                                                                                                                            | 无企业插件时端口等不可配                                          |
| 16  | **`mysql` 认证器常量无实现**                                                                                                                                                          | `Authenticator.java:26-28` vs `AdvancedAuthPlugin.java:59-67`                                                                                                                                                                                                                                         | 声明与实现不一致                                                  |
| 17  | **`/metrics` 未按 `showMetrics` 白名单过滤**                                                                                                                                          | `MetricController.java:197-205` 返回白名单，`:210` 遍历全部枚举输出                                                                                                                                                                                                                                   | 白名单仅影响前端                                                  |

---

## 5. shark-mqtt 已超越 smart-mqtt 的能力（反向清单）

| 能力                                                                                                             | shark-mqtt 证据                                                                                                                       | smart-mqtt 对照                                  |
| ---------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------ |
| **MQTT 5.0 逐消息属性中继**（PayloadFormat / ContentType / ResponseTopic / CorrelationData / UserProperty 保序） | `broker/publish_properties.go:34-57`；出站注入点 `broker/broker.go:2255-2257`                                                         | 5 个构造点全为空属性（Section 4 缺陷 1）                |
| **属性持久化 + 跨重传/重放保持**                                                                                 | `storeProperties` `publish_properties.go:100-120`；`protocolProperties` `:127-147`；`broker/session.go:667,684`                       | 无                                               |
| **AUTH 报文 + 增强认证 + 运行期 re-auth**                                                                        | 编解码 `protocol/subscribe.go:492-534`；CONNECT 触发 `broker.go:308-324`；握手 `broker.go:658-717`；**re-auth `broker.go:1405-1440`** | 类型 15 = `Reserved2`，收到即抛异常（Section 4 缺陷 2） |
| **Message Expiry 按剩余时间递减 + 重传不丢弃交付**                                                               | `publish_properties.go:75-89` `remainingExpiry`；`broker.go:2866-2896`、`:2921-2948`、`:2961-2999`                                    | 未实现（Section 4 缺陷 6）                              |
| **Session Expiry 的协商 / DISCONNECT 覆盖 / 过期清理循环**                                                       | `broker.go:463-481`、`:1385-1393`、`:844-918`；Redis 键 TTL 跟随 `store/redis/session_store.go:53-77`                                 | 未实现（Section 4 缺陷 7）                              |
| **Retain Handling 0/1/2 + Retain As Published + No Local**                                                       | `broker.go:1924-1933` `shouldDeliverRetained`；`:2232` RAP；`session.go:433-451` NL                                                   | 三者全忽略（Section 4 缺陷 8）                          |
| **Topic Alias 入站映射**（含 CONNACK 声明上限、未映射即 Protocol Error 断开）                                    | `broker.go:1492-1523`；`session.go:827-846`；上限 `broker.go:499-510`；CONNACK `:2660-2664`                                           | 未实现（Section 4 缺陷 5）                              |
| **Will Delay Interval + 重连取消延迟遗嘱**                                                                       | `broker.go:572-588`；`will_handler.go:108-125`；`broker.go:421-425`                                                                   | 未实现（Section 4 缺陷 9）                              |
| **`$SYS` 系统主题发布**                                                                                          | `broker.go:959-1000`（version / uptime / connections / retained / subscriptions）                                                     | 无发布实现（Section 4 缺陷 11）                         |
| **Assigned Client Identifier**                                                                                   | `broker.go:380-384` -> `:449-450` -> `:2672-2674`                                                                                       | 有 `createClientId()` 但从不回带                 |
| **客户端 Receive Maximum 约束出站 inflight**                                                                     | `broker.go:490-491`、`:2639`；`session.go:857`                                                                                        | 已实现（**双方持平**，非反超点）                 |
| 协议适配层抽象（Adapter / Spec / Registry，6 个能力位）                                                          | `protocol/spec.go:16-58,61-104`、`adapter.go`、`registry.go`、`broker/conn.go`                                                        | 无此抽象                                         |
| MQTT 3.1 完整语义                                                                                                | `spec.go` 能力表（`Name=MQIsdp`、`MaxClientIDLen=23`）+ 专项集成测试                                                                  | `MqttVersion.java:14,24-35` 有枚举，语义未细化   |
| 协议 fuzz（840 万+ 次执行无崩溃）                                                                                | `protocol/fuzz_test.go`                                                                                                               | 无                                               |
| 单二进制 / 零外部依赖                                                                                            | `go.mod`（badger / redis 为可选构建标签）                                                                                             | 需 JVM + 插件 JAR                                |
| 批量写队列有界 + 分档缓冲池                                                                                      | `broker/write_queue_test.go`、`pkg/bufferpool/pool.go`                                                                                | -                                                |
| 存储后端 fail-fast 校验                                                                                          | `store/errors.go`、`store/interfaces.go`                                                                                              | 无                                               |
| 文件型账户认证器（含 bcrypt）                                                                                    | `broker/auth_file.go:16-139`、`auth.go:105-135`                                                                                       | 无                                               |
| 指标标签基数收敛（防时间线爆炸）                                                                                 | `broker/metrics_cardinality_test.go`                                                                                                  | 无                                               |
| MQTTS / WS / WSS 一体化配置                                                                                      | `broker/server.go:130-182,155-157,171-180,464-496`；`config/config.go:183-221`                                                        | 依赖独立插件各起端口                             |

---

## 6. 建议补齐顺序（若决定向平台化演进）

```
阶段 0（接口地基，低风险）
  T01 listener 数组化 + 插件化端口                                  [未动]
  T02 插件生命周期抽象（版本/优先级/初始化/销毁 + 配置模型）           [未动]
  T03 事件总线（扩展为全事件）+ 消息总线                             [未动]
  T03b 钩子分发点补全（OnMessage 带 Topic/QoS/Retain/Payload）        [已完成 33c728a0e]
  T04 k8s "多副本 != 集群"告警                                       [已完成 f229539dd]

阶段 1（管理面）
  T05 HTTP 管理 API（只读：连接/订阅/主题统计）                       [未动]
  T06 连接管理（强制断开）+ 运行时可观测端点                          [未动]
  T07 连接防抖（Flapping 60s/5 次/封禁 300s）                        [未动]

阶段 2（出口与集群）
  T08 消息出站抽象 + Redis/Webhook 出口                              [未动]
  T09 集群：节点发现 + 订阅 delta 广播 + 粘性路由                      [未动]
  T10 优雅停机 drain（先行，独立于集群）                              [未动]

阶段 3（协议细节，与平台化正交，可随时插入）* 本轮新增
  T11 客户端 Maximum Packet Size 约束出站（[MQTT-3.1.2-24]/[-25]）      [未实现]
  T12 重叠订阅的 Subscription Identifier 全量回带（[MQTT-3.3.4-4]）     [未实现]
  T13 Will Properties 中继（除 Will Delay 外的全部）                   [未实现]
  T14 Will Delay x 会话结束 取先到者                                   [未实现]
  T15 Topic Alias 非法值按 Protocol Error 断开（0 与越界）              [未实现]
```

> **性价比重新排序**：上一版把 T03b 列为最高性价比（已完成）。本轮建议把 **T11** 提为最高 --
> 它是唯一一条"服务端主动发送即可违约"的 MUST（`MaximumPacketSize` 一旦被客户端声明，
> 任何超限的 PUBLISH / CONNACK 都是违规），且实现点集中：出站构造只有
> `broker/broker.go:2255-2257` 与 CONNACK `:2635-2675` 两处，收口成本低。
> **T12 / T13 同属"属性管道"问题，可与 T11 合并成一次"出站属性/尺寸收口"改造。**

---

## 附录 A - 规模与计数基线（2026-10-14 21:25 实测）

| 指标                       | shark-mqtt（HEAD `2395d63e1`）  | 上一版（`22c16b83b`） |
| -------------------------- | ------------------------------- | --------------------- |
| `.go` 文件总数             | **168**（67 非测试 / 101 测试） | 159（67 / 92）        |
| 非测试 Go 代码行数         | **16,567**                      | 16,009                |
| 测试 Go 代码行数           | **27,868**                      | 25,813                |
| 单元测试 / 集成测试 / 基准 | **461 / 159 / 61**              | 440 / 147 / 61        |
| 集成测试文件数             | **31**                          | 27                    |
| 对照：smart-mqtt `.java`   | **276**（28,465 行）            | 276                   |

分模块非测试行数（递归、排除 `_test.go`）：
`broker` 7,047（17 文件）,  `protocol` 3,727（13）,  `store` 1,447（10，含 `store/redis`+`store/badger`
的 862 行，二者受构建标签门控）,  `client` 916（2）,  `api` 532（2）,  `pkg` 466（7）, 
`config` 366（2）,  `plugin` 200（1）,  `cmd` 160（1）,  `errs` 43（1）, 
其余 1,663 = `examples` 147 + `scripts` 877 + `testutils` 639。

**口径复现**：测试计数 = 源码中声明的顶层 `Test*` / `Benchmark*` 函数数，单目录非递归统计，
`store/redis` 与 `store/badger`（构建标签）不计入默认口径。权威维护点 = `docs/guides/TESTING.md`「代码规模」。

---

## 附录 B - 证据索引

### smart-mqtt（路径均已按实际目录 `find` 复核）

- **插件规范**：`smart-mqtt-plugin-spec/src/main/java/tech/smartboot/mqtt/plugin/spec/`
  `Plugin.java:67,75,107,114,137,155,159,176,190,201,212,224,237,241,252,254,281,296,344,363` , 
  `BrokerContext.java` ,  `PluginRegistryImpl.java:35-43,50-56,57-71,111-160,162-167` , 
  `bus/EventType.java:33,38,43,48,53,57,62,66,71,76,80,85,90,95,100` , 
  `provider/Providers.java:19,36,43,52` ,  `provider/SessionStateProvider.java:20-35` , 
  `provider/SubscribeProvider.java:24,40-56` ,  `schema/Item.java:42,46,55,64,73,80,87,96`
- **Broker 核心**：`smart-mqtt-broker/src/main/java/tech/smartboot/mqtt/broker/`
  `BrokerContextImpl.java:126,139,275-322,300-305,390-409,449` , 
  `MqttBrokerMessageProcessor.java:71-84` ,  `MqttSessionImpl.java:86-94,110-181,188-215,263-288,271-274` , 
  `processor/ConnectProcessor.java:54-110,117-135,154-179,181-197,190-194` , 
  `processor/PublishProcessor.java:33-100` ,  `processor/PubRelProcessor.java:24-42` , 
  `processor/DisConnectProcessor.java:24-31` ,  `KeepAliveMonitorSubscriber.java:36-63` , 
  `topic/AdvancedMessageDeliver.java:55-93,79-82` ,  `topic/BaseMessageDeliver.java:146-149` , 
  `topic/SharedOrderedMessageDeliver.java:29-108,84-87` ,  `topic/SimpleMessageDeliver.java:96-112` , 
  `InflightQueue.java:40,51-56,107-127,132-186,237-269` ,  `common/.../Message.java:40-48` , 
  `common/.../SharedDeliverGroup.java:16-44`
- **协议层**：`smart-mqtt-common/src/main/java/tech/smartboot/mqtt/common/`
  `MqttHeaderDecoder.java:84,89-131`（**真实位置在此，非 `.../common/message/`**）, 
  `enums/MqttVersion.java:14,24-35` ,  `enums/MqttMessageType.java:18` , 
  `message/variable/properties/MqttProperties.java:81-82,161,223,399-401,470` , 
  `message/variable/properties/PublishProperties.java:28,35-85` , 
  `message/variable/properties/ConnectProperties.java:39-44,55-61,63-68,88-102` , 
  `message/variable/properties/ConnectAckProperties.java:76,84-89` , 
  `message/variable/properties/WillProperties.java:28` ,  `message/MqttPublishMessage.java:48-54` , 
  `message/MqttSubscribeMessage.java:44-54,66` ,  `message/MqttDisconnectMessage.java:32-38` , 
  `util/TopicToken.java:28-46,84-86` ,  `util/MqttUtil.java:39-41,43-58,60-86`
- **插件**：`plugins/`
  `enterprise-plugin/.../openapi/controller/`（`SystemController` ,  `ConnectionsController:276,289,301` , 
  `TopicController:120,132` ,  `MetricController:60,61,69-73,88-205,207-231,233-256,263-280` , 
  `LicenseController:73-84,103-131,146,176` ,  `PluginManagerController:71,125-135,143,148,160,186,238,270,306,388,402,419,460,477` , 
  `PluginRepositoryController:45,51,85` ,  `VersionController:33,40-73` ,  `AiController:35,57`）, 
  `enterprise-plugin/.../plugin/flapping/FlappingDetector.java:35,50,67-81,88-114,120-146` , 
  `enterprise-plugin/.../EnterprisePlugin.java:48,56,58,69-107,210-215` , 
  `cluster-plugin/.../cluster/Coordinator.java:22-56,63-111,154-233,235-308` , 
  `cluster-plugin/.../cluster/ClusterController.java:53-64,79,126,129-153,158-208` , 
  `kafka-storage-plugin/.../kafka/storage/kafka/server/KafkaServer.java:103-137,435-445` , 
  `kafka-storage-plugin/.../kafka/storage/store/{PartitionLog,LogSegment,PersistentMessageStore,ConsumerOffsetStore}.java` , 
  `advanced-auth-plugin/.../auth/advanced/AdvancedAuthPlugin.java:59-67,91-101,104-135,176-181` , 
  `advanced-auth-plugin/.../auth/advanced/provider/{HttpAuthenticator,RedisAuthenticator,AbstractAuthenticator}.java` , 
  `simple-auth-plugin/.../auth/SimpleAuthPlugin.java:26-48` , 
  `redis-bridge-plugin/.../bridge/redis/BridgeService.java:28-50,52-75` , 
  `memory-session-plugin/.../MemorySessionStateProvider.java:23-40` , 
  `mqtts-plugin/.../TlsPlugin.java:24-93` ,  `websocket-plugin/.../WebSocketPlugin.java:37-121` , 
  `bench-plugin/.../BenchPlugin.java:42-317`

### shark-mqtt（HEAD `2395d63e1`）

- **协议**：`protocol/spec.go:16-58,61-104,108-119` ,  `protocol/constants.go:26,59,68-71,98,187-192` , 
  `protocol/connect.go:23-27,151-160,193-205` ,  `protocol/codec.go:77-86` , 
  `protocol/properties.go:19,21-22,34,56,58-59,71,109,111-112,126,156,171,378-383,390-401,411-416,495-503,657-661,673-688,780-784,866` , 
  `protocol/subscribe.go:44-49,72-75,492-534,536-567` ,  `protocol/publish.go:65,75` , 
  `protocol/fuzz_test.go`
- **Broker**：`broker/broker.go:194,208,229,251,266,308-324,357-376,380-384,421-425,449-450,463-481,490-491,499-510,568,572-588,591,623-639,658-717,720-754,794-829,844-918,935-957,959-1000,1108-1115,1225,1352-1361,1385-1393,1405-1440,1492-1523,1525-1533,1787-1801,1883-1933,1983-1990,1996-2098,2121-2188,2213,2232,2255-2257,2635-2675,2866-2896,2907-2999,3001-3041` , 
  `broker/publish_properties.go:19-23,29-33,34-57,75-89,100-120,127-147` , 
  `broker/session.go:53-55,67,98,115-172,336-353,398-410,433-451,598-605,609-687,726,745-760,827-846,857` , 
  `broker/topic_tree.go:84-117,122,139,190-220,299,306,313` , 
  `broker/server.go:27,40,130-182,454-459,464-496,531` ,  `broker/ws_conn.go` , 
  `broker/will_handler.go:35,61,100-125,159-161` ,  `broker/qos_engine.go:154,171` , 
  `broker/rate_limit.go:8-62` ,  `broker/auth.go:37-44,52-58,105-135,158-164,168-251,258-294` , 
  `broker/auth_chain.go:13-59` ,  `broker/auth_file.go:16-139` ,  `broker/auth_noop.go:6-11` , 
  `broker/options.go:56-57,82,116-124`
- **扩展 / 配置**：`plugin/manager.go:15-25,34-53,56-60,88-117` , 
  `config/config.go:15,21,24-31,110-169,174-181,183-221` ,  `config/loader.go:55-74,76-100` , 
  `cmd/main.go:33-47,49-100,103-107,125`
- **存储 / 指标 / API**：`store/interfaces.go:8-46` ,  `store/types.go:19-23,34-35` ,  `store/errors.go` , 
  `store/memory/memory.go:13-393` ,  `store/redis/{session,message,retained}_store.go` , 
  `store/badger/{session,message,retained}_store.go` , 
  `pkg/metrics/prometheus.go:67-138` ,  `pkg/metrics/metrics.go:5-32` ,  `pkg/logger/slog.go:16-52` , 
  `api/api.go:226-247,350-393,408-468`
- **既有分析**：`docs/reports/SMART-MQTT-GAP-ANALYSIS-261012.md` ,  `...-261014.md` , 
  `docs/reports/EMQX-GAP-ANALYSIS-260910.md` ,  `docs/architecture/CLUSTER-FEASIBILITY.md` , 
  `docs/architecture/PROTOCOL-ADAPTER-DESIGN.md` ,  `docs/architecture/CONCURRENCY.md`

---

## 附录 C - 复核纪律（4 次误判的教训）

| #   | 误判                                           | 真相                                                                                                 | 触发它的坏习惯                                   |
| --- | ---------------------------------------------- | ---------------------------------------------------------------------------------------------------- | ------------------------------------------------ |
| 1   | CONNACK 未通告 Maximum QoS 是缺口              | Section 3.2.2.3.4：Maximum QoS **只允许 0/1，省略即表示支持 QoS 2**，省略是唯一正确编码                     | 把"字段缺席"当缺陷                               |
| 2   | Request Problem Information 仅解析未裁剪是缺口 | Section 3.1.2.11.7 只要求"置 0 时不得发 Reason String / User Property"；broker 侧对二者**零引用**，天然满足 | 同上                                             |
| 3   | `HookContext` 缺 Topic/Payload                 | 字段自 2026-04-23 就在，真缺口是**分发点未填充**                                                     | 读类型定义就推断运行时行为                       |
| 4   | **本轮**：`AssignedClientID` broker 侧零消费   | 实为完整实现（`broker.go:380->450->2672`）                                                             | **`grep` 后接了 `head`，真命中被截掉据就下结论** |

**统一对策**：
1. 判断"未实现"必须找**消费点**，找不到消费点才叫未实现；字段缺席 != 缺陷。
2. 断言"零命中"时**不得用 `head` / `tail` 截断**，先 `wc -l` 再取。
3. 引用外部（Java / 规范）路径与条款前，先 `find` / 取原文复核。
4. 涉及 MUST / SHOULD 的定性，先取规范原文再落笔，不凭记忆。

---

*生成时间：2026-10-14 21:25 ,  参照方：smart-mqtt（静态快照，276 个 .java / 28,465 行）,  被评估方：shark-mqtt `2395d63e1`*
*相对 `-261014` 版的变化：新增第 1 节（20 个提交的增量与 T03b 关闭）、Section 2.4（5 项新发现的协议细节缺口）、*
*第 3 节更正记录（4 处）、第 4 节 smart-mqtt 缺陷清单（扩到 17 条并逐条复核行号）；重排第 5 节；*
*刷新附录 A 规模基线（461 / 159 / 61；16,567 / 27,868 行）；修正 1 处 Java 路径与 1 处归属错误。*
