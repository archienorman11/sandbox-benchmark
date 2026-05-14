# Experimental vsock swarm prototype

This branch is an experimental extension on top of Ayush's
`ayush6624/sandbox-benchmark` Firecracker benchmarking repo. The original repo
measures Firecracker cold-start, teardown, snapshot, and concurrent spawn
latency. This prototype explores whether tiny sandboxed agents can coordinate
through a host-side coordinator with low communication overhead.

The prototype is intentionally small:

- `cmd/swarm-agent` runs inside each Firecracker guest as `init=/sandbox-agent`.
- `cmd/swarmbench` starts multiple microVMs, waits for agents to connect over
  Firecracker vsock, routes simple role messages, fans worker tasks out in
  parallel, and prints startup/message latency in the terminal.
- `cmd/swarmsnapbench` snapshots an agent after it has reached a reconnect-ready
  state, then measures restore-to-agent-ready latency.
- `scripts/setup-swarm-rootfs.sh` copies the agent binary into a rootfs for
  local experiments.

Current transport is newline-delimited JSON over vsock. That keeps the first
experiment easy to inspect before adding gRPC or protobuf framing.

Example host run after creating `/opt/fc/swarm-rootfs.ext4`:

```bash
./bin/swarmbench \
  -agents 4 \
  -rounds 10 \
  -label local-swarm \
  -results-dir results \
  -kernel /opt/fc/vmlinux \
  -rootfs /opt/fc/swarm-rootfs.ext4 \
  -firecracker /usr/local/bin/firecracker
```

On the initial Hetzner test host, agent-to-coordinator message round trips were
sub-millisecond once agents were ready. Startup-to-agent-ready was much slower
than raw Firecracker start time, which makes snapshots or a smaller guest image
the next latency target.

Each run writes a JSON report with startup samples, per-round message samples,
summary p50/p95/max timings, and basic host metadata. Use `-out` for an exact
report path or `-results-dir` for timestamped reports.

For a simple scaling sweep:

```bash
AGENT_COUNTS="1 4 8 16 32 64" ROUNDS=100 \
  ./scripts/run-swarm-sweep.sh
```

The sweep writes one report per agent count and prints a summary table. Existing
reports can be summarized directly with:

```bash
./scripts/summarize-swarm-reports.sh results/swarm-sweep-*/swarm-a*.json
```

For the snapshot experiment:

```bash
./bin/swarmsnapbench \
  -iterations 50 \
  -kernel /opt/fc/vmlinux \
  -rootfs /opt/fc/swarm-rootfs.ext4 \
  -firecracker /usr/local/bin/firecracker
```

The guest agent supports a `prepare_snapshot` control frame. On receipt, it
acknowledges readiness, closes its current vsock connection, and enters its
reconnect loop. `swarmsnapbench` snapshots that state so restored VMs reconnect
to the host listener instead of resuming a stale vsock connection.

Initial Hetzner result:

```text
cold agent-ready:        655.137 ms
snapshot create:         103.700 ms
restore start p50/p95:    13.438 / 14.405 ms
restore ready p50/p95:    18.450 / 19.823 ms
post-restore ping p50/p95: 0.373 / 0.668 ms
```
