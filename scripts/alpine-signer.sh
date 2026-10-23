#!/bin/bash
# alpine-signer.sh ROOT [PACKAGE...]: an Alpine x86_64 tree in ROOT for the
# jobs that hold the release key, with apk and the Alpine PACKAGEs (abuild
# for abuild-sign). The minirootfs is checked against platform/sources.lock
# (alpine-signer), the packages by apk against Alpine's signed indexes:
# never a Docker image, a cached file or a build output. Run commands in it
# as root with chroot ROOT; it needs no mounts. Remove it with
# rm -rf --one-file-system ROOT.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
root=$1
shift
as_root() { if [ "$(id -u)" = 0 ]; then "$@"; else sudo "$@"; fi; }
read -r _ url pin < <(awk '$1 == "alpine-signer"' "$here/../platform/sources.lock")
[ -n "$pin" ] || { echo "alpine-signer is not in platform/sources.lock" >&2; exit 1; }
tarball=$(mktemp)
trap 'rm -f "$tarball"' EXIT
curl -fsSL --retry 3 -o "$tarball" "$url"
echo "$pin  $tarball" | sha256sum -c --quiet
[ ! -e "$root" ] || { echo "$root exists" >&2; exit 1; }
as_root mkdir -p "$root"
as_root tar --numeric-owner -xzf "$tarball" -C "$root"
as_root cp -L /etc/resolv.conf "$root/etc/resolv.conf"
[ "$#" -eq 0 ] || as_root chroot "$root" /sbin/apk add --no-cache --quiet "$@"
