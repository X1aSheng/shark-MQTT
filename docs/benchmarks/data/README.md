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

"pinned 4/4" means the broker ran on cores 0-3 and the load generator on 4-7.
"pinned 6/2" means the broker got six cores and the generator two, which turned
out to starve the client and is documented as a negative result rather than a
broker measurement.

Host: 8 vCPU / 16 GB, Ubuntu 26.04, Go 1.26.1. Commit `d5e37f8f9`.

Findings and interpretation are in
[`../2026-10-15-server-baseline.md`](../2026-10-15-server-baseline.md).
