#!/usr/bin/env bash
set -euo pipefail

BASE_ROOTFS="${BASE_ROOTFS:-/opt/fc/rootfs.ext4}"
TARGET_ROOTFS="${TARGET_ROOTFS:-/opt/fc/swarmslice-rootfs.ext4}"
AGENT_BIN="${AGENT_BIN:-./bin/swarmslice-agent}"

if [ ! -f "$BASE_ROOTFS" ]; then
  echo "missing base rootfs: $BASE_ROOTFS" >&2
  exit 1
fi
if [ ! -f "$AGENT_BIN" ]; then
  echo "missing swarmslice agent binary: $AGENT_BIN" >&2
  exit 1
fi

tmpdir="$(mktemp -d)"
cleanup() {
  if mountpoint -q "$tmpdir/mnt"; then
    sudo umount "$tmpdir/mnt"
  fi
  rm -rf "$tmpdir"
}
trap cleanup EXIT

echo "==> copying $BASE_ROOTFS to $TARGET_ROOTFS"
sudo cp "$BASE_ROOTFS" "$TARGET_ROOTFS"
sudo mkdir -p "$tmpdir/mnt"
sudo mount -o loop "$TARGET_ROOTFS" "$tmpdir/mnt"

echo "==> installing $AGENT_BIN as /sandbox-agent"
sudo cp "$AGENT_BIN" "$tmpdir/mnt/sandbox-agent"
sudo chmod 0755 "$tmpdir/mnt/sandbox-agent"
sudo mkdir -p "$tmpdir/mnt/workspace"
sync

echo "==> swarmslice rootfs ready: $TARGET_ROOTFS"
