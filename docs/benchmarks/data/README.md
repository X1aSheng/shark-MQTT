# Benchmark Data

Raw results from end-to-end load runs. One file per scenario, written by
`tools/loadgen -json`.

Each file carries a `_meta` block recording the scheduling mode. This matters:
the same configuration produces different numbers under shared and pinned CPU,
so a result without it cannot be compared against another run.

| File | Subscribers | Publishers | QoS | Target msg/s | Payload | Schedule |
| --- | --- | --- | --- | --- | --- | --- |
| `A_qos1_baseline.json` | 4 | 8 | 1 | 20,000 | 256 B | shared |
| `B_qos0_baseline.json` | 4 | 8 | 0 | 20,000 | 256 B | shared |
| `C_qos0_100k.json` | 1 | 8 | 0 | 100,000 | 256 B | shared |
| `D_qos0_200k.json` | 1 | 8 | 0 | 200,000 | 256 B | shared |
| `E_fanout_20sub.json` | 20 | 8 | 1 | 10,000 | 256 B | shared |
| `F_payload_4k.json` | 4 | 8 | 1 | 8,000 | 4,096 B | shared |
| `G_qos2_baseline.json` | 4 | 8 | 2 | 10,000 | 256 B | shared |
| `T1_qos1_20k.json` | 4 | 8 | 1 | 20,000 | 256 B | pinned 4/4 |
| `T2_qos0_100k.json` | 1 | 8 | 0 | 100,000 | 256 B | pinned 4/4 |
| `T3_qos0_300k.json` | 1 | 8 | 0 | 300,000 | 256 B | pinned 4/4 |
| `V1_20sub_20k.json` | 20 | 8 | 0 | 20,000 | 256 B | pinned 4/4 |
| `V2_50sub_10k.json` | 50 | 8 | 0 | 10,000 | 256 B | pinned 4/4 |
| `W1_6core.json` | 20 | 8 | 0 | 20,000 | 256 B | pinned 6/2 |
| `X1_broker4c.json` | 8 | 8 | 0 | 20,000 | 256 B | pinned 4/4 |
| `X2_broker6c.json` | 8 | 8 | 0 | 20,000 | 256 B | pinned 6/2 |
| `C1_tunnel_qos0.json` | 4 | 8 | 0 | 20,000 | 256 B | cross-host, SSH tunnel |
| `C2_tunnel_qos1.json` | 4 | 8 | 1 | 20,000 | 256 B | cross-host, SSH tunnel |
| `D1_direct_qos0.json` | 2 | 4 | 0 | 10,000 | 256 B | cross-host, direct 18000 |
| `P1_sub200.json` | 200 | 10 | 0 | 1,000 | 128 B | same-host, upstream-shaped |
| `P2_sub2000.json` | 2000 | 10 | 0 | 1,000 | 128 B | same-host, upstream-shaped |
| `P3_sub2000_5k.json` | 2000 | 10 | 0 | 5,000 | 128 B | same-host, upstream-shaped |
| `Q1_sub200_5k.json` | 200 | 10 | 0 | 5,000 | 128 B | same-host, upstream-shaped |
| `R1_1sub_100mbit.json` | 1 | 8 | 0 | 20,000 | 256 B | cross-host, 100 Mbps link |
| `R2_8sub_100mbit.json` | 8 | 8 | 0 | 5,000 | 256 B | cross-host, 100 Mbps link |
| `P1_sub200.json` | 200 | 10 | 0 | 1,000 | 128 B | same-host, upstream-shaped |
| `P2_sub2000.json` | 2000 | 10 | 0 | 1,000 | 128 B | same-host, upstream-shaped |
| `P3_sub2000_5k.json` | 2000 | 10 | 0 | 5,000 | 128 B | same-host, upstream-shaped |
| `Q1_sub200_5k.json` | 200 | 10 | 0 | 5,000 | 128 B | same-host, upstream-shaped |

"pinned 4/4" means the broker ran on cores 0-3 and the load generator on 4-7.
"pinned 6/2" means the broker got six cores and the generator two, which turned
out to starve the client and is documented as a negative result rather than a
broker measurement.

The `C*` and `D*` files are cross-host runs over a **5 Mbps** link, which was the
constraint rather than the broker. The `R*` files repeat the exercise over a
**100 Mbps** link between two 8 vCPU hosts: delivery rises 15.5x and the
completion rate goes from 14% to 99.7%, while the broker uses about half a core.
See the baseline document for both.

`R*` files carry extra `_meta` fields recording broker CPU and RSS, which were
sampled on the broker host through `/proc` rather than by the load generator -
the generator cannot see a remote process.

Host: 8 vCPU / 16 GB, Ubuntu 26.04, Go 1.26.1. Commit `d5e37f8f9`.

The `P*` and `Q*` files reproduce the scenario shape upstream smart-mqtt
publishes (128-byte payload, 2000 connections, QoS 0). Their interpretation,
including why the upstream headline figure could not be matched, is in
[`../2026-10-15-upstream-comparison.md`](../2026-10-15-upstream-comparison.md).

The `P*` and `Q*` files reproduce the scenario shape upstream smart-mqtt
publishes (128-byte payload, 2000 connections, QoS 0). Their interpretation,
including why the upstream headline figure could not be matched, is in
[`../2026-10-15-upstream-comparison.md`](../2026-10-15-upstream-comparison.md).

Findings and interpretation are in
[`../2026-10-15-server-baseline.md`](../2026-10-15-server-baseline.md).
