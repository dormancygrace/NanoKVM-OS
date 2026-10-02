#!/bin/bash
# Assemble the corresponding-source archive for the kernel and U-Boot of the
# current NanoKVM OS v2 release, for attaching next to the image. Upstream
# archives are downloaded once into the cache and verified by SHA-256.
set -euo pipefail
export PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
[ "$#" -ge 1 ] && [ "$#" -le 2 ] || { echo "Usage: $0 output-dir [download-cache]" >&2; exit 2; }
repo=$(cd "$(dirname "$0")/.." && pwd)
out=$(realpath -m "$1")
cache=$(realpath -m "${2:-$out/downloads}")
version=$(sed -n 's/^NANOKVM_VERSION=//p' "$repo/firmware/alpine/release.env")
name=NanoKVM-OS-$version-gpl-source
[ -z "$(git -C "$repo" status --porcelain)" ] || { echo 'Commit or stash local changes first' >&2; exit 2; }
mkdir -p "$out" "$cache"
[ ! -e "$out/$name.tar" ] || { echo "$out/$name.tar already exists" >&2; exit 2; }

fetch() {
    local file=$1 url=$2 sha=$3
    if [ ! -f "$cache/$file" ]; then
        curl -fL --retry 3 -o "$cache/$file.part" "$url"
        mv "$cache/$file.part" "$cache/$file"
    fi
    echo "$sha  $cache/$file" | sha256sum -c --quiet
}
fetch linux-7.2.6.tar.xz https://cdn.kernel.org/pub/linux/kernel/v7.x/linux-7.2.6.tar.xz \
    039aef84f2b0994aeda3f4fcfc3d02ec9d7a9bbb9020ea264c43f446c860f606
fetch u-boot-2026.07.tar.bz2 https://ftp.denx.de/pub/u-boot/u-boot-2026.07.tar.bz2 \
    78e8bfc382fe388f9b55aa1daf8c563522a037779b5d4c349d1415e381f1243e

stage=$(mktemp -d "$out/.stage.XXXXXX")
trap 'rm -rf "$stage"' EXIT
root=$stage/$name
mkdir -p "$root/upstream" "$root/NanoKVM-OS"
cp "$cache/linux-7.2.6.tar.xz" "$cache/u-boot-2026.07.tar.bz2" "$root/upstream/"
# Project files from the committed tree: patches, configurations, build and
# packaging recipes, and the board device trees built into boot.sd.
git -C "$repo" archive HEAD LICENSE docs/SOURCE.md firmware/boards firmware/uboot \
    firmware/release/source-components/kernel firmware/release/source-components/uboot \
    scripts/build-enhanced-uboot.py scripts/build-enhanced-ram-fip.py scripts/build-universal-boot.py |
    tar -x -C "$root/NanoKVM-OS"
cp "$repo/docs/SOURCE.md" "$root/README.md"
git -C "$repo" rev-parse HEAD > "$root/SOURCE-COMMIT"
(cd "$root" && find . -type f ! -name SHA256SUMS | LC_ALL=C sort | xargs sha256sum > SHA256SUMS)
epoch=$(git -C "$repo" log -1 --format=%ct)
tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$epoch" \
    -C "$stage" -cf "$out/$name.tar" "$name"
(cd "$out" && sha256sum "$name.tar")
