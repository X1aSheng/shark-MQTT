# FIX_PLAN_01: CRITICAL 缺陷修复

> **关联**: CODE_REVIEW_20260428.md  
> **批次**: 第 1/4 批  
> **项目**: 12个 CRITICAL 缺陷

---

## 修复顺序（按影响范围和依赖关系）

### Step 1: 共享 Codec 数据竞争 (C1)
**文件**: `broker/server.go:52,177` + `broker/broker.go:105-107`
**方案**: 从 server 传递 `nil` codec，broker 创建独立实例（已有代码路径）
**验证**: `go test -race ./broker/...`

### Step 2: Drain 循环错误键 (C10)  
**文件**: `broker/broker.go:207-220`
**方案**: 将 `for _, id := range b.connections` 改为 `for clientID := range b.connections`
**验证**: `go test -race ./broker/...`

### Step 3: protocol 层安全加固 (C7, C8, C9)
**文件**: `protocol/codec.go`
**方案**:
- `readVarInt`: 最多 4 次迭代
- `writeString`: 检查长度 > 65535
- `readString` + `writeString`: 添加 UTF-8 验证
**验证**: `go test ./protocol/...`

### Step 4: CONNECT 协议验证 (C5)
**文件**: `protocol/connect.go` + `broker/broker.go`
**方案**: 新增 `ValidateConnect()` 函数，在 HandleConnection 中调用
**验证**: 新增协议验证测试

### Step 5: 连接生命周期修复 (C3, C6)
**文件**: `broker/broker.go:187-192,259-310`
**方案**: 移除 readLoop 的 disconnect 调用，由 HandleConnection 统一处理
**验证**: `go test -race ./broker/... && go test -race ./tests/integration/...`

### Step 6: WillHandler goroutine 管理 (C4)
**文件**: `broker/will_handler.go`
**方案**: 添加 sync.WaitGroup 追踪，Stop() 等待退出
**验证**: `go test -race ./broker/...`

### Step 7: QoS 引擎修复 (C2, C11)
**文件**: `broker/qos_engine.go` + `broker/broker.go`
**方案**:
- 分离入站/出站 QoS 确认逻辑
- QoS 2 延迟交付至 PUBCOMP
**验证**: `go test -race ./tests/integration/... -run "QoS"`

### Step 8: 客户端 QoS 发布阻塞 (C12)
**文件**: `client/client.go`
**方案**: 添加响应等待超时，readLoop 退出时关闭所有待处理 channel
**验证**: `go test -race ./client/...`

---

## 每步验证命令

```bash
go build ./...                    # 编译通过
go vet ./...                      # 无警告
go test -race -count=1 ./...      # 无 race + 测试通过
go test -count=1 ./tests/integration/...  # 集成测试通过
```
