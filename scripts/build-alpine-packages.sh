#!/bin/sh
# Build the small NanoKVM APK layer on top of Alpine.
#
# Usage:
#   PAYLOAD_ROOT=/build/payloads REPODEST=/build/repo \
#     ./scripts/build-alpine-packages.sh stock
#
# The payload tree is prepared by the NanoKVM image staging step. It may either
# contain package directories directly or contain one directory per profile:
#   PAYLOAD_ROOT/stock/{keys,base,kernel-sg2002,kmod-sg2002,firmware-sg2002,app,release}
#   PAYLOAD_ROOT/c906-scalar/{keys,base,kernel-sg2002,kmod-sg2002,firmware-sg2002,app,release}
# Without keys/, nanokvm-keys holds firmware/alpine/keys; without release/,
# a release marker is generated.
#
# This script only creates source archives and calls abuild. It does not install
# packages, change a device, or contact a NanoKVM. Build on Alpine with abuild
# and a configured signing key.
set -eu

usage() {
	cat >&2 <<'EOF'
usage: build-alpine-packages.sh [stock|c906-scalar]

Environment:
  PAYLOAD_ROOT  payload tree (default: work/alpine/payloads)
  REPODEST      APK repository output (default: work/alpine/repo)
  SRCDEST       source archive cache (default: work/alpine/distfiles)
  ABUILD        abuild command (default: abuild)
  ABUILD_FLAGS  optional abuild flags (for example -F in an isolated chroot)
  PKGVER        package version (default: firmware/alpine/release.env)
EOF
}

PROFILE=${1:-stock}
case "$PROFILE" in
	stock|c906-scalar) ;;
	-h|--help) usage; exit 0 ;;
	*) usage; exit 2 ;;
esac

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
PAYLOAD_ROOT=${PAYLOAD_ROOT:-$ROOT/work/alpine/payloads}
REPODEST=${REPODEST:-$ROOT/work/alpine/repo}
SRCDEST=${SRCDEST:-$ROOT/work/alpine/distfiles}
ABUILD=${ABUILD:-abuild}
ABUILD_FLAGS=${ABUILD_FLAGS:-}
# shellcheck source=firmware/alpine/release.env
. "$ROOT/firmware/alpine/release.env"
PKGVER=${PKGVER:-$NANOKVM_APK_VERSION}

command -v "$ABUILD" >/dev/null 2>&1 || {
	echo "build-alpine-packages: abuild is required" >&2
	exit 1
}
command -v tar >/dev/null 2>&1 || {
	echo "build-alpine-packages: tar is required" >&2
	exit 1
}

if [ -d "$PAYLOAD_ROOT/$PROFILE" ]; then
	PROFILE_ROOT=$PAYLOAD_ROOT/$PROFILE
else
	PROFILE_ROOT=$PAYLOAD_ROOT
fi

for required in base kernel-sg2002 kmod-sg2002 firmware-sg2002 app; do
	if [ ! -d "$PROFILE_ROOT/$required" ]; then
		echo "build-alpine-packages: missing payload $PROFILE_ROOT/$required" >&2
		exit 1
	fi
done

WORK=$(mktemp -d "${TMPDIR:-/tmp}/nanokvm-apk.XXXXXX")
trap 'rm -rf "$WORK"' EXIT INT TERM
REPODEST=$REPODEST/$PROFILE
mkdir -p "$SRCDEST" "$REPODEST"

make_release_payload() {
	release_dir=$PROFILE_ROOT/release
	if [ -d "$release_dir" ]; then
		if [ "$(cat "$release_dir/etc/nanokvm-build-profile")" != "$PROFILE" ] ||
		   ! grep -qxF "BUILD_PROFILE=\"$PROFILE\"" "$release_dir/etc/nanokvm-release"; then
			echo "build-alpine-packages: release metadata does not match $PROFILE; prepare payloads with --profile $PROFILE" >&2
			exit 1
		fi
		echo "$release_dir"
		return
	fi
	release_dir=$WORK/release
	mkdir -p "$release_dir/etc"
	cat > "$release_dir/etc/nanokvm-release" <<EOF
NAME="NanoKVM Alpine"
VERSION="$PKGVER"
ALPINE_VERSION="3.24"
BUILD_PROFILE="$PROFILE"
EOF
	printf '%s\n' "$PROFILE" > "$release_dir/etc/nanokvm-build-profile"
	echo "$release_dir"
}

make_keys_payload() {
	if [ -d "$PROFILE_ROOT/keys" ]; then
		echo "$PROFILE_ROOT/keys"
		return
	fi
	mkdir -p "$WORK/keys/etc/apk/keys"
	cp "$ROOT"/firmware/alpine/keys/*.pub "$WORK/keys/etc/apk/keys/"
	chmod 0644 "$WORK"/keys/etc/apk/keys/*
	echo "$WORK/keys"
}

archive_payload() {
	pkg=$1
	payload=$2
	version=${3:-$PKGVER}
	top="$WORK/$pkg-$version"
	rm -rf "$top"
	mkdir -p "$top"
	cp -a "$payload/." "$top/"
	tar -C "$WORK" -czf "$SRCDEST/$pkg-$version.tar.gz" "$pkg-$version"
	echo "prepared $SRCDEST/$pkg-$version.tar.gz"
}

keys_payload=$(make_keys_payload)
archive_payload nanokvm-keys "$keys_payload"
archive_payload nanokvm-base "$PROFILE_ROOT/base"
archive_payload nanokvm-kernel-sg2002 "$PROFILE_ROOT/kernel-sg2002"
archive_payload nanokvm-kmod-sg2002 "$PROFILE_ROOT/kmod-sg2002"
archive_payload nanokvm-firmware-sg2002 "$PROFILE_ROOT/firmware-sg2002"
archive_payload nanokvm-app "$PROFILE_ROOT/app"
release_payload=$(make_release_payload)
archive_payload nanokvm-release "$release_payload"

export CARCH=riscv64
export REPODEST SRCDEST PKGVER

# abuild signs each package and the index with abuild-sign and no type, which
# means RSA: RSA with SHA-1. abuild has no setting for the type, so a wrapper
# first in PATH asks for RSA256, RSA with SHA-256 (apk-tools 2.12 and later).
real_abuild_sign=$(command -v abuild-sign) || {
	echo "build-alpine-packages: abuild-sign is required" >&2
	exit 1
}
mkdir -p "$WORK/bin"
printf '#!/bin/sh\nexec %s -t RSA256 "$@"\n' "$real_abuild_sign" > "$WORK/bin/abuild-sign"
chmod 0755 "$WORK/bin/abuild-sign"
PATH=$WORK/bin:$PATH
export PATH

run_abuild() {
	if [ -n "$ABUILD_FLAGS" ]; then
		# Intentional word splitting: this variable is a short list of abuild flags.
		# shellcheck disable=SC2086
		"$ABUILD" $ABUILD_FLAGS "$@"
	else
		"$ABUILD" "$@"
	fi
}

prepare_recipe() {
	pkg=$1
	recipe_dir=$WORK/recipes/$pkg
	mkdir -p "$recipe_dir"
	cp -a "$ROOT/firmware/alpine/packages/$pkg/." "$recipe_dir/"
	# A source name without an URL is local to the APKBUILD directory. Keep the
	# generated archive there so both `abuild checksum` and the actual build read
	# the same bytes; SRCDEST remains the persistent archive cache/output.
	version=$PKGVER
	cp "$SRCDEST/$pkg-$version.tar.gz" "$recipe_dir/"
	(
		cd "$recipe_dir"
		run_abuild checksum
	)
	if grep -q 'sha256sums=""' "$recipe_dir/APKBUILD"; then
		echo "build-alpine-packages: abuild checksum produced no checksums for $pkg" >&2
		exit 1
	fi
	if grep -R -n -E '(^|[[:space:]])SKIP([[:space:]]|$)' "$recipe_dir"; then
		echo "build-alpine-packages: checksum generation left SKIP in $pkg" >&2
		exit 1
	fi
}

for pkg in nanokvm-keys nanokvm-base nanokvm-kernel-sg2002 nanokvm-kmod-sg2002 nanokvm-firmware-sg2002 \
	 nanokvm-app nanokvm-release; do
	echo "building $pkg ($PROFILE)"
	prepare_recipe "$pkg"
	(
		cd "$WORK/recipes/$pkg"
		run_abuild -r
	)
done

index=$(find "$REPODEST" -type f -name APKINDEX.tar.gz -print -quit)
[ -n "$index" ] || {
	echo "build-alpine-packages: abuild produced no APKINDEX.tar.gz" >&2
	exit 1
}
tar -tzf "$index" >/dev/null || {
	echo "build-alpine-packages: invalid APKINDEX.tar.gz: $index" >&2
	exit 1
}
tar -tzf "$index" | grep -qE '(^|/)\.SIGN\.RSA256\.' || {
	echo "build-alpine-packages: APKINDEX.tar.gz has no RSA256 signature: $index" >&2
	exit 1
}
apk_count=$(find "$REPODEST" -type f -name '*.apk' | wc -l)
[ "$apk_count" -ge 7 ] || {
	echo "build-alpine-packages: expected seven APKs, found $apk_count" >&2
	exit 1
}
find "$REPODEST" -type f -name '*.apk' -print | while IFS= read -r apk; do
	if ! tar -tzf "$apk" | grep -qE '(^|/)\.SIGN\.RSA256\.'; then
		echo "build-alpine-packages: APK without an RSA256 signature: $apk" >&2
		exit 1
	fi
done
echo "verified APK index: $index ($apk_count packages)"
echo "APK repository written below $REPODEST"
