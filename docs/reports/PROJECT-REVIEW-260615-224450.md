# Shark-MQTT 深度设计审查报告

**日期:** 2026-06-15  
**范围:** 全量源码审查 (21 Go 包, ~13,000 行)  
**方法:** Full-Stack Software Integration & Validation Engineer Skill — 步骤 1-3

---

## 执行摘要

对 shark-mqtt 项目全部 Go 源文件进行深度审查，从架构设计、并发安全、协议合规、资源管理和 Go 语言惯用法五个维度共发现 **19 项缺陷**，其中：
- 🔴 严重 (Critical): 2 项
- 🟠 高级 (High): 6 项  
- 🟡 中级 (Medium): 11 项
- 🔵 低级 (Low): 5 项

---

## 一、严重缺陷 (Critical)

### C-01: Session Takeover 竞态导致 Inflight 消息和订阅丢失

- **位置:** `broker/broker.go:330-337` (HandleConnection + disconnect)
- **描述:** 当同 ClientID 重连 (session takeover) 时，broker 先关闭旧连接释放 `b.mu`(L332 释放)，再注册新连接(L337)。在 L332-L337 的窗口期，旧的 `readLoop` 检测到关闭错误后调用 `abnormalDisconnect()` → `disconnect()`，后者在 L563 调用 `sessions.RemoveSession()` 并删除连接映射。当新连接注册时，session 已被销毁，`isResuming` 标志丢失。
- **影响:** 持久 session 的 inflight QoS 1/2 消息和订阅全部丢失，客户端需要重新订阅。
- **根因:** 释放锁后未原子性地完成 "踢旧+注册新" 操作。

### C-02: `$SYS` 主题保护被 ACL 和共享订阅绕过

- **位置:** `broker/auth.go:140-156` (CanPublish/CanSubscribe), `broker/topic_tree.go:283` (MatchShared)
- **描述:** MQTT 规范 §4.7.2 要求 `$SYS` 系统主题不被通配符订阅匹配。`TopicTree.Match()` 正确实现了此保护，但 `StaticAuth.CanPublish/CanSubscribe` 和 `TopicTree.MatchShared()` 直接调用 `protocol.MatchTopic()`，后者无 `$` 前缀感知。
- **影响:** ACL 通配符模式 `#` 可匹配 `$SYS/...` 主题，共享订阅也可接收系统消息。
- **修复方向:** 在 `StaticAuth` 的 topic 匹配中增加 `$SYS` 保护；`MatchShared` 改用 `TopicTree.Match` 或接受独立的 `$` 保护参数。

---

## 二、高级缺陷 (High)

### H-01: TLS 配置失败时静默降级为明文

- **位置:** `broker/server.go:63-67` (NewMQTTServer)
- **描述:** `cfg.TLSEnabled=true` 但 `cfg.TLSConfig()` 返回错误时（如证书文件缺失），仅打印一个 Warn 日志，`s.tlsConfig` 保持 nil——服务器在**明文 TCP** 上运行。
- **影响:** 运维人员收到的唯一提示是一条日志；所有连接未经加密。这是严重安全风险。
- **修复方向:** TLS 配置失败时应返回 error 并阻止服务器启动，或使用自签名临时证书并明确警告。

### H-02: `Start()` 可重复调用创建重复 goroutine

- **位置:** `broker/broker.go:387-394`, `broker/server.go:89-116`
- **描述:** `Broker.Start()` 和 `MQTTServer.Start()` 都未检查是否已启动。重复调用会创建重复的 QoSEngine retryLoop、sessionCleanupLoop、acceptLoop goroutine。
- **影响:** 冗余 goroutine 竞相处理相同资源。
- **修复方向:** 添加 `atomic.Bool started` 守卫或 context 检查。

### H-03: publishRateTracker 无并发保护

- **位置:** `broker/rate_limit.go:67` (文档: "NOT safe for concurrent use") + `broker/broker.go:663` (调用点无锁)
- **描述:** `publishRateTracker.Allow()` 被 `handlePublish` 调用时未持有 session 锁。Session takeover 场景中新旧 readLoop 可同时访问同一 tracker。
- **影响:** `count` 和 `windowStart` 上的数据竞态。
- **修复方向:** 在 `Allow()` 内使用 mutex，或将调用移到 session.mu 保护下。

### H-04: doRetry 先递增计数器再释放锁

- **位置:** `broker/qos_engine.go:346-386`
- **描述:** `doRetry` 在持有锁时对 entry 执行 `msg.Retries++` 和 `msg.SentAt=now`，然后释放锁后才调用 `republish` 回调。若 `republish` 因连接关闭等原因失败，重试计数已递增但实际未重发。
- **影响:** 重试计数不准确；消息可能提前达到 maxRetries 被丢弃。
- **修复方向:** 确认 republish 成功后再递增计数，或回滚。

### H-05: 最大包大小检查偏离 5 字节

- **位置:** `protocol/codec.go:50`
- **描述:** `fh.RemainingLength+5 > c.maxPacketSize` 假定固定头始终 5 字节。MQTT 规范 §2.2.3 规定最小固定头为 2 字节（RemainingLength<128 时）。剩余长度 <128 的包会被错误拒绝。
- **影响:** maxPacketSize=100 时，95 字节剩余长度 + 2 字节固定头 = 97 字节的合法包被拒绝 (95+5=100)。
- **修复方向:** 用 `fh.RemainingLength + actualHeaderSize` 替代硬编码 5。

### H-06: Codec 版本在 CONNECT 验证前设置

- **位置:** `protocol/connect.go:25` (decodeConnect)
- **描述:** `c.protocolVersion = protoVer` 在解码 CONNECT 后立即设置，但 `ValidateConnect` 在 broker.go 中稍后才调用。若验证失败，codec 留下无效版本。
- **影响:** 同一连接复用 codec 时会用错误版本解码（当前代码不复用，但潜在风险）。
- **修复方向:** 将 `protocolVersion` 的设置移到 `ValidateConnect` 成功之后。

---

## 三、中级缺陷 (Medium)

### M-01: writePacket 静默吞掉写错误

- **位置:** `broker/broker.go:1174-1187`
- **描述:** `cs.codec.Encode()` 错误仅 Debug 日志，不传播。`sendPubAck/sendPubRel/sendPubComp` 返回 nil 但实际未发送。
- **影响:** QoS 1/2 消息编码失败时 inflight 计数永久不一致（减计数只在 PUBACK/PUBCOMP 到达时触发）。
- **修复方向:** 传播写错误到 QoSEngine 触发重试，或更新 inflight 状态。

### M-02: shutdown 竞态和 inflight 计数不一致

- **位置:** `broker/broker.go:500-518` (Stop drain loop)
- **描述:** drain 循环在 RLock 下检查 inflight 计数，然后获取 Lock 关闭连接。之间新消息仍可通过 readLoop 到达并被计数。
- **修复方向:** drain 前先用 Lock 停止接受新连接。

### M-03: disconnect 存在 TOCTOU 窗口

- **位置:** `broker/broker.go:543-575`
- **描述:** `disconnect` 在 RUnlock (L545) 和 Lock (L570) 之间释放了锁。会话记录 (L563-564) 在两次加锁之间被更新。
- **修复方向:** 单次 Lock 持有完成所有 cleanup 步骤。

### M-04: Will Handler 延迟发布错误被丢弃

- **位置:** `broker/will_handler.go:103`
- **描述:** `wh.publishWillMessage(will)` 返回的错误被 `_ =` 忽略。
- **修复方向:** 至少记录 WARN 日志。

### M-05: UserProperties 无数量上限

- **位置:** `protocol/properties.go:285-294`
- **描述:** 解码 UserProperty 对时无上限（仅受 maxPacketSize 的字节限制）。恶意客户端可发送包含数千个短用户属性的包，导致大量内存分配。
- **修复方向:** 添加上限常量（如 100 个）。

### M-06: `maxConnections=0` 在不透明 API 中的语义模糊

- **位置:** `api/api.go:176-178`
- **描述:** `o.maxConnections == 0` 检查无法区分"API 用户显式设置 WithMaxConnections(0)"和"未设置"。前者应表示"无限"，后者应使用配置值。
- **修复方向:** 用 sentinel 值或指针区分。

### M-07: Health 服务器监听失败被静默忽略

- **位置:** `api/api.go:309-313`
- **描述:** `net.Listen` 失败时仅记录 Warn 日志，broker 继续运行且无 `/healthz` / `/metrics` 端点。
- **影响:** 运维人员不知道健康检查不可用。
- **修复方向:** 至少升级为 Error 日志，或通过 `initErr` 机制阻止启动。

### M-08: FileAuth 重复用户名静默覆盖

- **位置:** `broker/auth_file.go:77-81`
- **描述:** 加载凭证文件时，重复用户名在 `userIndex` map 中静默被后续条目覆盖。
- **修复方向:** 检测重复并返回错误。

### M-09: Redis retained 匹配模式过于宽泛

- **位置:** `store/redis/retained_store.go:114-126`
- **描述:** `topicPatternToRedis` 将 `+` 和 `#` 都转换为 `*`（Redis glob）。`sport/+` 变成 `sport/*` 会匹配 `sport/player1/rank`（多一层）。虽然后续 `protocol.MatchTopic` 会过滤，但每个假阳性都触发一次 Redis GET，`#` 模式导致全库 SCAN。
- **影响:** 性能下降；每个 `#` 订阅都触发全量 retained 检索。

### M-10: Redis ClearMessages 非原子

- **位置:** `store/redis/message_store.go:110-130`
- **描述:** SCAN → DEL 之间新消息可以到达。这是 Redis SCAN 的固有限制但未在文档或接口语义中说明。
- **修复方向:** 文档记录或使用事务（Lua script）。

### M-11: 连接 handler 错误静默记录后忽略

- **位置:** `broker/server.go:194`
- **描述:** `s.handler.HandleConnection()` 返回错误时，仅 Debug 日志记录。错误不会传播到调用方。
- **修复方向:** 根据错误类型升级日志级别。

---

## 四、低级缺陷 (Low)

### L-01: ParseSharedFilter 性能

- **位置:** `broker/topic_tree.go:200-212`
- **描述:** 用 `for i, c := range rest` 遍历找到第一个 `/`，不如 `strings.IndexByte(rest, '/')` 高效。
- **修复方向:** 替换为 `strings.IndexByte`。

### L-02: SubscribeSystem 等同于 Subscribe

- **位置:** `broker/topic_tree.go:56-68`
- **描述:** 两个函数实现完全相同，注释声称的特殊行为在 Match() 中实现而非此处。
- **修复方向:** 保留函数但修正注释，或删除重复代码。

### L-03: 配置验证不完整

- **位置:** `config/config.go` (Validate)
- **描述:** `BadgerPath` 在后端为 badger 时未验证；`TLSMinVersion > TLSMaxVersion` 未检查。
- **修复方向:** 添加缺失的验证规则。

### L-04: 缓冲池死代码分支

- **位置:** `pkg/bufferpool/pool.go:30-38`
- **描述:** `Get()` 中的 `case []byte` 分支永远不会执行——池始终存储 `*[]byte`。
- **修复方向:** 删除死代码。

### L-05: Client 缺少自动重连

- **位置:** `client/client.go` (缺失功能)
- **描述:** `readLoop` 检测到断连后客户端必须手动重建。缺少自动重连和指数退避。
- **修复方向:** 添加 `AutoReconnect` 选项。

---

## 五、跨切面问题

| 维度 | 评估 |
|------|------|
| **并发安全** | 3 项缺陷（C-01, H-02, H-03, M-02, M-03）— session takeover 和 shutdown 路径需加固 |
| **安全** | H-01（TLS 静默降级）严重；C-02（$SYS 泄漏）中等 |
| **协议合规** | H-05（包大小检查偏移）+ C-02（$SYS 保护） |
| **资源管理** | M-05（UserProperties 无上限）；M-06（连接语义） |
| **错误处理** | M-01, M-04, M-07 — 多处错误被静默吞掉 |
| **Go 惯用法** | L-01, L-02, L-04 — 小优化和清理 |

---

## 六、修复优先级

```
P0 (立即修复):
  ├── C-01: Session takeover race
  ├── C-02: $SYS topic protection bypass  
  └── H-01: TLS silent downgrade

P1 (高优先级):
  ├── H-04: doRetry retry count race
  ├── H-05: Packet size check off-by-5
  ├── M-01: writePacket error swallowing
  └── M-05: UserProperties unbounded

P2 (中优先级):
  ├── H-02: Start() idempotency
  ├── H-03: publishRateTracker concurrency
  ├── H-06: Codec version residue
  ├── M-06: maxConnections=0 ambiguity
  ├── M-07: Health server silent failure
  └── M-08: FileAuth duplicate detection

P3 (低优先级):
  ├── M-02 through M-04, M-09 through M-11
  └── L-01 through L-05
```

---

## 七、验证计划

每项修复后:
1. `go vet ./...` — 无警告
2. `go build ./...` — 编译通过
3. `go test -count=1 ./...` — 全部测试通过
4. `gofmt -d .` — 无格式差异
5. 针对性测试: 修复涉及路径的集成/单元测试
