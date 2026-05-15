# 项目审查报告 — shark-mqtt

**日期**: 2026-05-15
**审查人**: Claude (general-service-review skill)
**项目**: github.com/X1aSheng/shark-mqtt
**Go 版本**: 1.26.1

---

## 1. 概要

| 指标 | 值 |
|------|-----|
| 项目类型 | 独立 MQTT Broker (MQTT 3.1.1 + 5.0) |
| 代码规模 | ~20 个包 |
| 测试通过率 | 100% (全部通过) |
| 代码覆盖率 | 45.8% (总体) |
| Lint 问题 | 0 |
| Go vet 问题 | 0 |
| 安全漏洞 (已知) | 0 |
| 架构文档 | Architecture.md (1,804 行) |
| 总文档量 | ~5,000 行 (14 个文件) |

## 2. 架构评估

**等级: A** — 分层清晰，依赖方向严格自上而下，无循环依赖。

### 分层结构
```
api (入口层)
  └── broker (核心业务层)
        ├── store (→ errs)      — 存储抽象
        ├── protocol (→ errs)   — MQTT 协议编解码
        ├── plugin              — 插件系统
        ├── pkg/logger          — 结构化日志
        ├── pkg/metrics         — 指标收集
        ├── config              — 配置管理
        └── errs                — 错误定义
```

### 架构决策记录 (ADR)
6 个 ADR 覆盖了所有关键架构决策（独立仓库、网络与业务分离、会话状态机、broker 包聚合、TopicTree 系统主题保护、函数选项模式）。

### 设计原则
- P1: 网络层与业务层分离 (MQTTServer ↔ Broker)
- P2: 连接与会话分离 (Connection lifecycle ⊆ Session lifecycle)
- P3: 存储与运行时分离 (接口驱动存储)
- P4: QoS 状态机封装
- P5: 可观测性优先

详见: [architecture_review.md](architecture_review.md)

## 3. 缺陷清单

| ID | 类型 | 严重等级 | 位置 | 描述 | 修复建议 |
|----|------|----------|------|------|----------|
| DOC-001 | 文档 | Medium | [README.md:489](README.md#L489) | 引用不存在的 `docs/PROJECT_STATUS.md` | 移除或创建对应文件 |
| DOC-002 | 文档 | Medium | [README.md:489](README.md#L489) | 引用不存在的 `docs/CODE_REVIEW_AND_FIX_PLAN.md` | 移除或创建对应文件 |
| DOC-003 | 文档 | Medium | [README.md:489](README.md#L489) | 架构文档链接 `docs/shark-mqtt architecture.md` 含空格 | 修正为 `docs/Architecture.md` |
| SEC-001 | 安全 | Medium | [broker/auth.go:33](broker/auth.go#L33) | 内置认证器密码明文存储在内存中 | 添加可选的 bcrypt/argon2 哈希支持 |
| SEC-002 | 安全 | Low | [config/config.go:1](config/config.go) | TLS MinVersion 未显式设置 | 在 TLSConfig() 中设置 `tls.VersionTLS12` |
| OBS-001 | 可观测性 | High | [api/api.go:258](api/api.go#L258) | `/metrics` 端点未暴露，Prometheus 指标不可被抓取 | 添加 `promhttp.Handler()` 到健康检查 mux |
| OBS-002 | 可观测性 | Medium | — | 无 OpenTelemetry 分布式追踪集成 | 在关键路径添加 otel span |
| OBS-003 | 可观测性 | Medium | — | 无消息处理延迟直方图 | 添加 MessagesLatency Prometheus histogram |
| TST-001 | 测试 | High | broker/ | broker 包覆盖率 52.8%，核心模块需 ≥80% | 补充异常路径、session takeover、conn limit 测试 |
| TST-002 | 测试 | High | protocol/ | protocol 包覆盖率 49.1%，安全关键路径 | 补充 MQTT 5.0 属性、畸形包拒收、fuzz 测试 |
| TST-003 | 测试 | Medium | store/redis/ | Redis store 覆盖率 0% | 添加 miniredis/testcontainers 单元测试 |
| TST-004 | 测试 | Low | pkg/metrics/ | Prometheus impl 覆盖率 2.2% | 添加 Prometheus 指标单元测试 |
| PRF-001 | 性能 | Medium | [broker/broker.go:576](broker/broker.go#L576) | 高 fan-out 场景串行投递 | 对订阅者数量超过阈值时使用 worker pool 并行投递 |
| PRF-002 | 性能 | Low | — | BufferPool 未在生产路径使用 | 在 broker 读取路径集成 BufferPool |

## 4. 安全风险

**安全等级: A-**

### 已验证项目
- 密码比较使用 `crypto/subtle.ConstantTimeCompare`（常量时间，防时序攻击）
- 无硬编码凭据、密钥或令牌
- 密码不记录日志
- DoS 防护层层到位：连接数限制、包大小限制、keep-alive 超时、写队列限制、inflight 限制、重试次数上限
- CONNECT/PUBLISH/topic filter 输入验证完整
- SECURITY.md 覆盖 TLS、认证、网络安全、数据保护、生产环境检查清单
- `noop` 认证标记为"仅开发环境"

### 需改进
- 密码明文存储（见 SEC-001）
- TLS MinVersion 未显式设置（见 SEC-002）
- 未执行 `govulncheck`（网络限制），建议在 CI 中添加

详见: [security_fixes.md](security_fixes.md)

## 5. 可观测性差距

**等级: B+**

### 已有
- 结构化日志 (slog, JSON/text 格式)
- Prometheus 指标接口 (17 methods, CounterVec + Gauge)
- `/healthz` + `/readyz` 健康检查端点
- HTTP timeouts 配置正确

### 差距
- `/metrics` 端点未自动暴露（最关键的缺失）
- 无 OpenTelemetry 分布式追踪
- 无消息延迟直方图
- 无预配置 Grafana 面板

详见: [observability_gap.md](observability_gap.md)

## 6. 性能基准

**等级: A-**

### 关键指标 (AMD Ryzen 7 8845HS, Go 1.26.1, Windows 11)

| 操作 | ns/op | allocs/op |
|------|-------|-----------|
| TopicTree Subscribe | 245 | 0 |
| TopicTree Match (exact) | 470 | 2 |
| Codec Encode Publish | 560 | 6 |
| Codec Decode Publish | 873 | 10 |
| QoS 0 E2E Publish | 29,013 | 27 |
| QoS 1 E2E Publish | 104,404 | 39 |
| QoS 2 E2E Publish | 240,392 | 58 |

### 亮点
- 热路径零分配（TopicTree Subscribe/Unsubscribe）
- 微秒级端到端延迟（QoS 0 ~29µs）
- 写操作在客户端级别隔离（per-connection write mutex），无全局锁竞争
- retryLoop 在锁外收集重试消息，避免锁顺序死锁

详见: [performance_advice.md](performance_advice.md)

## 7. 文档问题

**等级: A**

文档库包含 14 个文件，约 5,000 行，覆盖架构、API、配置、测试、安全、部署、性能、开发指南。

3 个问题（见 DOC-001, DOC-002, DOC-003），均为 README 中的链接错误或不存在文件引用。

详见: [doc_inconsistencies.md](doc_inconsistencies.md)

## 8. 改进计划（按优先级排序）

### Critical (0 项)
无。项目核心功能完整，所有测试通过，lint/vet 通过。

### High (3 项)

| # | 项 | 描述 | 影响 |
|---|-----|------|------|
| H1 | TST-001/002 | broker+protocol 覆盖率提升至 80%+ | 核心模块测试保障不足 |
| H2 | OBS-001 | 暴露 `/metrics` 端点 | 生产可观测性缺失 |
| H3 | SEC-002 | 显式设置 TLS MinVersion | 安全合规差距 |

### Medium (7 项)

| # | 项 | 描述 |
|---|-----|------|
| M1 | DOC-001/002/003 | 修正 README 链接和不存在文件引用 |
| M2 | SEC-001 | 密码哈希支持 |
| M3 | TST-003 | Redis store 单元测试 |
| M4 | OBS-002 | OpenTelemetry 追踪集成 |
| M5 | OBS-003 | 消息延迟直方图 |
| M6 | PRF-001 | 高 fan-out 并行投递 |
| M7 | DOC | 审查产物移至 docs/reviews/ 子目录 |

### Low (4 项)

| # | 项 | 描述 |
|---|-----|------|
| L1 | TST-004 | metrics Prometheus 实现单元测试 |
| L2 | PRF-002 | 生产路径集成 BufferPool |
| L3 | — | 提供 Grafana 面板 JSON |
| L4 | — | 添加 protocol fuzz 测试 |

---

## 9. 总结

shark-mqtt 是一个**架构设计良好、代码质量高、文档齐全**的 MQTT Broker 项目。核心亮点：

- **架构**: 分层清晰，接口驱动，6 个 ADR 覆盖关键决策
- **代码**: 0 lint/vet 问题，并发安全，错误处理一致
- **安全**: 常量时间密码比较，DoS 层层防护，无硬编码机密
- **测试**: 354 个测试全通过，67 个基准测试，三层测试体系
- **文档**: ~5,000 行文档，覆盖所有方面
- **性能**: 纳秒级 codec，零分配热路径，微秒级端到端延迟

主要改进方向：**测试覆盖率提升**（核心模块从 ~50% → 80%）、**可观测性完善**（/metrics 端点 + 追踪）、**文档修正**（3 个链接错误）。
