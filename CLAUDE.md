# CLAUDE.md

## Project overview

Firecracker microVM benchmarking tool. Measures cold-start and teardown latency for Firecracker sandboxes. The end goal is to understand performance characteristics for building an e2b-like product.

## Build & run

```bash
make build            # Build for host OS
make build-linux      # Cross-compile to Linux amd64 (required for Firecracker)
```

Requires Linux with `/dev/kvm` and the `firecracker` binary. Run `sandboxbench doctor` to validate the environment.

```bash
# Validate environment
./bin/sandboxbench doctor

# Run benchmark (default 5 iterations)
./bin/sandboxbench bench -config configs/example.json -n 10

# Start a long-running VM
./bin/sandboxbench up -config configs/example.json

# Stop it
./bin/sandboxbench down
```

## Remote deployment

```bash
make sync REMOTE_HOST=myhost         # Deploy binary + configs
make remote-bench REMOTE_HOST=myhost BENCH_ITERS=20
```

## Code layout

```
cmd/sandboxbench/main.go        CLI entry point (doctor, up, down, bench)
internal/bench/bench.go         Benchmark loop, timing, stats (p50/p95), JSON report
internal/config/config.go       JSON config parsing with defaults
internal/firecracker/
  machine_linux.go              Firecracker SDK integration (Linux only)
  machine_stub.go               Stub for non-Linux dev (returns ErrLinuxOnly)
  options.go                    RunOptions struct and defaults
internal/state/state.go         VM state persistence (PID, socket, VMID)
configs/example.json            Example configuration
scripts/check-env.sh            Environment pre-flight checks
```

## Key dependencies

- `github.com/firecracker-microvm/firecracker-go-sdk` — official Firecracker Go SDK
- `github.com/google/uuid` — unique VM IDs and socket paths
- `github.com/sirupsen/logrus` — logging (SDK requirement)

## Conventions

- Platform-specific code uses build tags (`//go:build linux` / `//go:build !linux`)
- Config merging: JSON config file < CLI flags
- Socket paths auto-generate with UUIDs when left empty
- Benchmarks create a fresh VM per iteration for true cold-start isolation
