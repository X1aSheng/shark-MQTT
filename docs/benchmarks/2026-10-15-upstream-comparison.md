# Comparison with smart-mqtt's Published Figures

This note puts our measured numbers next to the performance figures upstream
smart-mqtt publishes, and states plainly what can and cannot be concluded from
the comparison.

## Where the upstream numbers come from

`pages/src/components/PerformanceSection.astro` and `PerformanceChart.astro` in
the upstream repository, rendered on the project site. The scenario and the test
environment are stated on the page itself:

> 测试环境：8核16G 云服务器 ,  2000 并发连接 ,  128 字节 payload
> 数据来源于 bench-plugin 官方压测插件

Two charts are published, both at 128-byte payload.

**Subscribe throughput** - 2000 subscribers, 10 publishers, 128 topics:

| QoS | smart-mqtt | A competitor | Stated advantage |
| --- | --- | --- | --- |
| 0 | 10,000,000 /s | 8,000,000 /s | +25% |
| 1 | 5,400,000 /s | 4,000,000 /s | +35% |
| 2 | 3,200,000 /s | 2,000,000 /s | +60% |

**Publish throughput** - 2000 publishers, 128 topics:

| QoS | smart-mqtt |
| --- | --- |
| 0 | 2,300,000 /s |
| 1 | 1,000,000 /s |
| 2 | 630,000 /s |

Also stated: average latency P99 under 1 ms, and a 100% delivery success rate.

The benchmark is driven by `plugins/bench-plugin`, which constructs real
`MqttClient` instances against the configured host and port and counts arrivals
in the message callback. So it is a genuine network measurement, not an
in-memory simulation - an important point, because the first guess ("they must
be counting something cheaper") turns out to be wrong.

## What we measured under the same described conditions

The upstream environment (8 vCPU, 16 GB) is the machine already used for our
baseline, so the scenario was reproduced directly: same host count, same
subscriber count, same payload size, same QoS 0, client and broker on one
machine as upstream does.

| Run | Subscribers | Publish rate | Delivered | Broker CPU | Peak RSS |
| --- | --- | --- | --- | --- | --- |
| `P1_sub200` | 200 | 1,000 /s | 199,983 /s | 2.98 cores | 26.9 MB |
| `P2_sub2000` | 2000 | 1,000 /s | 277,113 /s | 4.70 cores | 107.6 MB |
| `P3_sub2000_5k` | 2000 | 5,000 /s | 273,923 /s | 4.69 cores | 110.5 MB |
| `Q1_sub200_5k` | 200 | 5,000 /s | 320,220 /s | 4.39 cores | - |

The headline difference is **10,000,000/s versus roughly 280,000/s**, about 36
times. Before drawing any conclusion from that ratio, three things in the table
itself need explaining.

**Delivery stops scaling with the publish rate.** P2 and P3 differ by 5x in
publish rate and deliver the same 275,000 messages per second. The extra work is
discarded, and the broker says why:

```
shark_mqtt_messages_dropped_total{reason="write_queue_full"} 9682
```

**The broker is not out of CPU.** It holds at 4.4-4.7 cores on an 8-core host in
every run. Half the machine is idle while throughput is capped.

**Fewer subscribers does not help either.** Q1 uses a tenth of P2's subscriber
connections and reaches 320,000/s, only 16% more. So the cap is not "too many
connections to service".

The cap is the sum of what the broker and the load generator can do *together*
on eight shared cores. We established earlier that the same-host pair tops out
around 270,000-285,000 delivered messages per second, and these runs land in the
same place from three different directions.

## Is the 36x gap explained by that?

No. Same-host contention costs us maybe 2x - pinned runs earlier showed the
broker needs 4.7 of the 8 cores to deliver 275,000/s, so removing the client
entirely would not reach 10,000,000.

It is worth checking whether 10,000,000 deliveries/second is mechanically
reachable on this class of machine at all. The arithmetic:

| Quantity | Value |
| --- | --- |
| Deliveries per second | 10,000,000 |
| Cores available | 8 |
| Deliveries per core per second | 1,250,000 |
| Time budget per delivery | 0.8 us |
| Payload plus TCP/IP headers | ~170 B |
| Aggregate wire rate | ~1.7 GB/s |
| Per-core network throughput | ~212 MB/s |

A single MQTT delivery involves at least a read syscall, frame parsing, a topic
tree lookup, a queue write, and a write syscall - on the order of 2-15 us per
message in our measurements, which is why we see 60,000-70,000 per core. Fitting
125 messages into that 0.8 us budget, syscalls included, is roughly 18x better
per core than what we measure. Batched I/O could narrow that, but 18x is a large
gap to attribute to implementation quality alone.

So one of these is true, and we cannot tell which from the published material:

1. The upstream figure counts something that is not an end-to-end delivery -
   enqueue operations, or an intended rather than completed fan-out.
2. The upstream implementation is substantially faster per core than ours, by
   more than an order of magnitude.
3. The published scenario differs from its description in a way that matters -
   for example subscribers may not each receive every message.

The published page states only the headline numbers. There is no raw result, no
reproduction script, and no per-run breakdown, so none of the three can be
confirmed or eliminated from outside.

## What this comparison does and does not support

**It does support:**

- Our own numbers are reproducible. Every figure above has a raw JSON result in
  `docs/benchmarks/data/` and a command in the repro section of the baseline
  document. The upstream figures are a rendering of hard-coded constants in an
  Astro component: `smartMqttValue: 1000` and so on, with no run recorded
  alongside them.
- Our ceiling on this hardware is around 280,000 delivered messages per second,
  established from four directions (fan-out, core pinning, subscriber scaling,
  publish-rate scaling) and internally consistent at 60,000-70,000 per core.
- The failure mode under overload is explicit rather than silent: the broker
  counts what it discards and names the reason.

**It does not support:**

- Any claim that our broker is 36 times slower in general. The comparison
  compares one honest measurement against one unverifiable published constant,
  under scenarios that are described as similar but whose implementations and
  counting rules are not established to be identical.

## What a fair comparison would need

Running both brokers on the same machine, with the same load generator, the same
scenario, and both sides' raw results published. Two things make this awkward
today: the upstream benchmark ships as a Java plugin that runs inside its own
broker process, and its published numbers cannot be regenerated from the
repository without building and running that broker.

The practical version we can do: point `tools/loadgen` at both brokers on one
host, using identical flags, and report both. That requires an upstream binary;
the repository is source-only for the broker module in this checkout.

## Reproducing our half

```bash
# 2000 subscribers, 128-byte payload, QoS 0 - the upstream scenario shape
./loadgen -addr 127.0.0.1:18000 -subscribers 2000 -publishers 10 \
          -qos 0 -payload 128 -rate 1000 -duration 15s -latency=false -json

# Same, with a tenth of the subscribers
./loadgen -addr 127.0.0.1:18000 -subscribers 200 -publishers 10 \
          -qos 0 -payload 128 -rate 5000 -duration 15s -latency=false -json
```

`-latency=false` matters at this scale: recording a latency sample per delivery
puts the load generator itself on the critical path, and the resulting number
would describe the measuring tool rather than the broker.
