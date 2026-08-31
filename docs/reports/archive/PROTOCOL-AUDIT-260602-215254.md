# MQTT 协议实现完整性审计报告

**日期:** 2026-06-02  
**范围:** `protocol/` `broker/` `client/` 包的 MQTT 3.1.1 / 5.0 协议合规性

---

## 1. 报文类型 - 全部实现 [x]

| 报文 | 类型值 | 编解码 | MQTT 3.1.1 | MQTT 5.0 | 备注 |
|------|--------|--------|------------|----------|------|
| CONNECT | 1 | [x] | [x] | [x] | 三段式验证 (协议名->版本->flags) |
| CONNACK | 2 | [x] | [x] | [x] | 含 MQTT 5.0 能力宣告 |
| PUBLISH | 3 | [x] | [x] | [x] | 含 Qos0/1/2、DUP 检测 |
| PUBACK | 4 | [x] | [x] | [x] | 含 MQTT 5.0 ReasonCode + Properties |
| PUBREC | 5 | [x] | [x] | [x] | 含 MQTT 5.0 ReasonCode + Properties |
| PUBREL | 6 | [x] | [x] | [x] | 含 MQTT 5.0 ReasonCode + Properties |
| PUBCOMP | 7 | [x] | [x] | [x] | 含 MQTT 5.0 ReasonCode + Properties |
| SUBSCRIBE | 8 | [x] | [x] | [x] | 含 MQTT 5.0 Subscription Options |
| SUBACK | 9 | [x] | [x] | [x] | 支持多 ReasonCode |
| UNSUBSCRIBE | 10 | [x] | [x] | [x] | 含 MQTT 5.0 Properties |
| UNSUBACK | 11 | [x] | [x] | [x] | MQTT 5.0 多 ReasonCode |
| PINGREQ | 12 | [x] | [x] | [x] | Keep-alive 保活 |
| PINGRESP | 13 | [x] | [x] | [x] | |
| DISCONNECT | 14 | [x] | [x] | [x] | 含 MQTT 5.0 ReasonCode |
| AUTH | 15 | [x] | N/A | [x] | 基本支持 (Success/Continue/ReAuth) |

---

## 2. MQTT 5.0 属性 (Properties) - 缺少 2 项 [!]

### 已实现的属性 (24/26)

| ID | 属性名 | 编解码 | 类型 | 常量校验 | 业务逻辑 |
|----|--------|--------|------|----------|----------|
| 0x01 | PayloadFormatIndicator | [x] | Byte (0/1) | [x] | [ ] 未强制执行 |
| 0x02 | MessageExpiryInterval | [x] | UInt32 | [x] | [ ] 未强制执行 |
| 0x03 | ContentType | [x] | UTF-8 String | - | N/A (透传) |
| 0x08 | ResponseTopic | [x] | UTF-8 String | - | N/A (透传) |
| 0x09 | CorrelationData | [x] | Binary | - | N/A (透传) |
| 0x0B | SubscriptionIdentifier | [x] | VarInt | [x] !=0 | [x] 已实现投递转发 |
| 0x11 | SessionExpiryInterval | [x] | UInt32 | - | [x] 完整实现 |
| 0x12 | AssignedClientID | [x] | UTF-8 String | - | [x] CONNACK 发送 |
| 0x13 | ServerKeepAlive | [x] | UInt16 | - | [x] CONNACK 发送 |
| 0x15 | AuthenticationMethod | [x] | UTF-8 String | - | N/A (预留) |
| 0x16 | AuthenticationData | [x] | Binary | - | N/A (预留) |
| 0x17 | RequestProblemInfo | [x] | Byte (0/1) | [x] | N/A (预留) |
| 0x18 | WillDelayInterval | [x] | UInt32 | - | [x] 完整实现 |
| 0x1F | ReasonString | [x] | UTF-8 String | - | [x] 支持 |
| 0x21 | ReceiveMaximum | [x] | UInt16 | [x] !=0 | [x] 流控已实现 (doDeliver限速) |
| 0x22 | TopicAliasMaximum | [x] | UInt16 | - | [x] 双方协商，上限64 |
| 0x23 | TopicAlias | [x] | UInt16 | [x] !=0 | [x] 别名注册与解析 |
| 0x24 | MaximumQoS | [x] | Byte (0/1) | [x] | [ ] 仅 CONNACK 宣告 |
| 0x25 | RetainAvailable | [x] | Byte (0/1) | [x] | [x] 正确反映存储状态 |
| 0x26 | UserProperty | [x] | String Pair | - | [x] 支持多个 |
| 0x27 | MaximumPacketSize | [x] | UInt32 | [x] !=0 | [x] 编解码层拦截 |
| 0x28 | WildcardSubAvailable | [x] | Byte (0/1) | [x] | [x] CONNACK 宣告 |
| 0x29 | SubIDAvailable | [x] | Byte (0/1) | [x] | [x] CONNACK 宣告 (1) |
| 0x2A | SharedSubAvailable | [x] | Byte (0/1) | [x] | [x] 完整实现，round-robin |

### 缺失的属性 (2)

| ID | 属性名 | 用途 | 影响 |
|----|--------|------|------|
| **0x19** | **RequestResponseInformation** | 客户端请求服务器在 CONNACK 中返回 ResponseInformation | 低 - 仅用于请求/响应模式 |
| **0x1A** | **ResponseInformation** | 服务器返回用于创建响应主题的信息 | 低 - 仅用于请求/响应模式 |

> **建议:** 添加 `RequestResponseInformation` (0x19) 和 `ResponseInformation` (0x1A) 的编解码支持。这两个属性在 MQTT 5.0 请求/响应语义中使用，缺失会导致兼容性问题。

---

## 3. 主题处理 - 完全合规 [x]

| 规则 | 实现 | 测试覆盖 |
|------|------|----------|
| PUBLISH 主题禁止通配符 (#/+) | [x] `ValidatePublishTopic()` | 8 用例 |
| Topic Filter 验证 | [x] `ValidateTopicFilter()` | 12 用例 |
| `#` 必须是最后一个字符且前面有 `/` | [x] | 3 用例 |
| `+` 必须占据一整层 | [x] | 3 用例 |
| `$SYS` 系统主题保护 (MQTT 4.7.2) | [x] `matchNodeWithSys()` | 集成测试 |
| 空主题层支持 | [x] `SplitTopic()` | 4 用例 |
| `/` 分隔符语义 | [x] | |

---

## 4. CONNECT 验证 - 完全合规 [x]

| 验证项 | 规则依据 | 实现 | 测试 |
|--------|----------|------|------|
| 协议名无效 | MQTT 3.1.2.1 | [x] ConnAck 0x01 | [x] |
| 协议版本不支持 | MQTT 3.1.2.2 | [x] ConnAck 0x01 | [x] |
| Reserved flag != 0 | MQTT 3.1.2.3 | [x] 拒绝 | [x] |
| Will QoS = 3 | MQTT 3.1.2.6 | [x] 拒绝 | [x] |
| WillFlag=0 时 WillQoS!=0 | MQTT 3.1.2.6 | [x] 拒绝 | [x] |
| WillFlag=0 时 WillRetain=1 | MQTT 3.1.2.6 | [x] 拒绝 | [x] |
| Password 无 Username | MQTT 3.1.2.5 | [x] 拒绝 | [x] |
| ClientID 零长但 CleanSession=0 | MQTT 3.1.3.1 | [x] 拒绝 | [x] |
| WillFlag=1 但 WillTopic 为空 | 扩展校验 | [x] 拒绝 | [x] |

---

## 5. QoS 状态机 - 完全合规 [x]

| 场景 | 实现 | 测试 |
|------|------|------|
| QoS 0: 发布后丢弃 | [x] 无状态 | [x] |
| QoS 1: PUBLISH->PUBACK | [x] `TrackQoS1`/`AckQoS1` | [x] |
| QoS 2: PUBLISH->PUBREC->PUBREL->PUBCOMP | [x] `TrackQoS2`/`AckPubRec`/`AckPubRel`/`AckPubComp` | [x] |
| QoS 2 DUP 检测 (MQTT 4.3.3) | [x] `receivedQoS2` map | [x] |
| DUP flag = 1 on retry | [x] broker QoS engine | [x] |
| 最大重试次数后丢弃 | [x] `maxRetries` | [x] |
| 重试间隔控制 | [x] `retryInterval` | [x] |
| Inflight 最大限制 | [x] `maxInflight` | [x] |

---

## 6. 会话管理 - 合规 [!]

| 功能 | 实现 | MQTT 版本 |
|------|------|-----------|
| CleanSession=1 (清除会话) | [x] | 3.1.1 |
| CleanSession=0 (保留会话) | [x] 持久化到 store | 3.1.1 |
| Session Expiry Interval | [x] 完整实现 | 5.0 |
| 客户端重连恢复 | [x] `Restore()` + QoS 恢复 | 3.1.1/5.0 |
| Session Takeover (同一 ClientID) | [x] 旧连接关闭，新连接注册 | 3.1.1/5.0 |
| 过期会话清理 | [x] `sessionCleanupLoop` | 5.0 |

### 缺失的会话功能

| 功能 | 状态 | 影响 |
|------|------|------|
| **ServerKeepAlive in CONNACK** | [ ] 未发送 | MQTT 5.0 服务器可建议 Keep Alive |
| **AssignedClientID** | [ ] 未使用 | 客户端发送空 ClientID 时服务器应分配 |

---

## 7. Topic Alias - 未强制执行 [ ]

- **解码层:** [x] `TopicAlias` 属性可编解码，验证 > 0
- **CONNACK:** [x] 广告 `TopicAliasMaximum` (但始终为 0 = 不支持别名)
- **运行时:** [ ] 未实现别名映射表，PUBLISH 发布时不解引用 TopicAlias
- **影响:** 低 - 服务器可声明不支持 (TopicAliasMaximum=0)，规范允许

---

## 8. 流量控制 (Receive Maximum) - 部分实现 [!]

- **CONNACK:** [x] 广告 `ReceiveMaximum`
- **运行时:** [ ] 仅使用全局 `maxInflight` (默认 100)，不按客户端协商
- **影响:** 中 - 客户端可以声明 ReceiveMaximum < 服务器值，但服务器不遵从

---

## 9. 保留消息 - 合规 [x]

| 规则 | 实现 |
|------|------|
| 设置保留消息 (PUBLISH with Retain=1) | [x] `handleRetainedMessage()` |
| 删除保留消息 (零长 payload) | [x] 检查 `len(pkt.Payload) == 0` |
| 订阅时发送匹配的保留消息 | [x] `deliverRetainedMessages()` |
| QoS 降级 (取储存 QoS 和订阅 QoS 的最小值) | [x] |
| MQTT 5.0 RetainHandling (0/1/2) | [x] `shouldDeliverRetained()` |
| TTL/过期清理 | [ ] 保留消息永不过期 |

---

## 10. Will 遗嘱消息 - 合规 [x]

| 规则 | 实现 |
|------|------|
| 异常断开触发 Will | [x] `abnormalDisconnect()`->`TriggerWill()` |
| 正常断开不触发 Will | [x] `gracefulDisconnect()`->`RemoveWill()` |
| Will Delay (MQTT 5.0) | [x] goroutine + timer + cancel |
| Will 重连取消延迟 | [x] `cancel()` 先于新 RegisterWill |
| Will QoS 1/2 from connect flags | [x] |
| Will Retain | [x] |

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
| MQTT 5.0 Properties (语义) | 80% | 编解码完整；TopicAlias/ReceiveMax/ServerKeepAlive 已实现语义 |
| 主题系统 | 100% | $SYS 保护、空层、通配符验证 |
| QoS 状态机 | 100% | 0/1/2 完整，含 DUP 检测和重试 |
| 会话持久化 | 100% | Session Expiry、恢复、Takeover |
| 遗嘱消息 | 100% | 含 MQTT 5.0 Will Delay |
| 保留消息 | 95% | 缺少过期清理 |
| 测试覆盖 | 良好 | 12 个测试函数、90 个集成测试 |

### 待改进项 (按优先级)

| # | 问题 | 优先级 | 工作量 | 状态 |
|---|------|--------|--------|------|
| 1 | 添加缺失属性 RequestResponseInformation (0x19)/ResponseInformation (0x1A) | 中 | 20 min | [x] 已完成 (4b877e3) |
| 2 | 实现按客户端 ReceiveMaximum 流量控制 | 低 | 1 hr | [x] 已完成 (beb9820) |
| 3 | 发出 ServerKeepAlive (比客户端值更短的覆盖值) | 低 | 15 min | [x] 已完成 (beb9820) |
| 4 | Topic Alias 别名解析 | 低 | 1 hr | [x] 已完成 (beb9820) |
| 5 | 发送 AssignedClientID (客户端空 ClientID 时) | 低 | 30 min | [x] 已完成 (beb9820) |
| 6 | Message Expiry Interval 检查 | 低 | 30 min | [x] 已完成 (beb9820) |
| 7 | 保留消息添加 TTL/过期机制 | 低 | 1 hr | 未实现 |
