#!/usr/bin/env bash
set -euo pipefail

SWARMBENCH="${SWARMBENCH:-./bin/swarmbench}"
AGENT_COUNTS="${AGENT_COUNTS:-1 4 8 16 32 64}"
ROUNDS="${ROUNDS:-100}"
KERNEL="${KERNEL:-/opt/fc/vmlinux}"
ROOTFS="${ROOTFS:-/opt/fc/swarm-rootfs.ext4}"
FIRECRACKER="${FIRECRACKER:-/usr/local/bin/firecracker}"
VCPUS="${VCPUS:-1}"
MEM_MIB="${MEM_MIB:-128}"
START_TIMEOUT="${START_TIMEOUT:-20s}"
LABEL_PREFIX="${LABEL_PREFIX:-sweep}"
RESULTS_DIR="${RESULTS_DIR:-results/swarm-sweep-$(date -u +%Y%m%d-%H%M%S)}"
WORK_BASE="${WORK_BASE:-/tmp/swarmbench-sweep}"

if [ ! -x "$SWARMBENCH" ]; then
  echo "missing executable swarmbench: $SWARMBENCH" >&2
  exit 1
fi

mkdir -p "$RESULTS_DIR" "$WORK_BASE"

reports=()
for agents in $AGENT_COUNTS; do
  report="$RESULTS_DIR/swarm-a${agents}-r${ROUNDS}.json"
  work_dir="$WORK_BASE/a${agents}"

  echo
  echo "==> agents=$agents rounds=$ROUNDS"
  rm -rf "$work_dir"

  "$SWARMBENCH" \
    -agents "$agents" \
    -rounds "$ROUNDS" \
    -kernel "$KERNEL" \
    -rootfs "$ROOTFS" \
    -firecracker "$FIRECRACKER" \
    -vcpus "$VCPUS" \
    -mem-mib "$MEM_MIB" \
    -start-timeout "$START_TIMEOUT" \
    -work-dir "$work_dir" \
    -label "${LABEL_PREFIX}-a${agents}" \
    -out "$report" \
    -quiet

  reports+=("$report")
done

echo
echo "==> summary"
"$(dirname "$0")/summarize-swarm-reports.sh" "${reports[@]}"

echo
echo "reports saved under $RESULTS_DIR"
