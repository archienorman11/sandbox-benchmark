# Benchmark Results

## System Configuration

| Component | Details |
|---|---|
| CPU | AMD Ryzen 7 7700 8-Core (16 threads) |
| Clock | 545 - 5389 MHz |
| Cache | L1d 256 KiB, L1i 256 KiB, L2 8 MiB, L3 32 MiB |
| Memory | 61 GiB DDR |
| OS | Ubuntu 24.04.4 LTS (Noble Numbat) |
| Kernel | 6.8.0-107-generic |
| Firecracker | v1.15.0 |
| Arch | amd64 |

## Cold Start + Teardown (2026-04-11)

**Config:** `configs/example.json` | **Iterations:** 3

| Metric | p50 | p95 |
|---|---|---|
| Cold start | 19.31 ms | 19.31 ms |
| Teardown | 18.75 ms | 18.75 ms |

### Raw timings (ms)

| Iteration | Cold Start | Teardown |
|---|---|---|
| 1 | 27.43 | 18.75 |
| 2 | 19.31 | 16.65 |
| 3 | 18.98 | 19.00 |

Note: Iteration 1 cold start is higher due to initial system warm-up.
