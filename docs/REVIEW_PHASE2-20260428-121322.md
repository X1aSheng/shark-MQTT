# Shark-MQTT 代码审查改进计划

> 审查日期: 2026-04-28 | 审查范围: 全部 94 个 Go 源文件

---

## 🔴 P0 严重 — 数据竞争 / 协议违规 / 可观测性崩溃

### 1. QoS 重试循环中的数据竞争

**文件:** `broker/qos_engine.go:303-348`

`doRetry()` 持 `RLock` (读锁) 但直接修改 `msg.Retries++` 和 `msg.SentAt` (第 322-323 行)。
同时还调用了 `delete(clientInflight, packetID)` (第 318 行) 从 map 中删除元素。
RLock 不保证写操作的排他性 — 与 `AckQoS1`、`TrackQoS1` 等方法存在数据竞争。

**修复:** 将 `doRetry()` 中的 `RLock` 改为 `Lock`。

### 2. PUBLISH 未拒绝通配符主题

**文件:** `broker/broker.go:312-374`

MQTT 3.1.1 §3.3.2: "PUBLISH 包的主题名不得包含通配符字符"。
`handlePublish()` 直接接受任意主题，未验证 `#` 和 `+` 是否在主题中。
这属于协议违规，可能导致消息路由到错误的订阅者。

**修复:** 在 `handlePublish()` 中添加 `ValidatePublishTopic()` 检查，拒绝含通配符的主题。

### 3. Prometheus 标签基数爆炸

**文件:** `pkg/metrics/prometheus.go:148-153`

`IncMessagesPublished(topic, qos)` 和 `IncMessagesDelivered(clientID, qos)` 将**任意 topic** 和 **clientID** 作为 Prometheus 标签值。
每个唯一的 topic (如 `sensors/device-uuid-1234/temperature`) 会创建新的时间序列。
在生产中这会导致 Prometheus 服务器 OOM — 这是 Prometheus 的首要反模式。

**修复:** 移除 topic 和 clientID 标签维度；改用无界维度的聚合计数器。

---

## 🟡 P1 高 — 逻辑缺陷 / 安全 / 架构

### 4. StaticAuth ACL 使用 clientID 而非 username 查找

**文件:** `broker/auth.go:78-108`

`CanPublish(ctx, clientID, topic)` 和 `CanSubscribe(ctx, clientID, topic)` 使用 `clientID` 作为 ACL map 的 key。
认证凭据由 username 组织 (`credentials map[string]string`)，但授权查找却用 clientID。
这导致 ACL 完全失效 — 攻击者可以更改 clientID 绕过授权。

**修复:** 将参数名和 lookup key 从 `clientID` 改为 `username`。

### 5. 密码以明文形式记录在日志中

**文件:** `broker/broker.go:147`

`string(connectPkt.Password)` 将密码字节转为字符串传给 `Authenticate()`。
如果 `Authenticate` 的任何实现记录了错误 ("auth failed for user X with password Y")，
密码即泄露。此外，Go 的 `string([]byte)` 会在堆上分配不可清零的新内存。

**修复:** 删除密码的 `string()` 转换；确保所有 auth 实现使用 `subtle.ConstantTimeCompare` 且不记录密码。

### 6. 多个 topic 匹配实现重复

以下位置存在独立的 topic 通配符匹配实现：

| 位置 | 函数名 |
|------|--------|
| `protocol/topic.go` | `MatchTopic`, `matchLevels` |
| `broker/auth.go` | `matchTopic`, `topicMatch`, `splitPath` |
| `broker/topic_tree.go` | `splitTopic`, `ValidateTopicFilter` |
| `store/memory/memory.go` | `matchTopic` |
| `testutils/mock_store.go` | `topicMatchesPattern` |

存在 5 个 `matchTopic` 变体和 3 个 `splitTopic` 变体，行为可能不一致。

**修复:** 将所有 topic 匹配逻辑统一到 `protocol` 包，其他地方通过导入复用。

### 7. 内存存储 `MatchRetained` bug

**文件:** `store/memory/memory.go:208`

`patternParts[0] == "#"` 匹配的是切分后的第一部分等于 `"#"`，但 `strings.Split("#", "/")` 返回 `["#"]`。
这使得以 `#` 开头的 pattern (如 `#/test`) 会误匹配。此检查应使用完整的字符串比较。

**修复:** 使用 `protocol.MatchTopic` 替换临时实现。

### 8. 默认认证配置不一致

**文件:** `broker/options.go:32-34`

```go
authenticator:  DenyAllAuth{},   // 拒绝所有认证
authorizer:     AllowAllAuth{},  // 允许所有授权
```

默认行为自相矛盾：认证拒绝全部连接，但授权允许全部。服务默认无法使用。

**修复:** 对齐默认值；如果将 DenyAll 用作认证的默认值，授权也应为 DenyAll。

---

## 🟢 P2 中 — 代码质量 / 可维护性

### 9. 重复的错误定义

**文件:** `errs/errors.go` 与 `protocol/errors.go` 均定义了 `ErrInvalidPacket`、`ErrPacketTooLarge`、`ErrUnsupportedVersion`。

### 10. 未使用的代码

| 文件 | 代码 |
|------|------|
| `broker/server.go:191-195` | `SafeConn` 类型 — 已定义但从未使用 |
| `broker/server.go:208-212` | `DrainAndClose` 函数 — 已定义但从未使用 |
| `api/api.go:166-168` | `Broker.auth`、`Broker.authz` 字段 — 已存储但从未读取 |
| `broker/auth_noop.go` | `NoopAuth` 类型 — 与 `AllowAllAuth` 功能完全重复 |

### 11. 消息大小计算不一致

**文件:** `broker/broker.go:452, 490`

`TrackSent()` 同时统计了 topic 长度和 payload 长度，但 MQTT 统计应只计算 payload 字节数。

### 12. 健康检查端点缺少优雅关闭

**文件:** `api/api.go:193-194`

`b.healthSrv.Close()` 是硬关闭，未先等待进行中的请求完成。应使用 `Shutdown()` 配合超时上下文。

### 13. 硬编码的 MaxPacketSize

**文件:** `broker/broker.go:70`

`protocol.NewCodec(256 * 1024)` 使用的是硬编码常量，未使用已存在配置项的 `cfg.MaxPacketSize`。

### 14. cmd/main.go 使用不一致的日志实现

**文件:** `cmd/main.go:8`

使用了标准库的 `log`，而项目其余部分使用 `pkg/logger` 的结构化日志系统。

### 15. ConnectFlags.HasWillTopic 分支逻辑死代码

**文件:** `protocol/connect.go:41-46`

```go
if protoVer == Version50 {
    flags.WillTopicFlag = flags.WillFlag
} else {
    flags.WillTopicFlag = flags.WillFlag
}
```

两个分支结果相同，此外 `WillTopicFlag` 不是标准的 MQTT 标志。

### 16. 错误信息使用中文注释

**文件:** `errs/errors.go:8-36`

注释使用中文混合，而代码库其余部分使用英文。不一致的注释语言降低可维护性。

### 17. Broker 中的 context.Background() 使用

多处调用链中直接使用 `context.Background()` 而非传入父 context，
导致取消信号无法传播到存储操作等下游调用。

---

## 🔵 P3 低 — 增强 / 未来功能

### 18. 缺少共享订阅支持 (MQTT 5.0 要求)

MQTT 5.0 §4.8 要求原生支持共享订阅 (`$share/group/topic`)。当前完全未实现。

### 19. 缺少连接速率限制

无新连接到达速率的限制机制，在连接风暴场景下 broker 可能资源耗尽。

### 20. 无 TLS 证书热重载

`Config.TLSConfig()` 在启动时加载一次，运行中更新证书需要重启服务。

### 21. go.mod 指定 go 1.26.1

Go 1.26 尚未发布（当前最新稳定版 ~1.24）。可能应为 `go 1.24`。

---

## 修复顺序

1. **P0-1:** 修复 QoS 引擎数据竞争 → 可能导致数据损坏
2. **P0-2:** 拒绝通配符 PUBLISH → 协议合规性
3. **P0-3:** 修复 Prometheus 标签基数 → 可观测性崩溃风险
4. **P1-4:** 修复 ACL 查找键 → 安全漏洞
5. **P1-5:** 删除密码的 string 转换 → 安全加固
6. **P1-6:** 统一 topic 匹配 → 消除行为差异
7. **P1-7:** 修复内存 store MatchRetained → 功能正确性
8. **P2:** 次轮修复（死代码、日志、错误聚合）
9. **P3:** 功能增强（共享订阅、速率限制、TLS 热重载）
