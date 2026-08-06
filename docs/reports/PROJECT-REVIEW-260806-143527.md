# shark-mqtt 对照审查报告 (V7, 参照 smart-mqtt / smart-mqtt-4g / mica-mqtt)

- 日期: 2026-08-06 14:35
- 范围: 修复后 shark-mqtt 全量源码 + 依赖/体积 + 性能基准 + 功能对照
- 参照项目:
  - smart-mqtt (Java, smart-socket AIO, 发行包 <4MB, 插件化+事件驱动, 百万级连接)
  - smart-mqtt-4g (Go 单机协议内核, 领域模型参考 smart-mqtt)
  - mica-mqtt (Java AIO, 核心依赖 ~500KB, 低延迟, 支持 WebSocket/REST/集群)
- 基线: build OK, vet OK, gofmt 干净, 344 单元 PASS (13 Redis SKIP), 96 集成 PASS, 65 基准 PASS

## 1. 测试基线

| 检查项 | 结果 |
| --- | --- |
| go build ./... + ./examples/... | PASS |
| go vet ./... / gofmt -l | PASS / 干净 |
| 单元测试 | 344 PASS + 13 SKIP (Redis) |
| 集成测试 | 96 PASS |
| 基准测试 | 65 函数 PASS |
| 日志 | logs/20260806_143145_* |

关键基准 (Windows Ryzen 7 8845HS):
| 场景 | 数值 |
| --- | --- |
| QoS0 发布 (E2E TCP) | ~22.3us, 1837 B/op, 28 allocs |
| QoS1 发布 | ~38.3us |
| QoS2 发布 | ~86.0us |
| 并发发布 | ~32.6us |
| FanOut 10 订阅者 | ~149us |
| TopicTree.Match (通配符) | ~195ns, 2 allocs |
| Codec.DecodePublish | ~577ns, 759 B, 10 allocs |

## 2. 依赖与发行体积对照

| 维度 | shark-mqtt | smart-mqtt | mica-mqtt |
| --- | --- | --- | --- |
| 发行体积 | 二进制 11.5MB (strip), Docker 镜像 ~35MB | <4MB (宣传 <800KB) | 核心依赖 ~500KB |
| 传递依赖 | 61 个模块 (5 直接) | 极少 | 极少 |
| 运行时 | Go 1.26 静态 | JVM (AIO) | JVM (AIO) |

主要体积来源 (均无条件编译进 go.mod):
- badger/v4 (存储后端): 引入 ristretto, protobuf, flatbuffers, go-farm, google/uuid, go-humanize 等
- prometheus/client_golang (指标): 引入 kingpin, beorn7/perks, cespare/xxhash, procfs, common 等
- 默认存储为 memory, 默认指标为 prometheus; badger/redis 仅在显式配置 StorageBackend 时使用, 但依赖仍全部打进二进制.

对照结论: 功能完备 (多存储后端 + Prometheus) 换来了明显更大的体积, 与参考项目"极致轻量"取向相反.

## 3. 性能对照

- smart-mqtt 声称 QoS0 峰值 738W/s (高配 Java, 聚合吞吐); shark-mqtt 单机 E2E 单消息 ~22us (约 4.5 万/s 单线程). 方法论不同, 不可直接比较; 但 shark-mqtt 的每条消息 28 allocs / 1837 B 有优化空间.
- 核心差异: 参考项目基于异步非阻塞 I/O (AIO) + 每连接写队列; shark-mqtt 为每连接同步写 (见 R1).

## 4. 缺陷清单 (本轮新发现, 已确认)

### P2
| # | 缺陷 | 位置 | 证据 |
| --- | --- | --- | --- |
| R1 | 写队列未实现: config.WriteQueueSize 与 broker.WithWriteQueueSize 均声明但从未使用; 写路径为每连接同步 Encode, 慢订阅者阻塞发布者 readLoop (队头阻塞) | config/config.go:34, broker/options_server.go:20, broker/broker.go writePacket | writeQueueSize 全库仅声明/默认/赋值, 无任何写入路径使用; cs.codec.Encode(cs.conn) 同步阻塞 |
| R6 | 流控出站缓冲无界: ReceiveMax 窗口满时 BufferOutbound 无限追加, 不 ACK 的客户端可耗尽内存 | broker/session.go BufferOutbound, broker.go doDeliver | doDeliver 满窗口分支直接 append, 无上限/淘汰/断连策略 |

### P3
| # | 缺陷 | 位置 |
| --- | --- | --- |
| R2 | decodePublish 负载缓冲不用 bufferpool (每消息 make), 10 allocs/decode; readString 已用池 | protocol/publish.go:37 |
| R3 | TopicTree.Match 每次匹配分配 results+visited (2 allocs/次); smart-mqtt 用 O(1) 位图匹配, 高订阅量下有差距 | broker/topic_tree.go Match |
| R4 | 依赖体积偏重 (见第 2 节); badger/prometheus 无条件编译, 无 build tag 可选裁剪 | go.mod |
| R5 | 无 WebSocket MQTT 子协议传输 (mica/smart-mqtt 均支持); 仅 TCP/TLS | deploy/Dockerfile, broker/server.go |
| R7 | 共享订阅轮询计数在在线集合变化时分布偏移 (counter % len(online)) | broker/topic_tree.go matchShared |
| R8 | 无 $SYS broker 状态主题生成 (连接数/负载/版本等); 仅做 $SYS 保护与测试 | 全库 grep 无生成代码 |

## 5. 修复状态更新

- **R6 已修复 (本轮)**: BufferOutbound 增加 maxBufferedOutbound=1000 上限, 满时丢弃新消息并记指标; 回归测试 + 全量 + race 通过.
- R1, R2, R3, R4, R5, R7, R8 按下列计划推进.

### 优先级 1 (正确性/资源)
1. **R6**: 已修复 (出站缓冲上限).
2. **R1**: 实现每连接写队列 (异步写协程 + 有界队列 + 背压), 消除队头阻塞; 让 WriteQueueSize 配置生效. 这是较大的架构改动, 建议单独一轮.

### 优先级 2 (性能)
3. **R2**: decodePublish 小负载走 bufferpool (<= BufSize 时池化), 降低 allocs.
4. **R3**: 若追求高订阅量, 评估位图/布隆匹配替代 trie; 或复用 Match 的 results/visited (对象池). 当前 trie 对常规规模足够.

### 优先级 3 (功能/工程)
5. **R5**: 增加 WebSocket MQTT 子协议传输 (可参考 mica-mqtt 的 ws 通道), 扩展接入面.
6. **R4**: badger/redis/prometheus 依赖加 build tag 可选裁剪, 提供最小构建; 或文档明确体积权衡.
7. **R8**: 增加 $SYS 状态主题生成 (connections/load/version/uptime) 供运维订阅.
8. **R7**: 共享订阅轮询在成员集合变化时保持公平 (按在线成员稳定计数).

每个修复附回归测试, 全量 + race 验证, 逐个提交, 触发 CI.

## 6. 可重入性约束 (新修复须遵守)

- 写队列实现不得引入全局单例, 每连接独立.
- 缓冲上限/淘汰不得破坏 QoS1/2 已确认投递的语义 (优先丢弃未入队的新消息).
- 所有新并发路径保持现有锁序 (b.mu -> sessions -> topics), 避免死锁.

## 7. 结论

- 修复后基线全绿 (344/96/65), 协议正确性与并发健壮性已达标.
- 与参考项目对照, 主要差距集中在: (a) 体积/依赖偏重, (b) 同步写无背压 (R1), (c) 缺 WebSocket/$SYS 等接入与运维特性.
- 本轮先出报告; 优先修复 R6 (P2 资源问题), R1 写队列作为下一轮大项, 其余性能/功能项按计划逐项评估.
