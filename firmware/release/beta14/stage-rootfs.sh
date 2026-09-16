#!/bin/bash
# Run as root to mount and own the staged image contents.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)
base=${NANOKVM_BUILD_BASE:?Set the accepted build-input root}
old=${NANOKVM_BETA10_OUTPUT:-$base/releases/beta10-seq23}
out=${NANOKVM_BETA14_OUTPUT:-$base/releases/beta14-seq30}
test "$(id -u)" = 0
test ! -e "$out/rootfs.ext4"
mkdir -p "$out/rootfs-mount" "$out/logs"
! mountpoint -q "$out/rootfs-mount"
test -z "$(ls -A "$out/rootfs-mount")"
cp --reflink=auto --sparse=always "$old/rootfs.ext4" "$out/rootfs.ext4"
mount -o loop "$out/rootfs.ext4" "$out/rootfs-mount"
trap 'umount "$out/rootfs-mount"' EXIT
python3 "$repo/firmware/release/beta14/prepare-rootfs.py" \
  --repo "$repo" --output "$out" \
  --server "$out/server/NanoKVM-Server.stripped" --web "$out/web" \
  --capture "$out/capture/libkvm.so" --libraries "$out/server/dl_lib" \
  --system "$out/system/kvm_system" --updater "$out/server/nkos-update" \
  --edid "$out/edid" --utility "$out/tools/nanokvm_update_edid" \
  --board-stage "$out/board-stage" --boot "$out/boot"
