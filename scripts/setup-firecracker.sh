#!/usr/bin/env bash
set -euo pipefail

FC_VERSION="${FC_VERSION:-v1.15.0}"
ARCH="$(uname -m)"
INSTALL_DIR="/usr/local/bin"

echo "==> Installing Firecracker ${FC_VERSION} (${ARCH})"

RELEASE_URL="https://github.com/firecracker-microvm/firecracker/releases/download/${FC_VERSION}/firecracker-${FC_VERSION}-${ARCH}.tgz"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

echo "==> Downloading ${RELEASE_URL}"
curl -fSL "$RELEASE_URL" -o "$TMPDIR/firecracker.tgz"

echo "==> Extracting"
tar -xzf "$TMPDIR/firecracker.tgz" -C "$TMPDIR"

# The tarball extracts to release-<version>-<arch>/
RELEASE_DIR="$TMPDIR/release-${FC_VERSION}-${ARCH}"

echo "==> Installing to ${INSTALL_DIR}"
sudo cp "$RELEASE_DIR/firecracker-${FC_VERSION}-${ARCH}" "$INSTALL_DIR/firecracker"
sudo cp "$RELEASE_DIR/jailer-${FC_VERSION}-${ARCH}" "$INSTALL_DIR/jailer"
sudo chmod +x "$INSTALL_DIR/firecracker" "$INSTALL_DIR/jailer"

echo "==> Installed:"
firecracker --version
echo "Done."
