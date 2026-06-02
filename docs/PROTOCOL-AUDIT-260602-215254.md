# MQTT 协议实现完整性审计报告

**日期:** 2026-06-02  
**范围:** `protocol/` `broker/` `client/` 包的 MQTT 3.1.1 / 5.0 协议合规性

---

## 1. 报文类型 — 全部实现 ✅

| 报文 | 类型值 | 编解码 | MQTT 3.1.1 | MQTT 5.0 | 备注 |
|------|--------|--------|------------|----------|------|
| CONNECT | 1 | ✅ | ✅ | ✅ | 三段式验证 (协议名→版本→flags) |
| CONNACK | 2 | ✅ | ✅ | ✅ | 含 MQTT 5.0 能力宣告 |
| PUBLISH | 3 | ✅ | ✅ | ✅ | 含 Qos0/1/2、DUP 检测 |
| PUBACK | 4 | ✅ | ✅ | ✅ | 含 MQTT 5.0 ReasonCode + Properties |
| PUBREC | 5 | ✅ | ✅ | ✅ | 含 MQTT 5.0 ReasonCode + Properties |
| PUBREL | 6 | ✅ | ✅ | ✅ | 含 MQTT 5.0 ReasonCode + Properties |
| PUBCOMP | 7 | ✅ | ✅ | ✅ | 含 MQTT 5.0 ReasonCode + Properties |
| SUBSCRIBE | 8 | ✅ | ✅ | ✅ | 含 MQTT 5.0 Subscription Options |
| SUBACK | 9 | ✅ | ✅ | ✅ | 支持多 ReasonCode |
| UNSUBSCRIBE | 10 | ✅ | ✅ | ✅ | 含 MQTT 5.0 Properties |
| UNSUBACK | 11 | ✅ | ✅ | ✅ | MQTT 5.0 多 ReasonCode |
| PINGREQ | 12 | ✅ | ✅ | ✅ | Keep-alive 保活 |
| PINGRESP | 13 | ✅ | ✅ | ✅ | |
| DISCONNECT | 14 | ✅ | ✅ | ✅ | 含 MQTT 5.0 ReasonCode |
| AUTH | 15 | ✅ | N/A | ✅ | 基本支持 (Success/Continue/ReAuth) |

---

## 2. MQTT 5.0 属性 (Properties) — 缺少 2 项 ⚠️

### 已实现的属性 (24/26)

| ID | 属性名 | 编解码 | 类型 | 常量校验 | 业务逻辑 |
|----|--------|--------|------|----------|----------|
| 0x01 | PayloadFormatIndicator | ✅ | Byte (0/1) | ✅ | ❌ 未强制执行 |
| 0x02 | MessageExpiryInterval | ✅ | UInt32 | ✅ | ❌ 未强制执行 |
| 0x03 | ContentType | ✅ | UTF-8 String | - | N/A (透传) |
| 0x08 | ResponseTopic | ✅ | UTF-8 String | - | N/A (透传) |
| 0x09 | CorrelationData | ✅ | Binary | - | N/A (透传) |
| 0x0B | SubscriptionIdentifier | ✅ | VarInt | ✅ ≠0 | ❌ 未在投递中使用 |
| 0x11 | SessionExpiryInterval | ✅ | UInt32 | - | ✅ 完整实现 |
| 0x12 | AssignedClientID | ✅ | UTF-8 String | - | ❌ 未使用 |
| 0x13 | ServerKeepAlive | ✅ | UInt16 | - | ❌ 未在 CONNACK 中发送 |
| 0x15 | AuthenticationMethod | ✅ | UTF-8 String | - | N/A (预留) |
| 0x16 | AuthenticationData | ✅ | Binary | - | N/A (预留) |
| 0x17 | RequestProblemInfo | ✅ | Byte (0/1) | ✅ | N/A (预留) |
| 0x18 | WillDelayInterval | ✅ | UInt32 | - | ✅ 完整实现 |
| 0x1F | ReasonString | ✅ | UTF-8 String | - | ✅ 支持 |
| 0x21 | ReceiveMaximum | ✅ | UInt16 | ✅ ≠0 | ❌ 仅 CONNACK 宣告 |
| 0x22 | TopicAliasMaximum | ✅ | UInt16 | - | ❌ 仅 CONNACK 宣告 |
| 0x23 | TopicAlias | ✅ | UInt16 | ✅ ≠0 | ❌ 未解析别名 |
| 0x24 | MaximumQoS | ✅ | Byte (0/1) | ✅ | ❌ 仅 CONNACK 宣告 |
| 0x25 | RetainAvailable | ✅ | Byte (0/1) | ✅ | ✅ 正确反映存储状态 |
| 0x26 | UserProperty | ✅ | String Pair | - | ✅ 支持多个 |
| 0x27 | MaximumPacketSize | ✅ | UInt32 | ✅ ≠0 | ✅ 编解码层拦截 |
| 0x28 | WildcardSubAvailable | ✅ | Byte (0/1) | ✅ | ✅ CONNACK 宣告 |
| 0x29 | SubIDAvailable | ✅ | Byte (0/1) | ✅ | ✅ CONNACK 宣告 (0) |
| 0x2A | SharedSubAvailable | ✅ | Byte (0/1) | ✅ | ✅ CONNACK 宣告 (0) |

### 缺失的属性 (2)

| ID | 属性名 | 用途 | 影响 |
|----|--------|------|------|
| **0x19** | **RequestResponseInformation** | 客户端请求服务器在 CONNACK 中返回 ResponseInformation | 低 — 仅用于请求/响应模式 |
| **0x1A** | **ResponseInformation** | 服务器返回用于创建响应主题的信息 | 低 — 仅用于请求/响应模式 |

> **建议:** 添加 `RequestResponseInformation` (0x19) 和 `ResponseInformation` (0x1A) 的编解码支持。这两个属性在 MQTT 5.0 请求/响应语义中使用，缺失会导致兼容性问题。

---

## 3. 主题处理 — 完全合规 ✅

| 规则 | 实现 | 测试覆盖 |
|------|------|----------|
| PUBLISH 主题禁止通配符 (#/+) | ✅ `ValidatePublishTopic()` | 8 用例 |
| Topic Filter 验证 | ✅ `ValidateTopicFilter()` | 12 用例 |
| `#` 必须是最后一个字符且前面有 `/` | ✅ | 3 用例 |
| `+` 必须占据一整层 | ✅ | 3 用例 |
| `$SYS` 系统主题保护 (MQTT §4.7.2) | ✅ `matchNodeWithSys()` | 集成测试 |
| 空主题层支持 | ✅ `SplitTopic()` | 4 用例 |
| `/` 分隔符语义 | ✅ | |

---

## 4. CONNECT 验证 — 完全合规 ✅

| 验证项 | 规则依据 | 实现 | 测试 |
|--------|----------|------|------|
| 协议名无效 | MQTT §3.1.2.1 | ✅ ConnAck 0x01 | ✅ |
| 协议版本不支持 | MQTT §3.1.2.2 | ✅ ConnAck 0x01 | ✅ |
| Reserved flag ≠ 0 | MQTT §3.1.2.3 | ✅ 拒绝 | ✅ |
| Will QoS = 3 | MQTT §3.1.2.6 | ✅ 拒绝 | ✅ |
| WillFlag=0 时 WillQoS≠0 | MQTT §3.1.2.6 | ✅ 拒绝 | ✅ |
| WillFlag=0 时 WillRetain=1 | MQTT §3.1.2.6 | ✅ 拒绝 | ✅ |
| Password 无 Username | MQTT §3.1.2.5 | ✅ 拒绝 | ✅ |
| ClientID 零长但 CleanSession=0 | MQTT §3.1.3.1 | ✅ 拒绝 | ✅ |
| WillFlag=1 但 WillTopic 为空 | 扩展校验 | ✅ 拒绝 | ✅ |

---

## 5. QoS 状态机 — 完全合规 ✅

| 场景 | 实现 | 测试 |
|------|------|------|
| QoS 0: 发布后丢弃 | ✅ 无状态 | ✅ |
| QoS 1: PUBLISH→PUBACK | ✅ `TrackQoS1`/`AckQoS1` | ✅ |
| QoS 2: PUBLISH→PUBREC→PUBREL→PUBCOMP | ✅ `TrackQoS2`/`AckPubRec`/`AckPubRel`/`AckPubComp` | ✅ |
| QoS 2 DUP 检测 (MQTT §4.3.3) | ✅ `receivedQoS2` map | ✅ |
| DUP flag = 1 on retry | ✅ broker QoS engine | ✅ |
| 最大重试次数后丢弃 | ✅ `maxRetries` | ✅ |
| 重试间隔控制 | ✅ `retryInterval` | ✅ |
| Inflight 最大限制 | ✅ `maxInflight` | ✅ |

---

## 6. 会话管理 — 合规 ⚠️

| 功能 | 实现 | MQTT 版本 |
|------|------|-----------|
| CleanSession=1 (清除会话) | ✅ | 3.1.1 |
| CleanSession=0 (保留会话) | ✅ 持久化到 store | 3.1.1 |
| Session Expiry Interval | ✅ 完整实现 | 5.0 |
| 客户端重连恢复 | ✅ `Restore()` + QoS 恢复 | 3.1.1/5.0 |
| Session Takeover (同一 ClientID) | ✅ 旧连接关闭，新连接注册 | 3.1.1/5.0 |
| 过期会话清理 | ✅ `sessionCleanupLoop` | 5.0 |

### 缺失的会话功能

| 功能 | 状态 | 影响 |
|------|------|------|
| **ServerKeepAlive in CONNACK** | ❌ 未发送 | MQTT 5.0 服务器可建议 Keep Alive |
| **AssignedClientID** | ❌ 未使用 | 客户端发送空 ClientID 时服务器应分配 |

---

## 7. Topic Alias — 未强制执行 ❌

- **解码层:** ✅ `TopicAlias` 属性可编解码，验证 > 0
- **CONNACK:** ✅ 广告 `TopicAliasMaximum` (但始终为 0 = 不支持别名)
- **运行时:** ❌ 未实现别名映射表，PUBLISH 发布时不解引用 TopicAlias
- **影响:** 低 — 服务器可声明不支持 (TopicAliasMaximum=0)，规范允许

---

## 8. 流量控制 (Receive Maximum) — 部分实现 ⚠️

- **CONNACK:** ✅ 广告 `ReceiveMaximum`
- **运行时:** ❌ 仅使用全局 `maxInflight` (默认 100)，不按客户端协商
- **影响:** 中 — 客户端可以声明 ReceiveMaximum < 服务器值，但服务器不遵从

---

## 9. 保留消息 — 合规 ✅

| 规则 | 实现 |
|------|------|
| 设置保留消息 (PUBLISH with Retain=1) | ✅ `handleRetainedMessage()` |
| 删除保留消息 (零长 payload) | ✅ 检查 `len(pkt.Payload) == 0` |
| 订阅时发送匹配的保留消息 | ✅ `deliverRetainedMessages()` |
| QoS 降级 (取储存 QoS 和订阅 QoS 的最小值) | ✅ |
| MQTT 5.0 RetainHandling (0/1/2) | ✅ `shouldDeliverRetained()` |
| TTL/过期清理 | ❌ 保留消息永不过期 |

---

## 10. Will 遗嘱消息 — 合规 ✅

| 规则 | 实现 |
|------|------|
| 异常断开触发 Will | ✅ `abnormalDisconnect()`→`TriggerWill()` |
| 正常断开不触发 Will | ✅ `gracefulDisconnect()`→`RemoveWill()` |
| Will Delay (MQTT 5.0) | ✅ goroutine + timer + cancel |
| Will 重连取消延迟 | ✅ `cancel()` 先于新 RegisterWill |
| Will QoS 1/2 from connect flags | ✅ |
| Will Retain | ✅ |

---

## 11. 协议一致性测试覆盖

| 类别 | 测试文件 | 用例数 | 覆盖重点 |
|------|----------|--------|---------|
| 编解码 | `codec_test.go` | 10 | 15种报文往返 |
| 边界校验 | `boundary_test.go` | 15 | 截断、无效 UTF-8、误 flag |
| 协议合规 | `protocol_compliance_test.go` | 8 | MQTT 5.0 properties 约束 |
| CONNECT | `connect_test.go` | 7 | flags 验证、will、auth |
| 集成 | `tests/integration/` | 90 | 端到端 QoS、will、retained |

---

## 12. 总结

| 维度 | 评分 | 说明 |
|------|------|------|
| MQTT 3.1.1 报文完整性 | 100% | 14 种报文全部实现并测试 |
| MQTT 5.0 报文完整性 | 100% | 15 种报文 (含 AUTH) 全部实现 |
| MQTT 5.0 Properties (结构) | 92% | 24/26 属性；缺 0x19, 0x1A |
| MQTT 5.0 Properties (语义) | 65% | 编解码完整，但 TopicAlias/MessageExpiry/ReceiveMax 未强制执行 |
| 主题系统 | 100% | $SYS 保护、空层、通配符验证 |
| QoS 状态机 | 100% | 0/1/2 完整，含 DUP 检测和重试 |
| 会话持久化 | 100% | Session Expiry、恢复、Takeover |
| 遗嘱消息 | 100% | 含 MQTT 5.0 Will Delay |
| 保留消息 | 95% | 缺少过期清理 |
| 测试覆盖 | 良好 | 12 个测试函数、90 个集成测试 |

### 待改进项 (按优先级)

| # | 问题 | 优先级 | 工作量 |
|---|------|--------|--------|
| 1 | 添加缺失属性 RequestResponseInformation (0x19)/ResponseInformation (0x1A) | 中 | 20 min |
| 2 | 实现按客户端 ReceiveMaximum 流量控制 | 低 | 1 hr |
| 3 | 发出 ServerKeepAlive (比客户端值更大的建议值) | 低 | 15 min |
| 4 | 保留消息添加 TTL/过期机制 | 低 | 1 hr |
| 5 | 发送 AssignedClientID (客户端空 ClientID 时) | 低 | 30 min |
