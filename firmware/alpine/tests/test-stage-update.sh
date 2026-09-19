#!/bin/sh
set -eu

ROOT=$(mktemp -d)
trap 'rm -rf "$ROOT"' EXIT INT TERM HUP

STAGER=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)/compat/nanokvm-stage-update
DATA=$ROOT/data
BOOT=$ROOT/boot
mkdir -p "$DATA" "$BOOT"

make_bundle() {
	directory=$1
	mkdir -p "$directory"
	printf 'rootfs\n' >"$directory/alpine-rootfs.tar.gz"
	printf 'normal\n' >"$directory/boot-alpine.sd"
	printf 'recovery\n' >"$directory/boot-alpine-recovery.sd"
	(
		cd "$directory"
		sha256sum alpine-rootfs.tar.gz boot-alpine.sd boot-alpine-recovery.sd >SHA256SUMS
	)
}

# A source outside /data is copied and its destination is verified.
outside=$ROOT/outside
make_bundle "$outside"
NANOKVM_DATA_DIR=$DATA NANOKVM_BOOT_DIR=$BOOT "$STAGER" "$outside"
test -d "$outside"
cmp "$outside/alpine-rootfs.tar.gz" "$DATA/nanokvm-update/alpine-rootfs.tar.gz"

# A bundle already on /data is moved, avoiding a second large exFAT write.
rm -rf "$DATA/nanokvm-update"
on_data=$DATA/download
make_bundle "$on_data"
NANOKVM_DATA_DIR=$DATA NANOKVM_BOOT_DIR=$BOOT "$STAGER" "$on_data"
test ! -e "$on_data"
test -f "$DATA/nanokvm-update/alpine-rootfs.tar.gz"

# Activating the published directory must not restage it.
NANOKVM_DATA_DIR=$DATA NANOKVM_BOOT_DIR=$BOOT \
	"$STAGER" --activate "$DATA/nanokvm-update"
cmp "$DATA/nanokvm-update/boot-alpine-recovery.sd" "$BOOT/boot.sd"

# A damaged input must be rejected before replacing the staged bundle.
bad=$ROOT/bad
make_bundle "$bad"
printf 'damage\n' >>"$bad/alpine-rootfs.tar.gz"
if NANOKVM_DATA_DIR=$DATA NANOKVM_BOOT_DIR=$BOOT "$STAGER" "$bad"; then
	echo "damaged bundle unexpectedly accepted" >&2
	exit 1
fi
cmp "$DATA/nanokvm-update/boot-alpine-recovery.sd" "$BOOT/boot.sd"

echo "nanokvm-stage-update tests passed"
