#!/bin/sh
# Build the small NanoKVM APK layer on top of Alpine.
#
# Usage:
#   PAYLOAD_ROOT=/build/payloads REPODEST=/build/repo \
#     ./scripts/build-alpine-packages.sh stock
#
# The payload tree is prepared by the NanoKVM image staging step. It may either
# contain package directories directly or contain one directory per profile:
#   PAYLOAD_ROOT/stock/{base,kernel-sg2002,kmod-sg2002,firmware-sg2002,app,release}
#   PAYLOAD_ROOT/c906-scalar/{base,kernel-sg2002,kmod-sg2002,firmware-sg2002,app,release}
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

archive_payload nanokvm-base "$PROFILE_ROOT/base"
archive_payload nanokvm-kernel-sg2002 "$PROFILE_ROOT/kernel-sg2002"
archive_payload nanokvm-kmod-sg2002 "$PROFILE_ROOT/kmod-sg2002"
archive_payload nanokvm-firmware-sg2002 "$PROFILE_ROOT/firmware-sg2002"
archive_payload nanokvm-app "$PROFILE_ROOT/app"
archive_payload nanokvm-release "$(make_release_payload)"

export CARCH=riscv64
export REPODEST SRCDEST PKGVER

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

for pkg in nanokvm-base nanokvm-kernel-sg2002 nanokvm-kmod-sg2002 nanokvm-firmware-sg2002 \
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
apk_count=$(find "$REPODEST" -type f -name '*.apk' | wc -l)
[ "$apk_count" -ge 6 ] || {
	echo "build-alpine-packages: expected six APKs, found $apk_count" >&2
	exit 1
}
find "$REPODEST" -type f -name '*.apk' -print | while IFS= read -r apk; do
	if ! tar -tzf "$apk" | grep -qE '(^|/)\.SIGN\.RSA\.'; then
		echo "build-alpine-packages: unsigned APK: $apk" >&2
		exit 1
	fi
done
echo "verified APK index: $index ($apk_count packages)"
echo "APK repository written below $REPODEST"
