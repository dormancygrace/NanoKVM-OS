#!/bin/sh
# Supply the web serial terminal even when the base image lacks picocom.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
version=3.1
sha256=e6761ca932ffc6d09bd6b11ff018bdaf70b287ce518b3282d29e0270e88420bb
cc=${CC:-riscv64-unknown-linux-musl-gcc}
stage=$(mktemp -d "${TMPDIR:-/tmp}/nanokvm-picocom.XXXXXX")
trap 'rm -rf "$stage"' EXIT HUP INT TERM
archive="picocom-$version.tar.gz"
curl --fail --location --retry 2 --output "$stage/$archive" \
    "https://github.com/npat-efault/picocom/archive/$version.tar.gz"
printf '%s  %s\n' "$sha256" "$stage/$archive" | sha256sum -c -
tar -xzf "$stage/$archive" -C "$stage"
# Static musl avoids depending on base-image shared libraries. Bound pasted
# input to 64 KiB instead of picocom's default unbounded output queue.
make -C "$stage/picocom-$version" CC="$cc" CFLAGS='-Os' \
    LDFLAGS='-static -s' TTY_Q_SZ=65536
install -D -m 0755 "$stage/picocom-$version/picocom" "$root/kvmapp/system/bin/picocom"
# Ship the exact corresponding source and its license with the executable.
install -D -m 0644 "$stage/$archive" "$root/kvmapp/system/share/picocom/$archive"
install -m 0644 "$stage/picocom-$version/LICENSE.txt" "$root/kvmapp/system/share/picocom/LICENSE.txt"
