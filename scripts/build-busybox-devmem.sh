#!/bin/sh
# Build the BusyBox devmem applet used by Sophgo libsys.so, without replacing
# Alpine's BusyBox or installing any other applets.
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
OUTPUT=${1:-"$ROOT/work/devmem"}
BUILDROOT_OUTPUT=${NANOKVM_BUILDROOT_OUTPUT:?Set NANOKVM_BUILDROOT_OUTPUT to the Enhanced Buildroot output}
BUILDROOT_OUTPUT=$(CDPATH='' cd -- "$BUILDROOT_OUTPUT" && pwd)
CROSS="$BUILDROOT_OUTPUT/host/bin/riscv64-buildroot-linux-musl-"
VERSION=1.36.1
SHA256=b8cc24c9574d809e7279c3be349795c5d5ceb6fdf19ca709f80cde50e47de314
export SOURCE_DATE_EPOCH=1672704000

command -v make >/dev/null
command -v curl >/dev/null
command -v sha256sum >/dev/null
command -v readelf >/dev/null
test -x "${CROSS}gcc"
mkdir -p "$OUTPUT"
OUTPUT=$(CDPATH='' cd -- "$OUTPUT" && pwd)
SOURCE=${BUSYBOX_SOURCE_ARCHIVE:-"$OUTPUT/busybox-$VERSION.tar.bz2"}
if [ ! -f "$SOURCE" ]; then
    curl -fL --retry 2 "https://busybox.net/downloads/busybox-$VERSION.tar.bz2" -o "$SOURCE"
fi
printf '%s  %s\n' "$SHA256" "$SOURCE" | sha256sum -c -

WORK=$(mktemp -d "$OUTPUT/.busybox-devmem.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM
tar -xjf "$SOURCE" -C "$WORK"
SRC="$WORK/busybox-$VERSION"
BUILD="$WORK/build"
mkdir "$BUILD"
make -s -C "$SRC" O="$BUILD" allnoconfig
sed -i \
    -e 's/^# CONFIG_DEVMEM is not set$/CONFIG_DEVMEM=y/' \
    -e 's/^# CONFIG_SHOW_USAGE is not set$/CONFIG_SHOW_USAGE=y/' \
    -e 's/^# CONFIG_FEATURE_VERBOSE_USAGE is not set$/CONFIG_FEATURE_VERBOSE_USAGE=y/' \
    -e 's/^CONFIG_EXTRA_CFLAGS=""$/CONFIG_EXTRA_CFLAGS="-O2 -march=rv64gc -mabi=lp64d"/' \
    -e 's/^CONFIG_SH_IS_ASH=y$/# CONFIG_SH_IS_ASH is not set/' \
    -e 's/^# CONFIG_SH_IS_NONE is not set$/CONFIG_SH_IS_NONE=y/' \
    "$BUILD/.config"
make -s -C "$SRC" O="$BUILD" oldconfig </dev/null >/dev/null
grep -qx 'CONFIG_DEVMEM=y' "$BUILD/.config"
grep -qx 'CONFIG_SHOW_USAGE=y' "$BUILD/.config"
grep -qx 'CONFIG_FEATURE_VERBOSE_USAGE=y' "$BUILD/.config"
grep -qx 'CONFIG_EXTRA_CFLAGS="-O2 -march=rv64gc -mabi=lp64d"' "$BUILD/.config"
make -s -C "$SRC" O="$BUILD" CROSS_COMPILE="$CROSS" -j "${JOBS:-4}" busybox
grep -q '"devmem"' "$BUILD/include/applet_tables.h"
grep -qx '#define NUM_APPLETS 1' "$BUILD/include/NUM_APPLETS.h"
readelf -h "$BUILD/busybox" | grep -q 'Machine:.*RISC-V'
readelf -A "$BUILD/busybox" | grep -q 'Tag_RISCV_arch:.*rv64i'
if readelf -A "$BUILD/miscutils/devmem.o" | grep -q 'xthead'; then
    echo 'devmem applet was compiled for chip-specific instructions' >&2
    exit 1
fi
install -m 0755 "$BUILD/busybox" "$OUTPUT/devmem"
install -m 0644 "$SRC/LICENSE" "$OUTPUT/busybox-LICENSE"
sha256sum "$OUTPUT/devmem"
