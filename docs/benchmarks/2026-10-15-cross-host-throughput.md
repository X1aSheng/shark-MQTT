# Cross-Host Throughput, 2026-10-15

Two hosts, one broker, one load generator, and the public link between them.
This report answers one question: **when the client is not on the same machine,
what actually caps throughput, and by how much?**

It follows `2026-10-15-server-baseline.md`, which measured the same broker with
the client on the same host. Everything here is a different topology of the same
build, so the two are directly comparable.

## Environment

| | |
| --- | --- |
| Broker host | Aliyun ECS `s2` (47.120.44.128), Ubuntu 26.04.1 LTS, 8 vCPU / 16 GB, 100 Mbps peak |
| Client host | Aliyun ECS `s3` (8.130.190.141), Ubuntu 26.04.1 LTS, 8 vCPU / 16 GB, 100 Mbps peak |
| Go | 1.26.1 |
| Commit | `0d07f19f7689c46c44c916631289bf67c951f4ee` |
| Broker config | `storage_backend: memory`, `metrics_enabled`, `log_level: warn` |
| Client | `tools/loadgen`, built from the same commit |
| Payload | 32 bytes unless stated |

Two facts about the link, both measured, both load-bearing:

- **RTT s3 <-> s2 is 43.2 ms** (ping). The hosts are in different VPCs
  (`172.29/20` and `172.22/20`); private networking does not join them, so
  traffic takes the public path. For comparison, `s1` -> `s2` is 6.7 ms.
- **Usable bandwidth is ~110-118 Mbps**, measured below.

## Method

`tools/loadgen` drives the broker through the project's own `client` package, so
the numbers include the codec, inflight tracking and write queue a real client
exercises. All subscribers subscribe to the same topic, so **fanout equals the
subscriber count**: `-subscribers 20 -rate 10000` offers 200k deliveries/s from
10k publishes/s.

Three rounds:

1. **Defaults** - `write_queue_size` left at 256.
2. **Deep queue** - the same configs with `write_queue_size: 8192`, to test
   whether the drops seen in round 1 were queue depth.
3. **Rate sweep** - fanout 20 offered at 400k, 600k and 800k deliveries/s,
   cross-host and then on the broker host itself, to locate each ceiling.

Broker CPU and `VmHWM` come from a 1 Hz sampler reading `/proc/<pid>/stat` and
`/proc/<pid>/status` on the broker host, so they are never competing with the
load generator for measurement. CPU is window-averaged and expressed in cores.
`-latency` is off wherever the sample count would itself become the load; one
run that left it on is marked and discarded.

## Round 1: defaults, cross-host

| Run | Sub | Pub | QoS | Offered | Sent/s | Delivered/s | Ratio | P50 | Broker CPU | VmHWM |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| A1 | 1 | 4 | 0 | 20k | 19,837 | 19,825 | 99.94% | 50.15 ms | 1.17 | 20.5 MB |
| A2 | 1 | 8 | 0 | 50k | 49,736 | 49,441 | 99.41% | - | 2.47 | 20.4 MB |
| A3 | 1 | 8 | 0 | 100k | 99,488 | 91,837 | 92.31% | - | 3.10 | 21.4 MB |
| B1 | 20 | 8 | 0 | 100k | 4,971 | 99,158 | 19.95x | - | 2.11 | 21.6 MB |
| B2 | 20 | 8 | 0 | 200k | 9,943 | 198,270 | 19.94x | - | 2.58 | 21.6 MB |
| C1 | 1 | 4 | 1 | 5k | 73.8 | 73.8 | 100% | 49.37 ms | 0.08 | 21.6 MB |
| C2 | 1 | 8 | 1 | 10k | 154.4 | 154.5 | 100% | - | 0.05 | 21.6 MB |

Two things stand out, and they are unrelated.

**QoS 0 scales; QoS 1 does not.** C1 offered 5,000 msg/s and delivered 73.8; C2
offered 10,000 and delivered 154.5. Both are ~100% of what was *sent* - the
publishers simply could not send faster. Divide: 4 publishers at 18.4 msg/s each
is 54 ms per publish; 8 publishers at 19.3 msg/s each is 52 ms. That is the RTT.

The cause is in the client, not the wire. `client.Publish` blocks:

```go
// client/client.go:285-301
c.wmu.Lock()
if err := c.codec.Encode(conn, pkt); err != nil { ... }
c.wmu.Unlock()

if qos == 0 {
    return nil
}

select {
case <-ctx.Done():
    return ctx.Err()
case resp := <-respCh:      // blocks until PUBACK
```

One in-flight message per connection. Throughput is therefore
`publishers / RTT`, and on a 43 ms link that is 23 msg/s per connection no
matter what the broker can do. MQTT permits many more in flight; this is a
pipelining gap, not a protocol limit.

**A3 lost 7.7%.** B2 delivered twice as much (198k/s) and lost nothing, so the
loss is not about delivery volume. Every drop in this session carries the same
label:

```
shark_mqtt_messages_dropped_total{reason="write_queue_full"} 152188
```

`broker/pub_sub.go:996-1005` - a QoS 0 packet that finds the subscriber's
outbound channel full is dropped, and that is the only drop reason the broker
emitted all session. The channel is
`make(chan protocol.Packet, qcap)` with `qcap` from `write_queue_size`, default
256 (`broker/conn_lifecycle.go:489`, `broker/options.go:78`).

256 messages is smaller than one bandwidth-delay product here: 95k msg/s over 43
ms is ~4,000 messages in flight. The queue cannot hold the pipe.

## Round 2: the queue was the cause

Same configs, `write_queue_size: 8192`.

| Run | Sub | Offered | Sent/s | Delivered/s | Ratio |
| --- | --- | --- | --- | --- | --- |
| A3 (256) | 1 | 100k | 99,488 | 91,837 | 92.31% |
| D1 (8192) | 1 | 100k | 99,476 | 98,590 | 99.11% |
| B2 (256) | 20 | 200k | 9,943 | 198,270 | 19.94x |
| D2 (8192) | 20 | 200k | 9,947 | 198,417 | 19.95x |

A3 -> D1 is +7.4% throughput and 7.7% -> 0.89% loss, from one config line. D2 is
unchanged because B2 was never dropping - confirming the effect is real and
specific to the saturated single-subscriber path, not a general improvement.

The same-host runs in this round were not usable: E1 ran with `-latency` on at
100k msg/s, and recording 1.66 M samples on the subscriber's read loop slowed the
subscriber enough to become the bottleneck (82,315/s, P50 98 ms on loopback).
Round 3 repeats it with latency off.

## Round 3: where each ceiling is

Fanout 20, 32 B payload, QoS 0, offered at 400k / 600k / 800k deliveries/s.

| Run | Path | Offered | Sent/s | Delivered/s | x fanout | Broker CPU | VmHWM |
| --- | --- | --- | --- | --- | --- | --- | --- |
| F2 | cross-host | 400k | 19,887 | **237,082** | 11.92x | 3.26 | 49.5 MB |
| F3 | cross-host | 600k | 29,833 | **235,849** | 7.91x | 3.46 | 56.1 MB |
| F4 | cross-host | 800k | 39,794 | **236,216** | 5.94x | 3.70 | 59.1 MB |
| G1 | same-host | 100k (1 sub) | 99,993 | 82,167 | 82.2% | 2.93 | 58.5 MB |
| G2 | same-host | 400k | 19,974 | **320,259** | 16.03x | 4.40 | 58.5 MB |
| G3 | same-host | 600k | 29,963 | **338,762** | 11.31x | 4.35 | 58.5 MB |
| G4 | same-host | 800k | 39,928 | **317,570** | 7.95x | 4.37 | 58.5 MB |

Offering 400k, 600k and 800k cross-host produced 237k, 236k and 236k. That flat
line is the signature of a fixed-capacity resource, and the broker is not it -
CPU is 3.7 of 8 cores at F4.

### The ceiling is the link

One run was bracketed with per-second `eth0` counters on the broker host (run
H1, same config as F2, delivered 237,825/s):

```
epoch              eth0 TX bytes
1792077747.652     4,282,751,105
1792077763.238     4,496,566,122      -> 213,815,017 B over 15.59 s
                                      = 13.71 MB/s = 109.7 Mbps
```

Across the wider window the slope is 13.7-14.8 MB/s, i.e. **110-118 Mbps** -
the 100 Mbps peak link, saturated. Dividing the same counters by delivered
messages gives **59.9 bytes on the wire per 32-byte message**, and the packet
counters give **9.8 messages per TX packet**: the virtio NIC's TSO is coalescing
aggressively, so per-message framing overhead is amortised almost away.

Same-host tops out at 338k/s with the broker at 4.4 cores, so the broker is not
CPU-bound there either; the limit is subscriber-side drain.

**Cross-host costs about 30% of peak: 236k/s vs 338k/s.** For QoS 0 fanout that
entire gap is bandwidth, not latency - P50 latency barely enters, because QoS 0
publishing never waits for anything.

## Findings

1. **Cross-host QoS 0 ceiling is ~236k deliveries/s at 32 B, and it is the link.**
   Flat across a 2x offered-load range, at 110-118 Mbps on a 100 Mbps peak link,
   with the broker at 3.7 of 8 cores. Raising offered load past this point
   increases drops, not throughput.
2. **Same-host ceiling is ~338k deliveries/s**, broker at 4.4 of 8 cores. The
   broker has CPU headroom on both paths; the same-host limit is subscriber
   drain, not the broker.
3. **QoS 1 throughput is `publishers / RTT`.** 154 msg/s against 19,825 msg/s for
   QoS 0 on the same link - a 130x gap, because `client.Publish` blocks on PUBACK
   with one message in flight per connection. On a 43 ms link this is the single
   largest throughput risk in the system, and it is client-side.
4. **`write_queue_size` 256 is too small for a high-BDP path.** At fanout 1 and
   100k msg/s cross-host it cost 7.7% of all messages; 8192 recovered it (+7.4%
   throughput, loss to 0.89%). The default is fine for loopback and too small for
   a WAN subscriber.
5. **Memory is flat.** `VmHWM` 20.5 -> 21.6 MB with a 256-slot queue, 58.5 MB with
   8192 slots and 20 subscribers. No growth across a 2x offered-load sweep.
6. **The two-`write` framing costs no wire bytes.** Each MQTT packet is still
   emitted as two socket writes (2-byte fixed header, then body), but TSO
   coalesces ~9.8 messages per segment on the NIC, so the cost is a syscall per
   packet, not 40 extra bytes per packet. Worth fixing for CPU, not for bandwidth.

## Open item: counter reconciliation

At saturation the broker's own counters disagree with the client by ~12%. For
H1: `messages_delivered_total` rose 6,235,414 (399,899/s = 19,892 publishes x
20.1 fanout, so it counts *attempts*), `messages_dropped_total` rose 1,780,754
(114,225/s), leaving 285,674/s - but the client received 237,825/s.

At low load the two agree to 0.17% (run B1: 99,327/s net against 99,158/s
received), so the gap appears only when the queue is saturating. Messages still
queued or in flight when a run ends account for at most ~164k of it (20
connections x 8192 slots); the remaining ~800k is unexplained by any counter the
broker exposes, and `write_queue_full` is the only drop reason it emitted.
Worth a targeted look before these numbers are used as a capacity model.

## Reproducing

```bash
# broker host
write_queue_size: 8192        # or omit for the default-256 comparison
go build -ldflags="-s -w" -o shark ./cmd/

# client host, 20 subscribers all on one topic -> fanout 20
loadgen -addr <broker>:18000 -subscribers 20 -publishers 8 \
        -qos 0 -payload 32 -rate 20000 -duration 20s -latency=false -json

# broker host, per-second wire counters
awk '/eth0:/{print systime(), $10}' /proc/net/dev   # $10 = TX bytes
```

Raw JSON for every run above is in `docs/benchmarks/data/cross-host/`, named
`s3_*` (client on s3) and `s2_*` (client on the broker host).
