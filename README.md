# sandbox-benchmark

Benchmarking tool for Firecracker microVM cold-start and teardown latency. Built to measure sandbox spawning performance for an e2b-like runtime.

## What it measures

- **Cold-start latency** — time from VM creation to guest ready (includes VMM boot + InstanceStart API call)
- **Teardown latency** — time from SIGTERM to process exit
- **Statistical summary** — p50 and p95 across iterations

Results are written as JSON with per-iteration timings, summary stats, and host metadata.

## Requirements

- Linux with KVM (`/dev/kvm`)
- [Firecracker](https://github.com/firecracker-microvm/firecracker) binary
- A guest kernel (`vmlinux`) and root filesystem (`rootfs.ext4`)

## Quick start

```bash
make build-linux

# Copy binary to a Linux host with KVM
make sync REMOTE_HOST=myhost

# Validate the environment
make remote-run REMOTE_HOST=myhost ARGS=doctor

# Run 10 cold-start iterations
make remote-bench REMOTE_HOST=myhost BENCH_ITERS=10
```

Or run directly on a Linux machine:

```bash
make build
./bin/sandboxbench doctor
./bin/sandboxbench bench -config configs/example.json -n 10 -out results.json
```

## Configuration

See [`configs/example.json`](configs/example.json) for all options:

| Field | Default | Description |
|---|---|---|
| `firecracker_bin` | `firecracker` (PATH) | Path to Firecracker binary |
| `kernel_image` | — | Guest kernel path |
| `rootfs_path` | — | Root filesystem path |
| `vcpus` | 1 | vCPU count |
| `mem_mib` | 128 | RAM in MiB |

## Commands

| Command | Description |
|---|---|
| `doctor` | Validate KVM and Firecracker availability |
| `bench` | Run cold-start benchmark iterations |
| `up` | Start a long-running microVM |
| `down` | Stop a running microVM |

## Results

Measured on an AMD Ryzen 7 7700 (8C/16T), 64 GB RAM, Ubuntu 24.04, kernel 6.8.0, Firecracker v1.15.0.

| Metric | p50 | p95 |
|---|---|---|
| Cold start | 19.31 ms | 19.31 ms |
| Teardown | 18.75 ms | 18.75 ms |

Raw timings across 3 iterations (ms):

| Iteration | Cold Start | Teardown |
|---|---|---|
| 1 | 27.43 | 18.75 |
| 2 | 19.31 | 16.65 |
| 3 | 18.98 | 19.00 |

Iteration 1 is slightly higher due to initial system warm-up.
