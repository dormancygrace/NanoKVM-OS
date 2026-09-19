#!/bin/sh
# Build a complete manual NanoKVM Alpine update directory from a prepared root.
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
ROOTFS=${ROOTFS:-}
ROOTFS_ARCHIVE=${ROOTFS_ARCHIVE:-}
BOOT_FIT=${BOOT_FIT:-}
OUTPUT=${OUTPUT:-$ROOT/work/alpine/update}
RECOVERY_BUILDER=${RECOVERY_BUILDER:-$ROOT/scripts/build-alpine-recovery-fit.py}

usage() {
    cat <<'EOF'
Usage:
  ROOTFS=/path/to/alpine-root BOOT_FIT=/path/to/boot-alpine.sd \
    [OUTPUT=work/alpine/update] scripts/build-alpine-update-bundle.sh

  ROOTFS_ARCHIVE=/path/to/alpine-rootfs.tar.gz \
    BOOT_FIT=/path/to/boot-alpine.sd [OUTPUT=work/alpine/update] \
    scripts/build-alpine-update-bundle.sh

The output directory contains alpine-rootfs.tar.gz, boot-alpine.sd,
boot-alpine-recovery.sd, recovery-manifest.json and SHA256SUMS.
EOF
}

[ "${1:-}" != --help ] || { usage; exit 0; }
[ -z "$ROOTFS" ] || [ -z "$ROOTFS_ARCHIVE" ] || {
    echo "set only one of ROOTFS and ROOTFS_ARCHIVE" >&2
    exit 2
}
[ -n "$ROOTFS" ] || [ -n "$ROOTFS_ARCHIVE" ] || { usage >&2; exit 2; }
[ -n "$BOOT_FIT" ] || { usage >&2; exit 2; }
if [ -n "$ROOTFS" ]; then
    [ -d "$ROOTFS" ] || { echo "missing rootfs directory: $ROOTFS" >&2; exit 1; }
    [ -f "$ROOTFS/etc/alpine-release" ] || {
        echo "not an Alpine root: $ROOTFS" >&2
        exit 1
    }
    [ -x "$ROOTFS/etc/init.d/nanokvm-app" ] || {
        echo "NanoKVM app service missing from root: $ROOTFS" >&2
        exit 1
    }
else
    [ -f "$ROOTFS_ARCHIVE" ] || {
        echo "missing rootfs archive: $ROOTFS_ARCHIVE" >&2
        exit 1
    }
fi
[ -f "$BOOT_FIT" ] || { echo "missing normal FIT: $BOOT_FIT" >&2; exit 1; }
[ -f "$RECOVERY_BUILDER" ] || {
    echo "missing recovery builder: $RECOVERY_BUILDER" >&2
    exit 1
}

mkdir -p "$OUTPUT"
archive_tmp="$OUTPUT/.alpine-rootfs.tar.gz.$$"
boot_tmp="$OUTPUT/.boot-alpine.sd.$$"
recovery_tmp="$OUTPUT/.recovery-build.$$"
listing_tmp="$OUTPUT/.alpine-rootfs.list.$$"
cleanup() {
    rm -f "$archive_tmp" "$boot_tmp" "$listing_tmp"
    rm -rf "$recovery_tmp"
}
trap cleanup EXIT INT TERM

if [ -n "$ROOTFS" ]; then
    tar --numeric-owner -czf "$archive_tmp" \
        --exclude='./boot/*' \
        --exclude='./data/*' \
        --exclude='./dev/*' \
        --exclude='./proc/*' \
        --exclude='./sys/*' \
        --exclude='./run/*' \
        --exclude='./tmp/*' \
        -C "$ROOTFS" .
else
    cp "$ROOTFS_ARCHIVE" "$archive_tmp"
fi
tar -tzf "$archive_tmp" > "$listing_tmp"
grep -qx './etc/alpine-release' "$listing_tmp"
grep -qx './sbin/init' "$listing_tmp"
grep -qx './etc/init.d/nanokvm-app' "$listing_tmp"
grep -qx './kvmapp/server/NanoKVM-Server' "$listing_tmp"
if grep -E '^\./(boot|data|dev|proc|sys|run|tmp)/.+' "$listing_tmp"; then
    echo "archive contains mounted or volatile content" >&2
    exit 1
fi
# Leave practical F2FS headroom in the fixed 768 MiB system partition.  The
# decompressed tar stream is a conservative proxy for installed bytes because
# it also includes tar headers and padding.
rootfs_stream_bytes=$(gzip -dc "$archive_tmp" | wc -c)
case "$rootfs_stream_bytes" in ''|*[!0-9]*) echo "cannot measure rootfs" >&2; exit 1 ;; esac
max_rootfs_stream_bytes=$((650 * 1024 * 1024))
if [ "$rootfs_stream_bytes" -gt "$max_rootfs_stream_bytes" ]; then
    echo "rootfs tar stream exceeds the 650 MiB limit for a 768 MiB F2FS root" >&2
    exit 1
fi

cp "$BOOT_FIT" "$boot_tmp"
mv "$archive_tmp" "$OUTPUT/alpine-rootfs.tar.gz"
mv "$boot_tmp" "$OUTPUT/boot-alpine.sd"

rootfs_sha=$(sha256sum "$OUTPUT/alpine-rootfs.tar.gz")
rootfs_sha=${rootfs_sha%% *}
boot_sha=$(sha256sum "$OUTPUT/boot-alpine.sd")
boot_sha=${boot_sha%% *}

python3 "$RECOVERY_BUILDER" \
    --rootfs-sha256 "$rootfs_sha" \
    --boot-sha256 "$boot_sha" \
    --output "$recovery_tmp"
cp "$recovery_tmp/boot-alpine-recovery.sd" "$OUTPUT/boot-alpine-recovery.sd"
cp "$recovery_tmp/manifest.json" "$OUTPUT/recovery-manifest.json"

(
    cd "$OUTPUT"
    sha256sum alpine-rootfs.tar.gz boot-alpine.sd boot-alpine-recovery.sd \
        recovery-manifest.json \
        > SHA256SUMS
    sha256sum -c SHA256SUMS
)

echo "NanoKVM Alpine update bundle: $OUTPUT"
cat "$OUTPUT/SHA256SUMS"
