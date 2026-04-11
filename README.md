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

Measured on AMD Ryzen 7 7700 (8C/16T), 64 GB RAM, Ubuntu 24.04, kernel 6.8.0, Firecracker v1.15.0.
Config: 2 vCPUs, 512 MiB RAM.

### Cold Start Baseline (n=50)

| Metric | p50 | p95 |
|---|---|---|
| Cold start | 19.16 ms | 19.56 ms |
| Teardown | 16.70 ms | 27.02 ms |

### Concurrent VM Spawning

<p align="center">
  <img src="docs/concurrent-scaling.svg" alt="Concurrent VM cold-start scaling chart" width="680"/>
</p>

| Concurrency | Cold Start p50 | Cold Start p95 | Wall Time |
|---|---|---|---|
| 5 | 20 ms | 20 ms | 51 ms |
| 10 | 23 ms | 24 ms | 71 ms |
| 20 | 50 ms | 57 ms | 98 ms |
| 50 | 68 ms | 93 ms | 144 ms |
| 100 | 151 ms | 192 ms | 257 ms |
| 200 | 327 ms | 495 ms | 542 ms |

Zero failures up to 200 concurrent VMs. Latency stays flat to c=10, then degrades linearly with CPU oversubscription. Even at c=200 (25x CPU oversubscription), all VMs boot under 500 ms. RAM (~120 VMs at 512 MiB each) is the practical limit for sustained workloads on this machine.
