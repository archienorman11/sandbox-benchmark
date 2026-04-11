#!/usr/bin/env bash
set -euo pipefail

ASSET_DIR="${ASSET_DIR:-/opt/fc}"
ARCH="$(uname -m)"

# Resolve the latest Firecracker release to determine CI asset prefix
RELEASE_URL="https://github.com/firecracker-microvm/firecracker/releases"
LATEST_VERSION=$(basename "$(curl -fsSLI -o /dev/null -w %{url_effective} "${RELEASE_URL}/latest")")
CI_VERSION="${LATEST_VERSION%.*}"

echo "==> Firecracker ${LATEST_VERSION} (CI prefix: ${CI_VERSION}, arch: ${ARCH})"
echo "==> Installing guest assets to ${ASSET_DIR}"
sudo mkdir -p "$ASSET_DIR"

# --- Kernel ---
if [ -f "$ASSET_DIR/vmlinux" ]; then
  echo "  Kernel already exists, skipping"
else
  echo "  Finding latest kernel..."
  KERNEL_KEY=$(curl -s "http://spec.ccfc.min.s3.amazonaws.com/?prefix=firecracker-ci/${CI_VERSION}/${ARCH}/vmlinux-&list-type=2" \
    | grep -oP "(?<=<Key>)(firecracker-ci/${CI_VERSION}/${ARCH}/vmlinux-[0-9]+\.[0-9]+\.[0-9]{1,3})(?=</Key>)" \
    | sort -V | tail -1)

  if [ -z "$KERNEL_KEY" ]; then
    echo "ERROR: could not find kernel in S3 bucket"
    exit 1
  fi

  echo "  Downloading kernel: ${KERNEL_KEY}"
  sudo curl -fSL "https://s3.amazonaws.com/spec.ccfc.min/${KERNEL_KEY}" -o "$ASSET_DIR/vmlinux"
fi

# --- Rootfs ---
if [ -f "$ASSET_DIR/rootfs.ext4" ]; then
  echo "  Rootfs already exists, skipping"
else
  echo "  Finding latest Ubuntu rootfs..."
  ROOTFS_KEY=$(curl -s "http://spec.ccfc.min.s3.amazonaws.com/?prefix=firecracker-ci/${CI_VERSION}/${ARCH}/ubuntu-&list-type=2" \
    | grep -oP "(?<=<Key>)(firecracker-ci/${CI_VERSION}/${ARCH}/ubuntu-[0-9]+\.[0-9]+\.squashfs)(?=</Key>)" \
    | sort -V | tail -1)

  if [ -z "$ROOTFS_KEY" ]; then
    echo "ERROR: could not find rootfs in S3 bucket"
    exit 1
  fi

  UBUNTU_VERSION=$(basename "$ROOTFS_KEY" .squashfs | grep -oE '[0-9]+\.[0-9]+')
  echo "  Downloading rootfs: ${ROOTFS_KEY}"
  sudo curl -fSL "https://s3.amazonaws.com/spec.ccfc.min/${ROOTFS_KEY}" -o "$ASSET_DIR/ubuntu-${UBUNTU_VERSION}.squashfs"

  echo "  Converting squashfs to ext4..."
  TMPDIR="$(mktemp -d)"
  trap 'sudo rm -rf "$TMPDIR"' EXIT

  sudo unsquashfs -d "$TMPDIR/squashfs-root" "$ASSET_DIR/ubuntu-${UBUNTU_VERSION}.squashfs"
  sudo truncate -s 1G "$ASSET_DIR/rootfs.ext4"
  sudo mkfs.ext4 -d "$TMPDIR/squashfs-root" -F "$ASSET_DIR/rootfs.ext4"
  sudo rm -f "$ASSET_DIR/ubuntu-${UBUNTU_VERSION}.squashfs"
fi

echo
echo "==> Assets ready:"
ls -lh "$ASSET_DIR/vmlinux" "$ASSET_DIR/rootfs.ext4"
echo
echo "Config should have:"
echo "  \"kernel_image\": \"$ASSET_DIR/vmlinux\""
echo "  \"rootfs_path\":  \"$ASSET_DIR/rootfs.ext4\""
