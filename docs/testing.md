# Testing Guide

Shark-MQTT 的测试体系覆盖协议层、业务层和性能层三个维度，包含单元测试、集成测试和基准测试共 328 项。

---

## 目录

- [测试架构](#测试架构)
- [测试统计](#测试统计)
- [单元测试](#单元测试)
- [集成测试](#集成测试)
- [基准测试](#基准测试)
- [测试脚本](#测试脚本)
- [Makefile 目标](#makefile-目标)
- [日志系统](#日志系统)
- [覆盖率](#覆盖率)

---

## 测试架构

```
┌────────────────────────────────────────────────────────────┐
│  Benchmark Tests (57)                                     │
│  tests/bench/                                             │
│  ├── broker_bench_test.go    — 全栈 TCP 基准              │
│  ├── data_delivery_bench_test.go — E2E 数据验证基准       │
│  └── micro_bench_test.go     — 组件级微基准               │
├────────────────────────────────────────────────────────────┤
│  Integration Tests (47)                                    │
│  tests/integration/                                        │
│  ├── connect_test.go         — 连接与会话                 │
│  ├── pubsub_test.go          — 发布订阅                   │
│  ├── delivery_test.go        — 多订阅者消息投递           │
│  ├── qos_test.go             — QoS 0/1/2 完整流程         │
│  ├── persistent_session_test.go — 持久会话               │
│  ├── will_test.go            — 遗嘱消息                   │
│  ├── topic_wildcard_test.go  — 通配符匹配                 │
│  ├── unsubscribe_test.go     — 取消订阅                   │
│  ├── retained_test.go        — 保留消息                   │
│  └── edge_case_test.go       — 边界与异常                 │
├────────────────────────────────────────────────────────────┤
│  Unit Tests (224)                                          │
│  各包内 *_test.go 文件                                     │
│  broker/ protocol/ store/ pkg/ api/ client/ config/       │
│  plugin/ errs/                                             │
└────────────────────────────────────────────────────────────┘
```

---

## 测试统计

| 类型 | 数量 | 位置 |
|------|------|------|
| 单元测试 | 224 | 各包 `*_test.go` |
| 集成测试 | 47 | `tests/integration/` |
| 基准测试 | 57 | `tests/bench/` |
| **合计** | **328** | |

### 各包测试明细

| 包 | 测试数 | 基准测试数 | 说明 |
|----|--------|-----------|------|
| broker/ | 99 | 0 | 核心逻辑：TopicTree、QoS引擎、会话、遗嘱、认证、服务器 |
| store/memory/ | 20 | 0 | 内存存储：会话、消息、保留消息 |
| store/redis/ | 13 | 9 | Redis 存储：消息、保留、会话 |
| store/badger/ | 9 | 0 | BadgerDB 持久化存储 |
| protocol/ | 10 | 0 | MQTT 3.1.1 & 5.0 编解码（15种报文） |
| client/ | 8 | 0 | MQTT 客户端连接、发布、订阅 |
| plugin/ | 14 | 3 | 插件系统：注册、分发 |
| config/ | 7 | 0 | 配置解析与校验 |
| pkg/logger/ | 7 | 0 | slog 日志适配 |
| pkg/bufferpool/ | 10 | 0 | 缓冲池管理 |
| pkg/metrics/ | 3 | 0 | Prometheus 指标 |
| api/ | 4 | 0 | 公共 API 与健康端点 |
| errs/ | 4 | 0 | 错误定义 |

---

## 单元测试

单元测试在各包内直接定义，隔离测试单个组件逻辑。

### 运行方式

```bash
# 全部单元测试
go test -v -count=1 ./...

# 单独包
go test -v ./broker/...
go test -v ./protocol/...
go test -v ./store/memory/...

# 带 race 检测
go test -v -race -count=1 ./...
```

### 编写规范

```go
func TestTopicTree_Subscribe(t *testing.T) {
    tree := NewTopicTree()
    tree.Subscribe("home/temp", "client1", 1)

    subs := tree.Match("home/temp")
    if len(subs) != 1 {
        t.Fatalf("expected 1 subscription, got %d", len(subs))
    }
}
```

- 使用 `t.Helper()` 标记辅助函数
- 使用 `t.Cleanup()` 管理资源释放
- 优先使用 table-driven 子测试

---

## 集成测试

集成测试通过真实 TCP 连接验证完整的 MQTT 工作流，每个测试启动独立 broker 实例。

### 测试清单

#### 连接与会话（5 项）

| 测试函数 | 说明 |
|----------|------|
| `TestConnectFlow` | CONNECT/CONNACK 握手完整流程 |
| `TestPersistentSession_CleanSessionFalse` | CleanSession=false 会话保持 |
| `TestPersistentSession_Reconnect` | 断线重连后会话恢复 |
| `TestPersistentSession_OfflinePublish` | 离线期间消息暂存 |
| `TestPersistentSession_KickPrevious` | 重复 ClientID 踢掉旧连接 |

#### 发布订阅（4 项）

| 测试函数 | 说明 |
|----------|------|
| `TestPubSub` | 基础发布-订阅消息验证 |
| `TestQoS0` | QoS 0 消息投递 |
| `TestQoS1` | QoS 1 完整 PUBACK 流程 |
| `TestQoS2` | QoS 2 四报文握手（PUBLISH→PUBREC→PUBREL→PUBCOMP） |

#### 多订阅者消息投递（12 项）

| 测试函数 | 说明 |
|----------|------|
| `TestMultipleSubscribers_SameTopic` | 同一主题多订阅者广播 |
| `TestMixedQoSSubscribers` | 混合 QoS 订阅者降级投递 |
| `TestMessageOrdering` | 消息顺序保证 |
| `TestBurstPublish` | 连续发布多条消息 |
| `TestLargePayload` | 大负载投递（64KB） |
| `TestBinaryPayload` | 二进制负载投递 |
| `TestEmptyPayload` | 空负载投递 |
| `TestUnicodePayload` | Unicode 负载投递 |
| `TestOverlappingSubscriptions` | 重叠订阅匹配 |
| `TestMultiTopicSubscribe` | 单次 SUBSCRIBE 多主题 |
| `TestPublishToSelf` | 发布者也是订阅者（跳过自身） |
| `TestStructuredBinaryPayload` | 结构化二进制数据 |

#### 遗嘱消息（3 项）

| 测试函数 | 说明 |
|----------|------|
| `TestWillMessageOnAbnormalDisconnect` | 异常断连触发遗嘱 |
| `TestWillMessageNotPublishedOnGracefulDisconnect` | 正常 DISCONNECT 不触发 |
| `TestWillMessageQoS` | 遗嘱消息 QoS 投递 |

#### 通配符匹配（5 项）

| 测试函数 | 说明 |
|----------|------|
| `TestWildcardPlus` | 单层通配符 `+` |
| `TestWildcardHash` | 多层通配符 `#` |
| `TestWildcardHashRoot` | 根级 `#` 匹配所有 |
| `TestWildcardMixed` | 混合 `+` 和 `#` |
| `TestWildcardMultipleSubscribers` | 多订阅者通配符 |

#### 保留消息（5 项）

| 测试函数 | 说明 |
|----------|------|
| `TestRetainedMessage_NewSubscriber` | 新订阅者获取保留消息 |
| `TestRetainedMessage_Update` | 更新保留消息 |
| `TestRetainedMessage_Delete` | 空负载删除保留消息 |
| `TestRetainedMessage_WildcardDelivery` | 通配符匹配保留消息 |
| `TestRetainedMessage_QoSDowngrade` | QoS 降级投递 |

#### 取消订阅与系统主题（8 项）

| 测试函数 | 说明 |
|----------|------|
| `TestUnsubscribe_StopsDelivery` | 取消后停止投递 |
| `TestUnsubscribe_MultipleTopics` | 批量取消订阅 |
| `TestUnsubscribe_WildcardTopic` | 通配符取消订阅 |
| `TestResubscribeAfterUnsubscribe` | 取消后重新订阅 |
| `TestSystemTopic_NotReceivedByNormalSub` | 系统主题隔离 |
| `TestQoS1_PublisherAck` | QoS 1 发布者收到 PUBACK |
| `TestQoS2_FullHandshake` | QoS 2 四报文握手验证 |
| `TestQoS1_PublishNoSubscriber` | 无订阅者时正常完成 |

#### 边界与异常（5 项）

| 测试函数 | 说明 |
|----------|------|
| `TestAuth_Failure` | 认证失败返回 CONNACK 拒绝 |
| `TestDuplicateClientID` | 重复 ClientID 踢掉旧连接 |
| `TestInvalidTopicFilter` | 无效主题过滤器 |
| `TestMaxConnections` | 连接数上限 |
| `TestEmptyClientID` | 空 ClientID 处理 |

### 运行方式

```bash
# 全部集成测试
go test -v -count=1 -timeout 180s ./tests/integration/...

# 单个测试
go test -v -run TestQoS2 ./tests/integration/...
```

所有集成测试均验证 **端到端数据完整性**：发布消息后确认订阅者收到正确的 topic 和 payload。

---

## 基准测试

基准测试分为三层：全栈 TCP 基准、E2E 数据验证基准、组件微基准。

### 全栈 TCP 基准（18 项）— `broker_bench_test.go`

通过真实 TCP 连接测量完整 MQTT 协议栈性能。

| 基准函数 | 说明 |
|----------|------|
| `BenchmarkConnectionEstablish` | TCP 连接建立（dial + close） |
| `BenchmarkMQTTConnect` | 完整 MQTT CONNECT 握手 |
| `BenchmarkPublishQos0` | QoS 0 消息发布 |
| `BenchmarkPublishQos1` | QoS 1 发布 + PUBACK |
| `BenchmarkPublishQos2` | QoS 2 完整四报文握手 |
| `BenchmarkConcurrentPublish` | 并行发布（RunParallel） |
| `BenchmarkTopicWildcardMatch` | 通配符主题匹配 |
| `BenchmarkPersistentSession` | 持久会话 CONNECT+SUB+PUB |
| `BenchmarkPayload_64B` | 64 字节负载 |
| `BenchmarkPayload_256B` | 256 字节负载 |
| `BenchmarkPayload_1KB` | 1KB 负载 |
| `BenchmarkPayload_4KB` | 4KB 负载 |
| `BenchmarkPayload_16KB` | 16KB 负载 |
| `BenchmarkPayload_128KB` | 128KB 负载 |
| `BenchmarkFanOut_1Sub` | 1 订阅者扇出 |
| `BenchmarkFanOut_5Subs` | 5 订阅者扇出 |
| `BenchmarkFanOut_10Subs` | 10 订阅者扇出 |
| `BenchmarkFanOut_50Subs` | 50 订阅者扇出 |

### E2E 数据验证基准（12 项）— `data_delivery_bench_test.go`

与全栈基准不同，每个基准读取并 **验证** 订阅者收到的 PUBLISH 报文（topic + payload），测量完整的 publish→broker→subscribe→verify 往返。

| 基准函数 | 说明 |
|----------|------|
| `BenchmarkE2E_QoS0_DataVerify` | QoS 0 数据验证 |
| `BenchmarkE2E_QoS1_DataVerify` | QoS 1 数据验证 + PUBACK |
| `BenchmarkE2E_QoS2_DataVerify` | QoS 2 四报文验证 |
| `BenchmarkE2E_RetainedMessage` | 保留消息投递验证 |
| `BenchmarkE2E_WillMessage` | 遗嘱消息投递验证 |
| `BenchmarkE2E_WildcardDelivery` | 通配符投递验证 |
| `BenchmarkE2E_FanOut_VerifyAll` | 多订阅者全量验证 |
| `BenchmarkE2E_Payload_String` | 字符串负载验证 |
| `BenchmarkE2E_Payload_Binary` | 二进制负载验证 |
| `BenchmarkE2E_Payload_Unicode` | Unicode 负载验证 |
| `BenchmarkE2E_Payload_Empty` | 空负载验证 |
| `BenchmarkE2E_Payload_64KB` | 64KB 负载验证 |

### 组件微基准（27 项）— `micro_bench_test.go`

隔离测量单个组件性能，排除网络 I/O 干扰。

| 类别 | 基准函数 | 说明 |
|------|----------|------|
| **TopicTree** | `BenchmarkTopicTree_Subscribe` | 订阅操作 |
| | `BenchmarkTopicTree_Match_Exact` | 精确主题匹配 |
| | `BenchmarkTopicTree_Match_WildcardPlus` | `+` 通配符匹配 |
| | `BenchmarkTopicTree_Match_WildcardHash` | `#` 通配符匹配 |
| | `BenchmarkTopicTree_Match_ManySubscribers` | 多订阅者匹配 |
| | `BenchmarkTopicTree_Unsubscribe` | 取消订阅 |
| **Codec** | `BenchmarkCodec_EncodeConnect` | 编码 CONNECT |
| | `BenchmarkCodec_DecodeConnect` | 解码 CONNECT |
| | `BenchmarkCodec_EncodePublish` | 编码 PUBLISH |
| | `BenchmarkCodec_DecodePublish` | 解码 PUBLISH |
| | `BenchmarkCodec_EncodePublishQos1` | 编码 QoS 1 PUBLISH |
| | `BenchmarkCodec_RoundTripPublish` | 编解码往返 |
| | `BenchmarkCodec_EncodeLargePayload` | 大负载编码 |
| **QoSEngine** | `BenchmarkQoSEngine_TrackQoS1` | QoS 1 消息追踪 |
| | `BenchmarkQoSEngine_TrackAckQoS1` | QoS 1 ACK 处理 |
| | `BenchmarkQoSEngine_TrackQoS2` | QoS 2 消息追踪 |
| | `BenchmarkQoSEngine_MultiClient` | 多客户端 QoS |
| **Session** | `BenchmarkManager_CreateSession` | 创建会话 |
| | `BenchmarkManager_GetSession` | 查询会话 |
| | `BenchmarkManager_MultiClientLookup` | 多客户端查找 |
| | `BenchmarkManager_RestoreSession` | 恢复会话 |
| **BufferPool** | `BenchmarkBufferPool_GetPut` | 获取/归还 |
| | `BenchmarkBufferPool_GetPut_Default` | 默认池操作 |
| | `BenchmarkBufferPool_Parallel` | 并行操作 |
| | `BenchmarkBufferPool_AllocVsPool` | 池化 vs 直接分配 |
| **MemoryStore** | `BenchmarkMemoryStore_SessionSave` | 会话保存 |
| | `BenchmarkMemoryStore_SessionGet` | 会话读取 |

### 运行方式

```bash
# 全部基准测试
go test -bench=. -benchmem -benchtime=1s ./tests/bench/...

# 仅全栈基准
go test -bench=BenchmarkPublish -benchmem ./tests/bench/...

# 仅 E2E 数据验证基准
go test -bench=BenchmarkE2E -benchmem ./tests/bench/...

# 仅微基准
go test -bench=BenchmarkTopicTree -benchmem ./tests/bench/...

# 自定义时长
go test -bench=. -benchtime=5s ./tests/bench/...

# CPU profiling
go test -bench=. -cpuprofile=cpu.prof ./tests/bench/...
go tool pprof cpu.prof
```

---

## 测试脚本

三套跨平台测试脚本提供完全一致的功能，所有测试运行均自动保存带毫秒时间戳的日志到 `logs/` 目录。

日志格式：`logs/{YYYYMMDD_HHmmss_fff}_{type}.log`

### 脚本对应关系

| 平台 | 脚本 |
|------|------|
| Linux / macOS / Git Bash / WSL | `./scripts/test.sh <target>` |
| Windows CMD | `scripts\test.bat <target>` |
| Windows PowerShell | `.\scripts\test.ps1 <target>` |

### 目标

| 目标 | 说明 | 日志文件 |
|------|------|----------|
| `all` | 单元 + 集成 + 基准 + 汇总（默认） | 逐包日志 + 集成 + 基准 + 汇总 |
| `unit` | 全部单元测试 | `{ts}_unit.log` |
| `integration` | 集成测试（180s 超时） | `{ts}_integration.log` |
| `bench` | 基准测试（`BENCHTIME`，默认 1s） | `{ts}_benchmark.log` |
| `quick` | 快速基准（500ms） | `{ts}_benchmark.log` |
| `race` | 单元测试 + race 检测 | `{ts}_race.log` |
| `coverage` | 覆盖率报告（HTML + 文本） | `{ts}_coverage.log` |
| `redis` | Redis 存储测试（需 `MQTT_REDIS_ADDR`） | `{ts}_redis.log` |
| `ci` | CI 完整流水线（vet + race + build） | `{ts}_ci.log` |

### 示例

```bash
# 运行全部
./scripts/test.sh

# 单独目标
./scripts/test.sh unit
./scripts/test.sh integration
./scripts/test.sh bench
./scripts/test.sh race
./scripts/test.sh coverage
./scripts/test.sh ci

# Windows CMD
scripts\test.bat all
scripts\test.bat unit

# Windows PowerShell
.\scripts\test.ps1 all
.\scripts\test.ps1 unit

# 自定义基准时长
BENCHTIME=5s ./scripts/test.sh bench

# Redis 测试
MQTT_REDIS_ADDR=localhost:6379 ./scripts/test.sh redis
```

### `all` 目标日志结构

```
logs/
├── 20260426_194022_123_unit_api.log
├── 20260426_194022_123_unit_broker.log
├── 20260426_194022_123_unit_client.log
├── 20260426_194022_123_unit_config.log
├── 20260426_194022_123_unit_errs.log
├── 20260426_194022_123_unit_pkg.log
├── 20260426_194022_123_unit_plugin.log
├── 20260426_194022_123_unit_protocol.log
├── 20260426_194022_123_unit_store.log
├── 20260426_194035_456_integration.log
├── 20260426_194042_789_benchmark.log
└── 20260426_194205_012_summary.log
```

---

## Makefile 目标

```bash
make test              # 单元测试
make test-integration  # 集成测试
make test-race         # race 检测
make bench-quick       # 快速基准（1s）
make bench             # 完整基准（5s x 3 轮）
make test-coverage     # 覆盖率报告
make ci                # CI 完整流水线
```

---

## 日志系统

所有运行时日志和测试日志的时间戳精度为 **毫秒**。

- Go 运行时日志：`log.SetFlags(log.LstdFlags | log.Lmicroseconds)`（`pkg/logger/slog.go`）
- 测试脚本日志：`{YYYYMMDD_HHmmss_fff}` 毫秒时间戳
- Broker 连接日志：EOF/早关连接使用原子计数器汇总，每轮测试结束时输出 1 条统计（如 `[server] 10000 connections closed before CONNECT`）

---

## 覆盖率

### 生成报告

```bash
# 文本报告
go test -coverprofile=coverage.out -covermode=atomic -count=1 ./...
go tool cover -func=coverage.out

# HTML 报告
go tool cover -html=coverage.out -o coverage.html

# 使用脚本（自动保存日志）
./scripts/test.sh coverage
```

### 覆盖率阈值

| 模块 | 最低覆盖率 |
|------|-----------|
| protocol/ | 80% |
| broker/ | 75% |
| store/memory/ | 80% |
| store/redis/ | 70% |
| pkg/ | 90% |
| 总体 | 60% |

---

## 另见

- [开发指南](development.md)
- [配置指南](configuration.md)
- [性能指南](performance.md)
- [架构文档](shark-mqtt%20architecture.md)
