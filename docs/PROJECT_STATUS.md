# Shark-MQTT 项目状态报告

## 项目概览

Shark-MQTT 是一个用 Go 编写的高性能 MQTT Broker，支持 MQTT 3.1.1 和 5.0 协议。项目作为独立服务运行，采用分层架构设计；与其他系统通过数据库、Redis/cache、消息/事件数据契约进行互通，不维护进程内网关适配层。

**项目版本基准：1.0.0** | **整体完成度：约 96%** | 最后更新：2026-05-06 (Phase 9 complete)

---

## 项目规模

| 指标 | 数值 |
|------|------|
| Go 源文件 | 96（含 37 测试文件） |
| 代码行数 | ~21,000 |
| 单元测试 | 210 top-level / 307 passed runs |
| 集成测试 | 77 |
| 基准测试 | 67 |
| 测试总运行次数 | 386 passed runs + 67 benchmarks（13 Redis tests skipped when Redis is not configured） |
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

### 插件系统与基础设施

- ✅ **PluginManager**: Hook-based plugin manager，支持 OnAccept / OnConnected / OnMessage / OnClose
- ✅ **Logger**: Structured logging with slog
- ✅ **Metrics**: Prometheus/noop 指标接口，已接入连接、消息、会话、retained、订阅和错误指标
- ✅ **BufferPool**: 字节缓冲池
- ✅ **配置系统**: YAML + ENV 配置加载和验证
- ✅ **TLS**: 可配置 TLS (min TLS 1.2)
- ✅ **连接限制**: 可配置最大连接数（含预认证强制）
- ✅ **健康端点**: `/healthz` / `/readyz`
- ✅ **CLI**: flag 解析，`--allow-all` 开发模式开关

### 测试

- ✅ **单元测试**: 210 项 top-level（最新日志 307 passed runs），覆盖 broker, protocol, store, client, config, errs, pkg, plugin, api
- ✅ **集成测试**: 77 项，覆盖 connect, pubsub, delivery, qos, persistent_session, will, wildcard, retained, unsubscribe, edge_case, deploy
- ✅ **基准测试**: 67 项，覆盖 E2E、数据验证、微基准和 Redis store
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

完整历史已整合到 [代码审查与修复计划归档](CODE_REVIEW_AND_FIX_PLAN.md)。本状态页只保留当前版本摘要。

### 1.0.0 发布前完成项

- Phase 1-6：完成编译阻塞、MQTT 5.0 属性、CONNECT 校验、QoS、Session takeover、MemoryStore 深拷贝、Session Expiry、CONNACK capabilities、errs 整合、RemainingLength 上界等修复。
- Phase 7：完成 retained store 一致性、Redis wildcard、Accept backoff、Prometheus 安全注册、Topic filter 校验、PacketID 竞态、QoS maxInflight、API 配置错误传播、客户端 QoS2 去重等修复。
- Phase 8：完成会话过期清理、Broker QoS2 去重、指标接入、Session.Save 锁优化、客户端错误处理、retained trie 和解码路径统一。
- Phase 9：完成 retained metrics 精确计数、MQTT fixed header flags 校验、CONNECT Will flags 校验。

### 最新验证

- `go run scripts/run_tests.go -mode all` 通过
- `go test ./... -count=1 -timeout=300s` 通过
- `go vet ./...` 通过

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

| 文档 | 内容 |
|------|------|
| [CODE_REVIEW_AND_FIX_PLAN.md](CODE_REVIEW_AND_FIX_PLAN.md) | 所有历史代码审查报告与修复计划的统一归档 |
