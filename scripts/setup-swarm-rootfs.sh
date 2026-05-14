#!/usr/bin/env bash
set -euo pipefail

BASE_ROOTFS="${BASE_ROOTFS:-/opt/fc/rootfs.ext4}"
OUT_ROOTFS="${OUT_ROOTFS:-/opt/fc/swarm-rootfs.ext4}"
AGENT_BIN="${AGENT_BIN:-./bin/swarm-agent}"

if [ ! -f "$AGENT_BIN" ]; then
  echo "missing agent binary: $AGENT_BIN" >&2
  exit 1
fi

echo "==> Creating swarm rootfs"
echo "  base:  $BASE_ROOTFS"
echo "  out:   $OUT_ROOTFS"
echo "  agent: $AGENT_BIN"

sudo cp "$BASE_ROOTFS" "$OUT_ROOTFS"

MNT="$(mktemp -d)"
cleanup() {
  if mountpoint -q "$MNT"; then
    sudo umount "$MNT"
  fi
  rmdir "$MNT"
}
trap cleanup EXIT

sudo mount -o loop "$OUT_ROOTFS" "$MNT"
sudo cp "$AGENT_BIN" "$MNT/sandbox-agent"
sudo chmod 0755 "$MNT/sandbox-agent"
sync

echo "==> Installed /sandbox-agent"
ls -lh "$AGENT_BIN"
sudo ls -lh "$MNT/sandbox-agent"
