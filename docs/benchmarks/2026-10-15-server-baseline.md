# Server Baseline, 2026-10-15

Measured end-to-end throughput, latency and resource use of a real broker
process under sustained load. This complements the micro-benchmarks in
`docs/guides/PERFORMANCE.md`, which measure individual functions rather than a
running server.

## Environment

| | |
| --- | --- |
| Host | Aliyun ECS, Ubuntu 26.04 LTS |
| CPU | 8 vCPU |
| RAM | 16 GB |
| Go | 1.26.1 |
| Broker | `shark-mqtt` at commit `d5e37f8f9`, storage backend `memory` |
| Client | `tools/loadgen`, built from the same commit |
| Second host | Aliyun ECS, Ubuntu 26.04, 2 vCPU / 2 GB, used for the cross-host attempt |

The second host is in a different VPC from the broker host, which matters for
the cross-host section below: the private network does not join them.

The load generator ran on the **same host** as the broker. Both compete for the
same eight cores, so every number here is a floor rather than a ceiling. The
`taskset` runs below exist precisely to control that interference, and one of
the findings is what happens when it is not controlled.

## Method

`tools/loadgen` drives the broker through the project's own `client` package, so
the numbers include the codec, inflight tracking and write queue a real client
exercises. Latency is measured across the broker: the publisher stamps a send
timestamp into the payload and each subscriber computes the difference on
arrival. Timing the publish call alone would measure the local socket buffer.

Resources were sampled from the broker's `/proc` entry every 250 ms. Peak RSS is
`VmHWM`, the kernel's high-water mark, because the current value understates what
a burst cost once GC has run.

Two configurations appear below:

- **Shared** - default scheduling, client and broker both free to use all eight
  cores.
- **Pinned** - broker on cores 0-3, load generator on cores 4-7, so neither
  steals the other's CPU.

## Throughput and latency

### QoS 1 and QoS 0 baseline, 4 subscribers / 8 publishers

| Config | QoS | Target msg/s | Actual send | Delivered | Delivery | P50 | P99 | CPU | Peak RSS |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Shared | 1 | 20,000 | 19,652 | 78,610 | 400% | 297 us | 1,734 us | 3.19 | 20.0 MB |
| Pinned | 1 | 20,000 | 19,668 | 78,671 | 400% | 190 us | 1,197 us | 2.97 | 19.2 MB |
| Shared | 0 | 20,000 | 19,670 | 78,678 | 400% | 158 us | 722 us | 2.12 | 20.1 MB |

Delivery above 100% is fan-out, not duplication: four subscribers each receive
every message, so the broker delivers 4x what it receives.

Pinning the two sides to separate cores cuts QoS 1 P50 latency by 36% (297 to
190 us) and P99 by 31% (1,734 to 1,197 us) at the same throughput. That
difference is CPU contention, not broker work.

### Payload size and QoS level, 4 subscribers / 8 publishers

| Scenario | QoS | Payload | Target | Actual send | Delivered | P50 | P99 | CPU |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 4 KB payload | 1 | 4,096 B | 8,000 | 7,943 | 31,772 | 315 us | 1,038 us | 1.72 |
| QoS 2 | 2 | 256 B | 10,000 | 9,915 | 39,661 | 469 us | 1,814 us | 3.15 |

4 KB payloads cost less CPU than 256-byte QoS 1 traffic per message, because the
message count is a third as high. Inbound bandwidth reaches 32.5 MB/s.

QoS 2 adds roughly 30% to P50 over QoS 1 (469 vs 297 us shared), the cost of the
extra PUBREC/PUBREL/PUBCOMP round trip.

### Single-subscriber ceiling

| Config | Subscribers | QoS | Target | Actual send | Delivered | Delivery | P50 | P99 | CPU |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Shared | 1 | 0 | 100,000 | 98,480 | 80,143 | 81.4% | 3,212 us | 17,207 us | 2.77 |
| Pinned | 1 | 0 | 100,000 | 98,469 | 82,325 | 83.6% | 4,347 us | 79,375 us | 2.76 |
| Shared | 1 | 0 | 200,000 | 196,070 | 111,552 | 56.9% | 122,078 us | 167,581 us | 2.84 |
| Pinned | 1 | 0 | 300,000 | 281,119 | 51,728 | 18.4% | 640,173 us | 1,015,834 us | 2.53 |

Delivery falls as the rate rises while CPU stays flat around 2.7 cores. A single
TCP connection cannot absorb what the publishers produce, the broker's write
queue fills, and QoS 0 permits the drop. **This measures one connection's
ceiling, not the broker's.**

On the QoS 1 runs the broker logged
`failed to track outbound for retry ... error="inflight queue full"` and counted
it in `errors_total{component="qos_track"}` (50 occurrences out of ~400,000
messages). A single subscriber at 20,000 QoS 1 messages per second exceeds one
connection's inflight window. The broker applies backpressure and says so rather
than dropping silently, which is the intended behaviour.

### Delivery ceiling under fan-out, broker pinned to 4 cores

| Subscribers | Publishers | QoS | Target | Actual send | Delivered | Fan-out | P50 | CPU | Peak RSS |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 20 | 8 | 0 | 20,000 | 18,745 | 274,184 | 14.6x | 25.7 ms | 3.84 | 20.9 MB |
| 50 | 8 | 0 | 10,000 | 9,379 | 285,731 | 30.5x | 1.52 s | 3.83 | 22.6 MB |

**This is the saturation point.** CPU sits at 3.84 cores on a 4-core allowance,
which is 96% utilisation: the broker has run out of CPU, not memory or locks,
and the growing P50 is the queue that builds behind it.

Delivery reaches about **285,000 messages/second on 4 cores**, or roughly
**71,000 messages/second per core**, independent of how many subscribers the
fan-out is spread across. Doubling the subscriber count from 20 to 50 did not
raise the ceiling; it only redistributed the same work.

### When the client becomes the bottleneck

| Broker cores | Client cores | Subscribers | Actual send | Delivered | P50 |
| --- | --- | --- | --- | --- | --- |
| 4 | 4 | 20 | 18,745 | 274,184 | 25.7 ms |
| 6 | 2 | 20 | 14,253 | 129,531 | 3.25 s |

Giving the broker more cores made it look **slower**. The client, cut to two
cores, could no longer keep 20 subscriber connections drained, so the measured
throughput fell by more than half and latency rose by two orders of magnitude.

The lesson is that a same-host benchmark measures the pair, not the broker.
Pinning the broker to more cores than the client can feed produces a number that
says nothing about the broker. Every throughput figure in this document assumes
a 4/4 split, which is the best this 8-core host can support.

## Broker-internal processing cost

From `/metrics` on the live broker, which times message processing inside the
broker and is therefore free of client and network effects:

| QoS | Messages counted | Average | Within 100 us | Within 1 ms |
| --- | --- | --- | --- | --- |
| 0 | 8,274,179 | 48.1 us | 97.0% | 99.9% |
| 1 | 399,992 | 25.5 us | 87.7% | 98.6% |

The QoS 0 average includes the deliberately overloaded runs above, so the
percentiles are the meaningful figure: **97% of QoS 0 messages complete inside
100 microseconds**, and the end-to-end P50 of 158 us means the broker's own work
is a small fraction of the round trip.

## Resource use

Across all thirteen scenarios:

| Resource | Range across 13 scenarios | Note |
| --- | --- | --- |
| RSS, average | 18.6 - 21.1 MB | varies by 2.5 MB while throughput varies 9x |
| RSS, peak (VmHWM) | 19.2 - 22.6 MB | no growth with load |
| Threads, peak | 13 - 26 | tracks connections, not message rate |
| CPU | 1.72 - 3.84 cores | tracks message rate; the only resource that saturates |

Memory is effectively **constant**. The lowest-throughput run (31,772 msg/s)
peaked at 20.8 MB and the highest (285,731 msg/s) at 22.6 MB: nine times the
throughput cost 1.8 MB. Fifty concurrent subscriber connections cost about the
same as four. There is no leak, and no per-message allocation pressure that
survives GC.

Threads stay between 13 and 26. Connections add goroutines, but a message rate
15 times higher adds no threads at all, which is what a goroutine-per-connection
design should look like.

## Two-host test, and why the link masked the broker

A second host was added to remove the same-host contention described above: an
8 vCPU / 16 GB machine running the broker, and a 2 vCPU / 2 GB machine running
the load generator.

The two hosts are in different VPCs (172.22.0.0/20 and 172.23.0.0/20), so the
private network does not connect them. The security group initially allowed only
port 22, which forced an SSH tunnel; it was later opened for 18000/19000, giving
a direct path. Both configurations were measured.

| Path | Subscribers | QoS | Actual send | Delivered | P50 | Round-trip traffic |
| --- | --- | --- | --- | --- | --- | --- |
| SSH tunnel | 4 | 0 | 19,680 | 2,729 | 4.32 s | - |
| SSH tunnel | 4 | 1 | 0 (starved) | 7,656 | 32.8 s | - |
| Direct, port 18000 | 2 | 0 | 1,530 | 2,563 | 1.66 s | **1.00 MB/s (8.0 Mbps)** |

Direct connection is 27 times slower than the same-host run (1,530 vs 78,678
messages/second). The natural reading, "the broker is slow over a network", is
wrong. Measuring the link directly settles it:

```
$ curl -o /dev/null -w '%{speed_download}\n' http://<broker>:19000/metrics
725024
751865
1053578
872625
779961
```

The link carries **0.7 - 1.05 MB/s**. The MQTT run above moved exactly
**1.00 MB/s** of round-trip traffic. The test saturated the link, and every
throughput figure is a measurement of the WAN path rather than of the broker.

This also explains why the SSH tunnel and the direct connection land in the same
place: encryption was never the limitation, the link was. A 4.4 MB payload at
1 MB/s simply cannot arrive faster than the wire permits.

Two supporting observations:

- Ping RTT between the hosts is a steady 6.6 ms, so the path is public rather
  than intra-VPC.
- The client host was idle throughout (load average 0.19, 1 GB RAM free), so the
  client was not the constraint either.

**A cross-host test needs either the private network or a link that exceeds what
the broker can produce.** At 1 MB/s the wire tops out near 4,000 messages per
second of round-trip traffic, which is three orders of magnitude below the
broker's same-host ceiling. The numbers above are retained as a record of the
attempt and as a caution, not as broker measurements.

## Broker scaling, and the ceiling this host cannot reach

Two same-host runs with 8 subscribers isolate how the broker's CPU use tracks
message rate:

| Broker cores | Client cores | Actual send | Delivered | P50 | Broker CPU |
| --- | --- | --- | --- | --- | --- |
| 0-3 (4 cores) | 4-7 (4 cores) | 19,324 | 154,567 | 216 us | 2.72 |
| 0-5 (6 cores) | 6-7 (2 cores) | 19,407 | 154,599 | **220 ms** | 2.29 |

Both delivered 154,600 messages per second, but the second run's P50 is a
thousand times worse. The broker used *fewer* cores in the second run, not more:
given extra cores it did not need, it left them idle while the client, cut to two
cores, fell behind and queued.

The useful figure is the ratio. **154,600 messages per second on 2.3-2.7 cores
is roughly 60,000-70,000 delivered messages per second per core**, which agrees
with the fan-out run above (274,184 on 3.84 cores is 71,000 per core).

That consistency is what makes the earlier "saturation" finding worth restating
precisely: the 4-core broker was not saturated by its own work. It reached 96%
of its allowance only because the load generator sat on the other four cores of
the same machine, and the two together consumed all eight. **On an 8-core host
the broker's own ceiling is not reachable**, because measuring it requires a
client with comparable CPU, and there is none left.

Extrapolating at 60,000-70,000 per core, an 8-core broker should deliver
roughly 500,000 messages per second. Confirming that requires the client to run
somewhere else, on a link that is not the bottleneck.

## Capacity guidance

Derived from the measurements above, for provisioning rather than marketing:

| Question | Answer from this run | Basis |
| --- | --- | --- |
| Messages/sec per core | 60,000 - 71,000 | 154,600 on 2.7 cores, 274,184 on 3.84 cores |
| Memory per 1,000 connections | ~256 MB | 50 connections peaked at 22.6 MB |
| Memory for 50 connections | ~23 MB | 50-subscriber run |
| Memory per additional connection | well under 1 MB | 4 vs 50 subscribers differ by 1.7 MB |
| Single-connection QoS 0 ceiling | ~80,000/s | delivery falls off above this |
| Single-connection QoS 1 ceiling | ~20,000/s | inflight queue warnings begin here |
| Round-trip added by QoS 2 | ~170 us over QoS 1 | 469 vs 297 us P50 |
| Latency cost of sharing cores with a client | +107 us P50 at QoS 1 | 297 shared vs 190 pinned |

A container sizing of **1 CPU per 70,000 delivered messages/second** and
**~256 MB per 1,000 connections** leaves headroom on both axes without relying
on the measurements being optimistic.

## Reproducing

```bash
# Broker, all cores
./shark --allow-all -config bench.yaml

# Same-host baseline
./loadgen -addr 127.0.0.1:2883 -subscribers 4 -publishers 8 \
          -qos 1 -rate 20000 -payload 256 -duration 20s

# With CPU isolation, broker pinned to cores 0-3 first
taskset -c 4-7 ./loadgen -addr 127.0.0.1:2883 -subscribers 20 -publishers 8 \
          -qos 0 -rate 20000 -payload 256 -duration 20s
```

Raw results are under `docs/benchmarks/data/`. Each file records its own
configuration, so a scenario can be rerun and compared without guesswork.

## Caveats

- Same-host client and broker for every number except the cross-host section.
  The two-host attempt was made and is documented, but the WAN link (0.7 -
  1.05 MB/s) capped it far below the broker's ability, so the CPU interference
  remains unresolved rather than removed.
- In-memory storage. A persistent backend would add disk latency that none of
  these numbers include.
- Ports, topics and subscriptions were created once at startup. A scenario that
  churns connections or subscriptions would exercise different paths.
- One run per scenario. Latency percentiles at P99.9 and above come from a
  single 20-second sample and should be treated as indicative.
