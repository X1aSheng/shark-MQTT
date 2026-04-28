# Shark-MQTT 完整代码审查报告

> **审查日期**: 2026-04-28  
> **审查范围**: 全部 87 个 Go 源文件（broker/, protocol/, store/, pkg/, plugin/, client/, api/, errs/, cmd/, tests/, testutils/, scripts/, examples/, config/）  
> **审查方法**: 4 个并行审查代理，每个代理独立深度审查一个子系统

---

## 审查结果总览

| 严重级别 | 数量 | 说明 |
|----------|------|------|
| **CRITICAL** | 12  | 数据竞争、协议违规、goroutine泄漏、死锁风险 |
| **HIGH** | 18  | 并发安全、资源泄漏、测试不稳定、安全加固 |
| **MEDIUM** | 28  | 错误处理、性能优化、测试覆盖、代码质量 |
| **LOW** | 19  | Go idioms、未使用代码、文档改进 |
| **合计** | **77** | |

---

## CRITICAL 缺陷（12项）

### C1. 共享可变 Codec 导致数据竞争 — server.go:52, 177

`MQTTServer` 创建一个 `*protocol.Codec` 实例，所有连接共享。`Codec.protocolVersion` 在 `decodeConnect` 中被修改，多连接并发时产生数据竞争。

```go
// server.go:52 — 全局共享的 codec
s.codec = protocol.NewCodec(cfg.MaxPacketSize)
// server.go:177 — 传递给每个连接
s.handler.HandleConnection(conn, s.codec, tlsState)
```

**修复**: 传递 `nil`，让 `HandleConnection` 自行创建独立 codec（broker.go:105-107 已有此逻辑）。

---

### C2. QoS 重试循环发送错误的确认方向 — qos_engine.go:303-348

`doRetry` 在重试时错误地发布 PUBACK 给 publisher（应为 PUBREL/PUBREC 序列）。QoS 2 的 StateSent 阶段发 PUBACK 而非 PUBREC，混淆了入站和出站 QoS 状态机。

---

### C3. 连接生命周期双重断开 — broker.go:187-192, 259-310

`HandleConnection` 和 `readLoop` 各调用一次 `disconnect()`，导致双重删除 maps、双重移除 will、TCP 连接未关闭。

---

### C4. WillHandler goroutine 泄漏 — will_handler.go:71-101

每个延时 will 消息创建一个 goroutine，无上限增长。`Stop()` 不等待 goroutine 退出，shutdown 时可能使用已释放资源。

---

### C5. CONNECT 包无 MQTT 3.1.1 协议验证 — broker.go:103-193

未验证：协议名、协议版本、保留标志位、Will QoS 合法性、零长度 ClientID + CleanSession=0、密码需要用户名。

---

### C6. 会话接管竞争 — broker.go:160-163, server.go:166-174

新连接覆盖 `b.connections[clientID]` 时，旧连接未关闭。旧连接的 `readLoop` 继续运行，最终调用 `disconnect` 清除新连接的会话。

---

### C7. readVarInt 无上限迭代 — protocol/codec.go:224-239

`readVarInt` 无限循环接收恶意 `0x80` 字节。MQTT 5.0 规定最多 4 字节，此处未检查。`properties.go:489` 存在相同问题。

---

### C8. writeString 静默截断 — protocol/codec.go:192-203

字符串长度超过 65535 时，`uint16(len(s))` 截断产生畸形包，下游解析将级联错误。

---

### C9. 无 MQTT UTF-8 验证 — protocol/codec.go:176-189

未检查空字符 U+0000、无效 UTF-8 序列。违反 MQTT 3.1.1 Section 1.5.3。

---

### C10. Broker Drain 循环传入错误键类型 — broker.go:207-220

`for _, id := range b.connections` 遍历 `clientState` 值，却将 `id.conn.RemoteAddr().String()` 传给 `InflightCount()`，后者需要 `clientID` 字符串，永远返回 0。

---

### C11. QoS 2 在 PUBCOMP 前交付给订阅者 — broker.go:362-392

收到 QoS 2 PUBLISH 后，立即 `topics.Match()` + `deliverToClient()`，未等待 PUBCOMP 完成。违反 MQTT-3.3.1-7。

---

### C12. 客户端 QoS>0 发布在连接丢失后永久阻塞 — client/client.go:389-431

readLoop 退出后，`respCh` 无消费者，QoS 1/2 的 `Publish()` 在 `select { case <-respCh }` 永久阻塞。

---

## HIGH 缺陷（18项）

### H1. context.Background() 替代 broker context — broker.go:353,357,486

Retained store 操作使用 `context.Background()`，shutdown 时可能阻塞。

### H2. TopicTree 无压缩机制 — topic_tree.go:106-123

订阅/取消订阅后累积空节点，长期运行内存无界增长。

### H3. 潜在重入死锁 — topic_tree.go:77-86

`Match` 持有 `RLock` 递归调用 `matchNode`，外部 store 回调可能触发重入死锁。

### H4. WillHandler goroutine 无 WaitGroup 追踪 — will_handler.go:88-95

### H5. Server 共享 codec — server.go:52（同 C1）

### H6. 空 ClientID 未按规范处理 — broker.go:157

零长度 ClientID + CleanSession=0 应拒绝连接，未实现。

### H7. writePacket 绕过写锁 — broker.go:521-525

readLoop 的 `writePacket` 不获取 `cs.wmu`，与 `writePacketTo` 存在写竞争。

### H8. Prometheus QoS 标签潜在基数爆炸 — pkg/metrics/prometheus.go:148-153

`string(rune('0' + qos))` 对无效 QoS 值产生意外标签。

### H9. Plugin dispatch 无超时/panic 恢复 — plugin/manager.go:63-74

插件 panic 杀死连接处理 goroutine，阻塞连接处理。

### H10. skipPropertyValue 错误回退 — protocol/properties.go:293-325

未知属性 ID 猜测为 VarInt 跳过，反序列化后续内容。

### H11. Badger ClearMessages 迭代器在 Update 事务内 — badger/message_store.go:112-132

LSM 树并发冲突风险。

### H12. Redis replacePlus 使用错误 glob 模式 — redis/retained_store.go:109-143

`[!/]` 匹配 `!` 或 `/`，应使用 `[^/]`。

### H13. Memory store GetSession 返回可变指针 — memory/memory.go:33-41

返回 map 内部数据指针，调用方可无锁修改 session 数据。

### H14-H18. 测试不稳定性（详见下方测试部分）

---

## MEDIUM 缺陷（28项）

<details>
<summary>展开查看完整列表</summary>

| ID | 文件 | 行号 | 说明 |
|----|------|------|------|
| M1 | broker/broker.go | 274-278 | Keep-alive 在首个包之前未生效 |
| M2 | broker/options_server.go | 13-30 | WriteQueueSize/BufferSize 已定义但未使用 |
| M3 | broker/broker.go | 314-326 | MQTT 5.0 ReasonCode 用于 3.1.1 PUBACK |
| M4 | broker/session.go | 83-123 | Session 创建与 disconnect 清理存在竞争窗口 |
| M5 | broker/broker.go | 39,70 | Broker.codec 字段已定义但从未使用 |
| M6 | broker/topic_tree.go | 77-86 | matchNodeWithSys 定义但从未调用（$SYS保护未生效） |
| M7 | broker/broker.go | 350-360 | Retained 消息无数目/大小限制 |
| M8 | broker/broker.go | 234-238 | Plugin dispatch 无超时 |
| M9 | broker/broker.go | 115,132 | 拒绝原因标签可能导致高基数 |
| M10 | broker/session.go | 246-276 | Session.Save 在 disconnect 路径从未调用 |
| M11 | protocol/publish.go | 38-44 | 属性解析失败静默回退 |
| M12 | protocol/codec.go | 29-31 | maxPacketSize 检查未包含固定头 |
| M13 | config/loader.go | 53-62 | TOML 声称支持却使用 YAML 解析器 |
| M14 | config/config.go | 110-119 | TLSConfig 未设置 MinVersion（默认 TLS 1.0） |
| M15 | store/memory/memory.go | 26-30 | SaveSession 未深拷贝 Inflight map |
| M16 | store/redis/session_store.go | 78-98 | keyPrefix 为空时 SCAN 遍历整个 Redis |
| M17 | errs/errors.go vs broker/auth.go | - | 两处定义相同语义的错误 sentinel |
| M18 | cmd/main.go | 48 | AllowAllAuth 无警告日志 |
| M19 | tests/* | - | assertNoMessage 使用 150ms 超时（5处） |
| M20 | tests/integration/edge_case_test.go | 204-205 | TestMaxConnections 使用 50ms sleep |
| M21 | tests/integration/persistent_session_test.go | 393-432 | OfflinePublish 测试名误导 |
| M22 | tests/integration/persistent_session_test.go | 510 | KickPrevious 使用 100ms sleep |
| M23 | tests/bench/data_delivery_bench_test.go | 350-377 | E2E Will 消息竞态 |
| M24 | testutils/helpers.go | 130-136 | GenerateClientIDs 全部返回相同 ID（dead code） |
| M25 | testutils/mock_store.go | - | 不检查 context 取消 |
| M26 | tests/integration/sharksocket/adapter.go | 56 | Stop(nil) 传递 nil context |
| M27 | examples/sharksocket/main.go | 39 | Stop(nil) 调用 |
| M28 | tests/integration/deploy_test.go | - | 测试脆弱依赖具体字符串格式 |

</details>

---

## LOW 缺陷（19项）

<details>
<summary>展开查看完整列表</summary>

| ID | 文件 | 行号 | 说明 |
|----|------|------|------|
| L1 | broker/session.go | 212-221 | Packet ID 回绕无冲突检查 |
| L2 | 多个文件 | - | 命名返回值和错误变量遮蔽 |
| L3 | pkg/bufferpool/ | - | BufferPool 存在但未使用 |
| L4 | protocol/topic.go | 34-44 | SplitTopic 产生空字符串段 |
| L5 | broker/server.go | 32 | net.Conn 作为 map key（文档缺失） |
| L6 | broker/auth_file.go | 24-26 | 密码以明文 string 存储 |
| L7 | client/options.go | 20-21 | WriteBufferSize 未使用 |
| L8 | client/client.go | - | 无自动 keep-alive/PINGREQ |
| L9 | broker/session.go | 279-319 | Restore() 未设置时间戳 |
| L10 | broker/broker.go | 345 | AuthFailures 用于授权失败 |
| L11 | broker/broker.go | 150 | ConnAckBadUsernameOrPassword 用于所有 auth 失败 |
| L12 | api/api.go | 251 | Health server 失败静默 |
| L13 | config/loader.go | 125-131 | 环境变量负值转换错误 |
| L14 | tests/bench/micro_bench_test.go | - | string(rune(i)) 产生不可读字符 |
| L15 | scripts/parse_test_log.go | 81 | Scanner 缓冲区溢出风险 |
| L16 | scripts/run_tests.go | - | 多处错误返回静默丢弃 |
| L17 | tests/integration | - | 无 TLS 集成测试 |
| L18 | tests/integration | - | 无认证流程测试 |
| L19 | tests/integration | - | 无 MQTT 5.0 特性测试 |

</details>

---

## 修复优先级

### 第一批: CRITICAL (必须立即修复)
1. C1 - 共享 Codec 数据竞争
2. C10 - Drain 循环错误键
3. C7 - readVarInt 无限循环
4. C8 - writeString 静默截断
5. C9 - UTF-8 验证
6. C5 - CONNECT 协议验证
7. C3 - 连接生命周期双重断开
8. C6 - 会话接管竞争
9. C7+C8 汇总: protocol 层安全加固
10. C4 - WillHandler goroutine 泄漏
11. C2 - QoS 重试错误方向
12. C11 - QoS 2 提前交付
13. C12 - 客户端 QoS 发布永久阻塞

### 第二批: HIGH
### 第三批: MEDIUM
### 第四批: LOW + 测试改进

---

## 测试不足总结

| 缺失测试 | 严重度 |
|----------|--------|
| Auth Chain 单元测试 | MEDIUM |
| Auth File 单元测试 | MEDIUM |
| 并发会话接管测试 | HIGH |
| 优雅关闭测试 | MEDIUM |
| TLS 集成测试 | HIGH |
| 认证流程测试 | HIGH |
| MQTT 5.0 特性（Topic Alias 等）| MEDIUM |
| 消息过期测试 | MEDIUM |
| session state machine 非法转换 | MEDIUM |

---

> **下一步**: 见 `FIX_PLAN_01_CRITICAL.md` 开始第一批修复。
