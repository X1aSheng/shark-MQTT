# Shark-MQTT 项目状态报告

## 项目概览

Shark-MQTT 是一个用 Go 编写的高性能 MQTT Broker，支持 MQTT 3.1.1 和 5.0 协议。项目已从 Shark-Socket 中提取为独立项目，采用了分层架构设计。

## 已完成的功能

### 核心协议支持
- ✅ **15种MQTT包类型**: CONNECT, CONNACK, PUBLISH, PUBACK, PUBREC, PUBREL, PUBCOMP, SUBSCRIBE, SUBACK, UNSUBSCRIBE, UNSUBACK, PINGREQ, PINGRESP, DISCONNECT, AUTH
- ✅ **QoS等级**: QoS 0 (最多一次), QoS 1 (至少一次), QoS 2 (恰好一次)
- ✅ **主题通配符**: `+` (单级), `#` (多级)
- ✅ **保留消息**: Last known good value per topic
- ✅ **Will消息**: Last-will with delayed delivery
- ✅ **持久会话**: CleanSession=false 跨连接会话持久化

### 存储层
- ✅ **SessionStore**: Memory/Redis/BadgerDB 实现
- ✅ **MessageStore**: Memory/Redis/BadgerDB 实现
- ✅ **RetainedStore**: Memory/Redis/BadgerDB 实现

### 认证层
- ✅ **Authenticator接口**: NoopAuth/StaticAuth/FileAuth/ChainAuth
- ✅ **Authorizer接口**: ACL-based publish/subscribe checks

### 插件系统
- ✅ **PluginManager**: Hook-based plugin manager
- ✅ **基础钩子**: OnAccept, OnConnect, OnMessage, OnClose

### 基础设施
- ✅ **Logger**: Structured logging with slog
- ✅ **Metrics接口**: 17+ methods for Prometheus
- ✅ **配置系统**: YAML + ENV 配置加载

### 测试
- ✅ **集成测试**: connect_test.go, pubsub_test.go, qos_test.go
- ✅ **单元测试**: protocol, session, server, auth, config, api
- ✅ **测试工具**: testutils/ 包含 mock 实现

---

## 当前架构状态

### 核心架构已集成 ✅

`api/api.go` 已经正确使用了 `broker/broker.go`（而非 `infra/broker/broker.go`），这意味着：
- ✅ **TopicTree**: O(log n) 主题匹配已在运行路径中
- ✅ **QoSEngine**: QoS 自动重试和 inflight 追踪已启用
- ✅ **WillHandler**: 异常断开时触发 Will 消息
- ✅ **Metrics 集成**: broker 核心中已调用 metrics（连接、消息、认证失败等）
- ✅ **Plugin 集成**: PluginManager 已接入 broker

### 运行时架构

```
api.NewBroker()
  → broker.New()                 # 包含 TopicTree, QoSEngine, WillHandler
  → server.NewMQTTServer()       # 网络层
  
broker.Broker
  → TopicTree.Match()            # O(log n) 主题匹配
  → QoSEngine.RetryLoop()        # QoS 自动重试
  → WillHandler.Trigger()        # 异常断开时触发 Will
  → Metrics.Inc*()               # 指标收集
  → PluginManager.Dispatch()     # 插件钩子
```

---

## 待改进功能（按优先级排序）

### 🟠 P1 - 重要功能

#### 1. Session Registry 缺失
**状态**: 无 `session/registry.go`

**问题描述**:
- 架构文档要求 `session/registry.go` 管理所有 session
- 需要实现：Register, Unregister, Kick, ReplaceSession, SessionByClientID
- 当前无会话跟踪/踢出/替换功能

**需要创建的文件**:
- `session/registry.go` - Session registry with thread-safe map

**需要的功能**:
```go
type Registry struct {
    sessions map[string]*Session  // clientID -> Session
    mu       sync.RWMutex
}

func (r *Registry) Register(sess *Session) error
func (r *Registry) Unregister(clientID string)
func (r *Registry) Kick(clientID string, reason CloseReason) error
func (r *Registry) ReplaceSession(oldClientID, newClientID string) error
func (r *Registry) SessionByClientID(clientID string) *Session
func (r *Registry) CloseAll(reason CloseReason)
func (r *Registry) Count() int
```

#### 2. Session 状态机未实现
**状态**: `Session.State` 字段定义但从未使用

**问题描述**:
- `session/types.go` 定义了 `State` 枚举 (Created, Connected, Disconnected, Closed)
- `session/session.go` 有 State 字段但从不更新
- 无状态转换逻辑

**需要的实现**:
- 状态转换验证（不能从 Closed 到 Connected）
- 状态变更时的副作用（如通知 Metrics）

#### 3. Session 统计未追踪
**状态**: `Session.Stats` 定义但从未更新

**问题描述**:
- 定义了 MessagesSent, MessagesReceived, BytesSent, BytesReceived
- 但在消息发送/接收时从不更新这些计数器

#### 4. 授权未接入 Broker
**状态**: Authorizer 接口存在但未在业务逻辑中使用

**问题描述**:
- `HandlePublish` 不检查 `CanPublish`
- `HandleSubscribe` 不检查 `CanSubscribe`
- 所有发布/订阅请求都被允许，无 ACL 检查

**需要修改**:
- `broker/broker.go` - HandlePublish 中调用 authz.CanPublish
- `broker/broker.go` - HandleSubscribe 中调用 authz.CanSubscribe

---

### 🟡 P2 - 测试和基础设施

#### 5. 缺失测试
| 测试文件 | 状态 | 描述 |
|----------|------|------|
| `broker/broker_test.go` | ❌ 缺失 | Broker 核心单元测试 |
| `test/integration/will_test.go` | ❌ 缺失 | Will 消息集成测试 |
| `test/bench/*.go` | ❌ 缺失 | 基准测试（延迟/吞吐量） |
| `plugin/manager_test.go` | ❌ 缺失 | 插件系统测试 |
| `store/badger/*_test.go` | ❌ 缺失 | BadgerDB 后端测试 |
| `store/redis/*_test.go` | ❌ 缺失 | Redis 后端测试 |
| `session/registry_test.go` | ❌ 缺失 | Registry 测试（需先实现） |
| `errs/errors_test.go` | ❌ 缺失 | 错误包测试 |
| `infra/` 测试 | ❌ 缺失 | Logger, Metrics, BufferPool 测试 |

### 6. `infra/bufferpool/` 缺失
**状态**: 架构提及但目录不存在

**问题描述**:
- 用于包编码的可重用字节缓冲池
- 减少内存分配和 GC 压力

---

### 🟢 P3 - 次要功能

#### 7. MQTT 5.0 Enhanced Auth
**状态**: AuthPacket (type 15) 存在但无认证交换逻辑

#### 8. Server 选项模式未使用
**状态**: `server/options.go` 存在但 `NewMQTTServer` 忽略所有选项

#### 9. HTTP Admin API
**状态**: 未实现管理接口

---

## 已修复的关键问题

### 1. QoS 2 PUBCOMP 包类型缺失 ✅
**问题**: `sendPubComp` 和 `sendPubAck` 函数未设置 `FixedHeader.PacketType`，导致客户端收到无效 MQTT 包
**修复**: 在 `broker/broker.go` 中为 PUBCOMP 和 PUBACK 包设置正确的 `PacketType`
**状态**: ✅ 已修复，所有集成测试通过

### 2. QoS 重试测试时序问题 ✅
**问题**: `TestQoSEngine_Retry_MaxRetriesExceeded` 测试存在时序竞态条件
**修复**: 在追踪 QoS 消息前添加小延迟，确保重试 ticker 已启动
**状态**: ✅ 已修复，测试稳定通过

---

## 实现路线图

### Phase 1: 补全会话管理
1. 实现 `session/registry.go`
2. 接入 Session 状态机
3. 接入 Session 统计

### Phase 2: 测试和基础设施
4. 补充所有缺失的单元测试
5. 补充集成测试
6. 实现 benchmark 测试
7. 实现 `infra/bufferpool/`

### Phase 3: 高级功能
8. MQTT 5.0 Enhanced Auth
9. Server 选项模式重构
10. HTTP Admin API
