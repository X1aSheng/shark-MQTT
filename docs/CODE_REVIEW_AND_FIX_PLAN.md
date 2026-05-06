# Shark-MQTT 代码审查与修复计划归档

**项目版本基准**: 1.0.0  
**归档日期**: 2026-05-06  
**归档范围**: 原 `CODE_REVIEW_*.md` 与 `FIX_PLAN_*.md` 全部审查报告、修复计划和完成状态  
**当前状态**: 生产就绪核心，历史关键/高危缺陷已关闭；剩余事项为功能增强、性能优化和低风险工程改进

---

## 归档说明

本文件整合 docs 目录下历史代码审查文档和修复计划，作为唯一维护入口。原始分散文档中的问题、修复计划、提交记录、延期项和误报结论在此按阶段归档，后续新增审查也应追加到本文件，避免多份报告重复维护。

历史文档来源：

| 原文档 | 日期 | 归档内容 |
|--------|------|----------|
| `CODE_REVIEW_260426_105102.md` | 2026-04-26 | 初始改进路线图，覆盖 C1-C3、H1-H5、M1-M3、L1-L4 |
| `CODE_REVIEW_260428_121322.md` | 2026-04-28 | 第二轮项目审查，覆盖架构、协议、存储、测试和部署问题 |
| `CODE_REVIEW_260428_152513.md` | 2026-04-28 | 完整代码审查，识别 12 critical、18 high、28 medium、19 low |
| `FIX_PLAN_260428_174528_CRITICAL.md` | 2026-04-28 | Critical 缺陷修复计划 |
| `CODE_REVIEW_260428_181500.md` | 2026-04-28 | Phase 3 补充审查与修复状态 |
| `CODE_REVIEW_260428_201000.md` | 2026-04-28 | Phase 4 综合审查 |
| `CODE_REVIEW_260429_093000.md` | 2026-04-29 | Phase 5 审查，识别 18 个主要缺陷 |
| `FIX_PLAN_260429_094836_CRITICAL_MEDIUM.md` | 2026-04-29 | Phase 5 critical/medium 修复计划 |
| `FIX_PLAN_260429_135824_MEDIUM_LOW.md` | 2026-04-29 | Phase 6 medium/low 修复计划 |
| `CODE_REVIEW_260430_090000.md` | 2026-04-30 | Phase 6 审查，覆盖 1 critical、9 high、22 medium、27 low |
| `FIX_PLAN_260430_114515_CRITICAL_HIGH.md` | 2026-04-30 | Phase 6 critical/high 修复计划和误报确认 |
| `CODE_REVIEW_250430_112600.md` | 2026-04-30 | 综合审查，覆盖 4 critical、11 high、23 medium、24 low |
| `CODE_REVIEW_260430_114500.md` | 2026-04-30 | Phase 7 审查与全部完成的 8 项修复 |
| `CODE_REVIEW_260505_174239.md` | 2026-05-05 | Phase 8 审查与全部完成的 7 项修复 |
| `CODE_REVIEW_260506_122506.md` | 2026-05-06 | Phase 9 审查与全部完成的 3 项修复 |

---

## 阶段总览

| 阶段 | 日期 | 重点 | 最终状态 |
|------|------|------|----------|
| Phase 1-2 | 2026-04-25 ~ 2026-04-26 | 编译阻塞、协议基本正确性、QoS/Topic/健康检查/CI | 已完成 |
| Phase 3 | 2026-04-28 | 并发安全、CONNECT 验证、QoS2 语义、WillHandler、UTF-8、Redis retained | 已完成，部分低优先级延期 |
| Phase 4 | 2026-04-28 | MQTT 5.0 属性、会话恢复、客户端协议版本、测试稳定性 | 已完成或转入后续 |
| Phase 5 | 2026-04-29 | Session takeover、MemoryStore 深拷贝、订阅/取消订阅验证、插件错误处理 | 已完成 |
| Phase 6 | 2026-04-29 ~ 2026-04-30 | Session Expiry、CONNACK capabilities、errs 整合、RemainingLength 上界 | 已完成 |
| Phase 7 | 2026-04-30 | PacketID 竞态、QoS inflight 限制、API 配置错误、客户端 QoS2 去重 | 已完成 |
| Phase 8 | 2026-05-05 | 会话过期清理、Broker QoS2 去重、指标接入、Save 锁优化、retained trie | 已完成 |
| Phase 9 | 2026-05-06 | retained metrics 计数、MQTT fixed header flags、CONNECT Will flags | 已完成 |

---

## 已关闭的关键缺陷

### 协议层

- MQTT 5.0 PUBLISH/SUBSCRIBE/UNSUBSCRIBE/ACK 属性解码错误不再静默丢弃。
- CONNECT 增加协议名、协议版本、reserved bit、Will QoS、Will Retain、ClientID 等校验。
- Fixed header flags 增加统一校验，拒绝 PUBLISH QoS=3 和非 PUBLISH 报文非法 QoS/Retain 组合。
- `RemainingLength` 增加 MQTT 上界检查，避免超大包、负值和整数溢出。
- UTF-8 字符串校验补充 MQTT 禁止控制字符检查。
- QoS 2 服务端按 PUBREL 后投递，客户端和 Broker 均增加重复包检测。
- PUBREL/SUBSCRIBE/UNSUBSCRIBE 编码端自动设置规范要求的 QoS=1 / Retain=false。

### Broker 与会话

- Session takeover 使用连接身份检查，旧连接清理不再删除新连接状态。
- 每连接写互斥锁保证并发写帧安全。
- QoS retry/send error 处理修正，inflight tracking 支持 retry、ack、max inflight 限制。
- 持久会话保存和恢复支持订阅、inflight、Session Expiry Interval 和 ExpiryTime。
- 添加会话过期清理循环，非 clean session 不再无限残留。
- `Session.Save` 缩小锁范围，payload 深拷贝移出读锁。
- `NextPacketID` 竞态和耗尽问题已修复。

### 存储层

- MemoryStore 返回深拷贝，避免外部修改内部状态。
- Badger/Redis retained empty payload 行为与 Memory 保持一致。
- Redis retained wildcard `+` / `#` 匹配修正。
- Badger retained timestamp、Redis/Badger 错误处理、prefix guard 等问题已修复或确认无阻塞。
- Memory retained store 使用 trie 索引替代线性扫描。

### 认证、插件、配置与可观测性

- 默认认证配置明确，开发模式使用 `--allow-all`。
- StaticAuth / FileAuth / ChainAuth 与 Authorizer 路径已接入。
- Plugin dispatch 支持 context 取消、panic recover、错误收集且不中断后续插件。
- API `Start()` 返回配置验证错误，不再静默启动。
- Prometheus 注册从 `MustRegister` 改为安全复用，指标 label 限制为有界维度。
- 在线会话、订阅数、retained message 数、错误计数等指标已接入。
- Retained metrics 覆盖/删除场景计数精确，不再漂移或变负。

### 服务端、客户端与部署

- Server accept 循环增加退避并正确处理 listener 关闭。
- Max connections 支持预认证阶段限制。
- TLS 最低版本固定为 TLS 1.2。
- 健康检查 `/healthz`、就绪检查 `/readyz`、Docker/K8s/Helm 部署资产和测试脚本已补齐。
- 客户端 IPv6 地址解析、QoS2 channel reuse、错误回调、PacketID 上限保护等问题已修复。

---

## 历史修复记录

| 批次 | 修复内容 | 提交 |
|------|----------|------|
| Phase 5 critical/medium | Session takeover、MemoryStore 深拷贝、认证配置安全、UNSUBSCRIBE 验证 | 历史提交，见 git log |
| Phase 6 medium/low | Session Expiry、CONNACK MQTT 5.0 capabilities、Plugin dispatch 错误处理、errs 整合、RemainingLength 上界 | 历史提交，见 git log |
| Phase 6 critical/high | IPv6 解析、QoS2 channel reuse、配置验证、MemoryStore 深拷贝、Will topic 验证、PacketID 复用、Will goroutine 取消、retained store 错误处理 | 历史提交，见 git log |
| Phase 7 综合修复 | Retained empty payload 一致性、Redis retained `+` 匹配、属性解码错误、Accept backoff、Prometheus 安全注册、Topic filter empty level、MQTT 5 属性范围、UTF-8 控制字符 | `7714d6d`, `2e94d5e`, `d0893c0`, `fe13064`, `854d8bf`, `3bf2024`, `5626314`, `18d7f86`, `7b4c2e2`, `331050d`, `c0fd985`, `a606831` |
| Phase 7 第二轮 | `NextPacketID` 锁修复、QoS maxInflight enforcement、API 配置错误返回、decodeUnsubscribe 错误传播、session store 错误日志、客户端 PacketID 最大重试、客户端 QoS2 去重、移除 CONNECT MaximumQoS 死代码 | `172df99`, `d29a3ed`, `52ed036` |
| Phase 8 | 会话过期清理、Broker QoS2 去重、指标接入、Session.Save 锁优化、客户端错误处理、retained trie、解码路径统一 | `7815854`, `ef2daf3`, `99917bd`, `af4ef6c`, `d468690`, `cc0d4b2`, `f4042fc` |
| Phase 9 | retained metrics 精确计数、MQTT fixed header flags 校验、CONNECT Will flags 校验 | `74757b1`, `1fbb0ea`, `3512b63` |

---

## 误报与延期项

### 已确认误报

- `Codec.protocolVersion` race：后续架构中每连接使用独立 Codec，作为误报关闭。
- Session restore subscriptions map：Restore/CreateSession 路径已恢复订阅并重新注册 TopicTree，作为误报关闭。
- TopicTree orphan cleanup：后续实现已具备 prune/cleanup 逻辑，作为误报关闭。

### 延期或作为后续功能处理

| ID | 优先级 | 内容 | 说明 |
|----|--------|------|------|
| M-002 | Medium | Offline message queueing | 功能增强，不阻塞 1.0.0 核心发布 |
| M-005 | Medium | StaticAuth ACL 行为文档 | 文档完善项 |
| M-006 | Medium | TopicTree match caching | 性能优化项 |
| L-003 | Low | writePacket 错误传播增强 | 可观测性/错误处理优化 |
| L-005 | Low | client Connect TOCTOU | 客户端架构优化 |
| L-007 | Low | 测试命名超时常量 | 测试可维护性 |
| L-008 | Low | 协议 fuzz tests | 测试增强 |
| P3 | Feature | Enhanced Authentication、Topic Alias、HTTP Admin API、大规模压力测试 | 1.x 后续路线 |

---

## 1.0.0 发布基准

Shark-MQTT 1.0.0 的基准能力：

- MQTT 3.1.1 / 5.0 基础协议支持，覆盖 15 类报文。
- QoS 0/1/2、retained message、will message、topic wildcard、persistent session 可用。
- Memory / Redis / BadgerDB 三类存储后端可用。
- Authenticator / Authorizer / Plugin / Metrics / Logger / Config / TLS / Health endpoints 可用。
- Docker、docker-compose、K8s、Helm 部署资产可用。
- 最新完整验证：`go run scripts/run_tests.go -mode all`、`go test ./... -count=1 -timeout=300s`、`go vet ./...` 均通过。

1.0.0 不承诺：

- MQTT 5.0 Enhanced Authentication 完整握手。
- Topic Alias。
- Shared Subscription。
- 离线消息队列。
- HTTP Admin API。

---

## 后续维护规则

1. 新审查内容追加到本文件，不再创建 `CODE_REVIEW_YYMMDD_HHMMSS.md` 或 `FIX_PLAN_*.md` 分散文档。
2. 每个新增缺陷必须包含：问题、影响、验证测试、修复提交、最终验证。
3. 版本基准从 1.0.0 起按语义化版本维护：patch 修复兼容问题，minor 增加兼容功能，major 用于破坏性变更。
4. `docs/PROJECT_STATUS.md` 保持当前状态摘要；详细历史以本文件为准。
