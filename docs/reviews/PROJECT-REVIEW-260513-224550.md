# Shark-MQTT 审查报告 - 260513-224550

## 1. 审查概要

- 审查时间：2026-05-13 22:45:50 Asia/Shanghai
- 项目路径：`g:\shark-mqtt`
- 审查范围：当前工作区基于提交 `b9e1094`
- 架构版本：v1.0.0
- 测试通过率：100%（`go test ./... -count=1 -timeout 180s` 全部通过）
- 代码覆盖率：38.7% total（`go tool cover -func=coverage`）
- 静态检查：`golangci-lint run ./...` 通过，0 issues
- Race 检测：本机 Windows 环境缺少 C 编译器，`go test -race` 因 `gcc` 不存在无法执行

## 2. 符合性总结

- [PASS] 分层边界：`broker.MQTTServer` 仅处理 accept、连接计数和 handler 分发，未包含 CONNECT/PUBLISH 等协议业务逻辑。
- [PASS] 核心接口：`broker.Broker` 实现 `ConnectionHandler`；本次补充了认证相关实现的编译期接口断言。
- [PARTIAL] 协议实现：CONNECT 协议名/版本绑定、系统主题通配符、保留遗嘱已修复；MQTT 5 Properties、PUBACK/PUBREC/PUBREL/PUBCOMP、UNSUBACK 等编解码覆盖率仍不足。
- [PASS] 存储层：memory/badger 测试通过，memory retained store 关键路径 100%；Redis 包单元测试通过但覆盖率报告为 0.0%，需要真实 Redis 集成覆盖。
- [PARTIAL] 可观测性：连接、认证失败、消息发布/投递、在线会话等指标已埋点；`IncRetries` 未在 QoS 重试路径调用，`MetricsEnabled=true` 时未暴露 `/metrics`。
- [PARTIAL] 测试覆盖：TopicTree 关键函数已达 100%，memory store 91.4%；protocol 包 28.1%、pkg/metrics 2.2%，未达到审核目标。

## 3. 发现缺陷（按优先级排序）

| ID | 描述 | 位置 | 严重等级 | 状态 |
|----|------|------|----------|------|
| DEF-001 | `$SYS/#`、`$SYS/+` 这类显式系统主题订阅无法匹配 `$SYS/...`，而普通 `#` 又需要继续禁止匹配系统主题。违反 MQTT 3.1.1 §4.7.2 系统主题通配规则。 | `broker/topic_tree.go` | High | 已修复 |
| DEF-002 | Will Retain 置位时，异常断开触发的遗嘱只投递给在线订阅者，未写入 retained store。违反 MQTT 3.1.1 §3.1.2.7 对 Will Retain 的语义要求。 | `broker/broker.go` | High | 已修复 |
| DEF-003 | CONNECT 校验只分别检查协议名与协议级别，未强制 `MQTT` 对应 3.1.1/5.0、`MQIsdp` 对应 3.1。违反 MQTT 3.1.1 §3.1.2.1/§3.1.2.2。 | `protocol/connect.go` | High | 已修复 |
| RISK-001 | `go test -race` 未能在当前机器执行，缺少 CGO 所需 C 编译器。 | 本地验证环境 | Medium | 待环境补齐 |
| RISK-002 | protocol 包整体覆盖率 28.1%，远低于审核目标 95%；MQTT 5 Properties 与 ACK 类报文缺少充分回归。 | `protocol/` | Medium | 待改进 |
| RISK-003 | metrics 包覆盖率 2.2%，且 `/metrics` 暴露端点缺失。 | `pkg/metrics/`, `api/api.go` | Medium | 待改进 |

## 4. 架构偏差列表

- 设计规定：`pkg/metrics` 中 17 个指标方法应覆盖关键路径；实际实现：连接、认证、发布/投递等已覆盖，但 QoS retry 未调用 `IncRetries`。影响：重试风暴时可观测性不足。建议：在 `QoSEngine.doRetry` 或 Broker retry callback 中注入指标。
- 设计规定：启用 Metrics 时应具备健康检查与指标暴露；实际实现：`/healthz`、`/readyz` 存在，未见 `/metrics`。影响：Prometheus 抓取不可用。建议：`MetricsEnabled=true` 时注册 `promhttp.Handler()` 到 `/metrics`。
- 设计规定：协议层需覆盖所有 15 种 MQTT 包；实际实现：结构上存在 Decode/Encode 分发，但多种 ACK/AUTH/Properties 路径测试覆盖为 0%。影响：协议兼容风险无法由 CI 捕获。建议：优先补 MQTT 5 properties、UNSUBACK reason codes、PUBREL/PUBCOMP round-trip 表格测试。
- 设计规定：Session 状态转换应覆盖 Disconnected -> Connecting -> Connected -> Disconnecting；实际测试覆盖不足，`CloseReason`、`SetCloseReason`、`IsConnected` 覆盖率为 0%。影响：异常断开与会话恢复状态回归风险偏高。

## 5. 改进建议（非缺陷）

- 将 `coverage` 改回 `.gitignore` 已约定的 `coverage.out`，避免生成未忽略的覆盖率文件。
- 为 Redis 后端增加可选 integration profile，例如 Docker Compose 启 Redis 后运行 `go test -tags=integration ./store/redis/...`。
- 将 benchmark 日志输出降噪，避免每个迭代启动/停止日志淹没关键性能数据。
- 在 CI 中增加 Linux race job，并安装 CGO 编译器，保证并发风险不会只依赖开发机环境。

## 6. 协议合规性检查明细

- MQTT 3.1.1 §3.1.2.1/§3.1.2.2：CONNECT 协议名与协议级别配对校验已补齐。
- MQTT 3.1.1 §3.1.2.3：CONNECT reserved flag 校验已存在。
- MQTT 3.1.1 §3.1.2.5：PasswordFlag 置位时 UsernameFlag 必须置位，校验已存在。
- MQTT 3.1.1 §3.1.2.6：WillQoS 必须为 0/1/2，且 WillFlag=false 时 WillQoS/WillRetain 必须为 0，校验已存在。
- MQTT 3.1.1 §3.1.2.7：Will Retain 已修复为写入 retained store。
- MQTT 3.1.1 §4.7.2：普通通配符不匹配 `$` 开头主题，显式 `$SYS/#`/`$SYS/+` 可匹配系统主题，已修复。
- MQTT 5.0：CONNACK properties、UNSUBACK reason codes、AUTH 等路径存在实现，但测试覆盖不足，建议继续补充。

## 7. 修复与验证记录

- 修复 `broker/topic_tree.go`：系统主题首段显式 `$` 后允许嵌套通配；新增 `TestTopicTree_SystemTopicWildcardsRequireDollarPrefix`。
- 修复 `broker/broker.go`：retain will 调用 retained message 处理路径；新增 `TestBroker_PublishWillStoresRetainedMessage`。
- 修复 `protocol/connect.go`：CONNECT 协议名/版本配对校验；新增 `TestValidateConnectProtocolNameMatchesVersion`。
- 补充 `StaticAuth`、`AllowAllAuth`、`DenyAllAuth`、`FileAuth`、`ChainAuth` 编译期接口断言。

验证命令：

```bash
go test ./broker -run "TestTopicTree_SystemTopicWildcardsRequireDollarPrefix|TestBroker_PublishWillStoresRetainedMessage" -count=1 -v
go test ./protocol -run TestValidateConnectProtocolNameMatchesVersion -count=1 -v
go test ./... -count=1 -timeout 180s
go test ./... -coverprofile=coverage -timeout 180s
go tool cover -func=coverage
golangci-lint run ./...
go test -bench='.' -benchmem -run='^$' ./tests/bench/...
```

## 8. 审查结论

有条件批准。

本次已修复 3 个 High 级协议/业务缺陷，普通测试、覆盖率生成、lint、benchmark 均可通过。阻塞最终生产批准的剩余项是：CI/开发环境补齐 `go test -race`、protocol/metrics 覆盖率达标、`/metrics` 暴露端点补齐、QoS retry 指标补齐。
