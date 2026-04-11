#!/usr/bin/env sh
set -eu

ok=true
if [ ! -c /dev/kvm ]; then
  echo "/dev/kvm: missing or not a character device"
  ok=false
else
  echo "/dev/kvm: ok"
fi

if command -v firecracker >/dev/null 2>&1; then
  firecracker --version 2>/dev/null || firecracker --help >/dev/null 2>&1 || echo "firecracker: present (version parse skipped)"
else
  echo "firecracker: not found in PATH"
  ok=false
fi

if [ "$ok" = false ]; then
  exit 1
fi
