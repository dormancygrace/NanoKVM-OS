#!/bin/sh
# Build the small NanoKVM APK layer on top of Alpine.
#
# Usage:
#   PAYLOAD_ROOT=/build/payloads REPODEST=/build/repo \
#     ./scripts/build-alpine-packages.sh stock
#
# The payload tree is prepared by the NanoKVM image staging step. It may either
# contain package directories directly or below the profile directory:
#   PAYLOAD_ROOT/stock/{keys,base,kernel-sg2002,kmod-sg2002,firmware-sg2002,bootloader-sg2002,app,web,zlib,openssl,exfatprogs,i2c-tools,release}
# Without keys/, nanokvm-keys holds firmware/alpine/keys; without release/,
# a release marker is generated.
#
# This script only creates source archives and calls abuild. It does not install
# packages, change a device, or contact a NanoKVM. Build on Alpine with abuild
# and PACKAGER_PRIVKEY/PACKAGER_PUBKEY pointing to an ECDSA key pair.
set -eu

usage() {
	cat >&2 <<'EOF'
usage: build-alpine-packages.sh [stock]

Environment:
  PAYLOAD_ROOT  payload tree (default: work/alpine/payloads)
  REPODEST      APK repository output (default: work/alpine/repo)
  SRCDEST       source archive cache (default: work/alpine/distfiles)
  ABUILD        abuild command (default: abuild)
  ABUILD_FLAGS  optional abuild flags (for example -F in an isolated chroot)
  PACKAGER_PRIVKEY / PACKAGER_PUBKEY  ECDSA private/public key paths
  PKGVER        package version (default: firmware/alpine/release.env)
  PKGVERSIONS   optional file of "NAME VERSION CHANGED" lines
                (scripts/package-versions.py). A package with CHANGED 0 keeps
                its published file, which must already be in the repository.
EOF
}

PROFILE=${1:-stock}
case "$PROFILE" in
	stock) ;;
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
PKGVERSIONS=${PKGVERSIONS:-}
[ -z "$PKGVERSIONS" ] || [ -f "$PKGVERSIONS" ] || {
	echo "build-alpine-packages: $PKGVERSIONS is missing" >&2
	exit 1
}

# The version of package $1, and whether it is built (1) or reused (0).
pkg_version() {
	if [ -n "$PKGVERSIONS" ]; then
		awk -v n="$1" '$1 == n { print $2; found = 1 } END { exit !found }' "$PKGVERSIONS" || {
			echo "build-alpine-packages: $PKGVERSIONS has no version for $1" >&2
			exit 1
		}
	else
		echo "$PKGVER"
	fi
}
pkg_changed() {
	[ -z "$PKGVERSIONS" ] || [ "$(awk -v n="$1" '$1 == n { print $3 }' "$PKGVERSIONS")" = 1 ]
}

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

for required in base kernel-sg2002 kmod-sg2002 firmware-sg2002 bootloader-sg2002 app web zlib openssl exfatprogs i2c-tools; do
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
			echo "build-alpine-packages: release metadata does not match $PROFILE; supply matching payloads or select their recorded profile" >&2
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
	pkg_changed "$pkg" || return 0
	version=$(pkg_version "$pkg")
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
archive_payload nanokvm-bootloader-sg2002 "$PROFILE_ROOT/bootloader-sg2002"
archive_payload nanokvm "$PROFILE_ROOT/app"
archive_payload nanokvm-web "$PROFILE_ROOT/web"
archive_payload nanokvm-exfatprogs "$PROFILE_ROOT/exfatprogs"
archive_payload nanokvm-i2c-tools "$PROFILE_ROOT/i2c-tools"
archive_payload nanokvm-zlib "$PROFILE_ROOT/zlib"
archive_payload nanokvm-openssl "$PROFILE_ROOT/openssl"
release_payload=$(make_release_payload)
archive_payload nanokvm-release "$release_payload"

export CARCH=riscv64
export REPODEST SRCDEST PKGVER
# The versions of the other packages, for the dependencies of nanokvm-app and
# nanokvm-release: V_nanokvm_web for nanokvm-web, and so on.
for pkg in nanokvm-keys nanokvm-base nanokvm-kernel-sg2002 nanokvm-kmod-sg2002 nanokvm-firmware-sg2002 nanokvm-bootloader-sg2002 \
	 nanokvm nanokvm-web nanokvm-app nanokvm-zlib nanokvm-openssl nanokvm-exfatprogs nanokvm-i2c-tools nanokvm-release; do
	export "V_$(echo "$pkg" | tr - _)=$(pkg_version "$pkg")"
done

# APK v2's historical RSA256 marker selects SHA-256 in apk-tools; OpenSSL
# selects the signature algorithm from the PEM key. With our validated EC key
# the signature is ECDSA/SHA-256, NOT RSA. Keep v2 for abuild's metadata/scripts.
# Both the packages and APKINDEX.tar.gz are independently verifiable with EC.
: "${PACKAGER_PRIVKEY:?set the ECDSA private key}"
: "${PACKAGER_PUBKEY:?set its public key}"
"$ROOT/scripts/check-ec-key.sh" "$PACKAGER_PRIVKEY" "$PACKAGER_PUBKEY"
ABUILD_SIGN_REAL=$(command -v abuild-sign)
export ABUILD_SIGN_REAL PACKAGER_PRIVKEY PACKAGER_PUBKEY
mkdir -p "$WORK/bin"
cat > "$WORK/bin/abuild-sign" <<'SIGN'
#!/bin/sh
exec "$ABUILD_SIGN_REAL" -t RSA256 -k "$PACKAGER_PRIVKEY" -p "$PACKAGER_PUBKEY" "$@"
SIGN
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
	[ "$pkg" != nanokvm-app ] || return 0
	# A source name without an URL is local to the APKBUILD directory. Keep the
	# generated archive there so both `abuild checksum` and the actual build read
	# the same bytes; SRCDEST remains the persistent archive cache/output.
	version=$(pkg_version "$pkg")
	cp "$SRCDEST/$pkg-$version.tar.gz" "$recipe_dir/"
	(
		cd "$recipe_dir"
		PKGVER=$version
		export PKGVER
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

for pkg in nanokvm-keys nanokvm-base nanokvm-kernel-sg2002 nanokvm-kmod-sg2002 nanokvm-firmware-sg2002 nanokvm-bootloader-sg2002 \
	 nanokvm nanokvm-web nanokvm-app nanokvm-zlib nanokvm-openssl nanokvm-exfatprogs nanokvm-i2c-tools nanokvm-release; do
	version=$(pkg_version "$pkg")
	if ! pkg_changed "$pkg"; then
		# abuild indexes every package in the directory, this one included.
		[ -f "$REPODEST/recipes/$CARCH/$pkg-$version-r0.apk" ] || {
			echo "build-alpine-packages: unchanged $pkg-$version-r0.apk is not in $REPODEST/recipes/$CARCH" >&2
			exit 1
		}
		echo "keeping $pkg $version (unchanged)"
		continue
	fi
	echo "building $pkg $version ($PROFILE)"
	prepare_recipe "$pkg"
	(
		cd "$WORK/recipes/$pkg"
		PKGVER=$version
		export PKGVER
		run_abuild -r
		verify_keys=$WORK/verify-keys
		mkdir -p "$verify_keys"
		cp "$PACKAGER_PUBKEY" "$verify_keys/"
		apk --keys-dir "$verify_keys" verify "$REPODEST/recipes/$CARCH/$pkg-$version-r0.apk"
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
	echo "build-alpine-packages: APKINDEX.tar.gz has no SHA-256 signature: $index" >&2
	exit 1
}
verify_keys=$WORK/verify-keys
mkdir -p "$verify_keys"
cp "$PACKAGER_PUBKEY" "$verify_keys/"
apk --keys-dir "$verify_keys" verify "$index"
apk_count=$(find "$REPODEST" -type f -name '*.apk' | wc -l)
[ "$apk_count" -ge 14 ] || {
	echo "build-alpine-packages: expected at least fourteen APKs, found $apk_count" >&2
	exit 1
}
find "$REPODEST" -type f -name '*.apk' -print | while IFS= read -r apk; do
	if ! tar -tzf "$apk" | grep -qE '(^|/)\.SIGN\.RSA256\.'; then
		echo "build-alpine-packages: APK without a SHA-256 signature: $apk" >&2
		exit 1
	fi
done
echo "verified APK index: $index ($apk_count packages)"
echo "APK repository written below $REPODEST"
