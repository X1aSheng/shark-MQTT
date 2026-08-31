# 部署验证报告 (V7 修复后云上验证)

- 日期: 2026-08-06 20:13
- 服务器: 120.76.44.233 (Ubuntu 26.04, Linux 7.0.0-15-generic, hostname iZwz93b9fvc8lttmuewj0cZ)
- 硬件: **2 vCPU (1 核 × 2 线程, Intel Xeon Platinum), 内存 ~1.6GB, 磁盘 40G (27G 可用)**
  - 注: 该实例已从早前报告记录的 8 核/16G **降配至 2 vCPU/1.6GB**, 基准数值反映此规格, 与历史记录不可直接对比
- 访问: SSH key (免密), root 用户
- 源码: HEAD `c24360bf` (V7 修复 R1-R8 已全部合入; 含 race 守卫修正)
- 部署: 本地 `git archive HEAD` -> scp -> 解压至 `/opt/shark-mqtt`; Go `go1.26.5 linux/amd64`

## 1. 编译验证

| 检查项 | 结果 |
| --- | --- |
| `go build ./...` | PASS |
| `go build ./examples/...` | PASS |
| `go vet ./...` | PASS |
| `go build -tags=nometrics ./...` | PASS (最小构建) |

## 2. 单元 + 集成测试

| 项 | 数量 |
| --- | --- |
| 单元测试 PASS (不含集成) | 350 |
| 集成测试 PASS | 99 |
| SKIP | 13 (Redis) |
| 合计 | **449 PASS + 13 SKIP** |
| `go test -count=1 ./...` | 16 个包全部 `ok`, 集成 9.1s |

相对 V7 报告基线 (344 unit + 96 integration): 单元 +6、集成 +3, 来自 R1/R7/R8 回归测试与 R2 分配断言等。

## 3. 基准测试 (2 vCPU, benchtime=200ms)

### Codec (R2 验证: DecodePublish 分配)
| 基准 | 耗时 | 内存 | allocs |
| --- | --- | --- | --- |
| Codec_DecodePublish | 698 ns/op | 453 B/op | **8 allocs/op** (修复前 10) |
| Codec_RoundTripPublish | 1313 ns/op | 880 B/op | 14 allocs/op |
| Codec_EncodePublish | 574 ns/op | 422 B/op | 6 allocs/op |
| Codec_EncodePublishQos1 | 583 ns/op | 422 B/op | 6 allocs/op |
| Codec_EncodeLargePayload | 19.3 µs/op | 18.6 KB/op | 6 allocs/op |

### TopicTree (R3 验证: Match 分配)
| 基准 | 耗时 | 内存 | allocs |
| --- | --- | --- | --- |
| TopicTree_Match_Exact | 508 ns/op | 160 B/op | **2 allocs/op** (修复前 3) |
| TopicTree_Match_WildcardPlus | 618 ns/op | 160 B/op | 2 allocs/op |
| TopicTree_Match_WildcardHash | 467 ns/op | 160 B/op | 2 allocs/op |
| TopicTree_Match_ManySubscribers | 19.4 µs/op | 14.2 KB/op | 16 allocs/op |
| TopicTree_Subscribe | 275 ns/op | 51 B/op | 0 allocs/op |
| TopicTree_Unsubscribe | 74.6 ns/op | 0 B/op | 0 allocs/op |

### E2E Broker (R1 写队列 / 全链路)
| 基准 | 耗时 | 内存 | allocs |
| --- | --- | --- | --- |
| PublishQos0 | 11.5 µs/op | 1.1 KB/op | 19 allocs/op |
| PublishQos1 | 87.3 µs/op | 1.7 KB/op | 34 allocs/op |
| PublishQos2 | 159 µs/op | 2.5 KB/op | 55 allocs/op |
| ConcurrentPublish | 10.8 µs/op | 1.1 KB/op | 19 allocs/op |
| TopicWildcardMatch | 7.1 µs/op | 667 B/op | 15 allocs/op |
| FanOut_1Sub / 5Subs / 10Subs / 50Subs | 10.7 / 24.3 / 25.6 / 34.1 µs/op | - | 18/37/42/58 allocs |
| E2E QoS0 / QoS1 / QoS2 DataVerify | 81 / 124 / 236 µs/op | - | 38/62/103 allocs |
| E2E RetainedMessage | 95.5 µs/op | 1.6 KB/op | 46 allocs/op |
| E2E WillMessage | 638 µs/op | 10.8 KB/op | 143 allocs/op |
| E2E Payload 64B~128KB | 18.7 µs ~ 1.4 ms/op | - | 25~28 allocs |
| ConnectionEstablish | 191 µs/op | 2.1 KB/op | 44 allocs/op |
| MQTTConnect | 606 µs/op | 9.9 KB/op | 113 allocs/op |
| PersistentSession | 1.03 ms/op | 153 KB/op | 1016 allocs/op |

### 内部组件
| 基准 | 耗时 | allocs |
| --- | --- | --- |
| QoSEngine_TrackQoS1 / TrackQoS2 | 58.9 / 58.0 ns/op | 0 |
| Manager_CreateSession | 898 ns/op | 6 |
| Manager_GetSession / MultiClientLookup / RestoreSession | 29.1 / 59.9 / 23.8 ns/op | 0 |
| BufferPool_GetPut | 55.6 ns/op | 1 |
| MemoryStore_SessionSave / SessionGet | 34.8 / 20.6 ns/op | 0 |

## 4. 结论

- V7 修复 (R1-R8) 在云服务器 (Linux amd64) 上**编译 / 单元+集成测试 / 基准全部通过**。
- R2 分配优化在云上确认: `Codec_DecodePublish` = **8 allocs/op** (修复前 10); R3 确认: `TopicTree.Match` = **2 allocs/op** (修复前 3)。
- 该实例已降配至 **2 vCPU / 1.6GB**, 所有基准数值反映此硬件; 若需与 8 核/16G 的早前记录对齐, 应升级实例后复测。
- 云上残留 `/opt/shark-mqtt-bad`、`/opt/shark-mqtt-bad-old` (历史部署目录, 已移出活动路径, 可清理)。
