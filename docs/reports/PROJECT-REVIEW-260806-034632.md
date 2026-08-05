# shark-mqtt 项目深度审查报告

- 日期: 2026-08-06 03:46
- 范围: 全部 105 个 Go 源文件 + 部署清单 + CI
- 方法: 2 个并行审查代理深度审查 + 本人逐文件复核 + 复现测试实证
- 基线: build OK, vet OK, 单元测试 PASS, 集成测试 PASS, race 干净
- 已知波动: 全量并行测试偶发 Windows 临时端口竞争 (WSAEADDRINUSE), -p=1 稳定

## 1. 测试基线

| 检查项 | 结果 |
| --- | --- |
| go build ./... | PASS |
| go vet ./... | PASS |
| go test ./... (全部 16 包) | PASS (偶发端口竞争, -p=1 稳定) |
| race 检测 (CGO=1) | PASS, 0 data race |
| 脚本测试器 unit/integration | PASS |

日志: logs/ 下 20260806_* 日志

## 2. 缺陷清单 (按严重程度, 已去重)

### P1 - 核心协议语义损坏

| # | 缺陷 | 位置 | 实证 |
| --- | --- | --- | --- |
| P1-1 | 每条 QoS1 入站消息被重试 republish 向订阅者重复投递 (默认最多 4 次) | broker/qos_engine.go:328-393, broker.go:807 | 已复现: 1 次发布收到 3 次 (maxRetries=2) |
| P1-2 | QoS2 消息在 PUBREL 握手完成前被 doRetry 提前转发 → 双重投递, 恰好一次被破坏 | broker/qos_engine.go:328-393 | 代码级确认 |
| P1-3 | MQTT 5.0 Topic Alias 完全不可用: 空 TopicName PUBLISH 被 codec 拒绝, broker 别名分支是僵尸代码 | protocol/publish.go:14, topic.go:7-9 | 已复现 |
| P1-4 | Client 端 QoS2 接收残缺: readLoop 无 PubRel 分支, receivedQoS2 只增不删 → QoS2 消息黑洞 + 泄漏 | client/client.go:472-553 | 代码级确认 |
| P1-5 | 离线持久会话 QoS1/2 消息直接丢弃 (无离线队列); 出站 QoS 无重传; messageStore 形同虚设 | broker/broker.go:1027-1112 | 代码级确认 |
| P1-6 | 客户端无 PINGREQ 协程, 空闲超过 1.5x KeepAlive 被 broker 断连 | client/client.go:72-163 | 代码级确认 |

### P2 - 重要

| # | 缺陷 | 位置 |
| --- | --- | --- |
| P2-1 | CleanStart/CleanSession=1 会话接管仍返回 SessionPresent=1 | broker/broker.go:240, 371 |
| P2-2 | Broker Stop 后重新 Start 坏掉 (b.ctx 不重建, 清理/QoS 重试静默失效) | broker/broker.go:388-398, 500-531 |
| P2-3 | 会话持久化整体失效: AddInflight 从未被调用, StorageBackend 配置被忽略 | broker/session.go:367-433, api/api.go |
| P2-4 | Will 消息绕过发布鉴权 (ACL 绕过) | broker/broker.go:1379-1400 |
| P2-5 | Will 延迟: maxWillDelay=0 时上限失效 (可请求 136 年); 延迟内重连不取消; 接管竞争误发 | broker/broker.go:349-354, will_handler.go |
| P2-6 | ChainAuth 认证链 fail-open: 权威认证器拒绝凭证后被宽松兜底放行 | broker/auth_chain.go:30-45 |
| P2-7 | 超过 maxTopicFiltersPerSub 时 SUBACK 全发成功码 (谎报订阅成功) | broker/broker.go:887-900 |
| P2-8 | Retained 消息从不投递给 $share 共享订阅者 | broker/broker.go:950-963, 1137-1144 |
| P2-9 | 共享订阅轮询可能选中离线成员 → 消息丢失 | broker/topic_tree.go:273-323 |
| P2-10 | 会话接管竞态: 旧连接清理删掉新连接注册的 Will | broker/broker.go:542-561 |
| P2-11 | Client Disconnect 后无法重新 Connect (ctx 一次性) | client/client.go:57-66, 364-403, 440-445 |
| P2-12 | Client nextPacketID 不查 ID 占用, 回绕后 ACK 错配 | client/client.go:569-586 |
| P2-13 | Topic tree 订阅泄漏: 断连从不 Unsubscribe, 长跑内存/性能劣化 | broker/broker.go:542-587 |
| P2-14 | 订阅方 ReceiveMax 满时静默丢弃 QoS1 (至少一次被破坏) | broker/broker.go:1076-1082 |
| P2-15 | receivedQoS2 去重表无界, 恶意客户端可撑大 | broker/broker.go:774-780 |
| P2-16 | DecOutboundUnacked 可被滥发 PUBACK 刷成负数, 绕过流量控制 | broker/broker.go:1278-1322 |

### P3 - 边界

| # | 缺陷 | 位置 |
| --- | --- | --- |
| P3-1 | FixedHeader.HeaderSize 从未赋值, maxPacketSize 校验偏差 1-5 字节 | protocol/codec.go:53, packets.go:11 |
| P3-2 | 剩余长度非最小字节数编码被接受 (违反 MQTT-1.5.5-1) | protocol/codec.go:173-187 |
| P3-3 | v3.1.1 下 CONNACK/PUBACK 接受多余字节 | protocol/connect.go:325 |
| P3-4 | Retained store (memory/redis/badger) 缺 $ 系统主题保护 | store/{memory,redis,badger}/*.go |
| P3-5 | Retained TTL 重启后失效 (retainedExpirations 在内存) | broker/broker.go:472-494, 878-881 |
| P3-6 | cmd/main.go 无 Version 变量, build.sh -X main.Version 无效; 版本硬编码 v1.0.0 | cmd/main.go, scripts/build.sh |
| P3-7 | 全量并行测试偶发 Windows 临时端口竞争 (WSAEADDRINUSE) | tests/integration 等 |

## 3. 改进计划 (优先级排序)

1. **P1-1/P1-2** QoSEngine republish 语义重设计: 入站 QoS1 不再 Track; QoS2 只在 PUBREL 后路由; 重试改为重发 PUBACK/PUBREC 而非重路由订阅者
2. **P1-3** Topic Alias: decodePublish 允许空 topic (v5 且带别名); codec 按协议版本放宽
3. **P1-4** Client readLoop 增加 PubRel 分支, receivedQoS2 清理
4. **P1-5** 出站 QoS 写入 sess.Inflight + 离线队列
5. **P1-6** Client 增加 PINGREQ 保活协程
6. **P2-1** isResuming 结合 CleanSession
7. **P2-2** Broker Start 重建 b.ctx
8. **P2-4/P2-5** Will 鉴权 + 延迟修复
9. **P2-6** ChainAuth fail-closed
10. **P2-7** SUBACK 超限返回失败码
11. **P2-8/P2-9** 共享订阅 retained + 轮询跳过离线成员
12. **P2-3/P2-13** 持久化接线 + 断连清理订阅
13. 其余 P2/P3 逐个处理

每个缺陷的修复均附回归测试, 修复后全量测试 + race 验证, 逐个提交。
