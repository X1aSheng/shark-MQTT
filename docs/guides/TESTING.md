# Testing Guide

Shark-MQTT 的测试体系覆盖协议层、业务层和性能层三个维度，包含单元测试、集成测试、缺陷回归测试和基准测试。当前基线为 **461 个单元测试**、**147 个集成测试**、**61 个基准测试**（口径：源码中声明的顶层 `Test*` / `Benchmark*` 函数数，复现方式见 [测试统计](#测试统计)）。脚本 `unit` 模式已纳入 `tests/defects/...` 缺陷回归套件。

---

## 目录

- [测试架构](#测试架构)
- [测试统计](#测试统计)
- [代码规模](#代码规模)
- [单元测试](#单元测试)
- [集成测试](#集成测试)
- [基准测试](#基准测试)
- [测试脚本](#测试脚本)
- [Makefile 目标](#makefile-目标)
- [缺陷回归测试](#缺陷回归测试)
- [静态分析](#静态分析)
- [日志系统](#日志系统)
- [覆盖率](#覆盖率)

---

## 测试架构

```
+------------------------------------------------------------+
|  Benchmarks (61 declared, 58 run on Windows)               |
|  tests/bench/ + plugin/ + pkg/metrics/                     |
|  +-- broker_bench_test.go        - 全栈 TCP 基准 (18)      |
|  +-- data_delivery_bench_test.go - E2E 数据验证基准 (12)   |
|  +-- micro_bench_test.go         - 组件级微基准 (27)       |
+------------------------------------------------------------+
|  Integration Tests (147 declared)                          |
|  tests/integration/  (27 files)                            |
|  +-- connect / pubsub / qos / persistent_session           |
|  +-- delivery / retained / will / topic_wildcard           |
|  +-- session_expiry / message_expiry / outbound_retry      |
|  +-- mqtt31 / qa_mqtt31_verify / subscription_id           |
|  +-- websocket / wss / request_response                    |
|  +-- deploy / observability / graceful_shutdown            |
|  +-- retain_as_published / lifecycle_regression            |
|  (完整文件清单见「集成测试」章)                            |
+------------------------------------------------------------+
|  Defect Regression Tests (tests/defects + package tests)   |
|  tests/defects/                                            |
|  +-- api_default_auth_test.go - 默认认证拒绝回归           |
|  +-- broker_lifecycle_test.go - 服务生命周期回归           |
|  +-- broker_stop_start_race_test.go - Stop/Start 竞态回归  |
|  +-- invalid_will_topic_leak_test.go - 非法遗嘱主题泄漏回归|
|  +-- properties_defect_test.go - MQTT 5.0 属性边界         |
|  +-- topic_filter_defect_test.go - topic filter 边界       |
|  +-- windows_benchmark_test.go - Windows benchmark 隔离    |
+------------------------------------------------------------+
|  Unit Tests (461 declared)                                 |
|  各包内 *_test.go 文件                                     |
|  broker/ protocol/ client/ config/ plugin/ store/          |
|  store/memory/ pkg/ api/ errs/                             |
+------------------------------------------------------------+
```

---

## 测试统计

| 类型 | 数量 | 位置 |
|------|------|------|
| 单元测试 | 461 | 各包 `*_test.go`（含 `tests/defects/`） |
| 集成测试 | 147 | `tests/integration/` |
| 缺陷回归测试 | `tests/defects/` 7 个文件 / 8 个用例 + 包内回归 | `tests/defects/`、`broker/`、`client/`、`protocol/` |
| 基准测试 | 61（Windows 本地实际执行 58） | `tests/bench/`、`plugin/`、`pkg/metrics/` |

**统计口径**：以上均为源码中**声明的顶层 `Test*` / `Benchmark*` 函数数**，不是 `go test -json` 的"运行条数"（后者会把每个子测试 `t.Run` 计一条）。复现命令：

```bash
# 单元测试（默认构建，不含 store/redis 与 store/badger）
go test -list '.*' ./api/... ./broker/... ./client/... ./config/... ./errs/... \
  ./pkg/... ./plugin/... ./protocol/... ./store/... ./tests/defects/... \
  | grep -c '^Test'

# 集成测试
grep -c '^func Test' tests/integration/*_test.go

# 基准测试（默认构建；加 store/redis 的 9 个为 70）
grep -rhE '^func Benchmark' --include='*_test.go' ./tests/bench ./plugin ./pkg/metrics | wc -l
```

四点注意事项：

- **构建标签**：`store/redis/`（18 个测试 + 9 个基准）仅在 `-tags store_redis` 下编译，`store/badger/`（10 个测试）仅在 `-tags store_badger` 下编译，因此**不计入**默认口径。
- **存储层格式测试**：`store/` 根包（8 个测试）不带构建标签，直接验证 `encoding/json` 的会话与保留消息记录布局 —— 这是 `store/badger` 与 `store/redis` 共用的持久化格式，因此无需任一后端即可回归。
- **Windows 跳过**：`tests/bench/` 中 3 个连接 churn 基准在 Windows 下主动 `b.Skip`（见 [基准测试](#基准测试)），故本地执行数为 `61 - 3 = 58`。
- **历史日志**：`tests/logs/` 下的历史运行日志（如 `20260806_*`）记录的是当时的运行条数口径，与上表不可直接比较。

### 各包测试明细

| 包 | 测试数 | 基准测试数 | 说明 |
|----|--------|-----------|------|
| broker/ | 196 | 0 | 核心逻辑：TopicTree、QoS 引擎、会话、遗嘱、认证、服务器、retained metrics、MQTT 5.0 属性中继与过期边界、重连重传、按协议版本门控的连接内重传、插件钩子上下文 |
| protocol/ | 90 | 0 | MQTT 3.1 / 3.1.1 / 5.0 编解码、属性、报文 flags 校验（解码与编码对称）、流对齐不变量、属性出现次数与所属报文类型校验（Table 2-4，解码与编码双向）、协议适配层（15 种报文） |
| client/ | 40 | 0 | MQTT 客户端连接、发布、订阅、断开清理、QoS 2 入站 ack（PUBREC 保留 flags）与对端 reason-code 守卫 |
| config/ | 24 | 0 | 配置解析与校验 |
| store/memory/ | 23 | 0 | 内存存储：会话、消息、保留消息 |
| api/ | 25 | 0 | 公共 API、健康端点与健康监听的生命周期（Stop 释放端口/幂等、重启可重绑） |
| plugin/ | 16 | 3 | 插件系统：注册、分发、超时与 panic 隔离 |
| pkg/bufferpool/ | 10 | 0 | 缓冲池管理 |
| pkg/metrics/ | 10 | 1 | Prometheus 指标 |
| store/ | 8 | 0 | 存储层 JSON 格式：会话与保留消息的中继属性编解码及向后兼容 |
| pkg/logger/ | 7 | 0 | slog 日志适配 |
| errs/ | 4 | 0 | 错误定义 |
| tests/defects/ | 8 | 0 | 审查发现缺陷的最小复现和回归测试 |
| **单元测试合计** | **461** | **4** | |
| tests/integration/ | 147 | 0 | 端到端集成测试（含部署验证） |
| tests/bench/ | 0 | 57 | 全栈 TCP、E2E 数据验证、微基准 |
| **基准测试合计** | | **61** | |
| store/redis/（`store_redis`） | 18 | 9 | Redis 存储：消息、保留、会话（默认不编译） |
| store/badger/（`store_badger`） | 10 | 0 | BadgerDB 持久化存储（默认不编译） |

---

## 代码规模

和测试计数一样，这里是**唯一维护点**：架构文档请引用本节，不要内嵌这些数字 —— 内嵌必然漂移
（`PROTOCOL-ADAPTER-DESIGN.md` 与 `CLUSTER-FEASIBILITY.md` 就曾长期停留在 27,265 行）。

| 指标 | 数量 |
|------|------|
| `.go` 文件 | 164（67 非测试 / 97 测试） |
| 非测试代码行数 | 16,326 |
| 测试代码行数 | 27,220 |

主要模块的非测试行数：`broker` 6,811（17 文件）· `protocol` 3,720（13）· `store` 1,447（10）·
`client` 916（2）· `api` 532（2）· `pkg` 466（7）· `config` 366（2）· `plugin` 200（1）·
`cmd` 160（1）· `errs` 42（1）；其余分布在 `examples/`、`scripts/`、`tests/`。

```bash
# 全部 .go 文件数
find . -name '*.go' -not -path './.git/*' | wc -l
# 非测试 .go 文件数（换成 -name '*_test.go' 即为测试文件数）
find . -name '*.go' -not -name '*_test.go' -not -path './.git/*' | wc -l
# 非测试代码行数（换成 -name '*_test.go' 即为测试行数）
find . -name '*.go' -not -name '*_test.go' -not -path './.git/*' -exec cat {} + | wc -l
```

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

注意：Go race detector 依赖 CGO 和 C 编译器，默认 `PATH` 中没有 `gcc` 时 `-race` 无法编译（报 `-race requires cgo`）。Windows 本机可安装 w64devkit，并在当前 shell 中把 `D:\Programs\w64devkit\bin` 加入 `PATH` 后执行：

```bash
PATH="/d/Programs/w64devkit/bin:$PATH" CGO_ENABLED=1 go test -race -count=1 ./...
```

CI 无条件覆盖 race 检查：`.github/workflows/ci.yml` 在 Linux / macOS 上执行 `go test -race -count=1 ./...`，Windows job 因不提供 CGO 而明确跳过。本地装不出 CGO 时以 CI 结果为准。

已知需要在 `-race` 下覆盖的敏感路径：`Broker` 的 **Stop -> Start 重启周期**（后台循环必须在 `Stop()` 返回前退出，见 [缺陷回归测试](#缺陷回归测试) 中的 `TestDefectBrokerStopStartLeavesNoRacingLoop`）。

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

集成测试按文件组织，每个文件覆盖一个语义域。下表由源码直接统计
（`grep -c '^func Test' tests/integration/*_test.go`），合计 **147 个**：

| 文件 | 测试数 | 覆盖重点 |
|------|--------|----------|
| `deploy_test.go` | 44 | Dockerfile / docker-compose / K8s / Helm 清单、安全默认值与（单副本）部署告警 |
| `delivery_test.go` | 13 | 多订阅者投递、QoS 降级、顺序保证、各类负载（64KB / 二进制 / 空 / Unicode） |
| `retained_test.go` | 11 | 保留消息投递、更新、删除、通配符匹配、RetainHandling |
| `qa_mqtt31_verify_test.go` | 8 | MQTT 3.1 语义专项验收 |
| `unsubscribe_test.go` | 8 | 取消订阅、批量取消、通配符取消、重新订阅、系统主题隔离 |
| `edge_case_test.go` | 5 | 认证失败、重复 ClientID、非法主题过滤器、连接数上限、空 ClientID |
| `persistent_session_test.go` | 5 | CleanSession=false 会话保持、重连恢复、重复 ClientID 接管 |
| `retain_as_published_test.go` | 5 | Retain As Published 对出站 RETAIN 的影响（本轮修复回归） |
| `topic_wildcard_test.go` | 5 | `+` / `#` 通配符匹配（含根级 `#` 与混合形式） |
| `graceful_shutdown_test.go` | 4 | 优雅停机：抑制遗嘱风暴、MQTT 5.0 DISCONNECT 0x8B（本轮修复回归） |
| `mqtt31_test.go` | 4 | MQTT 3.1 连接、版本协商与字节级兼容 |
| `will_test.go` | 4 | 遗嘱触发、优雅断开不触发、遗嘱 QoS |
| `observability_test.go` | 3 | 拒绝连接的审计日志与凭据脱敏（本轮加固回归） |
| `pubsub_test.go` | 3 | 基础发布-订阅、自发布投递、MQTT 5.0 No Local |
| `qos_test.go` | 3 | QoS 0 / 1 / 2 完整流程 |
| `session_expiry_test.go` | 3 | 会话过期与清理 |
| `lifecycle_regression_test.go` | 2 | 服务生命周期回归 |
| `message_expiry_test.go` | 2 | 消息过期（Message Expiry Interval） |
| `publish_properties_test.go` | 3 | MQTT 5.0 PUBLISH 属性中继：在线投递、QoS 2 中转与版本门控（本轮新增） |
| `qos1_regression_test.go` | 2 | QoS 1 回归 |
| `websocket_test.go` | 2 | WebSocket 传输 |
| `wss_test.go` | 2 | WSS（WebSocket over TLS） |
| `connect_test.go` | 1 | CONNECT / CONNACK 握手完整流程 |
| `outbound_retry_test.go` | 2 | 出站消息重试：3.1.1 订户被重传，5.0 订户不重传（[MQTT-4.4.0-1]，本轮新增） |
| `request_response_test.go` | 1 | 请求 / 响应模式（Response Topic） |
| `subscription_id_test.go` | 1 | MQTT 5.0 Subscription Identifier |
| `write_queue_test.go` | 1 | 写队列与背压 |
| **合计** | **147** | |

具体测试函数名可用 `go test -list '.*' ./tests/integration/...` 列出。本表**不逐条维护函数名**，
以免每次新增用例都要同步文档、产生漂移；只维护"文件 -> 数量 -> 语义域"这一层。

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

### 全栈 TCP 基准（18 项）- `broker_bench_test.go`

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

### E2E 数据验证基准（12 项）- `data_delivery_bench_test.go`

与全栈基准不同，每个基准读取并 **验证** 订阅者收到的 PUBLISH 报文（topic + payload），测量完整的 publish->broker->subscribe->verify 往返。

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

### 组件微基准（27 项）- `micro_bench_test.go`

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

Windows 说明：`BenchmarkConnectionEstablish`、`BenchmarkMQTTConnect` 与 `BenchmarkE2E_WillMessage` 会主动制造大量短连接或异常断连，Windows 下会跳过这些连接 churn 基准，以免耗尽 ephemeral TCP ports 并影响同一测试进程后续 E2E 基准。发布、fanout、E2E QoS/retained/wildcard/payload 数据验证、codec、QoS engine、buffer pool、store 等基准仍会执行。

---

## 测试脚本

一个 Go 编写的跨平台测试运行器，搭配轻量级 shell 包装脚本，提供统一的测试执行体验。所有测试运行自动保存 JSON（原始 `go test -json` 输出）和 `.log`（解析后的报告）到 `tests/logs/` 目录。
脚本在底层 `go test` 或基准命令失败时返回非零退出码，同时保留 JSON 和解析报告，便于 CI 正确失败并保留诊断材料。

日志格式：`tests/logs/{YYYYMMDD_HHmmss}_{type}.{json,log}`

### 文档链接检查

文档相对链接（根目录 `*.md` 与 `docs/**`，排除 `docs/reports/archive/` 历史快照）由
`scripts/check_links.go` 校验——任何指向不存在文件的链接都会使检查失败：

```bash
go run scripts/check_links.go
```

该检查已集成进 CI（`lint` job）。**约定**：文档链接使用相对当前文件的有效路径（含 `.md`
后缀）；指向目录的链接需确保目录存在；`docs/reports/archive/` 下的历史文档不维护链接
（视为历史快照）。

### 测试产物目录约定

所有测试**过程文件**统一生成到 `tests/` 目录下，不落在项目根目录：

| 目录 | 内容 | 说明 |
|------|------|------|
| `tests/logs/` | 测试日志（`{ts}_*.json` / `{ts}_*.log`） | `run_tests.go` 默认输出（`-logdir` 可覆盖）；已 gitignore |
| `tests/artifacts/` | 覆盖率（`coverage*.out` / `*.html`）、profile（`*.prof`、`mem*.out`）、基准输出等过程产物 | 手动运行 `go test -coverprofile tests/artifacts/coverage.out` 等命令时的约定输出位置；已 gitignore |

约定：**任何测试产生的临时/过程文件不得写入项目根目录**——根目录只保留源码、配置与文档。

### 脚本对应关系

| 平台 | 脚本 |
|------|------|
| 通用（Go 运行器） | `go run scripts/run_tests.go -mode <mode>` |
| Linux / macOS / Git Bash / WSL | `bash scripts/run_tests.sh [--unit\|--integration\|--benchmark\|--cover\|--all]` |
| Windows CMD | `scripts\run_tests.bat [--unit\|--integration\|--benchmark\|--cover\|--all]` |

### 模式

| 模式 | 说明 | 日志文件 |
|------|------|----------|
| `all`（默认） | 单元 + 集成 + 基准 | `{ts}_unit.{json,log}`, `{ts}_integration.{json,log}`, `{ts}_benchmark.{json,log}` |
| `unit` | 全部单元测试 | `{ts}_unit.{json,log}` |
| `integration` | 集成测试 | `{ts}_integration.{json,log}` |
| `benchmark` | 基准测试 | `{ts}_benchmark.{json,log}` |
| `cover` | 覆盖率报告 | `{ts}_cover.log` |

### 示例

```bash
# 运行全部（默认）
bash scripts/run_tests.sh

# 单独模式
bash scripts/run_tests.sh --unit
bash scripts/run_tests.sh --integration
bash scripts/run_tests.sh --benchmark
bash scripts/run_tests.sh --cover

# Windows CMD
scripts\run_tests.bat --all
scripts\run_tests.bat --unit

# 或直接用 Go 运行器（跨平台通用）
go run scripts/run_tests.go -mode unit
go run scripts/run_tests.go -mode cover -timeout 10m
```

### `all` 模式日志结构

```
tests/logs/
+-- 20260428_190627_unit.json
+-- 20260428_190627_unit.log
+-- 20260428_190635_integration.json
+-- 20260428_190635_integration.log
+-- 20260428_190642_benchmark.json
+-- 20260428_190642_benchmark.log
```

---

## Makefile 目标

```bash
make test              # 单元测试
make test-integration  # 集成测试
make test-race         # race 检测
make bench-quick       # 快速基准（1s）
make bench             # 完整基准（5s x 3 轮）
make bench-cpu         # CPU profile
make bench-mem         # memory profile
make bench-profile     # CPU + memory profile
make test-coverage     # 覆盖率报告
make ci                # CI 完整流水线
```

---

## 缺陷回归测试

审查过程中确认的缺陷必须先在 `tests/defects/` 中添加最小复现，再修复代码。当前回归项（8 个在 `tests/defects/`，另 21 个在所属包内）：

| 位置 | 测试函数 | 覆盖缺陷 |
|------|----------|----------|
| `tests/defects/api_default_auth_test.go` | `TestDefectAPIDefaultBrokerRejectsUnauthenticatedClient` | 默认认证下匿名客户端必须被拒绝 |
| `tests/defects/broker_lifecycle_test.go` | `TestDefectMQTTServerRestartAfterStopAcceptsConnections` | `Stop()` 后重启必须能重新接受连接 |
| `tests/defects/broker_stop_start_race_test.go` | `TestDefectBrokerStopStartLeavesNoRacingLoop` | `Stop()` 必须等后台循环退出，否则 `-race` 在 Stop -> Start 周期上报 `b.ctx` / `b.startedAt` 数据竞争 |
| `tests/defects/invalid_will_topic_leak_test.go` | `TestDefectInvalidWillTopicDoesNotConsumeBrokerConnectionSlot` | 非法 Will Topic CONNECT 被拒绝后不能残留 session/connection 槽 |
| `tests/defects/properties_defect_test.go` | `TestDefectLongMQTT5PropertyStringReturnsEncodeError` | MQTT 5.0 UTF-8 属性字符串长度上限（codec 必须绑定 5.0，否则触发的是版本门控而不是长度检查） |
| `tests/defects/topic_filter_defect_test.go` | `TestDefectTopicFiltersMayContainEmptyLevels` | MQTT topic filter 中合法的零长度 topic level |
| `tests/defects/windows_benchmark_test.go` | `TestDefectWindowsConnectionChurnBenchmarksDoNotPoisonSuite` | Windows 连接 churn 基准不得污染同进程后续基准 |
| `tests/defects/windows_benchmark_test.go` | `TestDefectWindowsWillMessageBenchmarkDoesNotPoisonSuite` | Windows 遗嘱基准不得污染同进程后续基准 |
| `tests/integration/` | `integration.TestRetainedMessageRelaysPublisherProperties` | retained 记录只保存 topic/QoS/payload，订阅时重放会丢掉全部 MQTT 5 中继属性（Payload Format / Content Type / Response Topic / Correlation Data / User Properties），违反 §3.3.2.3；经 retained 引导的请求 / 响应流程永远无法完成 |
| `tests/integration/` | `integration.TestPublishPropertiesRelayedForQoS2` | QoS 2 发布在 PUBREL 之前暂存于 `InboundQoS2`，但暂存记录不携带属性，中转时属性被丢弃：在线可达的 §3.3.2.3 违规，直接破坏 QoS 2 的请求 / 响应流 |
| `broker/` | `broker.TestForwardedPublishProperties` | 已过期的 Message Expiry 截止时间换算剩余秒数时发生无符号回绕，会把 4294963696 秒写进 Message Expiry Interval，令消息在过期后长期存活 |
| `broker/` | `broker.TestSessionRestoreKeepsForwardedProperties` | 会话快照 / 恢复路径既不持久化中继属性也不持久化过期截止时间，重启后重传的 PUBLISH 丢失属性，且 §3.3.2.3.3 无法执行 |
| `broker/` | `broker.TestBroker_QoS2OutboundResendsPubRelOnReconnect` | 会话恢复路径无条件按 `StateSent` 重新登记 inflight，已过 PUBREC 的 QoS 2 投递被当作未确认 PUBLISH 重发：违反 §4.4 与 [MQTT-4.3.3-6]，握手从 PUBLISH 重新开始而非以 PUBREL 收尾（同族：`TestSessionInflightPubRelStageSurvivesRestart`、`TestRestoreSessionInflightUsesTheSavedStage`、`TestBroker_QoS2OutboundResendsPublishBeforePubRec`） |
| `broker/` | `broker.TestBroker_QoS2PubRecReasonCodeSelectsTheHandshakeEnding` | 订户 PUBREC 的 Reason Code 被整条丢弃（`handlePubRec(clientID, p.PacketID)` 根本没传），于是每个 PUBREC 都被当成「接受」：`>= 0x80` 时仍然回 PUBREL 并继续重传，违反 [MQTT-4.3.3-4] / [MQTT-4.4.0-2]，且 Packet Identifier 与接收窗口槽位都不释放（§4.3.3） |
| `broker/` | `broker.TestBroker_RetransmissionFollowsTheSessionVersion` | 重传循环完全不区分协议版本：MQTT 5.0 §4.4 在同一 MUST-resend 条款之外追加了禁止性规定「Clients and Servers MUST NOT resend messages at any other time」[MQTT-4.4.0-1]，而 3.1.1 §4.4 只把重投递限定为「REQUIRED」、并不禁止，于是 5.0 订户在连接内被反复重发（同族：`TestQoSEngine_Retry_FollowsProtocolVersionPolicy`、`TestQoSEngine_Retry_GateIsPerSessionAndSkipsTheGiveUp`、`TestQoSEngine_Retry_VersionClearedWithClient`、`integration.TestQoS1OutboundRetrySuppressedForMQTT5`、`integration.TestQoS1OutboundRetryResends`） |
| `client/` | `client.TestDisconnectClosesConnectionBeforeWaitingForReadLoop` | 客户端主动断开时必须先唤醒阻塞读循环，避免 `Disconnect` 卡住 |
| `broker/` | `broker.TestQoSEngine_Retry_MaxRetriesExceededReportsError` | QoS inflight 消息达到最大重试次数被丢弃时必须发出错误回调 |
| `broker/` | `broker.TestRepublishKeepsExpiredDeliveryThatWasAlreadySent` | 重传路径把 Message Expiry 当成**重试期**过滤器：`republish` 在截止时间已过时删掉会话记录并丢弃投递，而 [MQTT-3.3.2-5] 只在「尚未开始向订阅者转发」时才要求删除、[MQTT-4.3.3-7] 更禁止对已发出的 PUBLISH 施加过期。该丢弃同时破坏记账：会话记录没了而 QoS 引擎仍在重试同一个 packet id，下一 tick 落到入站兜底分支，向线上的**出站**投递发出一个凭空出现的 PUBREC（同族：`TestRepublishRetriesExpiredDeliveryWithoutStrayPubRec`、`TestDoDeliverStillDropsExpiryBeforeTheFirstAttempt`） |
| `protocol/` | `protocol.TestEncodePublishPropertySectionVersionGated` | `encodePublish` 只判 `Properties != nil`、不判 codec 版本，把 v5 属性字节写进 3.1.1 PUBLISH（对端读成 payload） |
| `protocol/` | `protocol.TestPropertySectionVersionGatedAcrossPackets` | 同一门控遗漏扩散到 CONNACK / SUBSCRIBE / UNSUBSCRIBE / SUBACK；对每种报文断言「pre-v5 带属性必拒、pre-v5 无属性仍可编码、v5 带属性能编码」 |
| `client/` | `client.TestQoS2InboundDeliverySurvivesPubRec`、`client.TestHandlePublishQoS2PubRecUsesReservedFlags` | `handlePublish` 两处 PUBREC 都设了 `QoS: 1`，而 PUBREC 的固定头 flags 是保留 `0b0000`（MQTT 3.1.1 Table 2.2 / MQTT 5.0 Table 2-2，只有 PUBREL / SUBSCRIBE / UNSUBSCRIBE 是 `0b0010`）。对端的 `validateFixedHeaderFlags` 直接判 `ErrInvalidPacket` 并断连：第一条 QoS 2 消息仍能投出（PUBREC 在投递之后发），随后订阅静默失效 |
| `client/` | `client.TestPublishQoS2StopsBeforePubRelWhenPubRecRejects`、`client.TestPublishQoS2ReportsPubCompFailure`、`client.TestUnsubscribeReportsUnsubAckFailure` | 对端拒绝的 Reason Code 被静默当成成功：PUBREC `>= 0x80` 时仍回 PUBREL 并挂到 ctx 超时（违反 [MQTT-4.3.3-4] / [MQTT-4.4.0-2]）、PUBCOMP 非 0x00 时 `Publish` 返回 nil、UNSUBACK 逐主题的 `>= 0x80` 被整个丢弃。三处目前线上不可达（client 固定 3.1.1），属防御性深度，测试须用 `protocol.NewCodecForVersion` 绑定 5.0 才能构造 |
| `protocol/` | `protocol.TestDecodeRejectsInvalidFixedHeaderFlags`、`protocol.TestDecodeAcceptsDupOnQoS2ControlPackets` | 解码端保留位校验把 flags 只由 QoS / RETAIN 折叠而成，漏掉 DUP 位（`0x08`）：对端发来 nibble 为 `0b1000` 的控制报文（如 `0xC8` PINGREQ+DUP、`0x18` CONNECT+DUP、`0x48` PUBACK+DUP）会被接受，违反 [MQTT-2.2.2-1] / [MQTT-2.2.2-2]。PUBREL / SUBSCRIBE / UNSUBSCRIBE 的 DUP 位**故意不校验**并由第二个测试单独锁定：smart-mqtt 重传时会给它们置 DUP（`InflightQueue` 的 `PUB_REL_HEADER_DUP` / `SUBSCRIBE_HEADER_DUP`），收紧会断连该对端 |
| `protocol/` | `protocol.TestEncodeRejectsInvalidFixedHeaderFlags`、`protocol.TestEncodeAcceptsSpecCompliantFixedHeaderFlags` | `Encode` 从不校验固定头 flags，是 PUBREC 缺陷逃过全部单测的根因：非法 nibble 直接上线缆，只有真实对端才会拒绝并断连。现改为与 `Decode` 对称（写出前拒绝），并把 20 处用 `QoS: 0` 构造 SUBSCRIBE / UNSUBSCRIBE / PUBREL 的测试脚手架改为规范值 |
| `protocol/` | `protocol.TestDecodeRejectedPacketDoesNotOverConsumeTheStream` | SUBSCRIBE / UNSUBSCRIBE / PUBLISH 在读取首个报文字段（packetID、topic 长度前缀）**之后**才检查 Remaining Length 是否容得下它：RL=0 的报文会把下一个报文的字节吃掉（PUBLISH 更会用攻击者可控的 topic 长度向流里再索取最多 64 KiB），流被静默错位。`decodeSubAck` / `decodeUnsubAck` 本就有这道守卫，现补齐为全族一致的不变量：被拒报文最多只消费自己的 Remaining Length |
| `protocol/` | `protocol.TestDecodeRejectsDuplicateAtMostOnceProperty`、`protocol.TestDecodeAcceptsRepeatedSubscriptionIdentifierOnlyInPublish`、`protocol.TestDecodeAcceptsRepeatedUserProperty` | `decodeProperties` 不跟踪属性出现次数，重复的属性静默「后者胜出」，而规范为每处属性定义写明 "It is a Protocol Error to include the X more than once"。唯一有实际后果的是 SUBSCRIBE 里的 Subscription Identifier：`decodeSubscribe` 把包级标识符盖到每一个 Topic Filter 上，客户端按 ID=1 标注的过滤器会静默继承 ID=2 的语义（§3.8.2.1.2 明令禁止）。可重复性取决于报文类型（PUBLISH 中的多标识符合法，§3.3.2.3.8），故按 (报文类型, 属性 ID) 判定并返回新的 `ErrProtocolError`（0x82） |
| `protocol/` | `protocol.TestPropertyLegalityMatchesTable24`、`protocol.TestDecodeRejectsPropertyNotValidForPacketType`、`protocol.TestDecodeRoundTripsServerReference` | 解码器只认属性 ID 的数据类型、从不校验它是否属于该报文类型，违反 §2.2.2.2（此错误是 0x81 Malformed Packet，与重复属性的 0x82 分属不同 reason code）。该检查当场抓出 broker 把 CONNECT 专有的 `Request Response Information`（0x19）写进 CONNACK：合规客户端必须判其为 Malformed Packet 并断连，而本地单测因共用同一个宽松解码器而全绿。另补上 `Server Reference`（0x1C，CONNACK / DISCONNECT）—— 它在常量、类型表、结构、解码 switch 四处全缺，使重定向流程整包解码失败 |
| `tests/integration/` | `integration.TestRequestResponseInformation` | 该测试原先断言 CONNACK 回显 `Request Response Information`（0x19），而 Table 2-4 把 0x19 归为 CONNECT 专有 —— 等于把违规行为钉成了预期契约。现改为断言合规契约：CONNACK 带 `Response Information`（0x1A）且不带 0x19 |
| `protocol/` | `protocol.TestEncodeLocusMatchesTable24`、`protocol.TestPropertyProbesCoverPropertyLoci`、`protocol.TestEncodeRejectsConnectOnlyPropertyOnConnAck` | 编码端从不校验属性归属，是上一行 0x19 缺陷逃过全部单测的根因：`encodeProperties` 只写调用方给的字段，只有真实对端才会拒绝。现与解码端共用同一张 `propertyLoci` 表，在写出前按 `locus` 校验并返回 `ErrMalformedPacket`（与解码端同分类，§2.2.2.2），使两侧对称；`TestPropertyProbesCoverPropertyLoci` 锁定探针表与 Table 2-4 成员完全一致，防止新增属性静默绕过校验。顺带修正 `TestPropertySectionVersionGatedAcrossPackets` 的 fixture —— 它原先拿 PUBLISH 专有的 Content Type 当全部 9 种报文的属性，等于把非法组合钉成了脚手架 |

运行方式：

```bash
go test ./tests/defects -v
```

---

## 静态分析

项目使用 Go 标准工具和 golangci-lint v2：

```bash
go vet ./...
golangci-lint run ./...
```

`golangci-lint` 配置文件为 `.golangci.yaml`，测试文件中清理类 `errcheck` 噪声按规则排除；生产代码仍要求通过默认静态检查。

---

## 日志系统

所有运行时日志和测试日志的时间戳精度为 **毫秒**。

- Go 运行时日志：`log.SetFlags(log.LstdFlags | log.Lmicroseconds)`，由 `cmd/main.go` 在启动时设置。
  `pkg/logger` 作为可嵌入库**不改动标准库全局 logger**，避免污染宿主进程（历史上曾放在 `init()` 中，已移除）。
- 测试脚本日志：`{YYYYMMDD_HHmmss}` 时间戳
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
go run scripts/run_tests.go -mode cover
```

### 覆盖率阈值

当前 CI 硬性门槛为总覆盖率 55%（`.github/workflows/ci.yml`，仅 Ubuntu + Redis 上校验）。下表是项目质量目标，不是当前 CI 硬性门槛；2026-08-06 本地实测各包覆盖率为 broker 49.0%、protocol 65.2%、client 84.5%、api 83.2%、store/memory 91.4%、store/badger 88.5%、plugin 90.6%、pkg/metrics 98.0%、pkg/bufferpool 100%（Redis store 与 cmd 未计入，详见 `docs/reports/PROJECT-REVIEW-260806-121651.md`），后续应优先补齐 `broker`、`protocol` 与 Redis store 覆盖。

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

- [开发指南](DEVELOPMENT.md)
- [配置指南](CONFIGURATION.md)
- [性能指南](PERFORMANCE.md)
- [架构文档](../architecture/ARCHITECTURE.md)
- [最新审查报告](../reports/PROJECT-REVIEW-260806-143527.md)







