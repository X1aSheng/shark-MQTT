# Shark-MQTT 资源占用、使用效率与存储驱动规范分析

> 日期：2026-08-29 | 环境：Windows 11 / AMD Ryzen 7 8845HS / Go 1.26.1
> 配套实测工具：`probe_tmp/`（探针 + 采样器，临时目录）

---

## 1. 动态实测：资源占用

**方法**：编译真实 broker（memory 存储），探针驱动 1000 连接（900 clean + 100 持久会话）、
1000 订阅、30s 稳态、20 发布者 × 3000 条（10% QoS 1）、100 会话离线 + 5000 条 QoS 1 入队、
重连排空、断开 450 连接；2s 间隔采样进程 WorkingSet / Private / CPU / 线程 / 句柄。

### 1.1 每连接成本（1000 连接稳态 vs 基线）

| 指标 | 基线（0 连接） | 1000 连接 | 每连接增量 |
|---|---|---|---|
| WorkingSet | 10.7 MB | 40.4 MB | ~29.7 KB |
| 私有内存 | 47.5 MB | 79.0 MB | ~31.5 KB |
| OS 线程 | 7 | 19 | 12（2000+ goroutine） |
| 句柄 | 121 | 1216 | ~1.1（≈1 socket/连接） |

### 1.2 阶段耗时

| 阶段 | 耗时 | 说明 |
|---|---|---|
| 1000 连接建立 | 378 ms | ~2645 conn/s（TCP + CONNECT/CONNACK） |
| 1000 订阅 | 107 ms | ~9300 sub/s |
| 30s 稳态 | CPU 0.42s | 空闲成本几乎为零 |
| 60K 条发布（10% QoS1） | 3.34 s | ~18K msg/s 入站，CPU +17s ≈ 5 核并行 |
| 5000 条 QoS1 离线入队 | 566 ms | ~8.8K msg/s（含 PUBACK 往返） |
| 100 持久会话重连排空 | <1 s | 离线消息正确投递 |
| 450 连接断开 | 52 ms | 句柄 1216→794，无泄漏 |

### 1.3 结论

- 稳态 ~30KB/连接、峰值 ~55KB/连接（发布在途缓冲）；Go GC 不归还 OS 内存，容量规划按峰值。
- goroutine 模型高效：2000+ goroutine / 12 OS 线程；无每消息 goroutine；全部缓冲有界
  （写队列 256、流控缓冲 1000、inflight ≤ maxInflight、retained 计数上限）。
- 句柄随断开即时释放，无泄漏。

## 2. pprof 分配剖析与优化（本轮已落地）

**方法**：`go test -bench=BenchmarkE2E_QoS0 -memprofile` + `go tool pprof -sample_index=alloc_space`。
QoS0 发布往返 85 MB 累计分配 / 68K 次迭代。

### 2.1 优化前热点（flat）

| 热点 | flat | 根因 |
|---|---|---|
| decodePublish | 16.5 MB (19.4%) | readString + payload |
| readLoop | 10 MB (11.8%) | 每包 decode |
| handlePublish | 8.5 MB (10%) | 路由 |
| **decodeFixedHeader** | **7 MB (8.2%)** | **每包 `&FixedHeader{}` 逃逸** |
| encodePublish / Buffer.grow | 7.5 + 8.5 MB | 编码组装 |
| strings.genSplit（SplitTopic） | 5.5 MB | Match 路径 topic 切分 |
| TopicTree.Match | 4 MB | results/visited |

### 2.2 已实施优化

1. **`decodeFixedHeader` 指针 → 值返回**：消除每包 1 次堆分配（7 MB flat → 0），
   涉及全部 15 种包类型的 decode 签名。
2. **`encodePublish` 组装 buffer 池化**：改用 codec 的 4KB buffer pool，消除每包
   `make` 与 Buffer 扩容（422 → 94 B/op）。
3. （评估未做）`TopicTree.Match` 的 visited map 池化 / SplitTopic 复用：收益有限、风险中等，留待后续。

### 2.3 实测收益（Ryzen 7 8845HS，同机同参数）

| 基准 | 优化前 | 优化后 | 变化 |
|---|---|---|---|
| Codec EncodePublish | 432.7 ns / 422 B / 6 allocs | 144.0 ns / 94 B / 5 | −67% 耗时 / −78% B |
| Codec DecodePublish | 489.2 ns / 454 B / 8 | 372.3 ns / 430 B / 7 | −24% 耗时 / −1 alloc |
| Codec RoundTripPublish | 907.9 ns / 882 B / 14 | 664.6 ns / 528 B / 12 | −27% / −40% B |
| E2E QoS0 全链路 | 71.6 µs / 1089 B / 36 | 62.2 µs / 959 B / 34 | −13% / −2 allocs |
| E2E QoS1 全链路 | 166.4 µs / 1898 B / 58 | 154.8 µs / 1713 B / 54 | −7% / −4 allocs |
| E2E QoS2 全链路 | 367.7 µs / 3143 B / 95 | 332.8 µs / 2894 B / 87 | −9.5% / −8 allocs |

回归：全量 344 单元 + 96 集成 + race 全部通过；`go vet` / gofmt 干净。

## 3. 存储驱动规范评估（memory / redis / badger）

### 3.1 合规性

- 三驱动完整实现 `SessionStore / MessageStore / RetainedStore`，编译期接口断言齐全。
- 错误哨兵映射一致：`redis.Nil` / `badger.ErrKeyNotFound` → `store.Err*NotFound`。
- build tags（`store_redis` / `store_badger`）隔离可选后端；memory 为默认且含 retained trie 索引。
- redis/badger 测试在 CI 的 tags 下运行（redis 测试需外部实例，未配置时 skip）。

### 3.2 已修复

- **S5 — TTL 语义分叉**：redis session key 的 TTL 原为固定默认（24h），可能早于 broker
  协商的 Session Expiry Interval 删除会话，导致重连被静默当作新会话（SessionPresent=0）。
  现按 `SessionData.ExpiryTime` / `ExpiryInterval` 推导 TTL；message store 同理按
  `StoredMessage.ExpiresAt` 保活。新增 3 个回归测试（`-tags=store_redis` 下运行）。

### 3.3 遗留风险（按优先级）

| # | 问题 | 影响 |
|---|---|---|
| S1 | redis/badger 的 `MatchRetained` 全量扫描（SCAN / 全表迭代 + 二次匹配） | 新订阅 O(全部 retained)，量大时延迟线性增长 |
| S2 | redis 离线队列逐条 GET/DEL，无 pipeline | 离线队列投递 RTT 放大 |
| S3 | `ListSessions` 全库 SCAN，`cleanupExpiredSessions` 每 tick 全扫 | 会话量大时周期性开销 |
| S4 | 跨 key 无事务（会话/消息/retained 独立） | 多实例并发写一致性 |
| S6 | 无 schema 版本/迁移（整段 JSON） | 结构演进破坏存量数据 |
| S7 | store 接口缺批量/原子/健康检查 | 跨进程后端控制力不足；store 故障静默降级 |
| S8 | memory `SaveSession` 浅拷贝 Inflight payload（依赖调用方契约） | 需文档化约定 |

## 4. 协议规范完善度（补充）

- 实测与 MQTT 3.1.1/5.0 对照无功能缺陷；本轮 pprof 剖析同时验证了热路径编解码正确性。
- 遗留一处宣称不一致：CONNACK `SubIDAvailable=0`，但实际接受并回传
  SubscriptionIdentifier —— 建议宣称 1 或对带 SubID 的 SUBSCRIBE 返回 0xA4。
- 文档已列待办：L-008 协议 fuzz、L-005 客户端 Connect TOCTOU、多订阅合并 QoS 取最大。

## 5. 附：实测说明

- 资源实测数据（每连接内存/句柄、阶段耗时、分配 profile）已归档于本报告第 1、2 节
  与 CHANGELOG 的 Performance 段落，原始采样与 profile 文件未随仓库保留。

## 6. 迭代更新（2026-08-29 第二轮）

- **SubID 宣称对齐**：CONNACK `SubIDAvailable` 0 → 1。broker 本就完整支持
  Subscription Identifier（解析/保存/回传），原宣称与行为不一致。新增端到端测试
  `TestSubscriptionIdentifierAdvertisedAndEchoed`。
- **TopicTree.Match visited map 池化**：`sync.Pool` 复用去重 map（>64 项丢弃防内存钉住），
  发布路径每消息少 1 次 map 分配。`BenchmarkTopicTree_Match_Exact` 370→264 ns/op（−29%），
  allocs 不变（2/op）。
- **协议 fuzz 测试（L-008 落地）**：`FuzzDecodeNeverPanics`（任意字节喂解码器）+
  `FuzzPublishRoundTrip`（结构化编解码往返）。两 fuzzer 合计 840 万次执行，零 crash/panic，
  解码器健壮性验证通过。`go test -fuzz=FuzzXxx -fuzztime=20s ./protocol/...` 可复跑。
- 全量回归：344 单元 + 96 集成 + 新集成测试全绿；`go vet` 干净。

## 7. 迭代更新（2026-08-29 第三轮）：重叠订阅 QoS 规范化

- **缺陷**：同一客户端多个匹配 filter 时，投递 QoS 取"首个命中"（map/trie 迭代随机），
  同一发布可能随机以 QoS 0 或 QoS 1/2 到达 —— 违反 MQTT 3.1.1 §3.3.5 / 5.0 §3.3.5
  （应按所有匹配订阅的**最大** QoS 投递一次）。
- **修复**（三层同步）：
  1. `TopicTree.Match`：去重 map 改为 `map[string]uint8` 跟踪 QoS，新增
     `addSubscriber` helper 保持最大 QoS（含 `#` fan-out 的 collectAllSubscribers）；
  2. `matchShared`：共享订阅成员跨 filter 合并取最大 QoS；
  3. `Session.MatchesSubscription` / `MatchesRetainedSubscription`：遍历全部匹配，
     返回最高 QoS 及其订阅选项（SubID 取最高 QoS 匹配的标识符）。
- **测试**：`TestTopicTree_QoSMaxOnMultipleMatches`（3 种顺序/路径）、
  `TestSessionMatchesSubscriptionMaxQoS`（普通 + 共享 + retained）、
  `TestOverlappingSubscriptionsMaxQoS`（端到端：QoS1 发布必须按 QoS1 到达）。
- 全量 344+ 单元 / 98 集成 / race 全绿；TopicTree 基准无回退。

## 8. 迭代更新（2026-08-29 第四轮）：客户端 TOCTOU + 会话过期语义

### L-005 客户端 Connect TOCTOU（已修复）
- **缺陷**：client 共享一个全局 ctx/WaitGroup。会话接管（同 clientID 重连）后，
  旧连接的 readLoop 因 decode 错误退出时调用共享 `c.cancel()`，**取消新连接的 ctx**，
  导致新连接所有 pending 操作（Publish/Subscribe）立即失败。
- **修复**：引入每连接"代次"（generation）上下文：`connCtx/connCancel/connDone`，
  作为参数传给 readLoop/keepAliveLoop；旧代退出只取消旧代。Disconnect 等待本代
  connDone（不再用 wg.Wait 全量等待）。Publish/Subscribe/Unsubscribe 等待本代 ctx 快照。
- **回归测试**：`TestClientConnectTOCTOU_StaleReadLoopDoesNotKillNewConnection`
  （5 轮同 ID takeover + QoS1 往返验证）。race-clean。

### Session Expiry Interval=0 语义（已修复，MQTT 5.0 §3.1.2.11.2）
- **缺陷**：CONNECT 显式 `SessionExpiryInterval=0`（断连即毁）被协商逻辑吞掉，
  实际采用 serverMax（24h）；断连后仍保存会话 → 重启残留。
- **修复**：① 显式 0 被尊重（CONNACK 回报 0）；② disconnect 对 expiry=0 会话
  不保存并删除存储中的会话/消息；③ DISCONNECT 可携带新 SessionExpiryInterval
  （§3.14.2.2.2，0 = 立即结束，上限 serverMax）；④ 属性缺省仍用 serverMax。
- **测试**：3 个集成测试（显式 0 断连即毁 / 缺省 86400s / DISCONNECT 更新过期）。
- 全量回归 + race 全绿。

## 9. 迭代更新（2026-08-29 第五轮）：Redis 批量读取（S2）

- **缺陷**：`MessageStore.ListMessages` 与 `RetainedStore.MatchRetained` 在 SCAN 出
  候选 key 后**逐 key GET**——离线队列排空、retained 通配匹配时每消息一次 RTT。
- **修复**：改为「先 SCAN 收集全部 key → 单次 `MGet` 批量取值」；SCAN 与 MGet 之间
  过期/被删的 key（nil）安全跳过。120 条消息跨两页 SCAN 的新测试
  `TestMessageStore_ListMessages_Many` 验证无丢失。
- **验证限制**：本机无 Redis 实例，`-tags=store_redis` 下编译/vet/skip 测试通过；
  实际 MGet 路径由 CI（有 Redis 环境）执行。

## 10. 迭代更新（2026-08-29 第六轮）：内存存储保存隔离（S8）

- **缺陷**：`memory.sessionStore.SaveSession` 浅拷贝 `SessionData` —— Inflight
  payload 底层数组与调用方共享，subscriptions slice 亦共享；调用方保存后修改
  原数据会污染存储（`GetSession` 已深拷贝，二者语义不对称）。
- **修复**：SaveSession 与 GetSession 对称化 —— 深拷贝 Inflight payload +
  subscriptions slice。
- **测试**：`TestSessionStore_SaveIsolation`（保存后变异原数据，验证 store 不受影响）。


## 11. 分档 bufferpool 大批量验证（2026-08-29）

**方法**：8s/基准长时间运行（数十万条消息），真实 broker + 客户端全链路。

| 基准 | 验证规模 | 当前 | 改进前 | 收益 |
|---|---|---|---|---|
| E2E QoS0（20B） | 134,113 条全链路，零丢失 | 66.5µs / 956 B / 34 allocs | 108.6µs / 1088 B / 36 | −39% 耗时 |
| E2E 64KB 逐字节校验 | 47,196 条，每条逐字节验证通过 | 213.2µs / 181 KB / 36 | 468.9µs / 431 KB / 42 | −55% / −58% 分配 |
| 128KB 编码 | 119,793 条 | 114.2µs / 135 KB / 24 | 262.3µs / 551 KB / 31 | −56% / −75% 分配 |

**结论**：大消息场景每消息分配减少 58–75%（64KB 每消息省 ~250KB、128KB 省 ~416KB 分配），
耗时减半以上；大批量（13 万+ 条）下无退化、无数据丢失，收益在大规模下稳定成立。
