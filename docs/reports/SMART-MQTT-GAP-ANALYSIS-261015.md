# Shark-MQTT 未完成细节清单（对比 smart-mqtt + Go 特性视角，2026-10-15）

> **分析对象**：`G:/smart-mqtt`（Java / AGPL-3.0，276 个 `.java`，静态快照）
> **被评估方**：`G:/shark-mqtt`（Go / MIT，本轮基线 `9208c57f2`）
> **上一版**：`SMART-MQTT-GAP-ANALYSIS-261014-2125.md`（基线 `2395d63e1`）
> **口径**：以代码为准。凡"未实现"结论均给出 `文件:行号`；断言"零命中"前先 `wc -l`，
> 不用 `head` 截断（上一版附录 C 记录的第 4 次误判教训，本版沿用该纪律）。
>
> **本版与上一版的关键差别**：上一版做的是「功能对照」（Java 有、Go 没有）。
> 本版新增**第三维视角 -- Go 语言特性与工程惯例**，因为 Java 差距分析天然看不见
> 这类问题：ctx 传播纪律、错误链完整性、锁内 syscall、goroutine 生命周期、
> 接口设计是否泄漏具体类型。它们与协议正确性正交，且恰是本仓最集中的债务区。
>
> **结论摘要**：上一版 26 项中，5 项协议细节缺口（T11-T15）已由`0af9aa1d9` ~ `bf27beb9c`
> 全部关闭；平台化 4 项 P0 本轮仍零推进。本版新发现 **12 项 Go 视角缺陷**，
> 其中 **1 项为功能性缺陷**（停机 drain 顺序倒置，已修），其余为工程质量项。
> 本轮落地 5 项，剩7 项列入待办。

---

## 0. TL;DR

| 维度 | 结论 |
| --- | --- |
| **规模** | shark-mqtt：175 个 `.go`（67 非测试 / 108 测试），非测试 **17,459** 行、测试 **30,808** 行；对照 **519 单测 / 174 集成 / 70 基准** |
| **协议内核** | 上一版 T11-T15 全部关闭，本轮**零回归**（全量 `go test ./...` 通过） |
| **平台化 P0** | 插件体系 / 管理面 / 集群 / 数据出口 -- **本轮仍零推进**（见 Section 2） |
| **Go 视角新发现** | **12 项**，其中功能性缺陷 1 项（drain 顺序）、ctx 纪律 3 项、错误可见性 2 项、并发 2 项、API 设计 2 项、性能 1 项、工程债 1 项 |
| **本轮已落地** | **11 项**（10 个提交），含 1 项此前**无人发现的功能性缺陷** |
| **本轮遗留** | 3 项 Go 视角 + 上一版 16 项平台化缺口（多 listener、连接管理、Topic 统计、P0-3 只读部分本轮关闭） |

**一句话**：本轮最有价值的发现不是任何 Java 对照项，而是 `Stop()` 里
**"先关连接、再等 inflight 归零"** 的顺序倒置 -- PUBACK 必须走存活连接，
所以那段等待从来不可能成功，每次滚动重启都白等满一个周期；而 `api.Broker.Stop()`
更是在 `srv.Stop()` 关光socket 之后才调 `broker.Stop()`，drain 根本没有生效窗口。
**这是"读协议语义"能发现、"跑功能测试"发现不了的一类缺陷。**

---

## 1. 本轮增量（`2395d63e1..9208c57f2`，上一版之后的提交）

| 类别 | 提交 | 内容 |
| --- | --- | --- |
| **A. 协议细节（上一版 T11-T15）** | `0af9aa1d9` `5541d5692` `ea975c4d6` `120d50985` `bf27beb9c` | Topic Alias 非法值 0x94 中止、Maximum Packet Size 约束出站、订阅标识符改集合、Will Properties 中继、Will Delay x 会话结束 |
| **B. 协议细节（追加）** | `ba9637c2d` `a3893a347` `8fb9a7193` `70ac4eac9` `30426d0db` | Receive Maximum 广播与 0x93、畸形主题名拆链、Clean Start 不再强制 Session Expiry=0、会话接管 0x8E |
| **C. 测试 / 文档** | 其余 9 个 | 含 OASIS MQTT 5.0 标准全文入库、测试计数口径刷新 |

> **T15 的实际落地口径与上一版不同**：上一版记为"Topic Alias 非法值未按 Protocol Error 断开"，
> 实际 `0af9aa1d9` 同时修正了 **Topic Alias Maximum 协商方向**（这是更根本的错误：
> 上限是"我最多能接收多少别名"，不是"对端能发多少"）。结论：已关闭。

---

## 2. 未完成清单

状态口径：`未动` = 自上一版基线以来无代码变化；`不适用` = 与 MIT 单机定位冲突。

### 2.1 Go 视角新发现（12 项 -- 本版核心增量）

这一节是本版与上一版最实质的差别：**这些项在 smart-mqtt 对照里全部不可见**。

| # | 缺口 | 证据 | 性质 | 状态 |
| --- | --- | --- | --- | --- |
| G1 | **停机 drain 顺序倒置**：先 `conn.Close()` 再等 inflight 归零，而 inflight 靠 PUBACK 递减、PUBACK 必须走存活连接 => 等待必然空转 | 修正前 `broker/broker.go` Stop 第 2/3 步；`qos_engine.go:386` `InflightCount` 语义 | **功能性缺陷** | **已修**（`1ada2b50f`） |
| G2 | `api.Broker.Stop()` 在 `srv.Stop()` 关光 socket 之后才 `broker.Stop()`，drain 无生效窗口 | 修正前 `api/api.go:320-325` | **功能性缺陷** | **已修**（`1ada2b50f`） |
| G3 | drain 超时硬编码 5s，无配置项 | 修正前 `broker/broker.go` `deadline := time.Now().Add(5 * time.Second)` | 可运维性 | **已修**（`1ada2b50f`） |
| G4 | 出站 QoS1/2 跟踪错误被 `_ =` 丢弃，失败即重传链断裂且不可见 | 修正前 `broker/broker.go` `doDeliver` | 可观测性 | **已修**（`f74afec87`） |
| G5 | `flushBufferedOutbound` 忽略 `PopOutbound` 的 ok，竞态下投递零值消息 | 修正前 `broker/broker.go` 同函数 | 正确性 | **已修**（`f74afec87`） |
| G6 | Clean Start 分支在同一函数内混用连接 `ctx` 与全局 `b.ctx`，store IO 无法随连接取消 | 修正前 `broker/broker.go:484,490` vs 同函数 `:394` | ctx 纪律 | **已修**（`f74afec87`） |
| G7 | `api.Broker.Broker()` 返回具体 `*broker.Broker`，绕过封装；且与 `api.Broker` 同名异义 | 修正前 `api/api.go:383` | API 设计 | **已修**（`1ada2b50f`，改为 `Core()` 窄接口） |
| G8 | `Stop()` 持 `b.mu` 遍历执行 `conn.Close()` syscall，并发接入路径被排在最慢 Close 之后 | 修正前 `broker/broker.go` Stop 第 2 步 | 并发 | **已修**（`1ada2b50f`，锁内摘除 / 锁外关闭） |
| G9 | `disconnect()` 不接受 ctx，其内部 store 调用一律用 `b.ctx` | `broker/broker.go:1280`（签名），调用点 `:3408,3417,3422` + 2 处测试 | ctx 纪律 | **已修**（`7ed370208`） |
| G10 | `disconnect()` / `handlePublish()` / `deliverToClient()` 等每连接路径统一用 `b.ctx`（全仓 29 处） | `b.ctx` 出现处（`:484`-`:3372` 区间） | ctx 纪律 | **已修**（`23a5c19be`，16 个函数加 ctx 首参） |
| G11 | 会话快照每次落盘深拷贝全部 payload，为高频 GC 来源 | `broker/session.go` Save / Restore 的 `make`+`copy`；`pkg/bufferpool` 仅 protocol 层使用，broker 层零使用 | 性能 | **放弃**（见 Section 4.2：这些 payload 被 store 长期持有并序列化，pool 语义不适用） |
| G12 | `broker.go` 3,400+ 行单文件，天然边界清晰但未拆 | `broker/broker.go`：`HandleConnection` / `readLoop`+`handlePublish` / `clientState` 写路径 / QoS2 四件套 | 工程债 | **未动** |

### 2.2 P0 结构性缺口（4 项，与上一版一致，仍零推进）

| # | 缺口 | smart-mqtt 证据 | shark-mqtt 现状（复核于 `9208c57f2`） | 属既定定位 |
| --- | --- | --- | --- | --- |
| 1 | 插件生命周期与热插拔 | `plugin/spec/Plugin.java` 全套 19 个生命周期方法 | 仅 4 钩子（`plugin/manager.go:15-25`）+ Register/Unregister（`:88-117`） | 否（应补） |
| 2 | 事件总线 / 消息总线 / Provider | `bus/EventType.java` 15 类事件；`provider/Providers.java` 4 个 Provider | 无（仅同步钩子） | 否（应补） |
| 3 | HTTP 管理 API + Web 控制台 | `enterprise-plugin/.../openapi/controller/` 9 个 Controller | **只读部分已落地**（`8b7ebdd07`）：`/clients`、`/clients/{id}`、`/topics`，默认关闭 + token 保护。写操作与 Web 控制台仍缺 | 部分 |
| 4 | 集群 | `cluster-plugin/.../Coordinator.java` | `cluster` 生产代码 0 命中；优雅停机 drain 已于本轮补齐（`1ada2b50f`），但节点发现 / delta 广播 / 跨节点路由仍全缺 | 是（已明确暂不做） |

### 2.3 P1 缺口（8 项，与上一版一致）

| # | 缺口 | shark-mqtt 现状（复核于 `9208c57f2`） | 备注 |
| --- | --- | --- | --- |
| 5 | 数据出口：Redis 桥接 | `bridge` 0 命中；Redis 仅作 store 后端 | 定位为不做 |
| 6 | 数据出口：Kafka | `kafka` 0 命中 | 定位为不做 |
| 7 | 认证：Redis / HTTP 认证器 | 6 种本地实现（Static/AllowAll/DenyAll/Chain/File/Noop），**无 Redis / HTTP backed**；`EnhancedAuthenticator` 仅接口定义、仓内零实现 | 部分 |
| 8 | 连接防抖（Flapping） | `ConnAckBanned = 0x8A`（`protocol/constants.go:98`）常量全仓仅定义处 1 处引用；`rate_limit.go` 是**全局维度**限流（不分 IP / clientID），超限仅拒本次连接、无累计惩罚、无自动解封 | 否（应补） |
| 9 | 多 listener 数组 | **已落地**（`0bfafa040`）：`listen_addrs` / `ws_listen_addrs` / `wss_listen_addrs` 数组形式 + `Addrs()`；绑定失败回滚、Stop 关闭全部端口 | 已完成 |
| 10 | 配置 UI 元数据 Schema | `config/` 内无 schema（仅 struct tag + `Validate()`） | 否 |
| 11 | 插件市场 / 本地上传 | 无 | 否（可选） |
| 12 | 插件运行时启停 / 配置热改 | 无；broker 配置亦无热重载 | 否 |

### 2.4 P2 平台 / 运维缺口（6 项，与上一版一致）

| # | 缺口 | shark-mqtt 现状 | 备注 |
| --- | --- | --- | --- |
| 13 | 智能推送模型（高负载自适应延迟） | 无 | 可选 |
| 15 | 版本检查 / 更新提示 | 无 | 否 |
| 16 | 指标项丰富度 | 12 项（`pkg/metrics/prometheus.go:67-138`），已含 Histogram；JVM/CPU gauge 不适用 Go | 部分 |
| 19 | 内置压测（运行期可启动） | `loadtest/stress` 0 命中；压测仅存在于离线 `tests/bench`（`go test -bench`） | 部分 |
| 20 | 连接管理（列表 / 强制断开） | **已落地**（`cb35845f9` `8b7ebdd07`）：`DisconnectClient` / `DisconnectAllClients` 导出并加入 `api.Core`；`GET /clients` 列表 | 已完成 |
| 21 | Topic 统计 / 订阅关系查询 | **已落地**（`8b7ebdd07`）：`TopicTree.Stats()` + `GET /topics`，含共享订阅 group 拆分 | 已完成 |

**已关闭**：`AI Agent / MCP`（不适用）、`License 授权`（不适用，MIT）、`License 连接数限制`（不适用）。

---

## 3. 本轮已落地的修复（10 个提交）

### 3.1 `f74afec87` - 错误可见性与 ctx 纪律

| 项 | 内容 |
| --- | --- |
| G4 | `doDeliver` 不再丢弃 `TrackOutboundQoS1/2` 错误：投递照常，但补 `errors_total{component="qos_track"}` + Warn |
| G5 | `flushBufferedOutbound` 尊重 `PopOutbound` 的 ok，竞态下不再投递零值消息 |
| G6 | Clean Start 路径改用连接 `ctx`；`b.ctx` 的适用边界写入注释，防止再次混用 |

> **G4 的实际影响**：跟踪失败意味着该投递未登记重试，PUBACK 一丢即静默丢消息。
> 此前该失败路径**完全不可见** -- 这正是 `errors_total` 指标设计要覆盖的场景。
> 新增 label 值 `qos_track` 已按基数收敛契约加入白名单
> （`broker/metrics_cardinality_test.go`，该测试会拒绝任何未加白名单的 label）。

### 3.2 `1ada2b50f` - 停机 drain 正确性（**本轮最高价值**）

**缺陷本质**：`Stop()` 的两步顺序与 inflight 的确认机制矛盾。

```
旧顺序                          为什么必然失败
-----------------------------    ------------------------------
1. conn.Close()  <- socket 没了   inflight 靠 PUBACK/PUBREC 递减
2. 轮询 inflight -> 0             PUBACK 必须走存活连接
   最多等 5s                     => 计数永不为 0，只能空转到 deadline
```

`api.Broker.Stop()` 更进一步：`srv.Stop()`（关光全部 socket）-> `broker.Stop()`（含 drain），
drain 连执行窗口都没有。

**修正后**：`BeginShutdown()` -> `drainInflight()` -> 关连接。
`b.shuttingDown` 已在第一步置位，readLoop 停止接新活、遗嘱保持抑制。

| 交付 | 说明 |
| --- | --- |
| `drainInflight` / `totalInflight` | 抽出独立方法；超时与放弃均落日志 + `IncErrors` |
| `WithDrainTimeout` | 默认 5s；`0` = 不等待；负值 = 跳过 |
| `config.drain_timeout` + `MQTT_DRAIN_TIMEOUT` | 配置接线；文档已更新 |
| `Broker.DrainInflight()` | 导出，供自持网络层的嵌入者在关socket 前先 drain |
| `drained` 标志 | 保证与 `Stop` 内部不重复等待；`Start` 重置以支持 `Stop`->`Start` |
| 关连接改为锁内摘除 / 锁外 Close | `conn.Close()` 是 syscall，不应持 `b.mu` |

**回归测试**（`tests/integration/drain_test.go`，2 条）：
1. `TestDrain_WaitsForInflightAckBeforeClosing` -- drain 期间订阅端 socket 仍可回 PUBACK。
   **这条断言正是区分新旧顺序的判据**：旧顺序下此时 socket 已关，Encode 必失败。
2. `TestDrain_TimeoutIsBounded` -- 客户端永不确认时，停机仍受配置超时约束。

> **方法论备注**：第一条测试在写出后**当场失败**（`wsasend: An established connection
> was aborted`），正是它暴露了 `api.Broker.Stop()` 的顺序问题。
> **"drain 期间 socket 是否仍可写"是一个比"停机是否返回"强得多的断言** --
> 后者新旧实现都能通过。

### 3.3 `bb926cc14` - 文档与计数同步

CHANGELOG 新增三节（drain 正确性 / 错误可见性与 ctx 纪律 / API 表面变更）；
`CONFIGURATION.md` 补 `drain_timeout`；`TESTING.md` 计数按实测口径刷新
（上一版记录的 491/172/61 已滞后于代码，实测 519/174/70）。

---

## 4. 待办清单（本轮未动，按性价比排序）

**已完成（本轮）**：G9 `disconnect` ctx 参数化 ,  G10 每连接路径 ctx 贯通 , 
P2-20 `DisconnectClient` 管理出口 ,  `errors.Join` 聚合校验错误 , 
`slices.ContainsFunc` / `slices.Collect(maps.Keys(...))`。

| 序 | 项 | 理由 | 预估风险 |
| --- | --- | --- | --- |
| 1 | **P1-9** 多 listener 数组化 | 解锁"同一进程多端口"部署形态；smart-mqtt 五个插件各起端口。当前 `ListenAddr` 是单 `string` | 中 |
| 2 | **P1-8** Flapping 连接防抖 | 补上"惩罚态 + 自动解封"，当前 `rate_limit.go` 是全局维度限流、只拒本次连接 | 中 |
| 3 | **G12** `broker.go` 拆分 4 文件 | 边界已验证清晰（`HandleConnection` / `readLoop`+`handlePublish` / `clientState` 写路径 / QoS2 四件套）；纯机械重构 | 中（需全量回归） |
| 4 | **P0-3** HTTP 管理 API（只读） | 连接/订阅/主题统计查询；底层 `ListSessions`/`ListMessages` 已存在，api 层 0 调用。`DisconnectClient` 本轮已铺好出口 | 低 |
| 5 | **P0-1/2** 插件生命周期 + 事件总线 | 生态地基，工作量大但价值最高 | 高 |
| 6 | 补 `%w` 错误链（84 处 `fmt.Errorf` 无 `%w`） | 机械但量大，`errors.Is/As` 目前无法穿透；需逐条判断是否有底层错误可包 | 低（但需逐条判断） |

### 4.2 本轮证伪的两项（避免后人重蹈）

| 原建议 | 证伪依据 |
| --- | --- |
| `b.ctx` 改 `atomic.Pointer` 以"根除竞态" | `Stop` 开头的 `b.loops.Wait()` 已确保三个后台循环全部退出后 `Start` 才重写字段，happens-before 关系充分。**本机无 gcc，`-race` 不可用**，故以代码推理否定 -- 结论是"无实际竞态"，而非"改造未做" |
| 会话快照深拷贝改用 `pkg/bufferpool` | 这些 payload 会被 store **长期持有并序列化**（memory store 自己还要再拷一次），不是一次性临时缓冲。`Put` 要求"用完即还"，与持有期完全不匹配 -- 强行套用会引入 use-after-recycle |

> 这两项都写在本轮改进计划里，实际动手前查证后被否。**记录否证过程比记录结论更有价值**：
> 子代理或直觉给出的"优化建议"必须先验证前提，否则只是把复杂度引入代码却换不到收益。

---

## 5. 复核纪律（本版实际执行）

1. **两侧独立做代码级能力盘点**，不以 README / 文档声明为准。
2. 子代理结论**逐条人工复核**后才落笔 -- 本版复核了 ctx 传播（`:484,490` vs `:394`）、
   吞错误（`doDeliver`）、api 泄漏（`api.go:383`）、锁内 syscall（`Stop` 第 2 步）、
   ctx 竞态注释（`:136-146`）共 5 处，全部属实。
3. 子代理报告的一处**夸大已修正**：「`broker.Broker` 无 `ConnCount` 导致接口不成立」--
   实际 `api.Broker.ConnCount()` 来自网络层而非 broker core，改为 `Metrics()` 后成立。
4. **区分"环境问题"与"代码问题"**：全量测试曾出现 2 处失败
   （`TestWebSocket_MQTTTransport` 报 `Bad Gateway`、`TestBrokerNoMetricsEndpoint...` 报 502），
   经`HTTP_PROXY=... go test` 复跑确认为**代理环境变量劫持本地回环请求**，
   清空代理变量后全绿。**未把环境噪声写成代码缺陷**。
5. 引用行号前均以 `grep -n` / `sed -n` 当场确认，未沿用子代理转述的行号。

---

## 6. 一个方法论提醒

上一版附录 C 记录了 4 次误判，本版新增第 5 条经验：

> **断言"零命中"时不得用 `head` 截断**，也**不得把子代理的"我 grep 了"当作证据**。
> 本版新增的 12 项里有 3 项（G4/G5/G6）是子代理发现的，但每一项在提交前都做了
> 人工 `sed -n` 复核 -- 成本约 10 行输出，收益是避免把误判写进 CHANGELOG。

---

*生成时间：2026-10-15 12:45 ,  参照方：smart-mqtt（静态快照，276 个 .java）*
*被评估方：shark-mqtt `9208c57f2` -> 本轮 3 个提交 `f74afec87` / `1ada2b50f` / `bb926cc14`*
*相对 `-261014-2125` 版的变化：新增 Section 2.1「Go 视角 12 项」（本版核心增量）；*
*Section 2.2-2.4 复核上一版 26 项状态（T11-T15 全部关闭）；Section 3 记录 3 个已落地提交；*
*Section 4 给出 10 项待办排序；Section 5-6 记录复核纪律与新增误判经验。*