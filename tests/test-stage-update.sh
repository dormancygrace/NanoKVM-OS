#!/bin/sh
set -eu

ROOT=$(mktemp -d)
# Keep the status: under set -e a failing check must fail the test.
trap 'status=$?; rm -rf "$ROOT"; exit "$status"' EXIT INT TERM HUP

STAGER=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)/firmware/alpine/compat/nanokvm-stage-update
DATA=$ROOT/data
BOOT=$ROOT/boot
mkdir -p "$DATA" "$BOOT"
# The device recovery runs on: p1 vfat on the boot directory, p3 exFAT on
# the data directory, and the partition layout recovery/init checks.
SYS=$ROOT/sys
for field in mmcblk0p2/start:131073 mmcblk0p2/size:1572864 mmcblk0p3/start:1705984; do
	mkdir -p "$SYS/${field%%/*}"
	printf '%s\n' "${field#*:}" >"$SYS/${field%%:*}"
done
printf '/dev/mmcblk0p1 %s vfat rw 0 0\n/dev/mmcblk0p3 %s exfat rw 0 0\n' "$BOOT" "$DATA" >"$ROOT/mounts"
export NANOKVM_PROC_MOUNTS="$ROOT/mounts" NANOKVM_SYS_BLOCK="$SYS"
printf 'running image\n' >"$BOOT/boot.sd"

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

# Activating the published directory must not restage it. What an earlier,
# interrupted recovery recorded there is not resumed by the new one.
printf 'stale\n' >"$DATA/nanokvm-update/recovery-formatting"
printf 'root:OLD:1::::::\n' >"$DATA/nanokvm-update/root-shadow.saved"
NANOKVM_DATA_DIR=$DATA NANOKVM_BOOT_DIR=$BOOT \
	"$STAGER" --activate "$DATA/nanokvm-update"
cmp "$DATA/nanokvm-update/boot-alpine-recovery.sd" "$BOOT/boot.sd"
test ! -e "$DATA/nanokvm-update/recovery-formatting"
test ! -e "$DATA/nanokvm-update/root-shadow.saved"
# The image that ran is kept for recovery to go back to, with its hash.
test "$(cat "$BOOT/boot.before-recovery")" = "running image"
test "$(cat "$BOOT/boot.before-recovery.sha256")" = "$(sha256sum "$BOOT/boot.before-recovery" | cut -d ' ' -f 1)"
test "$(cat "$BOOT/boot.before-recovery.for")" = "$(sha256sum "$BOOT/boot.sd" | cut -d ' ' -f 1)"
# Activating again keeps that image, not the recovery FIT now in boot.sd.
NANOKVM_DATA_DIR=$DATA NANOKVM_BOOT_DIR=$BOOT \
	"$STAGER" --activate "$DATA/nanokvm-update"
test "$(cat "$BOOT/boot.before-recovery")" = "running image"
# A recovery installed its image but its cleanup was cut short: the copy
# left behind is older than boot.sd, and the next activation keeps boot.sd.
printf 'installed image\n' >"$BOOT/boot.sd"
NANOKVM_DATA_DIR=$DATA NANOKVM_BOOT_DIR=$BOOT \
	"$STAGER" --activate "$DATA/nanokvm-update"
test "$(cat "$BOOT/boot.before-recovery")" = "installed image"
test "$(cat "$BOOT/boot.before-recovery.sha256")" = "$(sha256sum "$BOOT/boot.before-recovery" | cut -d ' ' -f 1)"
# Without .for (cut before it was written) a copy is never trusted either.
printf 'next image\n' >"$BOOT/boot.sd"
rm "$BOOT/boot.before-recovery.for"
NANOKVM_DATA_DIR=$DATA NANOKVM_BOOT_DIR=$BOOT \
	"$STAGER" --activate "$DATA/nanokvm-update"
test "$(cat "$BOOT/boot.before-recovery")" = "next image"

# Recovery B activated over an activated recovery A, cut before or after
# boot.sd changes: the image that ran before A stays the one to go back to.
rm -f "$BOOT"/boot.before-recovery*
printf 'normal image\n' >"$BOOT/boot.sd"
NANOKVM_DATA_DIR=$DATA NANOKVM_BOOT_DIR=$BOOT "$STAGER" --activate "$DATA/nanokvm-update"
recovery_a=$(sha256sum "$BOOT/boot.sd" | cut -d ' ' -f 1)
other=$ROOT/other
make_bundle "$other"
printf 'recovery B\n' >"$other/boot-alpine-recovery.sd"
(cd "$other" && sha256sum alpine-rootfs.tar.gz boot-alpine.sd boot-alpine-recovery.sd >SHA256SUMS)
NANOKVM_DATA_DIR=$DATA NANOKVM_BOOT_DIR=$BOOT "$STAGER" "$other"
shim=$ROOT/shim
mkdir -p "$shim"
printf '#!/bin/sh\ncase "$*" in *.boot.sd.tmp.*boot.sd) exit 1 ;; esac\nexec /bin/mv "$@"\n' >"$shim/mv"
chmod +x "$shim/mv"
if PATH=$shim:$PATH NANOKVM_DATA_DIR=$DATA NANOKVM_BOOT_DIR=$BOOT \
	"$STAGER" --activate "$DATA/nanokvm-update" 2>/dev/null; then
	echo "activation with a failing rename unexpectedly succeeded" >&2
	exit 1
fi
test "$(sha256sum "$BOOT/boot.sd" | cut -d ' ' -f 1)" = "$recovery_a"
test "$(cat "$BOOT/boot.before-recovery")" = "normal image"
grep -qxF "$recovery_a" "$BOOT/boot.before-recovery.for"
# The retry completes; recovery B's way back is still the normal image.
NANOKVM_DATA_DIR=$DATA NANOKVM_BOOT_DIR=$BOOT "$STAGER" --activate "$DATA/nanokvm-update"
test "$(cat "$BOOT/boot.sd")" = "recovery B"
test "$(cat "$BOOT/boot.before-recovery")" = "normal image"
grep -qxF "$(sha256sum "$BOOT/boot.sd" | cut -d ' ' -f 1)" "$BOOT/boot.before-recovery.for"
# Activated again after the rename (a cut before its own output): same.
NANOKVM_DATA_DIR=$DATA NANOKVM_BOOT_DIR=$BOOT "$STAGER" --activate "$DATA/nanokvm-update"
test "$(cat "$BOOT/boot.before-recovery")" = "normal image"

# A device recovery cannot run on is refused before boot.sd changes.
printf 'running image\n' >"$BOOT/boot.sd"
refused() {
	if NANOKVM_DATA_DIR=$DATA NANOKVM_BOOT_DIR=$BOOT "$STAGER" --activate "$DATA/nanokvm-update" 2>/dev/null; then
		echo "activation unexpectedly accepted: $1" >&2
		exit 1
	fi
	test "$(cat "$BOOT/boot.sd")" = "running image"
}
printf '/dev/mmcblk0p1 %s vfat rw 0 0\n/dev/mmcblk0p3 %s vfat rw 0 0\n' "$BOOT" "$DATA" >"$ROOT/mounts"
refused "data is not exFAT"
printf '/dev/mmcblk0p1 %s vfat rw 0 0\n/dev/mmcblk0p3 %s exfat rw 0 0\n' "$BOOT" "$DATA" >"$ROOT/mounts"
printf '2000000\n' >"$SYS/mmcblk0p3/start"
refused "unexpected data partition start"
printf '1705984\n' >"$SYS/mmcblk0p3/start"
printf '1000000\n' >"$SYS/mmcblk0p2/size"
refused "unexpected root partition size"
printf '1572864\n' >"$SYS/mmcblk0p2/size"

# A damaged input must be rejected before replacing the staged bundle.
bad=$ROOT/bad
make_bundle "$bad"
printf 'damage\n' >>"$bad/alpine-rootfs.tar.gz"
if NANOKVM_DATA_DIR=$DATA NANOKVM_BOOT_DIR=$BOOT "$STAGER" "$bad"; then
	echo "damaged bundle unexpectedly accepted" >&2
	exit 1
fi
test "$(cat "$BOOT/boot.sd")" = "running image"

echo "nanokvm-stage-update tests passed"
