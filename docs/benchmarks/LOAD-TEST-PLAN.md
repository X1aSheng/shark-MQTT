# 负载测试方案

> 版本: 2026-10-17
> 适用: shark-mqtt 单机 broker (集群不支持, 见 CLUSTER-FEASIBILITY.md)
> 相关: 微基准见 [PERFORMANCE](../guides/PERFORMANCE.md); 已测数据见 [2026-10-17 Aliyun 快照](2026-10-17-aliyun-2c4g.md)

本方案回答四个问题, 每个问题对应一组测试, 不要混着做:

| 问题 | 测试 | 产出 |
|------|------|------|
| 这台机器最多能搬多少消息 | 速率扫描 (第 6.2 节) | 饱和点 + 饱和时的资源 |
| 多少订阅者还算实时 | fanout 扫描 (6.3) | 延迟拐点 |
| 一台设备闲置要多少内存 | 连接扫描 (6.4) | 每连接字节数 |
| 这次改动有没有变慢 | 回归门禁 (7) | 通过 / 不通过 |

---

## 1. 测试维度

六个维度, 每次只变一个, 其余固定在基线值:

| 维度 | 基线值 | 扫描范围 |
|------|--------|---------|
| QoS | 0 | 0 / 1 / 2 |
| payload | 256 B | 64 B / 256 B / 1 KB / 4 KB / 16 KB / 64 KB |
| rate (总发布速率) | 20,000 msg/s | 到饱和为止, 每次 x1.5 |
| subscribers (fanout) | 4 | 4 / 100 / 200 / 500 |
| publishers | 8 | 1 / 8 / 32 |
| 空闲连接数 | - | 1k / 5k / 10k |

**同时改两个维度的数据没有解释力。** 尤其不要一边加 rate 一边加 fanout -- 分不清是扇出成本还是入站成本。

---

## 2. 环境规则

以下五条不是建议, 是踩过的坑:

### 2.1 broker 与压测端不要同机

同机会让两个进程抢同一批核。实测同一份二进制、同样 4 订阅者 8 发布者:

| | 投递/s | P50 | P99 | broker CPU |
|---|---|---|---|---|
| 同机 (2 核) | 79,165 | 306 us | 869 us | 0.55 核 |
| 跨机 (2 核) | 79,214 | 182 us | 556 us | 0.43 核 |

吞吐几乎一样, 延迟差 40%, CPU 差 22%。**同机数据只能用作下限, 不能写进对外材料。**

压测机与 broker 的链路要先测: 阿里云同 VPC 私网 RTT 0.1 ms 级, 带宽 2.9 Gbps 以上 (见 2026-10-17 快照)。**用私网 IP, 不要走公网**, 否则 100 Mbps 的出口会把 broker 的数字压到无意义。

### 2.2 每轮之间重启 broker

上一轮残留的连接会改变下一轮结果。判断方法: `ss -s | head -2` 看 `estab` 是否归零。不归零就重启。

### 2.3 客户端源地址要轮转

单源 IP 只有 16,384 个动态端口 (Windows), 客户端主动关闭产生的 TIME_WAIT 会在下一轮把端口吃光。`loadgen` 已在 `127.0.0.2` - `127.0.0.17` 上轮转。

Linux 上 `ip_local_port_range` 默认 28,232 个, 更宽松, 但一轮 10k 连接后仍建议等 TIME_WAIT 清空 (`ss -s | grep timewait`)。

### 2.4 不要让 shell 管道杀掉被测程序

`loadgen ... | head -20` 会在读满 20 行后关闭管道, 程序收到 EPIPE 退出, 其 socket 不正常关闭。**表现是下一轮失败率随轮次单调上升, 看起来像 broker 崩了。**

用 `tail`, 或重定向到文件, 或 `-json` 落盘。

### 2.5 二进制上传前先停进程

Linux 不允许覆盖正在执行的 ELF, SFTP 会报 `OSError: Failure`。顺序永远是 `pkill` -> 上传 -> `chmod +x` -> 启动。

---

## 3. 工具

### 3.1 loadgen (首选)

```bash
# 负载模式
./loadgen -addr <broker> -qos 0 -rate 20000 -subscribers 4 -publishers 8 \
          -payload 256 -duration 20s -pid <broker-pid>

# 混合 QoS (70% QoS 0 + 30% QoS 1); 订阅自动取最高 QoS
./loadgen -addr <broker> -mix "qos0:70,qos1:30" -rate 20000 \
          -subscribers 4 -publishers 8 -duration 20s

# 长稳, 每 30 秒记录一个资源点, 落盘带 timeline
./loadgen -addr <broker> -mix "qos0:70,qos1:30" -rate 20000 -duration 10m \
          -sample-interval 30s -json -note "soak 10min" > soak.json

# 空闲连接模式
./loadgen -addr <broker> -mode idle -connections 5000 -idle-parallel 64 -duration 10s
```

**`-mix` 用固定模式而不是随机抽样**: 权重整除时任意窗口内的比例都精确, 这样不同长度的两次运行才可比。订阅 QoS 自动取模式里的最高值 -- 否则 broker 会把投递降级, 测的就不是混合负载的成本。

`-pid` 让 loadgen 采样 broker 的 `/proc`, 输出 RSS 均值/峰值/线程数/CPU 核数。**只有 Linux 可用**; Windows 上该列为空, 需另行用 `Get-Process` 采样。

`-json` 输出结构化结果, 直接重定向进 `docs/benchmarks/data/`。

### 3.2 独立客户端复核 (必须)

当结果看起来像产品缺陷时, 换一个实现复核。本轮就是靠这个把 "broker 只支持 5000 连接" 证伪的:

```python
# 20 行即可: 裸 socket + 手写 CONNECT + 读 CONNACK
s = socket.socket(); s.bind(('127.0.0.%d' % (2 + i % 16), 0)); s.connect(ADDR)
s.sendall(connect_pkt(client_id)); assert len(s.recv(4)) >= 2
```

一个语言、一套库的全部数据都来自同一个客户端时, 客户端的 bug 会伪装成产品缺陷。

### 3.3 资源采集 (无 loadgen 时)

```bash
# 峰值内存 (进程生命周期内高水位)
grep VmHWM /proc/<pid>/status

# 平均 CPU 核数: 采样前后 utime+stime 差值 / CLK_TCK / 墙钟
awk '{print $14, $15}' /proc/<pid>/stat; getconf CLK_TCK
```

`VmHWM` 而不是 `VmRSS`: 后者是瞬时值, GC 之后会回落, 看不出突发峰值。

---

## 4. 指标定义

| 指标 | 定义 | 注意 |
|------|------|------|
| 发送 (msg/s) | 压测端实际 publish 出去的速率 | 与 `-rate` 的差值就是背压程度 |
| 投递 (msg/s) | 订阅端实际收到的速率 | 等于发送 x 订阅者数 x (1 - 丢失率) |
| 达成率 | 投递 / (发送 x 订阅者数) | 400% 表示 4 订阅者全部收到 |
| P50 / P99 / P999 | 端到端延迟分位 | 发布端打时间戳进 payload, 订阅端计算差值 |
| CPU (核) | 窗口平均核数 | 不是瞬时值, 跨机采集才有意义 |
| RSS 峰值 | `VmHWM` | 不是 `VmRSS` |
| 带宽 | 投递/s x 帧大小 | 与链路容量比较, 判断瓶颈归属 |

**延迟必须是端到端的。** 只测 publish 调用耗时, 量到的是本地 socket buffer。

**P50 为 0 要当异常处理。** Windows 计时器精度不足时会出现, 该列在 Windows 上不可用。

---

## 5. 执行序列

### 5.1 冒烟 (2 分钟)

确认链路与工具正常, 不做任何结论:

```bash
./loadgen -qos 0 -rate 5000 -subscribers 4 -publishers 1 -payload 256 -duration 5s
```

达成率必须 400%、错误 0。否则先修环境, 不要继续。

### 5.2 速率扫描 (找饱和点)

固定 QoS 0 / 4 订阅者 / 8 发布者 / 256 B, rate 从 5,000 起每次 x1.5, 直到达成率跌破 95%:

| rate | 达成率 | 投递/s | P50 | CPU |
|------|-------|--------|-----|-----|
| 5,000 | | | | |
| 20,000 | | | | |
| 60,000 | | | | |
| 100,000 | | | | |

**饱和点 = 最后一个达成率 >= 95% 的档位。** 记录饱和时的 CPU 与带宽:

- CPU 接近核数上限 -> 瓶颈在 broker
- CPU 有余量但达成率掉了 -> 瓶颈在链路或压测端, 去查带宽和压测机 CPU

参考值 (2 核 / 1 KB payload): 100k rate 时 353k 投递/s, CPU 1.37 核, 2.9 Gbps -- 链路先到极限。

### 5.3 fanout 扫描 (找实时拐点)

固定 rate 2,000 / 1 发布者, subscribers 从 4 到 500:

| fanout | 投递/s | P50 | P99 |
|--------|--------|-----|-----|
| 4 | | | |
| 100 | | | |
| 200 | | | |
| 500 | | | |

**实时拐点 = P99 首次超过 10 ms 的 fanout。** 参考值 (2 核独占): fanout 200 时 P99 21.5 ms, 已越界; fanout 500 时 P99 6.96 s。

### 5.4 payload 扫描

固定 rate 20,000, payload 64 B 到 64 KB。预期吞吐几乎不变、延迟随帧增大而上升。若吞吐明显下降, 说明带宽或拷贝开销成为主导, 单独记录。

### 5.5 连接扫描

`-mode idle`, 1k / 5k / 10k。记录每连接字节数 (工作集增量 / 连接数) 与线程数。

**线程数不应随连接数增长**; 若增长, 说明每连接起了 OS 线程, 是设计问题。

> **已知限制: `-mode idle` 在 5000 个连接处停住。** 现象是恰好 5000 成功、其余全部
> 失败, 客户端报 `EOF`, 且**串行 (`-idle-parallel 1`) 与并发 64 结果一致**, Windows 与
> Linux 也一致。已排除: broker 拒绝 (日志里 0 条 `max connections`)、并发度、TIME_WAIT
> 残留 (清空后仍 5000)、端口耗尽 (同一台机器同一时刻, 一段 20 行的 Python 裸 socket
> 串行建 8000 个全部成功)。
>
> 因此这是 **loadgen 客户端**的问题, 不是 broker 的, 根因未定位。**测 5000 以上连接时
> 用 Python 脚本, 不要用 `-mode idle`** —— 见 3.2 节。这个限制值得修, 但在修好之前
> 用它得出的"连接数上限"结论都是错的。

### 5.6 长稳 (可选, 30 分钟以上)

固定一个 70% 负载点跑 30 分钟, 加 `-sample-interval 30s` 让 JSON 里带 `rss_timeline` 数组 (每次采样含 `t_s` / `rss_mb` / `peak_mb` / `threads` / `cpu_nano`), 再按序列判断趋势而不是只看首尾两个数。判据:

- RSS 不持续增长 (允许 GC 锯齿, 不允许单调爬升)
- 投递率不衰减
- 无 `-race` 报告 (需 race 构建)

---

## 6. 通过标准 (回归门禁)

同一环境、同一二进制参数的两次运行之间:

| 指标 | 允许变化 |
|------|---------|
| 投递/s | 不低于基线 5% |
| P99 | 不高于基线 50% |
| CPU | 不高于基线 10% |
| RSS 峰值 | 不高于基线 15% |

超出即视为回归, 需要解释。**两次 20 秒运行之间 P50 抖动 8-9% 属正常** (见 2026-10-17 快照 4.8 节的记录), 所以用 P99 而不是 P50 做门禁。

---

## 7. 数据落盘

每次正式测量存一份 JSON 到 `docs/benchmarks/data/`, 命名 `[序号]_[场景]_[日期].json`:

```
A_qos0_261017.json
R_1k_rate100k_261017.json
F_200sub_261017.json
C_idle5000_261017.json
```

必须在文件里带 `_meta`, 至少包含:

```json
{
  "_meta": {
    "date": "2026-10-17",
    "commit": "<git sha>",
    "broker_host": "aliyun 2C4G / ubuntu 26.04",
    "broker_cpu_cores": 2,
    "loadgen_host": "aliyun 2C4G (separate host, private net 0.1ms)",
    "co_located": false,
    "args": "-qos 0 -rate 100000 -subscribers 4 -payload 1024",
    "broker_vmhwm_kb": 51340,
    "broker_cpu_cores_used": 1.37
  }
}
```

没有 `_meta` 的数字三个月后无法解释, 也无法比较。

---

## 8. 已知陷阱汇总

| # | 陷阱 | 症状 | 处置 |
|---|------|------|------|
| 1 | 同机压测 | 数字系统性偏低 40% | 跨机, 用私网 |
| 2 | rate 当常量 | 把 79k 误读成上限, 实际 4.5 倍于此 | 至少两维扫描 |
| 3 | 管道 head | 失败率逐轮上升, 像 broker 崩溃 | 用 tail / 重定向 |
| 4 | 单源 IP | 4,500 连接后大量失败 | 源地址轮转 |
| 5 | 不重启 broker | 上一轮残留影响下一轮 | 每轮重启 |
| 6 | 只用一个客户端 | 客户端 bug 伪装成产品缺陷 | 换 Python 裸 socket 复核 |
| 7 | 看 `VmRSS` | 低估突发峰值 | 用 `VmHWM` |
| 8 | 覆盖运行中的二进制 | SFTP `OSError: Failure` | 先 pkill |
| 9 | 用 P50 做门禁 | 噪声 8-9%, 误报频繁 | 用 P99 |
| 10 | 忘了记 commit / 核数 | 数据不可比较 | 落盘带 `_meta` |

---

## 9. 工具能力

`-json` 输出自带 `_meta`, 并在 `-sample-interval > 0` 时附带 `rss_timeline`:

```json
"_meta": {
  "tool": "loadgen",
  "commit": "<vcs.revision>",
  "timestamp": "2026-10-17T17:11:11+08:00",
  "go_version": "go1.26.1",
  "platform": "linux/amd64",
  "mode": "load",
  "note": "<-note 的内容>"
},
"rss_timeline": [
  {"t_s": 0.0, "rss_mb": 16.4, "peak_mb": 16.6, "threads": 9, "cpu_nano": 120000000},
  {"t_s": 30.1, "rss_mb": 17.1, "peak_mb": 17.9, "threads": 9, "cpu_nano": 3800000000}
]
```

`commit` 读自二进制里的 `vcs.revision`, 所以 **用 `go build` 构建时是真实值, `go run` 下是 `unknown`**。正式测量必须用构建产物。

timeline 由已有的 250 ms 资源采样降采样得到: 半小时长稳原始有 7,200 个点, 全塞进 JSON 只会把趋势埋进噪声。按 `-sample-interval` 抽点后既保留单调爬升的可辨识性, 又不至于让人读不下去。

**跨机时 `-pid` 采不到 broker 资源** -- loadgen 只能读本机的 `/proc`。跨机长稳要在 broker 主机上另起一个采样循环 (第 3.3 节的命令), 再与 loadgen 的吞吐数据对齐时间戳。

### 多 payload 混合

`-payload-mix "64:80,4096:20"` 支持按比例混合帧大小, 用来模拟"心跳 + 遥测"共存的设备群。同样用固定模式而非随机抽样, buffer 预分配并按模式轮转, **热路径不分配内存** -- 否则测到的会是分配器的行为。

实测 (80% 的 64 B + 20% 的 4 KB, 跨机 2 核): 投递率与纯 64 B 完全一致 (79,200 对 79,207), 代价只出现在 P99 (564 us -> 692 us)。见快照 H 组。

## 10. 与微基准的分工

| | 微基准 (`go test -bench`) | 本方案 |
|---|---|---|
| 测什么 | 单个函数 (编码、匹配、入队) | 一个运行中的 broker |
| 单位 | ns/op, B/op, allocs/op | msg/s, 分位延迟, 核数, MB |
| 用途 | 判断某次代码改动是否拖慢单点 | 判断产品容量与是否回归 |
| 波动 | 小, 适合进 CI | 大, 需要跨机与多次运行 |

微基准的参考值见 [PERFORMANCE.md](../guides/PERFORMANCE.md)。**两者不可互相替代**: 微基准全绿而端到端退化的情况真实存在 (锁竞争、GC 压力在单函数里看不出来)。
