#!/bin/bash
# Run as root to mount and own the staged image contents.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd)
base=${NANOKVM_BUILD_BASE:?Set the accepted build-input root}
old=${NANOKVM_BETA10_OUTPUT:-$base/releases/beta10-seq23}
out=${NANOKVM_BETA11_OUTPUT:-$base/releases/beta11-seq27}
test "$(id -u)" = 0
test ! -e "$out/rootfs.ext4"
mkdir -p "$out/rootfs-mount" "$out/logs"
! mountpoint -q "$out/rootfs-mount"
test -z "$(ls -A "$out/rootfs-mount")"
cp --reflink=auto --sparse=always "$old/rootfs.ext4" "$out/rootfs.ext4"
mount -o loop "$out/rootfs.ext4" "$out/rootfs-mount"
trap 'umount "$out/rootfs-mount"' EXIT
python3 "$repo/firmware/release/beta11/prepare-rootfs.py" \
  --repo "$repo" --output "$out" \
  --server "$base/wol-no-gadget-8fc52ce1/server/NanoKVM-Server" \
  --web "$out/web" \
  --capture "$base/ironkvm-c01-debug-20260914/capture/libkvm.so" \
  --board-stage "$old/board-stage"
