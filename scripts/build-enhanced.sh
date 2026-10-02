#!/bin/bash
# Reproducible NanoKVM Enhanced userspace/toolchain entry point.
# Defaults to the toolchain while the 7.2 media-driver port is in progress.
set -euo pipefail
# WSL imports Windows PATH entries containing spaces; Buildroot rejects them.
export PATH=${NANOKVM_HOST_PATH:-/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin}
ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
BUILD=${NANOKVM_BUILD_DIR:-$ROOT/build/enhanced}
BR_VERSION=2026.08
BR_SHA256=87aaca4164ea9d5c8085854953018263f7963f07c22e73a2a2185cc98c581c34
BR=${NANOKVM_BUILDROOT_DIR:-$BUILD/sources/buildroot-$BR_VERSION}
OUT=${NANOKVM_BUILDROOT_OUTPUT:-$BUILD/buildroot-output}
JOBS=${JOBS:-12}
TARGET=${1:-toolchain}
case "$TARGET" in toolchain|all|legal-info|show-info) ;; *) echo "Usage: $0 [toolchain|all|legal-info|show-info]" >&2; exit 2;; esac
mkdir -p "$BUILD/downloads" "$BUILD/sources"
if [ ! -f "$BR/Makefile" ]; then
    archive=$BUILD/downloads/buildroot-$BR_VERSION.tar.xz
    if [ ! -f "$archive" ]; then
        curl -fL --retry 3 --connect-timeout 20 --max-time 300 "https://buildroot.org/downloads/buildroot-$BR_VERSION.tar.xz" -o "$archive.part"
        printf '%s  %s\n' "$BR_SHA256" "$archive.part" | sha256sum -c -
        mv "$archive.part" "$archive"
    fi
    printf '%s  %s\n' "$BR_SHA256" "$archive" | sha256sum -c -
    mkdir -p "$BR"
    tar -xJf "$archive" -C "$BR" --strip-components=1
fi
# Apply checked-in Buildroot source changes exactly once, including json-c.
for patchfile in "$ROOT"/firmware/buildroot/source-patches/*.patch; do
    if patch -d "$BR" -p1 --forward --dry-run < "$patchfile" >/dev/null 2>&1; then
        patch -d "$BR" -p1 --forward < "$patchfile"
    elif ! patch -d "$BR" -p1 --reverse --dry-run < "$patchfile" >/dev/null 2>&1; then
        echo "Buildroot source patch does not match: $patchfile" >&2
        exit 1
    fi
done
# Do not silently use a different Buildroot release with the same output path.
grep -q '^export BR2_VERSION := 2026.08$' "$BR/Makefile"
if [ "$TARGET" = all ]; then
    : "${NANOKVM_APP_STAGE:?Set this to the application rebuilt against the Enhanced toolchain}"
    : "${NANOKVM_BOARD_ASSETS:?Set this to the kernel 7.2.4 boot/modules staging directory}"
    export NANOKVM_APP_STAGE NANOKVM_BOARD_ASSETS
fi
make -C "$BR" O="$OUT" BR2_EXTERNAL="$ROOT/firmware/buildroot" nanokvm_enhanced_defconfig
make -C "$BR" O="$OUT" BR2_EXTERNAL="$ROOT/firmware/buildroot" -j"$JOBS" "$TARGET"

if [ "$TARGET" = legal-info ]; then
    # legal-info recreates its output tree, so include these on every run.
    archive=${NANOKVM_BUILDROOT_ARCHIVE:-$BUILD/downloads/buildroot-$BR_VERSION.tar.xz}
    if [ ! -f "$archive" ]; then
        mkdir -p "$(dirname "$archive")"
        curl -fL --retry 3 --connect-timeout 20 --max-time 300 "https://buildroot.org/downloads/buildroot-$BR_VERSION.tar.xz" -o "$archive.part"
        printf '%s  %s\n' "$BR_SHA256" "$archive.part" | sha256sum -c -
        mv "$archive.part" "$archive"
    fi
    printf '%s  %s\n' "$BR_SHA256" "$archive" | sha256sum -c -
    source_bundle=$OUT/legal-info/buildroot-source
    mkdir -p "$source_bundle/BR2_EXTERNAL_NANOKVM"
    cp "$archive" "$source_bundle/buildroot-$BR_VERSION.tar.xz"
    for item in Config.in external.desc external.mk board configs package patches source-patches; do
        cp -a "$ROOT/firmware/buildroot/$item" "$source_bundle/BR2_EXTERNAL_NANOKVM/"
    done
    cat > "$source_bundle/README.txt" <<EOF
Buildroot $BR_VERSION source archive plus the selected NanoKVM BR2_EXTERNAL files.
Apply BR2_EXTERNAL_NANOKVM/source-patches to Buildroot before using its recipes.
Package patches are selected by BR2_GLOBAL_PATCH_DIR in the defconfig.
This is not the complete OS corresponding-source bundle: externally staged
kernel/modules, application, toolchain modifications, FIP, native dependencies
and all applicable notices still need their matching source/provenance package.
EOF
fi
