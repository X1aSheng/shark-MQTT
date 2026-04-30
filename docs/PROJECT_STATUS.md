# Shark-MQTT 项目状态报告

## 项目概览

Shark-MQTT 是一个用 Go 编写的高性能 MQTT Broker，支持 MQTT 3.1.1 和 5.0 协议。项目已从 Shark-Socket 中提取为独立项目，采用分层架构设计。

**整体完成度：约 93%** | 最后更新：2026-04-30

---

## 项目规模

| 指标 | 数值 |
|------|------|
| Go 源文件 | 96（含 37 测试文件） |
| 代码行数 | ~21,000 |
| 单元测试 | 207 |
| 集成测试 | 77 |
| 基准测试 | 69 |
| 测试总运行次数 | 388（含子测试） |
| 总覆盖率 | 36.1% |
| 核心包覆盖率（pkg/logger） | 100% |
| 核心包覆盖率（pkg/bufferpool） | 100% |
| 核心包覆盖率（plugin） | 96.6% |
| 核心包覆盖率（store/memory） | 87.4% |
| 核心包覆盖率（store/badger） | 88.3% |
| 核心包覆盖率（broker） | 51.7% |

---

## 已完成的功能

### 核心协议支持
- ✅ **15 种 MQTT 包类型**: CONNECT, CONNACK, PUBLISH, PUBACK, PUBREC, PUBREL, PUBCOMP, SUBSCRIBE, SUBACK, UNSUBSCRIBE, UNSUBACK, PINGREQ, PINGRESP, DISCONNECT, AUTH
- ✅ **双协议版本**: MQTT 3.1.1 & 5.0
- ✅ **QoS 等级**: QoS 0 (最多一次), QoS 1 (至少一次), QoS 2 (恰好一次)
- ✅ **主题通配符**: `+` (单级), `#` (多级) + `$SYS` 系统主题隔离
- ✅ **保留消息**: Last known good value per topic
- ✅ **Will 消息**: 遗嘱消息 + MQTT 5.0 Will Delay Interval
- ✅ **MQTT 5.0 Session Expiry Interval**: 完整的过期间隔支持
- ✅ **MQTT 5.0 CONNACK 属性**: Server 广告 ReceiveMaximum, MaximumQoS, RetainAvailable 等

### 会话管理
- ✅ **Manager**: 线程安全的会话管理（Register/Unregister/Kick）
- ✅ **Session 状态机**: State()/SetState()/IsConnected()/Disconnecting()
- ✅ **Session Takeover 安全**: conn identity check 防止旧连接清理新连接状态
- ✅ **持久会话**: Save/Restore 支持 CleanSession=false，含 ExpiryInterval 持久化
- ✅ **Session 统计**: TrackReceived()/TrackSent()/Stats()

### 存储层
- ✅ **SessionStore**: Memory/Redis/BadgerDB 实现
- ✅ **MessageStore**: Memory/Redis/BadgerDB 实现
- ✅ **RetainedStore**: Memory/Redis/BadgerDB 实现
- ✅ **深层拷贝**: GetSession 安全拷贝 Inflight map 和 Subscriptions slice

### 认证与授权
- ✅ **Authenticator 接口**: AllowAll/DenyAll/StaticAuth/FileAuth/ChainAuth
- ✅ **Authorizer 接口**: ACL-based publish/subscribe checks
- ✅ **授权已接入**: HandlePublish 调用 CanPublish, HandleSubscribe 调用 CanSubscribe
- ✅ **错误统一**: auth.go 和 store/errors.go 均 alias errs 包错误

### 插件系统
- ✅ **PluginManager**: Hook-based plugin manager
- ✅ **基础钩子**: OnAccept, OnConnected, OnMessage, OnClose
- ✅ **错误收集**: Dispatch 继续执行所有插件，收集全部错误

### 基础设施
- ✅ **Logger**: Structured logging with slog (100% 测试覆盖)
- ✅ **Metrics**: 17+ Prometheus 指标接口 (1.8% 覆盖，noop 实现)
- ✅ **BufferPool**: 字节缓冲池 (100% 测试覆盖)
- ✅ **配置系统**: YAML + ENV 配置加载 (54.3% 覆盖)
- ✅ **TLS**: 可配置 TLS (min TLS 1.2)
- ✅ **连接限制**: 可配置最大连接数（含预认证强制）
- ✅ **健康端点**: `/healthz` / `/readyz`
- ✅ **CLI**: flag 解析，`--allow-all` 开发模式开关

### 测试
- ✅ **单元测试**: 207 项，覆盖 broker, protocol, store, client, config, errs, pkg, plugin, api
- ✅ **集成测试**: 77 项，覆盖 connect, pubsub, delivery, qos, persistent_session, will, wildcard, retained, unsubscribe, edge_case, deploy
- ✅ **基准测试**: 69 项，覆盖 E2E、数据验证、微基准
- ✅ **跨平台脚本**: Go runner + shell/bat wrappers

---

## 运行时架构

```
api.NewBroker()
  → broker.New()              # TopicTree, QoSEngine, WillHandler, Manager
  → server.NewMQTTServer()    # 网络层

broker.Broker
  → TopicTree.Match()         # O(log n) 主题匹配
  → QoSEngine.RetryLoop()     # QoS 自动重试
  → WillHandler.Trigger()     # 异常断开时触发 Will
  → Manager.CreateSession()   # 会话管理（含 Kick 旧会话，conn identity check）
  → Metrics.Inc*()            # 指标收集
  → PluginManager.Dispatch()  # 插件钩子（错误收集模式）
```

---

## 审查与修复历史

### Phase 1-2 (2026-04-25 ~ 2026-04-26)
- 修复包名冲突、编译阻塞问题
- 移除死代码 session_registry.go
- 修复 QoS 2 PUBCOMP 包类型缺失

### Phase 3 (2026-04-28)
- 全面代码审查（REVIEW_PHASE1 ~ PHASE3）
- 测试增强
- 文档完善

### Phase 4 (2026-04-28)
- MQTT 5.0 属性编解码完善
- 性能基准扩展

### Phase 5 (2026-04-29)
- 全面审查发现 18 个缺陷（REVIEW_PHASE5）
- C-001: Session Takeover 竞态条件修复（conn identity check）
- C-002: MemoryStore 深层拷贝
- C-003: 验证订阅主题过滤器
- M-003: handleUnsubscribe 主题验证

### Phase 6 (2026-04-29)
- M-001: MQTT 5.0 Session Expiry Interval 支持
- M-004: CONNACK MQTT 5.0 能力属性广告
- M-007: Plugin Dispatch 错误收集模式
- L-001/L-002: errs 包错误整合
- L-004: RemainingLength 上界验证

### Phase 7 — 代码审查修复 (2026-04-30)
基于 CODE_REVIEW_250430_112600.md (62 项发现)，修复了以下问题：

**Critical 修复:**
- ✅ C-2: 保留消息空 payload 跨存储一致性（Badger/Redis 空 payload 正确删除）
- ✅ C-4: Redis topicPatternToRedis `+` 通配符映射（`[^/]` → `*`）
- ✅ C-1: 协议属性解码错误不再静默丢弃（subscribe/suback/unsubscribe/unsuback）
- ⏳ C-3: 客户端 readLoop 连接竞态（延期 — 需架构性改动）

**High 修复:**
- ✅ H-5: Accept 循环添加退避 + `net.ErrClosed` 检查
- ✅ H-11: Prometheus `MustRegister` → `registerOrReuse` 安全注册
- ✅ H-9: Topic 过滤器空 level 检查（leading/trailing/consecutive `/`）
- ✅ H-8: MQTT 5.0 属性值范围验证（PayloadFormat、RequestProblemInfo、MaximumQoS）
- ✅ H-10: `NextPacketID` RLock 替代 Lock

**Medium 修复:**
- ✅ M-1: Plugin Dispatch 上下文取消传播
- ✅ M-2: Plugin panic 恢复包含堆栈跟踪
- ✅ M-19: Logger 支持 JSON 格式输出（`WithFormat` option）
- ✅ M-21: `qosLabel` 预计算查找表
- ✅ M-13: UTF-8 控制字符验证（U+0001-U+001F, U+007F-U+009F）

---

## 测试覆盖率详情

| 包 | 覆盖率 | 状态 |
|------|--------|------|
| pkg/bufferpool | 100.0% | ✅ |
| pkg/logger | 100.0% | ✅ |
| plugin | 96.6% | ✅ |
| store/badger | 88.3% | ✅ |
| store/memory | 87.4% | ✅ |
| config | 54.3% | ✅ |
| api | 52.3% | ✅ |
| broker | 51.7% | ✅ |
| protocol | 25.0% | 🟡 |
| client | 22.7% | 🟡 |
| pkg/metrics | 1.8% | 🟡 (noop 实现) |
| store/redis | 0.0% | 🟡 (需 Redis 实例) |

---

## 剩余待改进项

### 🟠 P1 - 中等优先级
| ID | 描述 |
|----|------|
| M-002 | 实现离线消息队列 |
| M-005 | 文档化 StaticAuth ACL 行为 |
| M-006 | TopicTree 匹配缓存 |

### 🟡 P2 - 低优先级
| ID | 描述 |
|----|------|
| L-003 | 改进 writePacket 错误传播 |
| L-005 | 修复 client Connect TOCTOU |
| L-007 | 测试中使用命名超时常量 |
| L-008 | 添加协议模糊测试 |

### 🟢 P3 - 高级功能
- MQTT 5.0 Enhanced Authentication (AUTH 报文交换)
- HTTP Admin API
- Topic Alias 支持
- 大规模连接压力测试

---

## 审查文档索引

| 文档 | 日期 | 内容 |
|------|------|------|
| REVIEW_PHASE1-20260426-105102.md | 2026-04-26 | 初始审查 |
| REVIEW_PHASE2-20260428-121322.md | 2026-04-28 | 第二轮审查 |
| REVIEW_PHASE3-20260428-152513.md | 2026-04-28 | 第三轮审查 |
| REVIEW_PHASE3-20260428-181500.md | 2026-04-28 | 第三轮补充 |
| REVIEW_PHASE4-20260428-201000.md | 2026-04-28 | 第四轮审查 |
| REVIEW_PHASE5-20260429-093000.md | 2026-04-29 | 第五轮审查（18 缺陷） |
| FIX_PLAN_01_CRITICAL.md | 2026-04-28 | 关键缺陷修复 |
| FIX_PLAN_02_CRITICAL_MEDIUM.md | 2026-04-29 | Phase 5 修复（C-001~M-003） |
| FIX_PLAN_03_MEDIUM_LOW.md | 2026-04-29 | Phase 6 修复（M-001~L-004） |
