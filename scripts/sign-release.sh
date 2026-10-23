#!/bin/bash
# Sign the APK repository of a release built with platform/build.sh -u:
#
#   scripts/sign-release.sh -o OUTPUT (-e ECKEY | -d)
#
# The Release workflow's sign job runs this and no build tool, as it holds
# the release key. OUTPUT/release/apk/recipes/riscv64 has the packages,
# signed with the throwaway build key, and both indexes.
#   - Reused packages (changed: false in packages.json) are files of an
#     earlier release and must verify with a published key.
#   - The changed ones get the release signature in place of the build key's.
#     Only the first gzip stream, the signature, changes, so their identity
#     (the control hash) stays; their size does not.
#   - The add-ons must match the committed firmware/alpine/addons.json and
#     declare nothing but their files (scripts/addon_packages.py).
#   - Both indexes are made anew from the final files and signed.
# apk and abuild-sign come from Alpine's x86_64 minirootfs, checked against
# platform/sources.lock, and Alpine's signed repository. -d signs with an EC
# test key in OUTPUT/keys, as platform/build.sh -d does.
set -euo pipefail
export PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin LC_ALL=C
umask 022
here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
out=
eckey=
devkey=
while getopts o:e:d opt; do
    case $opt in
        o) out=$(realpath -m "$OPTARG") ;; e) eckey=$(realpath "$OPTARG") ;; d) devkey=1 ;;
        *) sed -n '2,/^set -euo/{ /^#/s/^# \{0,1\}//p; }' "$0" >&2; exit 2 ;;
    esac
done
[ -n "$out" ] && [ -n "$eckey$devkey" ] && { [ -z "$eckey" ] || [ -z "$devkey" ]; } || {
    echo "usage: $0 -o OUTPUT (-e ECKEY | -d)" >&2; exit 2; }
version=${NANOKVM_VERSION:-$(sed -n 's/^NANOKVM_VERSION=//p' "$repo/firmware/alpine/release.env")}
dir=$out/release/apk/recipes/riscv64
[ -f "$dir/packages.json" ] || { echo "$dir/packages.json is missing" >&2; exit 1; }
as_root() { if [ "$(id -u)" = 0 ]; then "$@"; else sudo "$@"; fi; }

# The key, checked as platform/build.sh checks it.
if [ -n "$eckey" ]; then
    "$here/check-release-key.sh" "$eckey"
else
    echo "Signing with a local EC test key: do not publish this repository" >&2
    mkdir -p "$out/keys"
    eckey=$(find "$out/keys" -maxdepth 1 -name 'nanokvm-test-ec-*.key' | LC_ALL=C sort | head -n 1)
    if [ -z "$eckey" ]; then
        tmp=$out/keys/new-ec
        openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out "$tmp.key" 2> /dev/null
        openssl pkey -in "$tmp.key" -pubout -out "$tmp.pub" 2> /dev/null
        eckey=$out/keys/nanokvm-test-ec-$(openssl pkey -pubin -in "$tmp.pub" -outform DER | sha256sum | cut -c1-8).key
        mv "$tmp.key" "$eckey"
        mv "$tmp.pub" "${eckey%.key}.pub"
    fi
    "$here/check-ec-key.sh" "$eckey" "${eckey%.key}.pub"
fi
ecpub=${eckey%.key}.pub
pubname=${ecpub##*/}

# The packages: exactly the core packages of packages.json and the add-ons
# of the committed addons.json.
cmp -s "$dir/addons.json" "$repo/firmware/alpine/addons.json" || {
    echo "$dir/addons.json differs from firmware/alpine/addons.json" >&2; exit 1; }
mapfile -t reused < <(python3 -c 'import json, sys
for p in json.load(open(sys.argv[1]))["packages"].values():
    p["changed"] or print(p["apk"])' "$dir/packages.json")
mapfile -t changed < <(python3 -c 'import json, sys
for p in json.load(open(sys.argv[1]))["packages"].values():
    p["changed"] and print(p["apk"])' "$dir/packages.json")
mapfile -t addons < <(python3 -c 'import json, sys
print(*json.load(open(sys.argv[1]))["packages"], sep="\n")' "$repo/firmware/alpine/addons.json")
core=("${reused[@]}" "${changed[@]}")
[ "$(cd "$dir" && find . -maxdepth 1 -name '*.apk' | sed 's|^\./||' | LC_ALL=C sort)" = \
  "$(printf '%s\n' "${core[@]}" "${addons[@]}" | LC_ALL=C sort)" ] || {
    echo "$dir has other packages than packages.json and addons.json" >&2; exit 1; }
PYTHONPATH=$here python3 -c 'import sys
from pathlib import Path
import addon_packages
d = Path(sys.argv[1])
for name, item in addon_packages.manifest(Path(sys.argv[2]).read_bytes()).items():
    addon_packages.checked(name, item, lambda url: (d / name).read_bytes(), item["size"])' \
    "$dir" "$repo/firmware/alpine/addons.json"

# The signer: Alpine's x86_64 minirootfs with abuild, without mounts, so
# that its removal can never reach the host's /dev or /proc.
root=$out/signer
cleanup() { as_root rm -rf --one-file-system "$root"; }
trap cleanup EXIT
cleanup
"$here/alpine-signer.sh" "$root" abuild
# Host TMPDIR may be outside this root; abuild-sign creates temporary files.
signer() { as_root chroot "$root" /usr/bin/env TMPDIR=/tmp "$@"; }
as_root install -d -m 0700 "$root/work/keys"
as_root install -m 0600 "$eckey" "$root/work/keys/release.key"
as_root install -d "$root/work/published" "$root/work/release" "$root/work/repo" "$root/work/resign"
for pub in "$repo"/firmware/alpine/keys/*.pub; do as_root install -m 0644 "$pub" "$root/work/published/"; done
as_root install -m 0644 "$ecpub" "$root/work/release/$pubname"
as_root cp "$dir"/*.apk "$root/work/repo/"

# Reused packages carry a published signature; the changed ones get the
# release signature and must then verify with it alone.
[ "${#reused[@]}" -eq 0 ] || signer /sbin/apk --keys-dir /work/published verify "${reused[@]/#//work/repo/}"
# The packages are taken apart and put together as this user; only the
# control stream goes through the signer.
w=$(mktemp -d)
trap 'rm -rf "$w"; cleanup' EXIT
for apk in "${changed[@]}"; do
    python3 "$here/apk_resign.py" split "$dir/$apk" "$w"
    as_root install -m 0644 "$w/control.tar.gz" "$root/work/resign/control.tar.gz"
    signer abuild-sign -q -t RSA256 -k /work/keys/release.key -p "$pubname" /work/resign/control.tar.gz
    as_root cat "$root/work/resign/control.tar.gz" > "$w/control.tar.gz"
    python3 "$here/apk_resign.py" join "$w/$apk" "$w" "$pubname"
    as_root install -m 0644 "$w/$apk" "$root/work/repo/$apk"
    rm -f "$w"/*
done
[ "${#changed[@]}" -eq 0 ] || signer /sbin/apk --keys-dir /work/release verify "${changed[@]/#//work/repo/}"
for addon in "${addons[@]}"; do
    signer /sbin/apk --allow-untrusted adbdump "/work/repo/$addon" | python3 "$here/addon_packages.py" --check "$addon"
done

# The indexes, from the final files: the v2 index of the core packages as
# abuild makes it, the v3 index of everything. Every signature was checked above.
signer /bin/sh -euc 'cd /work/repo
/sbin/apk index --no-warnings --quiet --allow-untrusted --rewrite-arch riscv64 --description "recipes " \
    --output APKINDEX.tar.gz "$@"' sh "${core[@]}"
signer abuild-sign -q -t RSA256 -k /work/keys/release.key -p "$pubname" /work/repo/APKINDEX.tar.gz
signer /bin/sh -euc 'cd /work/repo
version=$1; shift
/sbin/apk --sign-key /work/keys/release.key --allow-untrusted mkndx \
    --description "NanoKVM OS $version" --output Packages.adb "$@"' sh "$version" "${core[@]}" "${addons[@]}"
as_root rm -rf "$root/work/keys"
signer /sbin/apk --keys-dir /work/release verify /work/repo/APKINDEX.tar.gz /work/repo/Packages.adb

# Back to OUTPUT, with packages.json for the new files and the release key's
# public half in place of the build key's.
for file in "${changed[@]}" APKINDEX.tar.gz Packages.adb; do
    as_root cat "$root/work/repo/$file" > "$dir/$file.new"
    mv "$dir/$file.new" "$dir/$file"
done
cp "$dir/packages.json" "$out/release/packages.json.build"
python3 "$here/package-versions.py" manifest --versions "$out/release/packages.json.build" --repo "$dir"
rm -f "$out/release/packages.json.build" "$out/release/apk"/*.pub
cp "$ecpub" "$out/release/apk/"
echo "Signed $dir with $pubname: ${#changed[@]} packages re-signed, ${#reused[@]} reused, ${#addons[@]} add-ons"
