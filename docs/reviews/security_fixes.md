# 安全审查 — shark-mqtt

**日期**: 2026-05-15

## 工具扫描

| 工具 | 结果 |
|------|------|
| gosec (via golangci-lint) | 0 issues |
| govulncheck | 未执行（网络限制） |

## 人工审查结果

### 认证与授权

| 检查项 | 状态 | 备注 |
|--------|------|------|
| 密码常量时间比较 | ✓ | `crypto/subtle.ConstantTimeCompare` 用于 StaticAuth 和 FileAuth |
| 无硬编码凭据 | ✓ | 所有凭据来自配置或 API |
| 密码不记录日志 | ✓ | grep 确认全代码库无密码泄露 |
| noop auth 标记为仅开发 | ✓ | NoopAuth 文档明确标注 "development-only pass-through" |
| 认证器链支持 | ✓ | ChainAuth 支持多认证器组合 |

### 输入验证

| 检查项 | 状态 | 备注 |
|--------|------|------|
| CONNECT 包验证 | ✓ | `ValidateConnect()` 检查协议名、版本、标志一致性 |
| PUBLISH topic 验证 | ✓ | 拒绝通配符 topic，拒绝空 topic |
| Topic filter 验证 | ✓ | `ValidateTopicFilter()` 检查格式 |
| 包大小限制 | ✓ | MaxPacketSize 默认 256KB，Codec 强制 |
| 连接数限制 | ✓ | MaxConnections 可配置 |

### DoS 防护

| 检查项 | 状态 |
|--------|------|
| 最大连接数限制 | ✓ |
| 最大包大小限制 | ✓ |
| Keep-alive 超时 | ✓ (1.5x KeepAlive) |
| 写队列大小限制 | ✓ (WriteQueueSize: 256) |
| CONNECT 超时 | ✓ (10 秒) |
| 最大 inflight 限制 | ✓ (MaxInflight: 100) |
| QoS 重试次数限制 | ✓ (MaxRetries: 3) |

### 依赖安全

未执行 `govulncheck`（网络受限）。建议在有网络的环境中运行。

## 发现的问题

### Medium: 密码明文存储

FileAuth 和 StaticAuth 中密码以明文形式存储在内存中（`map[string]string`）。
SECURITY.md 已建议使用 bcrypt/argon2，但内置实现未遵循。

**建议**: 在 FileAuth 和 StaticAuth 中添加可选的 bcrypt/argon2 哈希支持，
或更新文档明确说明这些是为测试/开发场景设计的，生产环境应使用自定义 Authenticator。

### Low: TLS 最低版本未强制执行

TLS 配置使用 Go 默认值（TLS 1.2+），但代码中未显式设置 `MinVersion`。
SECURITY.md 建议最低 TLS 1.2，推荐 TLS 1.3。

**建议**: 在 `Config.TLSConfig()` 中设置 `MinVersion: tls.VersionTLS12`。

## 结论

**安全等级**: A- — 认证框架完善（常量时间比较、ACL、链式认证），
DoS 防护层层到位，输入验证全面。主要关注点是密码明文存储和 TLS 最低版本未显式设置。
