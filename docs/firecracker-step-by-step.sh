#!/usr/bin/env bash
# =============================================================================
# Firecracker Step-by-Step — What the SDK Does Under the Hood
# =============================================================================
#
# This script walks through every API call needed to boot a Firecracker microVM,
# interact with it, and tear it down. Run each block manually to understand
# what's happening.
#
# Prerequisites:
#   - Linux with /dev/kvm
#   - firecracker binary in PATH
#   - Guest kernel at /opt/fc/vmlinux
#   - Root filesystem at /opt/fc/rootfs.ext4
#
# Usage: Read and run blocks one at a time. Don't execute the whole file.
# =============================================================================

set -euo pipefail

# --- Configuration -----------------------------------------------------------

SOCKET="/tmp/fc-demo.sock"
KERNEL="/opt/fc/vmlinux"
ROOTFS="/opt/fc/rootfs.ext4"
LOGFILE="/tmp/fc-demo.log"

# Helper: all Firecracker API calls go through this Unix socket
FC_API="curl -s --unix-socket $SOCKET http://localhost"

# =============================================================================
# STEP 1: Start the Firecracker process
# =============================================================================
# This just starts the VMM process. No VM exists yet — it's an HTTP server
# waiting for configuration via the API socket.
#
# Think of it like starting Docker daemon — nothing is running until you
# tell it what to do.

rm -f "$SOCKET"
firecracker --api-sock "$SOCKET" --no-seccomp &
FC_PID=$!
echo "Firecracker PID: $FC_PID"
sleep 0.5  # wait for socket to appear

# Verify the socket is alive:
$FC_API/ 2>/dev/null && echo "API socket ready"

# =============================================================================
# STEP 2: Configure the machine (vCPUs + memory)
# =============================================================================
# PUT /machine-config — tells Firecracker how many vCPUs and how much RAM
# the guest VM should have.
#
# This is just metadata at this point. No resources are allocated yet.

$FC_API/machine-config -X PUT -d '{
  "vcpu_count": 2,
  "mem_size_mib": 512
}'
echo "Machine config set: 2 vCPUs, 512 MiB"

# =============================================================================
# STEP 3: Set the boot source (kernel + boot args)
# =============================================================================
# PUT /boot-source — points Firecracker at the kernel binary and provides
# the kernel command line arguments.
#
# The kernel args control how Linux boots inside the guest:
#   console=ttyS0   → serial console (how we see guest output)
#   reboot=k        → use keyboard controller for reboot (Firecracker's reset)
#   panic=1         → reboot 1 second after kernel panic
#   pci=off         → skip PCI bus scan (Firecracker has no PCI)
#   root=/dev/vda   → mount the first virtio block device as root
#   rw              → mount it read-write

$FC_API/boot-source -X PUT -d "{
  \"kernel_image_path\": \"$KERNEL\",
  \"boot_args\": \"console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw\"
}"
echo "Boot source set: $KERNEL"

# =============================================================================
# STEP 4: Attach the root filesystem
# =============================================================================
# PUT /drives/{drive_id} — attaches a host file as a virtio block device
# in the guest.
#
# The guest will see this as /dev/vda. The kernel mounts it as / because
# of the "root=/dev/vda" boot arg.
#
# is_root_device: true  → this is the boot disk
# is_read_only: false   → guest can write to it (writes go to the host file)

$FC_API/drives/rootfs -X PUT -d "{
  \"drive_id\": \"rootfs\",
  \"path_on_host\": \"$ROOTFS\",
  \"is_root_device\": true,
  \"is_read_only\": false
}"
echo "Rootfs attached: $ROOTFS"

# =============================================================================
# STEP 5: (Optional) Configure logging
# =============================================================================
# PUT /logger — tells Firecracker where to write its own logs.
# Not guest logs — these are VMM-level logs (device emulation, API, etc.)

$FC_API/logger -X PUT -d "{
  \"log_path\": \"$LOGFILE\",
  \"level\": \"Info\",
  \"show_level\": true,
  \"show_log_origin\": true
}"
echo "Logger configured: $LOGFILE"

# =============================================================================
# STEP 6: Boot the VM
# =============================================================================
# PUT /actions {"action_type": "InstanceStart"} — THIS is where the magic
# happens. Everything before this was just configuration.
#
# When Firecracker receives InstanceStart, it:
#   1. Allocates guest memory (512 MiB in our case)
#   2. Creates the KVM VM (ioctl KVM_CREATE_VM)
#   3. Creates vCPUs (ioctl KVM_CREATE_VCPU)
#   4. Loads vmlinux into guest memory
#   5. Places boot args in guest memory
#   6. Sets vCPU registers to kernel entry point
#   7. Calls KVM_RUN — guest starts executing
#
# The guest kernel boots, mounts /dev/vda, runs init (systemd), and
# eventually presents a login prompt on the serial console.

echo ""
echo "=== Booting VM... ==="
echo "(Guest output will appear below — this is the serial console)"
echo ""

$FC_API/actions -X PUT -d '{
  "action_type": "InstanceStart"
}'

# At this point the VM is running. The Firecracker process (backgrounded
# in step 1) is handling the guest's I/O.
#
# If you ran this interactively, you'd see Linux boot messages in the
# terminal where Firecracker is running (because we set console=ttyS0
# and Firecracker's stdout is our terminal).

# =============================================================================
# STEP 7: Inspect the running VM
# =============================================================================
# You can query the VM state while it's running:

echo ""
echo "=== VM Info ==="
echo "Machine config:"
$FC_API/machine-config | python3 -m json.tool 2>/dev/null || $FC_API/machine-config

echo ""
echo "VM state:"
$FC_API/vm | python3 -m json.tool 2>/dev/null || $FC_API/vm

# =============================================================================
# STEP 8: Stop the VM
# =============================================================================
# Option A: Graceful shutdown (sends CtrlAltDel to guest → guest shuts down)
#
# $FC_API/actions -X PUT -d '{"action_type": "SendCtrlAltDel"}'
# sleep 5  # wait for guest to shut down
#
# Option B: Force kill (what our benchmark does — SIGTERM the process)
#
# This is what StopForce() in our code does. It's faster because we don't
# wait for the guest OS to gracefully shut down.

echo ""
echo "=== Stopping VM ==="
kill $FC_PID 2>/dev/null || true
wait $FC_PID 2>/dev/null || true
rm -f "$SOCKET"
echo "VM stopped (PID $FC_PID killed)"

# =============================================================================
# STEP 9: Verify cleanup
# =============================================================================

echo ""
echo "=== Cleanup ==="
[ -e "$SOCKET" ] && echo "WARNING: socket still exists" || echo "Socket cleaned up"
ps -p $FC_PID &>/dev/null && echo "WARNING: process still running" || echo "Process cleaned up"
echo ""
echo "Done. The VM lived and died."

# =============================================================================
# WHAT OUR BENCHMARK MEASURES
# =============================================================================
#
# Our bench tool repeats this cycle N times and measures:
#
#   cold_start_ms = time(Step 1 through Step 6)
#     → spawn process + all API calls + InstanceStart
#     → This is ~19-20ms on your machine
#
#   teardown_ms = time(Step 8 + wait for exit)
#     → SIGTERM + process cleanup
#     → This is ~18-19ms on your machine
#
# The SDK (firecracker-go-sdk) that our Go code uses does exactly these
# curl calls — it just does them programmatically over the Unix socket.
#
# =============================================================================
# WHAT'S NEXT: SNAPSHOTS
# =============================================================================
#
# Instead of steps 1-6 every time, you can:
#
#   # After step 6, once the guest is fully booted:
#   curl --unix-socket $SOCKET http://localhost/vm -X PATCH \
#     -d '{"state": "Paused"}'
#
#   curl --unix-socket $SOCKET http://localhost/snapshot/create -X PUT \
#     -d '{
#       "snapshot_type": "Full",
#       "snapshot_path": "/tmp/fc-snapshot",
#       "mem_file_path": "/tmp/fc-memory"
#     }'
#
#   # Then for every new sandbox:
#   firecracker --restore-from-snapshot /tmp/fc-snapshot \
#     --api-sock /tmp/fc-restored.sock
#
# This skips the entire boot sequence. The guest resumes exactly where
# it was when you took the snapshot.
