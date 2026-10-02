#!/bin/sh
# One-time migration of an existing C906 Alpine installation. Normal updates
# continue to use apk update / apk upgrade afterwards; no runtime wrapper.
set -eu
mode=${1:---simulate}
case "$mode" in --simulate|--apply) ;; *) echo "usage: $0 [--simulate|--apply]" >&2; exit 2 ;; esac
[ "$(id -u)" -eq 0 ] || { echo "Run as root" >&2; exit 1; }
case "$(cat /etc/alpine-release)" in 3.24|3.24.*) ;; *) echo "Expected Alpine 3.24" >&2; exit 1 ;; esac
case "$(cat /etc/nanokvm-build-profile)" in stock|c906-scalar) ;; *) echo "Unknown installed profile" >&2; exit 1 ;; esac

# Stage the intended repository configuration, not a rollback copy. Keep the
# NanoKVM board/application channel, Alpine mirrors and explicit edge tags.
repos=$(mktemp /etc/apk/.stock-repositories.XXXXXX)
trap 'rm -f "$repos"' EXIT HUP INT TERM
awk '$1 !~ /^https:\/\/nkos\.pesin\.pro\/repos\/c906-qualified\/?$/ { print }' /etc/apk/repositories > "$repos"
# Custom experimental endpoints must be removed explicitly by their operator.
# Never guess at unrelated repository ownership or silently discard it.
if grep -E '^[[:space:]]*[^#].*c906' "$repos"; then
    echo "Remove custom C906 repository entries before migrating" >&2
    exit 1
fi
if [ -d /etc/apk/repositories.d ] && grep -rE '^[[:space:]]*[^#].*c906' /etc/apk/repositories.d; then
    echo "Remove C906 entries from repositories.d before migrating" >&2
    exit 1
fi

# Select installed subpackages by source origin: this includes libcrypto3,
# libssl3, busybox-extras/openrc, coreutils-env, liblz4 and libzstd as needed.
# XZ/zlib cover older experimental images, although the qualified set omitted them.
packages=$(awk '
    BEGIN { RS=""; FS="\n" }
    { name=""; origin=""; for (i=1; i<=NF; i++) {
        if ($i ~ /^P:/) name=substr($i,3)
        if ($i ~ /^o:/) origin=substr($i,3)
      }
      if (origin ~ /^(busybox|coreutils|lz4|zstd|openssl|xz|zlib)$/) print name
    }' /lib/apk/db/installed)
[ -n "$packages" ] || { echo "No Alpine base packages found" >&2; exit 1; }
# Only installed APK names enter this intentional shell word splitting.
# shellcheck disable=SC2086
set -- $packages
printf 'Aligning installed packages with configured Alpine repositories: %s\n' "$*"
apk --repositories-file "$repos" update
if [ "$mode" = --simulate ]; then
    apk --repositories-file "$repos" upgrade --available --simulate "$@"
    exit
fi
# APK resolves constraints, verifies signatures, preserves protected config and
# runs the ordinary maintainer scripts and transaction hooks. Failure leaves
# the active repository list/profile unchanged; do not override world holds.
apk --repositories-file "$repos" upgrade --available "$@"
chmod 0644 "$repos"
mv "$repos" /etc/apk/repositories
printf 'stock\n' > /etc/nanokvm-build-profile
sed -i 's/^BUILD_PROFILE=.*/BUILD_PROFILE="stock"/' /etc/nanokvm-release
echo 'Stock Alpine migration complete. Use apk update and apk upgrade normally.'
