# 四个 MQTT Broker 对比与 shark-mqtt 性能补齐

> 日期: 2026-10-17 12:15 (UTC+8)
> shark-mqtt 基线: `81d95f344`
> 对比对象: smart-mqtt (Java), smart-mqtt-4g (Go), mica-mqtt (Java)
> 目的: 用可核对的数据定位 shark-mqtt 的位置, 并标出还缺哪些测量

---

## 1. 先说结论

| 结论 | 依据 |
|------|------|
| shark-mqtt 的**协议完整性**在四者中最好 | 仅 shark-mqtt 处理 AUTH(0x0F) 全流程; 属性合法性与固定头 flags 双向校验为独有 |
| shark-mqtt 的**体积**比 Java 方案大一个量级, 但比"Java + JRE"小两个量级 | 11.7 MB Go 静态二进制, 含运行时; smart-mqtt 的 <800 KB 是不含 JRE 的 jar |
| shark-mqtt 的**吞吐数据是四者里最不完整的** | 只测过 4 订阅者小 fanout, 没测过高连接数与高 fanout; 另两家有公开的 35 万 - 90 万 msg/s 数字 |
| 现有数字**不能直接横向比** | fanout 倍数、硬件、平台、是否含客户端开销全部不同; 第 4 节给出归一化口径 |
| mica-mqtt 与 shark-mqtt **不是同类** | 前者是嵌入式组件 (client + server 库), 后者是完整 broker 二进制 |

---

## 2. 定位矩阵

| 维度 | shark-mqtt | smart-mqtt | smart-mqtt-4g | mica-mqtt |
|------|-----------|-----------|--------------|-----------|
| 语言 | Go | Java | Go | Java |
| 形态 | 完整 broker 二进制 + 可嵌入库 | 完整 broker 发行包 | 单机协议内核 | 组件库 (client + server) |
| 协议 | 3.1 / 3.1.1 / 5.0 | 3.1.1 / 5.0 | 3.1.1 硬标准 + 5.0 基础兼容 | 3.1 / 3.1.1 / 5.0 |
| 许可 | MIT | AGPL-3.0 + 商业授权限制 | MIT | MIT (Dromara) |
| 存储抽象 | 接口 + memory/redis/badger | 插件化 | 单机内存为主 | 无 (靠外部) |
| WebSocket | 支持 (ws/wss) | 支持 | 未在 README 声明 | 支持 |
| 集群 | 不支持 (见 CLUSTER-FEASIBILITY) | 原生多节点 | POC | redis stream 插件 |
| 面向场景 | 单机生产 + 可嵌入 | 企业级万级设备 | 协议内核 + 自测门禁 | 业务系统内嵌 MQTT |

**许可差异值得单列**: smart-mqtt 页面明确写"仅供个人学习使用, 未经授权禁止用于商业目的", 这与 AGPL-3.0 的表述并存, 商业使用需单独授权。shark-mqtt 是 MIT, 无此限制。

---

## 3. 协议完整性对比

这一项 shark-mqtt 领先, 且有可指认的代码依据:

| 能力 | shark-mqtt | smart-mqtt | 说明 |
|------|-----------|-----------|------|
| AUTH (0x0F) 报文 | 完整 (握手期 + 会话内重认证) | **完全不处理** | smart-mqtt `MqttHeaderDecoder` 的 switch 无 case 15, 收到 AUTH 直接抛异常 |
| 出站 PUBLISH 属性中继 | 逐属性转发, Topic Alias 剥离 | **每条出站 PUBLISH 构造空 PublishProperties** | 导致 MQTT 5.0 Section 4.10 的请求/响应模式在 smart-mqtt 上不可用 |
| 属性归属校验 (Table 2-4) | 编解码双向, 11 个 locus + 27 条 probe | 未做 | 非法属性 ID 与重复属性分别判 Malformed / Protocol Error |
| 固定头 flags 校验 | 编解码双向 | 部分 | shark 对 PUBREL/SUBSCRIBE/UNSUBSCRIBE 的 0b0010 组有专门处理 |
| 出站 Maximum Packet Size 约束 | 编码层收口 | 未见 | 超限报文按规范丢弃而非截断 |
| 解码流对齐 | 拒绝的报文只消费自己的 Remaining Length | 未见 | 防止用对端可控长度索赔 64 KiB |

> 注: smart-mqtt 的两项判定来自其 Java 源码 (`enums/MqttMessageType.java:18`, `MqttHeaderDecoder` switch), 记录在 `docs/reports/SMART-MQTT-GAP-ANALYSIS-261014-2125.md`。

---

## 4. 性能数据

### 4.1 shark-mqtt 实测 (本轮)

**环境**: Windows 11, AMD Ryzen 7 8845HS (8C16T), Go 1.26.1, broker 与 loadgen 同机竞争 CPU。
**方法**: `tools/loadgen` 通过项目自身 client 包驱动, 延迟由载荷内时间戳在订阅端计算 (端到端, 含编解码与写队列)。

| 场景 | 目标 | 实际发送 | 投递 | frout | P50 | P99 | P999 |
|------|------|---------|------|-------|-----|-----|------|
| QoS 0, 4 sub / 8 pub, 256 B | 20,000 msg/s | 19,968 msg/s | 79,873 msg/s | 400% | 0.0 us | 1,048 us | 1,823 us |
| QoS 1, 4 sub / 8 pub, 256 B | 10,000 msg/s | 9,994 msg/s | 39,975 msg/s | 400% | 0.0 us | 598 us | 1,212 us |

`P50 = 0.0 us` 是 Windows 计时器精度造成的下溢, 不代表真实零延迟; 该列在 Linux 基线上才有意义 (见 4.2)。

**资源**: 空载工作集 14.5 MB / 9 线程; 两轮压测后工作集 23.1 MB, 峰值 24.4 MB。

### 4.2 shark-mqtt Linux 基线 (2026-10-15, commit d5e37f8f9)

这是唯一在 Linux 上带资源采样的完整数据, 记录在 `docs/benchmarks/2026-10-15-server-baseline.md`:

| 配置 | QoS | 发送 | 投递 | P50 | P99 | CPU (核) | 峰值 RSS |
|------|-----|------|------|-----|-----|---------|---------|
| Shared | 1 | 19,652 | 78,610 | 297 us | 1,734 us | 3.19 | 20.0 MB |
| Pinned (broker 0-3, client 4-7) | 1 | 19,668 | 78,671 | 190 us | 1,197 us | 2.97 | 19.2 MB |
| Shared | 0 | 19,670 | 78,678 | 158 us | 722 us | 2.12 | 20.1 MB |

**归一化**: Pinned QoS 1 下 78,671 投递/s / 2.97 核 = **26.5k 投递/核/s**; QoS 0 下 78,678 / 2.12 = **37.1k 投递/核/s**。

### 4.3 另外三家公开的数字

| 项目 | 场景 | 数字 | 硬件 |
|------|------|------|------|
| smart-mqtt-4g | M2 fanout 2000x10x100 | 908,000 msg/s | i9-12900HK |
| smart-mqtt-4g | M1 fanout 750x3x500 | 1,171,000 msg/s | i9-12900HK |
| smart-mqtt-4g | M3 publish 2000 稳态 | 406,000 msg/s | i9-12900HK |
| smart-mqtt-4g | QoS 0 publish 稳态 | 351,000 msg/s | i9-12900HK |
| smart-mqtt | 未给具体 QPS | "10 万+ 设备接入" | 未给 |
| mica-mqtt | 未给具体 QPS | 无 | 未给 |

smart-mqtt-4g 自己注明: 这些数字是**移植 fanout-workers / decoder-read-buffer 优化之前的基线**, 需用 `run-fair-benchmark.sh` 重跑后更新。

### 4.4 为什么不能直接比

| 差异 | shark-mqtt | smart-mqtt-4g | 影响 |
|------|-----------|--------------|------|
| fanout 倍数 | 4 | 2000 | 投递数是 fanout 的倍数, 900k 除以 2000 只有 454 主题发布/s |
| 发布者数 | 8 | 10-2000 | 并发连接数不同 |
| 是否含客户端开销 | 含 (项目自身 client) | 未声明 | 若用外部压测器, broker 侧数字会更好看 |
| 硬件 | 8C16T 笔记本 CPU | i9-12900HK (14C20T) | 核数差 ~1.7 倍 |
| CPU 占用是否公布 | 是 | 否 | 无法归一化 |

**换算尝试** (仅作量级参考, 不可作为结论): 若把 4g 的 M2 数字按 fanout 归一, 主题发布率约 454/s; shark 在同口径 (4 sub) 下是 79,873 / 4 = **19,968 主题发布/s**。这个对比反而显示 shark 在小 fanout 下更强, 但两者的订阅者规模相差 500 倍, 场景完全不同, 因此**不能据此下结论**。

真正缺的是: **shark-mqtt 没有在高 fanout (数百至数千订阅者) 与大连接数 (万级) 下测过**。这是本节最重要的发现。

---

## 5. 资源占用

### 5.1 二进制体积

交叉编译 (linux/amd64, CGO off, `-trimpath -ldflags="-s -w"`):

| 构建 | 体积 | 说明 |
|------|------|------|
| 默认 | 11,747,490 B (11.2 MiB) | 含 Prometheus、WebSocket、配置加载 |
| `-tags nometrics` | 8,032,418 B (7.7 MiB) | 去掉指标子系统 |

构成 (粗略): Go 运行时约 2 MB, Prometheus client 约 2-3 MB, 其余为业务代码与 codec。

smart-mqtt 的 <800 KB 是 **jar 包**, 运行时另需 JRE (17+ 约 100-180 MB)。mica-mqtt 的 500 KB 同理。所以三者不是同一量级的比较对象:

| 方案 | 发行物 | 运行时 | 部署总占用 |
|------|--------|--------|-----------|
| shark-mqtt | 8-11 MB 静态二进制 | 无 | 8-11 MB |
| smart-mqtt | <800 KB jar | JRE 17+ | 100+ MB |
| mica-mqtt | 500 KB 核心依赖 | JRE 17+ 或 GraalVM 原生 | 100+ MB 或数 MB |

mica-mqtt 支持 GraalVM 原生编译, 这是 Java 阵营缩小体积差距的路径; 但那需要额外构建步骤, 且反射/动态代理受限。

### 5.2 运行内存

| 状态 | shark-mqtt (Windows) | shark-mqtt (Linux 基线) |
|------|---------------------|------------------------|
| 空载 | 14.5 MB 工作集 / 9 线程 | 未测 |
| 4 sub / 8 pub 压测后 | 23.1 MB, 峰值 24.4 MB | 20.1 MB (VmHWM) |
| 内存后端 | in-process map | 同 |

另两家的内存数字未公开。这是一个**可以主动争取的优势项**: 20 MB 级别的 RSS 在 Java 方案上通常做不到 (JVM 堆基线就在数十 MB)。

### 5.3 外部依赖

直接依赖 6 个 (go.mod `require` 块):

```
github.com/dgraph-io/badger/v4     存储后端 (build tag)
github.com/gorilla/websocket        WebSocket 传输
github.com/prometheus/client_golang 指标
github.com/redis/go-redis/v9        存储后端 (build tag)
golang.org/x/crypto                 bcrypt
gopkg.in/yaml.v3                    配置
```

其中 badger 与 go-redis 只在对应 build tag 下编译进二进制。默认构建 (`-tags nometrics`) 的实际依赖只有 websocket + crypto + yaml。间接依赖 20 个, 全部来自 prometheus。

"极少外部依赖"这一条 shark-mqtt 成立: 静态二进制 + 零运行时服务依赖, 这在容器镜像里体现为 `FROM scratch` 可用。

---

## 6. shark-mqtt 的位置

| 维度 | 判断 |
|------|------|
| 协议正确性 | **领先**。四者中唯一完整实现 AUTH 与属性归属双向校验 |
| 单机小 fanout 吞吐 | 与 4g 同量级 (需同硬件复测才能定论) |
| 高 fanout / 高连接数吞吐 | **数据缺失, 也无法宣称** |
| 冷启动与内存 | 领先 Java 方案一个量级 (20 MB vs 数十至数百 MB) |
| 发行物体积 | 输给 Java jar (11 MB vs 800 KB), 赢过 jar+JRE 总和 |
| 集群 | 无。smart-mqtt 原生支持, 4g 有 POC |
| 生态与上位机 | 弱于 mica-mqtt (后者有 Spring/Solon/JFinal 全套 starter) |

一句话: **shark-mqtt 目前是"协议最严谨、资源最省的单机 Go broker", 不是"吞吐最高的 broker", 且没有证据支持后者。**

---

## 7. 补齐计划

要能在对外材料里写性能, 还缺四组测量。按信息价值排序:

| 序号 | 测量 | 目的 | 方法 |
|------|------|------|------|
| 1 | 高 fanout: 1 pub / 500 sub, QoS 0 | 对标 4g 的 M2 场景 | `loadgen -subscribers 500 -publishers 1`, 记录投递/s 与 CPU |
| 2 | 大连接数: 10,000 空闲连接 | 对标 smart-mqtt 的"10 万+ 设备" | 新增 `loadgen -mode idle` 或独立工具, 记录每连接内存与建连速率 |
| 3 | 同硬件复测 4g 的 M3 场景 | 消除 fanout 与硬件两个变量 | 需在 14C+ 机器上各跑一轮, 同 payload 同速率 |
| 4 | Linux 上的当前 HEAD 基线 | 2026-10-15 的基线落后 14 个提交 | 重复 `2026-10-15-server-baseline.md` 的方法, 确认无退化 |

其中 1 和 2 只需改 loadgen 参数或加一个模式, 成本最低; 3 需要外部机器; 4 需要一台 Linux 主机 (云主机已释放)。

在完成 1、2 之前, 对外只应写"协议完整性"与"资源占用", 不应写吞吐数字。

---

## 8. 数据来源

| 数据 | 来源 | 可信度 |
|------|------|--------|
| shark 体积 | 本轮交叉编译实测 | 高 |
| shark 内存 | 本轮 Windows 实测 + 2026-10-15 Linux 基线 | 中 (平台不同) |
| shark 吞吐 | 本轮 Windows 实测 + 2026-10-15 Linux 基线 | 中 (小 fanout) |
| smart-mqtt 800 KB / 10 万设备 | 其 Gitee README | 宣称值, 未复核 |
| smart-mqtt-4g 90.8 万等 | 其 Gitee README 的性能快照 | 宣称值, 自带"待重跑"标注 |
| mica-mqtt 500 KB | 其 GitHub README | 宣称值, 未复核 |
| smart-mqtt 协议缺陷 | 其 Java 源码, 记录于 SMART-MQTT-GAP-ANALYSIS | 高 (有 file:line) |
