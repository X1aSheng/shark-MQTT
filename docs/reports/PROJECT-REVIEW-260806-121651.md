# shark-mqtt 项目深度审查报告 (Review Round)

- 日期: 2026-08-06 12:16
- 范围: 全部 Go 源文件 + 部署清单 + CI workflow + 文档
- 方法: 3 个并行审查代理深度审查 + 本人逐文件复核核心模块 + 全量测试实证
- 基线: build OK, vet OK, gofmt 干净, 单元测试 PASS, 集成测试 PASS, benchmark PASS

## 1. 测试基线

| 检查项 | 结果 |
| --- | --- |
| go build ./... | PASS |
| go build ./examples/... | PASS |
| go vet ./... | PASS (0 问题) |
| gofmt -l . | 空 (无未格式化文件) |
| 单元测试 (14 包) | PASS: 326 PASS, 13 SKIP (Redis 未配置) |
| 集成测试 (tests/integration) | PASS: 92 PASS |
| 基准测试 (tests/bench + 组件) | PASS: 71 个 Benchmark 函数 |
| 测试日志 | logs/20260806_120538_{unit,integration,benchmark}.log |
| 覆盖率 (本地, Redis 跳过) | broker 49.0%, protocol 65.2%, client 84.5%, api 83.2%, store/memory 91.4%, store/badger 88.5%, plugin 90.6%, pkg/metrics 98.0%, pkg/bufferpool 100%; cmd/redis/examples 0% (无测试/跳过) |

说明: 与上一轮报告 (260806-034632) 相比, 单元测试计数差异 (326 vs 419) 来自测试集在 V5 修复期间增删, 以本次实测为准。store/redis 13 个测试因本地无 Redis 跳过 (CI 中已配 MQTT_REDIS_ADDR 会执行)。

## 2. 缺陷状态清单 (对照上轮 31 项 P1-1..P3-7)

### 2.1 已修复 (V5 Audit Fixes, 本轮逐项复核确认)

| # | 缺陷 | 修复证据 |
| --- | --- | --- |
| P1-1 | QoS1 重复投递 | handlePublish 入站 QoS1 不再 Track (broker.go:809-821) |
| P1-2 | QoS2 提前转发 | republish 回调重发 PUBREC 而非重路由 (broker.go:1368-1383) |
| P1-3 | Topic Alias 不可用 | 空 topic + 别名 编解码与解析均支持 (broker.go:680-706) |
| P1-4 | Client 端 QoS2 接收残缺 | readLoop 处理 PUBREL -> PUBCOMP 并清 receivedQoS2 (client.go:481-484, 561-576) |
| P2-1 | CleanSession 接管误报 SessionPresent | isResuming 只在 CleanSession=false 时判定 (broker.go:242) |
| P2-2 | Broker Stop->Start 失效 | Start 重建 b.ctx 与 qos.Start (broker.go:396-397) |
| P2-4 | Will 绕过发布鉴权 | publishWill 检查 authorizer.CanPublish (broker.go:1389-1393) |
| P2-6 | ChainAuth fail-open | 识别用户被拒即终止链, 仅 ErrUserNotFound 继续 (auth_chain.go:51-54) |
| P2-7 | SUBACK 超限谎报成功 | 超限时全部返回 SubAckFailure (broker.go:897-900) |
| P2-8 | 共享订阅收不到 retained | MatchesSubscription 剥离 $share/ 前缀 (session.go:291-298) |
| - | 停机等待 1.5x keepalive | MQTTServer.Stop 先关连接再 wg.Wait |

### 2.2 仍存在 (本轮已确认)

| # | 缺陷 | 位置 | 证据 |
| --- | --- | --- | --- |
| P1-5 | 离线持久会话无离线队列, QoS1/2 直接丢弃; messageStore 从未被读写 | broker/broker.go:1038-1041, 38 | deliverToClient 对离线会话直接 return; messageStore 无生产调用点 |
| P1-6 | Client 无 PINGREQ 保活协程 | client/client.go | 全文件仅 1 处 KeepAlive (line 108, 仅写入 CONNECT), 无 ping 定时器 |
| P2-3 | 会话持久化失效: AddInflight/RemoveInflight 仅测试调用, messageStore 忽略 | broker/session.go:352-364, broker.go:38 | 全库 grep 仅 session_test.go 调用 |
| P2-5 | Will 延迟: (a) maxWillDelay=0 上限失效, 可请求 136 年; (b) 延迟 will 永不触发 | broker/broker.go:352-356, will_handler.go | abnormalDisconnect -> TriggerWill(起定时) -> disconnect -> RemoveWill(取消定时), 定时 will 被立即取消 |
| P2-5c | 接管竞争: 旧连接异常断开会触发/删除新连接已注册的 Will | broker/broker.go:548 | RemoveWill 在连接身份检查 (562) 之前无条件执行 |
| P2-9 | 共享订阅轮询选中离线成员 -> 消息丢失 | broker/topic_tree.go:311-314, broker.go:1061-1064 | MatchShared 不筛在线成员, deliverToSharedClient 对无会话直接 return |
| P2-10 | 会话接管竞态删新 Will | broker/broker.go:548 | 同 P2-5c |
| P2-11 | Client Disconnect 后无法重新 Connect (ctx 一次性) | client/client.go:57-66, 383 | cancel 在 New 中创建, Connect 从不重建; Disconnect 后 readLoop 立即退出 |
| P2-12 | Client nextPacketID 不查在途占用, 回绕后 ACK 错配 | client/client.go:592-609 | 纯原子自增, 不检查 inflight/pending |
| P2-13 | Topic 树订阅泄漏: 断连从不 Unsubscribe | broker/broker.go:547-592 | disconnect 无任何 topics.Unsubscribe/UnsubscribeShared 调用 |
| P2-14 | ReceiveMax 满静默丢弃 QoS1 (至少一次被破坏) | broker/broker.go:1086-1092 | CanSendOutbound 失败即 drop, 无队列无重试 |
| P2-15 | receivedQoS2 去重表无界 | broker/broker.go:51, 780-785 | 先入表再 TrackQoS2; 仅 PUBCOMP/disconnect 清理; 无 TTL/eviction |
| P2-16 | DecOutboundUnacked 被滥发 PUBACK 刷成负数 | broker/broker.go:1288-1293, session.go:602-604 | AckQoS1 对未知 ID 是 no-op, 但 DecOutboundUnacked 无条件 -1 |
| P3-1 | FixedHeader.HeaderSize 从未赋值, maxPacketSize 校验偏差 1-5 字节 | protocol/packets.go:11, codec.go:165-170 | 全库 grep 无 HeaderSize 赋值; 校验退化为 RemainingLength > maxPacketSize |
| P3-2 | 剩余长度非最小字节数编码被接受 (违反 MQTT-1.5.5-1) | protocol/codec.go:172-187 | 解码循环无最小编码校验, [0x80,0x00] 表示 0 会被接受 |
| P3-3 | v3.1.1 CONNACK 仍接受多余字节 (PUBACK/UNSUBACK/DISCONNECT 已修) | protocol/connect.go:325 | decodeConnAck 无 version!=5 且 RemainingLength!=2 的严格校验; 部分修复 |
| P3-4 | retained 存储无 $ 系统主题保护, retained 投递绕过 §4.7.2 | store/{memory,redis,badger}/retained, broker.go:824 | 三个 SaveRetained 均无 $ 检查; deliverRetainedMessages 走 store.MatchRetained 绕过 TopicTree |
| P3-5 | Retained TTL 重启后失效 (retainedExpirations 仅内存) | broker/broker.go:58, 78, 884 | 重启后空, 过期 retained 永不清理 |
| P3-6 | cmd 无 Version 变量, build.sh -X main.Version 无效 | cmd/main.go:56, scripts/build.sh:5 | 版本硬编码 v1.0.0, ldflags 被静默忽略 |
| P3-7 | Windows 全量并行偶发 WSAEADDRINUSE | tests (环境问题) | 所有测试均绑定 :0 且清理正确; 属 TIME_WAIT 端口耗尽, 非代码缺陷 |

### 2.3 新发现缺陷 (本轮新增)

| # | 级别 | 缺陷 | 位置 |
| --- | --- | --- | --- |
| NEW-1 | P1 | 出站 QoS1/2 (broker->订阅者) 从不 Track/重试: doDeliver 只 IncOutboundUnacked, 订阅者不 ACK 即丢失 | broker/broker.go:1110-1120 |
| NEW-2 | P2 | publishRateTracker 非并发安全, 会话接管时新旧 readLoop 并发 Allow()/SetMaxRate() 数据竞争 | broker/rate_limit.go:65-102, broker.go:670 |
| NEW-3 | P2 | 共享订阅条目断连从不清理 (sharedSubs 泄漏, 加剧 P2-9) | broker/broker.go:547-592, topic_tree.go:237 |
| NEW-4 | P2 | retained 投递绕过 ReceiveMax 流控 (不 IncOutboundUnacked, 不查 CanSendOutbound) | broker/broker.go:1133-1183 |
| NEW-5 | P2 | maxRetainedTopics 检查 TOCTOU 竞态 (锁在两次取用之间释放) | broker/broker.go:834-843 |
| NEW-6 | P2 | disconnect 用已取消的 b.ctx 保存会话, 停机期间断连丢失状态 | broker/broker.go:552, 506 |
| NEW-7 | P3 | config.ListenAddr 空串不校验, 报 opaque 错误 | config/config.go:87 |
| NEW-8 | P3 | config.LogLevel 非法值不报错, 静默回退 info | config/config.go:87 |
| NEW-9 | P3 | 环境变量整数解析用 fmt.Sscanf, "100abc" 静默接受为 100 | config/loader.go:118-128 |
| NEW-10 | P3 | NewBroker 校验失败返回半初始化 broker (误导性非致命) | api/api.go:135-137 |
| NEW-11 | P3 | cmd 无 -config 参数, 无法用 YAML 配置启动 | cmd/main.go |
| NEW-12 | P3 | metrics registerOrReuse 对非 AlreadyRegisteredError panic | pkg/metrics/prometheus.go:40-49 |
| NEW-13 | P3 | badger MatchRetained 迭代器 PrefetchValues 默认 true, 全表扫描额外分配 | store/badger/retained_store.go:97 |
| NEW-14 | P3 | cmd log.Fatalf 跳过 deferred stop() 清理 (进程退出, 无害) | cmd/main.go:72 |
| NEW-15 | 设计 | 默认认证器为 DenyAll (fail-closed), 新用户直连会被拒; 属安全默认设计, 非缺陷 | broker/options.go:48 |
| NEW-16 | P3 | Client 并发 Connect 有 TOCTOU: 两协程同时通过 !connected 检查, 一条连接泄漏 | client/client.go:72-78, 152-156 |
| NEW-17 | P3 | Client receivedQoS2 仅 handlePubRel 清理, Disconnect/readLoop 错误路径不清理 (低水位泄漏) | client/client.go:44, 499-515 |
| NEW-18 | P3 | MQTT5 CONNACK/DISCONNECT/AUTH 属性解析后不校验 reader.Len()==0, 尾随字节被静默忽略 | protocol/connect.go:304-338, subscribe.go:399-488 |
| NEW-19 | P3 | v3.1.1 下 SUBSCRIBE 接受 MQTT5 选项位 (NoLocal/RetainAsPublished/RetainHandling 应为 0) | protocol/subscribe.go:53-54 |

## 3. 改进计划 (优先级排序)

### 优先级 1 - 协议语义 / 数据完整性 (P1)
1. **P1-5 + NEW-1**: 建立离线持久会话队列 + 出站 QoS 重试.
   - 方案: deliverToClient 对离线持久会话 (sessionStore 存在且未过期) 写 messageStore 排队; 重连后恢复出队.
   - 出站 QoS1/2 写入 sess.Inflight + qos engine Track (broker->订阅者方向), 订阅者 PUBACK/PUBCOMP 驱动 Ack.
   - 验证: 新增持久会话离线收发集成测试 (QoS1/QoS2), 全量回归 + race.
2. **P1-6**: Client 增加保活协程, 每 KeepAlive/2 发送 PINGREQ, 失败触发断连回调.

### 优先级 2 - 正确性 / 并发 (P2)
3. **P2-5 (b) + P2-10**: 延迟 will 处理重构.
   - 修正 abnormalDisconnect/disconnect 的 TriggerWill 与 RemoveWill 顺序: 延迟 will 不得被 disconnect 的 RemoveWill 取消; 接管时旧连接不得触发/删除新连接 will (identity-aware).
   - 方案: disconnect 增加参数区分 graceful/abnormal + 接管身份判定.
   - 验证: will_handler 延迟触发测试 + 接管场景 race 测试.
4. **P2-5 (a)**: maxWillDelay 默认值设为合理上限 (如 60s), 且显式配置 0 时仍应设安全上限或文档化.
5. **P2-3**: 接线会话持久化 — doDeliver 调用 sess.AddInflight, handlePubAck/PubComp 调用 RemoveInflight; sessionStore 与 messageStore 在 api.NewBroker 中真正注入.
6. **P2-9**: MatchShared 轮询跳过离线成员 (查 sessions 在线状态), 或对离线成员的消息回退到队列.
7. **P2-13 + NEW-3**: disconnect 时按会话订阅列表批量 Unsubscribe/UnsubscribeShared.
8. **P2-16**: DecOutboundUnacked 下限保护 (仅当对应 inflight 存在才减).
9. **P2-14**: ReceiveMax 满时不再静默丢弃 — 排队或降级, 至少 QoS1 不丢.
10. **P2-15**: receivedQoS2 表加每客户端上限并与 maxInflight 对齐, 增加陈旧条目清理.
11. **P2-11**: Connect 重建 ctx/cancel, 支持 Disconnect 后重连.
12. **P2-12**: client nextPacketID 检查 in-flight 占用 (参考 broker Session.NextPacketID 的查找法).
13. **NEW-2**: publishRateTracker 改为原子字段或在 session 锁内调用.
14. **NEW-5**: maxRetainedTopics 检查与写入合并到一个临界区 (已持有 retainedMu 时完成计数检查).
15. **NEW-6**: disconnect 用独立 context (如 context.Background + timeout) 保存会话, 不依赖 b.ctx.
16. **NEW-4**: retained 投递计入 outboundUnacked 并遵守 ReceiveMax.

### 优先级 3 - 边界 / 健壮性 (P3)
17. **P3-1/P3-2/P3-3**: 修正 FixedHeader.HeaderSize 赋值, 剩余长度最小字节校验, v3.1.1 严格剩余长度检查.
18. **P3-4**: retained 存储层加 $ 前缀保护 (与 TopicTree §4.7.2 对齐), deliverRetainedMessages 使用系统主题感知的匹配.
19. **P3-5**: retained TTL 持久化到 store (或重启后按存储时间戳重算).
20. **P3-6**: cmd 增加 `var Version = "dev"` 并由 build.sh 注入; banner 使用该变量.
21. **NEW-7/8/9**: config 校验 ListenAddr 非空、LogLevel 合法值, env 整数解析改 strconv.
22. **NEW-10**: NewBroker 校验失败返回 nil + error.
23. **NEW-11**: cmd 增加 -config YAML 加载.
24. **NEW-12**: registerOrReuse 捕获非 AlreadyRegisteredError 返回错误而非 panic.
25. **NEW-13**: badger MatchRetained 设置 PrefetchValues:false.
26. **NEW-16**: Client Connect 增加互斥/一次性状态机, 防止并发 Connect 双连接.
27. **NEW-17**: Client Disconnect/readLoop 错误路径清理 receivedQoS2.
28. **NEW-18**: CONNACK/DISCONNECT/AUTH 属性解析后校验 reader 无剩余字节.
29. **NEW-19**: v3.1.1 下 SUBSCRIBE 拒绝 MQTT5 选项位.
30. **P3-7**: 保持现状 (环境问题), 文档注明 Windows 下建议 -p=1.

每个缺陷修复均附回归测试, 修复后全量测试 + race 验证, 逐个提交, 并触发 GitHub Actions 检查。

## 4. 可重入性设计约束 (改进计划必须遵守)

本项目"可重入性"体现在以下维度, 后续修复不得破坏:
1. **Broker Start/Stop 可重复**: Start 必须重建 ctx/cancel (P2-2 已修复的写法是模板), 修复 P2-3/NEW-6 时保持.
2. **Client Connect/Disconnect 可循环**: 修复 P2-11 必须把 ctx/cancel 生命周期移入 Connect (而非 New), 使 Disconnect 后可再次 Connect.
3. **会话接管身份判定**: 所有按 clientID 的清理 (will, connections, topics) 必须做连接身份检查, 防止旧连接清理波及新连接.
4. **并发处理器安全**: 每连接独立 readLoop, 共享状态 (connections, sessions, topic tree, will) 均需锁; 修复 NEW-2 时给 publishRateTracker 加同步.
5. **插件/存储可替换**: Broker 只依赖接口, 新 store/plugin 不得要求全局单例.
6. **灰度/平滑重启**: retainedExpirations 等内存态应可重建, 修复 P3-5 时保持内存态可重放.

## 5. 云验证状态

- 本地无 docker/kubectl/helm, 云服务器验证另行执行 (见 DEPLOYMENT-VALIDATION-260806-*.md).
- 云服务器 120.76.44.233: 清理 shark-socket 残留进程/配置/镜像 + docker 全清, 原生编译测试, docker/k8s 部署验证.
- 本轮 CI 状态: GitHub Actions 最近 8 次运行全绿 (最后一次 push fa371f2). 已核对 ci.yml 结构无缺陷.
- 注意: git `origin` push URL 指向 gitee 而 fetch 指向 GitHub, 直接 `git push origin main` 不会触发 GitHub CI, 需显式 push 到 GitHub 或修正 remote 配置.

## 6. 结论

- 测试基线全部通过; 无编译/vet/gofmt 问题.
- 上轮 31 项中 11 项已修复 (含停机优化), 18 项仍存在 + 1 项部分修复 (P3-3), 其中 P3-7 为环境问题; 本轮另新发现 19 项.
- 剩余缺陷集中在: 离线持久会话队列 (P1-5), 出站 QoS 重试 (NEW-1), will 延迟/接管语义 (P2-5/P2-10), 会话持久化接线 (P2-3), 订阅泄漏 (P2-13/NEW-3), 流控边角 (P2-14/16, NEW-4), 以及一批 P3 健壮性项.
- 本报告仅确认+出计划, 未改动生产代码; 改进按第 3 节优先级逐个实现并提交.
