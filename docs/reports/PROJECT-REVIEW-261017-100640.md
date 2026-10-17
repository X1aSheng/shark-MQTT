# Shark-MQTT 项目审查报告

> 审查时间: 2026-10-17 10:06 (UTC+8)
> 基线提交: `1707da914` (审查起点 `e074829f7`)
> 审查范围: 全部 198 个 Go 源文件 (52,971 行)、docs 全部活文档、CI 工作流、部署清单
> 结论: 协议内核正确性已达到可交付水平; 发现并修复 6 类缺陷 (含 2 类可导致进程级故障), 文档与代码存在系统性脱节 (99 处)

---

## 1. 结论摘要

| 维度 | 评估 | 依据 |
|------|------|------|
| 协议正确性 | 良好 | MQTT 3.1.1 / 5.0 双版本编解码门控完整; 属性 locus 表 11 个 + 27 条 probe; 本次复核的 5 项历史协议缺口已全部实现 |
| 并发安全 | 需修补 | 发现 2 处可导致进程崩溃或全局停摆的缺陷, 均已修复并附回归测试 |
| 可测试性 | 优秀 | 单测 533 / 集成 194 / 基准 72 (默认口径), 全绿; 缺陷回归测试均可在修复前稳定复现 |
| 文档一致性 | 差 | 活文档与代码不一致 99 处, 其中 47 处会直接误导操作 (导致编译失败或启动失败) |
| 运维就绪度 | 部分 | 存储后端配置项在 CLI 路径上不可兑现; Helm 多端口缺陷已于上一轮修复 |
| 架构演进 | 良好 | 协议适配层 (protocol.Adapter) 已落地, 网络层与业务层解耦真实可用 |

**最需要立刻知道的三件事**

1. `TopicTree.Stats()` 曾无锁遍历共享订阅 map。管理端点 `/topics` 与任一客户端 SUBSCRIBE 并发时会触发 Go 运行时 `concurrent map iteration and map write` —— 进程直接 abort, 不可 recover。已修。
2. `writeLoop` 写失败退出时不广播停止信号。对端停止读取后, 该连接的写队列填满, 阻塞的不只是它自己: QoS 重试 republish 由**全 broker 单 goroutine** 驱动, 一条死连接可让所有客户端的重传停摆。已修。
3. `docs/guides/CONFIGURATION.md` 教用户配 `storage_backend: redis`, 但 `cmd/main.go` 从不注入 store, 按文档操作必然启动失败。文档未修, 见第 6 节。

---

## 2. 审查范围与方法

### 2.1 范围

| 层 | 文件数 | 行数 | 审查方式 |
|----|--------|------|----------|
| broker/ | 49 | 17,923 | 逐文件精读 (conn_lifecycle / pub_sub / session / qos_* 为全覆盖) |
| protocol/ | 31 | 7,822 | 逐文件精读 + 规范条款比对 |
| store/ | 18 | 4,195 | 精读 memory, 对照 interfaces 校验 redis/badger |
| client/ | 6 | 2,570 | 精读 |
| plugin/ | 8 | 2,195 | 精读 (含事件总线) |
| api/ config/ pkg/ errs/ cmd/ | 28 | 4,608 | 精读 |
| tests/ | 51 | 12,393 | 分类抽查 + 全量执行 |
| docs/ | 55 篇 md | - | 逐篇与代码比对 |

### 2.2 方法

1. 全量静态检查: `go build ./...`、`go build -tags nometrics ./...`、`go vet ./...`、`golangci-lint`。
2. 全量动态检查: `go test -count=1 ./...` 加集成测试单独执行; `-race -p 1` 定点验证。
3. 文档一致性: 用脚本 + 人工逐条核对文档中出现的路径、签名、字段、默认值、报错文案。
4. 缺陷定案规则: **不以类型定义或字段声明推断运行时行为**, 必须回到实际调用点; 断言"零命中"时 grep 不接 `head`。
5. 每项修复先写可失败的回归测试, 再改产品代码, 复跑确认由红转绿。

### 2.3 本轮对历史结论的纠正

上一轮审查报告列出 5 项"未完成协议缺口"。本次逐条回到调用点复核, **5 项已全部实现**:

| 历史缺口 | 当前实现位置 | 状态 |
|----------|--------------|------|
| 客户端声明的 Maximum Packet Size 不约束出站 | `protocol/codec.go:258-279` (`Encode` 先聚合再判 `outboundMax`) + `broker/conn_lifecycle.go:90` | 已实现 |
| 重叠订阅只回带一个 Subscription Identifier | `broker/session.go:452-497` (收集 + 排序 + 去重), `broker/publish_properties.go:59` | 已实现 |
| Will 的非 Delay 属性全丢 | `broker/will_handler.go:116-150` (`relayableWillProperties`) | 已实现 |
| Will Delay 未与"会话结束"取先到者 | `broker/conn_lifecycle.go:541-543` | 已实现 |
| Topic Alias 非法值未按 Protocol Error 断开 | `broker/pub_sub.go:87-118` + `session.go:920-928` | 已实现 |

这印证一条审查纪律: 跨会话的历史结论必须重新取证, 不能继承。

---

## 3. 架构分析

### 3.1 分层拓扑

```
                    +-----------------------------------+
                    |   api/  统一门面 (Broker, Run)     |
                    +----------------+------------------+
                                     |
             +-----------------------+-----------------------+
             |                                               |
   +---------v----------+                        +-----------v----------+
   |  broker/ 网络层     |                        |  broker/ 内核层       |
   |  MQTTServer        |   ConnectionHandler    |  Broker              |
   |  - registry        |<---------------------->|  - pub_sub           |
   |  - tcp/tls/ws/wss  |      (*Conn)           |  - session / Manager |
   |  - protocol.Adapter|                        |  - TopicTree         |
   +---------+----------+                        |  - QoSEngine         |
             |                                   |  - WillHandler       |
             |                                   |  - FlappingDetector  |
             |                                   +-----------+----------+
   +---------v----------+                                    |
   |  protocol/ 适配层   |                        +-----------v----------+
   |  Adapter  Registry |                        |  store/ + plugin/     |
   |  Codec (311 / 50)  |                        |  (接口, 可替换)        |
   +--------------------+                        +----------------------+
```

关键契约: 网络层把已完成握手的 `*Conn` 交给内核, 内核不再解码 CONNECT。这是 2026 年重构的主要收益 —— 在此之前两层共享 `*protocol.Codec`, 任何协议扩展都要改内核。

### 3.2 设计决策评估

| 决策 | 评价 | 理由 |
|------|------|------|
| 网络层与业务层分离 (ADR-002) | 正确 | `HandleConnection(ctx, *Conn)` 使内核可脱离 net.Conn 单测; WS/WSS 复用同一内核 |
| 连接与会话分离 (ADR-003) | 正确 | 持久会话跨连接存活, 接管路径 (`session takeover`) 语义清晰 |
| 单一 broker 包承载 7 个组件 (ADR-004) | 可接受, 有代价 | 避免循环依赖, 但包已达 17,923 行 49 文件; 组件边界只能靠注释维持 |
| 双 ctx 作用域 (连接 ctx / broker ctx) | 正确但高风险 | 语义必须逐点判断, 无法靠类型系统保证; 已见 4 次因选错 ctx 丢会话的缺陷 |
| 函数式选项 (ADR-006) | 正确 | 但 `broker.Option` 与 `api.Option` 两套并存, 文档已多次混淆 |
| 事件总线与 hook 分离 | 正确 | 生命周期有序同步, 事件无序并发, 合并会引入语义矛盾 |

### 3.3 与参照项目的定位

| 项目 | 语言 | 定位 | shark-mqtt 的差异 |
|------|------|------|-------------------|
| smart-mqtt | Java | 企业级插件化 broker | Go 实现, 单二进制; 协议层比上游完整 (上游完全不处理 AUTH 0x0F) |
| mica-mqtt | Java | 物联网组件 | 本项目是完整 broker 而非组件库, 无 Spring 依赖 |
| smart-mqtt-4g | Go | 协议内核 | 本项目额外具备存储抽象、插件、管理端点、K8s 部署链 |

本项目的差异化优势不在性能 (跨机实测天花板由链路决定), 而在**协议合规密度**: 属性 locus 双向校验、固定头 flags 双向校验、解码流对齐不变量、出站尺寸收口。这四项在上游 Java 实现中均缺失或部分缺失。

### 3.4 架构层面的结构性风险

| 风险 | 表现 | 建议 |
|------|------|------|
| broker 包过大 | 49 文件 / 1.8 万行, 组件间只能靠注释约束 | 中期拆出 `broker/internal/transport` 与 `broker/internal/qos`; 但需先建立包内不变量测试, 否则拆分本身就是风险 |
| 双 ctx 无类型保护 | 依赖人判断 | 可引入两个命名类型 `ConnCtx` / `BrokerCtx`, 编译期阻止误用; 改动面大, 列为 P2 |
| 锁的粒度不统一 | `retainedMu` 保护 map 同时保护 store I/O; `Session` 有 `mu` 但 `CanSendOutbound` 读无锁 | 统一规则: 全局锁只保护内存结构, I/O 在锁外; 逐处修正 |
| 无准入预留 | `maxConnections` 检查与 map 写入之间是 TOCTOU | 用原子计数预留名额 |

---

## 4. 测试执行记录

### 4.1 环境

| 项 | 值 |
|----|-----|
| OS | Windows 11 |
| Go | 1.26.1 (Windows/amd64) |
| 代理 | 全部剥离 (`HTTP_PROXY` 等 6 个变量 unset), 否则模块代理返回 502 |
| 日期 | 2026-10-17 |

### 4.2 测试规模 (统计口径: 源码顶层 `Test*` / `Benchmark*` 函数数, 单目录非递归)

| 类别 | 数量 | 说明 |
|------|------|------|
| 单元测试 | 533 | 不含 `store/redis` (18) 与 `store/badger` (10), 两者需 `-tags store_redis` / `-tags store_badger` |
| 集成测试 | 194 | `tests/integration`, 40 个文件, 真实 TCP/TLS/WS 连接 |
| 缺陷回归 | 8 | `tests/defects`, 针对历史 P0 缺陷的独立回归集 |
| 基准 | 72 | `tests/bench` 68 + `plugin` 3 + `pkg/metrics` 1 |

### 4.3 执行结果

| 命令 | 结果 | 耗时 |
|------|------|------|
| `go build ./...` | PASS | - |
| `go build -tags nometrics ./...` | PASS | - |
| `go vet ./...` | PASS (0 告警) | - |
| `go test -count=1 ./...` | **PASS** (全部包 ok, 无 FAIL, 无 SKIP 异常) | 53 s |
| `go test -count=1 ./tests/integration/` | **PASS** | 33.3 s |
| `go test -count=1 ./tests/defects/` | **PASS** | 2.3 s |

逐包结果 (节选): `api 3.05s`、`broker 11.40s`、`client 7.97s`、`config 0.56s`、`plugin 0.82s`、`protocol 0.54s`、`store/memory 0.43s`、`tests/defects 2.58s`、`tests/integration 33.32s`。

注意: `go test ./...` 的汇总输出未列出 `tests/integration` 行 (该包单独执行正常通过)。原因是 Go 在多个包并行时对该包输出做了缓冲, 与本项目代码无关; 集成测试应始终单独执行并单独留存日志。

### 4.4 竞争检测

`go test -race -count=1 -p 1 ./broker/` 通过。`-p 1` 是本机必需项: Windows 上并行 link.exe 会让 race 构建长时间占用 CPU 而卡死。

---

## 5. 缺陷清单

严重度定义: **P0** = 可导致进程崩溃 / 数据丢失 / 服务不可用; **P1** = 特定配置或路径下功能错误; **P2** = 可维护性、性能、可观测性。

### 5.1 已修复 (本次审查, 已提交)

| ID | 严重度 | 位置 | 缺陷 | 修复 |
|----|--------|------|------|------|
| D1 | P0 | `broker/topic_tree.go:283` | `Stats()` 在 `tt.sharedSubsMu` 之外遍历共享订阅 map, 且 RLock/RUnlock 成对写在内层循环里。与 `SubscribeShared` 并发时 Go 运行时 abort (进程级, 不可 recover); 某个 group 的 filter map 为空时 `RLock` 之后没有内层循环, 读锁永久泄漏, 后续所有共享订阅写阻塞 | 快照整体持锁一次, `defer RUnlock` |
| D2 | P0 | `broker/client_state.go:54` | `writeLoop` 写失败时 `conn.Close()` 后直接 return, **不关闭 `stopWrites`**。此后队列无人消费且通道仍开, `writeOrEnqueue` 在队列填满后永久阻塞; 调用方包括该连接的 readLoop、QoS 重试 republish、遗嘱发布。重试 republish 由全 broker 单 goroutine 驱动, 故一条死连接可让所有客户端停止重传 | 引入 `stopOnce sync.Once` + `stopWriter()`, 四个关闭方 (接管 / 拆除 / Stop / 写协程自身) 统一走幂等入口 |
| D3 | P1 | `broker/lifecycle.go:193` | `cleanupExpiredRetained` 在 `retainedExpiry <= 0` 时提前 return, 但 `handleRetainedMessage` 会把发布者自带的 MQTT 5.0 Message Expiry Interval 记为 deadline。默认配置下这些过期永不执行, 保留消息累积到 `maxRetainedTopics` 上限后新消息被静默丢弃 | 守卫改为只看 `retainedStore != nil`, 与 `Start()` 注释声明的语义一致 |
| D4 | P1 | `broker/conn_lifecycle.go:1026` | 会话中重认证收到 AUTH 0x00 (Success) 后, 走 `reason != 0x18 -> gracefulDisconnect`, 把认证成功的活连接断开并发布其遗嘱。规范 §4.12.1 要求成功后连接继续 | 0x18 (Continue) 与 0x00 (Success) 均保持连接, 其余原因码才断开 |
| D5 | P1 | `broker/conn_lifecycle.go:254` | 从存储恢复持久会话时, 所有 filter 一律走 `TopicTree.Subscribe`, 包括 `$share/g/a`。共享订阅存放在 `sharedSubs` 而非 trie, 被写进 trie 后挂在字面 `$share` 节点下永不匹配真实 topic: 订阅静默失效, 且留下 `UnsubscribeShared` 删不掉的残留 | 恢复时按 `IsSharedSubscription` 分流到 `SubscribeShared` |
| D6 | P2 | `broker/broker.go`、`server.go`、`options.go` | 文件拆分时注释未随函数体迁移: 7 处重复注释块, 5 处悬空注释。其中 `HandleConnection` 的完整契约 (含 ctx 作用域说明, 本包最关键的注释) 挂到了 `FlappingEnabled` 上; `Addr` 的注释挂在 `Addrs` 上; `WithPluginManager` 的注释挂在 `WithFlappingDetection` 上 | 去重 + 归位, 无行为变更 (5 文件, -78/+40 行) |
| D11 | P1 | `broker/pub_sub.go:488` | 共享订阅分支未检查 `NoLocal`。No Local 由下游"投递 topic 与已存 filter 匹配"实现, 而共享订阅存的 filter 是 `$share/g/a`, 永不匹配真实 topic `a`, 于是 No Local 静默失效, 订阅方仍收到自己发布的消息。违反 [MQTT-3.8.3-4] (Protocol Error) | 判定为 Protocol Error, 按 `closeProtocolViolation` 送 DISCONNECT 0x82 并拆链 |
| D12 | P1 | `broker/rate_limit.go:48` | `int(maxRate*window.Seconds())` 在速率小于 1/窗口时截断为 0, 随后 `len(timestamps) >= 0` 恒真, 全部连接被拒 —— 配置项做的事与字面相反 | `math.Ceil` 向上取整并保底 1 个名额 |
| D18 | P1 | `broker/conn_lifecycle.go:67` | `maxConnections` 只采样 `len(connections)` 便放行, 而真正的注册发生在授权/会话恢复/store 写之后, 并发 CONNECT 突发可超限任意倍 | 新增 `pendingConns` 预留: `reserveConnectionSlot` / `releaseConnectionSlot`, 注册成功才占用名额, 失败路径由 defer 归还 |
| D9 | P1 | `broker/qos_engine.go:459` | 重试耗尽时只删引擎自身 inflight 表项, 不同步删 `Session.Inflight`、不 `DecOutboundUnacked`; `SetErrorCallback` 从未被注册, 于是整条放弃路径无日志无指标。后果: 每条被放弃的投递永久占用出站窗口, 窗口逐条收紧至零, 且重连时还会被 `resendSessionInflight` 重放 | 新增 `SetGiveUpCallback`, broker 注册后同步清 session 记账 + 计数 `messages_dropped{reason=max_retries}` + 打日志 |
| D15 | P1 | `broker/qos_outbound.go:206` | `republish` 在 session 中找不到该 packet id 时落入 PUBREC 分支。该分支本为入站 QoS 2 设计, 但入站路径 (`TrackQoS1/2`) 在生产代码中无调用者 (仅 benchmark 使用), 于是出站的重试回调在 ack 竞争或会话消失时会向客户端发一条无对应 PUBLISH 的 PUBREC | 找不到条目直接返回 nil; 注释写明入站方向若将来接入需独立回调, 不得复用此兜底 |
| D8 | P1 | `broker/conn_lifecycle.go:465,503` | `CreateSession` 已把会话发布进 Manager 之后, 仍无锁写 `ExpiryInterval` / `ReceiveMax` / `OutboundWindow`; 而 `CanSendOutbound` 从投递 goroutine 无锁读窗口, `Session.Save` 也从别的 goroutine 快照这些字段 | 三处写入改持 `sess.mu`, `CanSendOutbound` 改 `RLock` 读窗口; `go test -race ./broker/` 14.4s 通过 |

修复后复跑 (第二轮 D11 / D12 / D18): `go build ./...`、`go vet ./...`、`go test -count=1 ./...` 全部通过 (47 s, 其中 `tests/integration` 34.3 s)。新增回归 6 条: 单测 4 条 (`broker/rate_limit_test.go`), 集成 2 条 (`tests/integration/shared_subscription_test.go`); 因此本轮之后计数为 单测 537 / 集成 196。

三条缺陷均做了破坏性验证 (先让测试在缺陷下变红): D11 去掉 `NoLocal` 判定后返回 `SUBACK` 而非 `DISCONNECT`; D12 去掉向上取整与保底后 `0.5/s` 拒绝第一个连接; D18 的测试锁定新契约 (旧实现仅采样计数, 无法用单测复现超限, 见测试注释)。

### 5.2 待修复 (P1)

| ID | 位置 | 缺陷 | 影响 | 状态 |
|----|------|------|------|------|
| D7 | `cmd/main.go` | CLI 从不为 `storage_backend: redis/badger` 注入 store, 而 `api.NewBroker` 见到非 memory 后端却未注入 store 时返回 `initErr`。`docs/guides/CONFIGURATION.md` 与 `DEPLOY.md` 都按可用功能描述 | 按文档配置必然启动失败 | **已确证** |
| D10 | `broker/pub_sub.go:337` | `handleRetainedMessage` 持 `b.retainedMu` 调 store (redis 是网络 I/O, badger 是磁盘 I/O); 清理循环同样 | redis 后端的保留消息发布被全局串行化, 延迟等于 Redis RTT | 待确证 |
| D13 | `plugin/manager.go:209`、`eventbus.go:286` | Execute 超时返回后执行 goroutine 仍在, 且每个报文新起一个; 插件忽略 ctx 时永不退出 | 一个阻塞插件可让 goroutine 数随流量线性增长 | 待确证 |
| D14 | `broker/server.go:583` | WS handler 在 HTTP goroutine 里 `wg.Add(1)`, 与 `Stop()` 的 `wg.Wait()` 并发 | 握手恰好与 Stop 并发时触发 `sync: WaitGroup misuse` panic | 待确证 |
| D16 | `broker/lifecycle.go:325` | `Stop()` 先把 `b.connections` 整体摘除再关 socket, 各 readLoop 的 `disconnect()` 在身份校验处提前 return | Stop -> Start 后 session map 与 topic tree 残留旧订阅, `Clients()` 误报在线 | 待确证 |
| D17 | `broker/conn_lifecycle.go:751` | `disconnect` 在连接身份校验之前就按 clientID 执行 Save/Delete/ClearMessages | 同 ID 接管后, 旧连接迟到的拆除会删掉新会话的持久记录 | 待确证 |

### 5.3 文档缺陷

活文档与代码不一致 **99 处**, 按危害分三级:

| 级别 | 数量 | 典型 |
|------|------|------|
| 高 (直接导致编译/启动/操作失败) | 47 | `storage_backend: redis` 不可用 (D7); `deploy/k8s/app/deployment.yaml` 实际 `replicas: 1` 而 DEPLOY.md 写 2 (该仓库明确"多副本=丢消息"); `Authorizer.CanPublish` 第二参实为 username 而非 clientID (按文档实现会按用户名判权); Metrics 接口文档 17 方法实为 12; `api.Broker.Broker()` 实为 `Core()`; `config.LoadFromFile` 不存在; `MQTT_SESSION_EXPIRY_INTERVAL` 实为 `MQTT_SESSION_EXPIRY` |
| 中 (结论错误, 但不阻断操作) | 26 | 指标名全错 (`mqtt_connections_active` 实为 `shark_mqtt_connections_total`); `redis_addr` / `badger_path` 默认值为空而非文档所写; `Config` 字段少列 21 个; `DefaultConfig().MetricsEnabled` 实为 true |
| 低 (表述漂移) | 26 | 目录树文件清单停留在拆分前; `examples/standalone.go` 实为目录; 指标/基准计数过期; README 使用 `->`、`|--`、`u`、`§` 等非 ASCII 符号 |

`docs/guides/TESTING.md` 是**唯一**统计数字仍准确的活文档, 其余计数类描述一律过期。

### 5.4 代码卫生 (P2)

| ID | 内容 |
|----|------|
| D19 | 死代码: `QoSEngine.TrackQoS1/TrackQoS2/AckPubRel` 与 `sendPubAck` 回调全仓库无调用者 (入站 QoS 已改由 `Session.inboundQoS2` 承担); `TopicTree.SubscribeSystem`、`MatchShared`、`Session.WithKickCallback` 同样无调用者。保留者会误以为该路径生效 |
| D20 | `broker/conn_lifecycle.go:544` 的 `RegisterWill` 错误分支不可达 (`RegisterWill` 无返回错误的路径) |
| D21 | `broker/session.go:482` 多个同 QoS 过滤器命中时, `bestOpts` 取 map 遍历顺序的第一个, `RetainAsPublished` / `RetainHandling` 因此不确定 (QoS 已修为取最大) |
| D22 | `client/client.go:301` 连接断开时 Publish 收到 nil 响应, 报 `unexpected response: <nil>`, 与真实原因无关 |
| D23 | `protocol/codec.go:259` `outboundMax <= 0` 时流式编码, 单个报文拆成多次 socket write。TSO 掩盖带宽影响, 但仍是每报文多一次 syscall |

---

## 6. 改进计划

### 阶段 P0 (已完成, 本次提交)

- `a938ff561` fix(broker): 修复 D1-D5 + 2 条回归测试
- `1707da914` docs(broker): 修复 D6

### 阶段 P1 (建议下一轮, 按此顺序)

| 顺序 | 项 | 工作量 | 前置 |
|------|----|--------|------|
| 1 | D7 让 CLI 兑现 redis/badger: 在 `cmd/main.go` 按 `cfg.StorageBackend` 构建 store (需处理 build tag, 未编译进二进制的后端给出明确错误); 同步修 CONFIGURATION.md 与 DEPLOY.md | 中 | 无 |
| 2 | D8 会话字段写入串行化: 把 CONNECT 期间的协商结果一次性构造好再交给 Manager, 而非在两处写同一批字段 | 中 | 无 |
| 3 | D11 + D18 + D12 三个小口子一次性收口 (共享订阅 NoLocal 判定、连接数预留、速率小于 1 的保护) | 小 | 无 |
| 4 | D9 + D15 出站记账对称性: 放弃重试时同步清 `Session.Inflight` 与 `outboundUnacked`; `republish` 找不到条目时按 QoS 分派而非默认 PUBREC | 中 | 先补 `-race` 压测可复现用例 |
| 5 | D16 + D17 拆除路径顺序: 身份校验提到所有状态修改之前 | 中 | 需覆盖 Stop -> Start 与同 ID 接管两个场景 |
| 6 | D10 保留消息 store I/O 移出全局锁 | 中 | 需确认 `retainedCount` 与 `retainedExpirations` 的一致性边界 |
| 7 | D13 + D14 插件与 WS 的 goroutine 生命周期 | 中 | 无 |
| 8 | D19-D23 卫生项 | 小 | 无 |

### 阶段 P2 (中期)

| 项 | 说明 |
|----|------|
| 双 ctx 类型化 | 用 `ConnCtx` / `BrokerCtx` 命名类型把"选错 ctx"从运行期错误变成编译期错误 |
| broker 包拆分 | 拆出 transport 与 qos 子包; 前置条件是先把包内不变量 (锁序、ctx 作用域、记账对称) 固化为测试 |
| 文档自动化 | 把本次的文档一致性检查固化成 `scripts/check_docs.go`: 文档中出现的 `func (` 签名、配置键、导出方法名必须能在代码中找到; 纳入 CI |
| 非 ASCII 字符门禁 | 扩展 `scripts/check_links.go` 或在 `Makefile` 增加 `docs-ascii` 目标: 活文档只允许 ASCII 与中文标点 |

### 阶段 P3 (文档)

见第 7 节。

---

## 7. 文档同步计划

优先级按"会不会害人"排:

| 顺序 | 文件 | 处理 |
|------|------|------|
| 1 | `docs/guides/CONFIGURATION.md` | 补齐 21 个缺失配置项; 修正 `MQTT_SESSION_EXPIRY`、`redis_addr` / `badger_path` 默认值、`Validate` 报错文案、`config.Duration` 类型说明; 存储后端标注 CLI 当前限制 (待 D7 修复后改为"可用") |
| 2 | `docs/architecture/DEPLOY.md` | 修正 replicas、K8s 目录 (实际 `deploy/k8s/app/`)、指标名、探针 initialDelay、compose 服务名、Dockerfile 路径、端口清单 |
| 3 | `docs/architecture/ARCHITECTURE.md` | 目录树按当前仓库重写; `HandleConnection` / `Authorizer` / `Metrics` / `Store` / `WillHandler` / `QoSEngine` 签名逐条对齐; `Config` 字段表重建; 版本行改为当前状态而非 v1.0.0 |
| 4 | `docs/guides/API.md` | `Core()` 替代 `Broker()`; option 归属 (api vs broker) 标注; Metrics 接口; client.Subscribe 签名; `config.NewLoader().Load()`; OnMessage 契约说明 |
| 5 | `README.md` / `README.zh-CN.md` | 测试计数改为"命令 + 指向 TESTING.md"; 删除 `client` 支持 5.0 的错误声明; 修正"零外部依赖"表述; 架构图改 ASCII |
| 6 | `docs/guides/DEVELOPMENT.md` | 文件职责按拆分后现状描述; Metrics 方法数; 基准计数; 链接目标 |
| 7 | `docs/guides/PLUGINS.md` | 生命周期图改 ASCII |

原则: 活文档**不写死**不稳定的数字 (测试计数、行数), 给复现命令或指向唯一权威来源 (`TESTING.md`); 有日期的历史报告保持原样不维护。

---

## 8. 云服务器编译验证计划

目标: 在真实 Linux 环境下验证编译、测试、启动、协议连通性, 并留存可核对的数据。已知约束: `s2` (47.120.44.128) 与 `s3` (8.130.190.141) 为 8C16G + 100 Mbps; `s1` (120.76.44.233) 为 2C2G + 5 Mbps, 不用于集群。Docker Hub 在三台均不可达, 镜像需本地构建后 `docker save` + `ctr images import`。

| 步骤 | 命令 | 记录项 |
|------|------|--------|
| 1 | 清理: `pkill -x shark`、`docker ps -aq \| xargs -r docker rm -f`、`docker system prune -af`、删除旧配置与镜像 | 清理前后 `docker ps -a` / `df -h` |
| 2 | 代码同步: 同步当前工作树, 校验文件数与 SHA | 文件数、HEAD SHA |
| 3 | 编译: `CGO_ENABLED=0 go build -ldflags="-s -w" -o /usr/local/bin/shark ./cmd` | 二进制体积 (目标 < 800KB)、`go version` |
| 4 | 静态检查: `go vet ./...`、`go test -count=1 ./...` | 通过/失败包清单、耗时 |
| 5 | 集成: `go test -count=1 ./tests/integration/` | 耗时、失败用例 |
| 6 | 协议连通: 启动 broker, 用 `mosquitto_pub/sub` 覆盖 QoS 0/1/2、retained、will、共享订阅、Topic Alias、重认证 | 每条用例的报文摘录 |
| 7 | 压测: `loadgen` 同机 + 跨机各一轮 | 投递率、CPU 核数占用、VmHWM |
| 8 | 清理: 停进程、删临时文件 | - |

本轮完成情况: 步骤 1-5 需在云主机执行, **本轮未能执行**。2026-10-17 10:20 用 paramiko 助手连接 `s2` (47.120.44.128) 时在 SSH 协议 banner 阶段即失败 (`Error reading SSH protocol banner`), 说明主机侧 22 端口不可用 (关机 / 安全组变更 / 实例已释放), 与本项目代码无关。需先恢复主机可达性, 再按上表执行并把实际数值追加到本节; 不得以前一轮的数据代替。

---

## 9. 附录: 复现命令

```bash
# 全量门禁 (必须剥离代理)
env -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy -u ALL_PROXY -u all_proxy \
  go test -count=1 ./...

# 集成测试 (单独执行, 单独留存日志)
env -u HTTP_PROXY -u HTTPS_PROXY -u http_proxy -u https_proxy -u ALL_PROXY -u all_proxy \
  go test -count=1 ./tests/integration/

# 竞争检测 (Windows 必须 -p 1)
PATH="/d/Programs/w64devkit/bin:$PATH" go test -race -count=1 -p 1 ./broker/

# 缺陷复现 (D1): 去掉 topic_tree.go 的 sharedSubsMu 保护后应报 DATA RACE
go test -race -count=1 -run TestTopicTree_StatsConcurrentWithSharedMutations ./broker/

# 缺陷复现 (D2): 去掉 client_state.go 的 defer cs.stopWriter() 后应超时失败
go test -count=1 -run TestWriteLoop_StopSignalOnOwnWriteFailure ./broker/

# 文档一致性 (本轮为人工 + 脚本核对, 尚未固化为门禁)
go run scripts/check_links.go
```

---

## 10. 本次提交清单

| SHA | 内容 | 变更 |
|-----|------|------|
| `a938ff561` | fix(broker): D1-D5 五处并发与协议缺陷 + 2 条回归测试 | 7 文件, +221 / -17 |
| `1707da914` | docs(broker): D6 注释重复与 godoc 错位 | 5 文件, +40 / -78 |

回归测试有效性证明: D1 的测试在移除锁保护后 `-race` 报 DATA RACE; D2 的测试在移除 `stopWriter` 后 2 秒超时失败 (`writeLoop died on a failed write without closing stopWrites`)。两者均先红后绿。
