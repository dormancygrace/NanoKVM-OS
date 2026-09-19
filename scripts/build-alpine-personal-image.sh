#!/bin/sh
# Build one request-specific NanoKVM Alpine update bundle.
set -eu

PROGRAM=${0##*/}
PROJECT_ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
UPDATE_BUILDER=$PROJECT_ROOT/scripts/build-alpine-update-bundle.sh

usage() {
	cat <<'EOF'
Usage:
  build-alpine-personal-image.sh [options]

Required inputs:
  --profile stock|c906-scalar
  --base-rootfs FILE          Clean Alpine riscv64 minirootfs/root tarball
  --base-sha256 SHA256        Expected hash of --base-rootfs
  --boot-fit FILE             Normal NanoKVM Alpine boot.sd
  --boot-sha256 SHA256        Expected hash of --boot-fit
  --nanokvm-repo URL          Signed repository containing nanokvm-release
  --repo-key FILE             Trusted APK public key (repeatable)
  --output DIRECTORY          New update bundle directory

Packages and repositories:
  --package NAME              Add one APK package (repeatable)
  --packages-file FILE        Add package names, one per line; blank lines and
                              lines beginning with # are ignored
  --tuned-repo URL            Signed C906 repository; required for c906-scalar
  --runtime-nanokvm-repo URL  Device-visible URL (defaults to --nanokvm-repo)
  --runtime-tuned-repo URL    Device-visible URL (defaults to --tuned-repo)
  --alpine-version X.Y        Alpine branch (default: 3.24)
  --alpine-main URL           Override Alpine main repository
  --alpine-community URL      Override Alpine community repository

Execution:
  --qemu-static FILE          qemu-riscv64-static for a non-riscv64 builder
  --validate-only             Validate and print the canonical request only
  -h, --help                  Show this help

Only unversioned APK names are accepted in a request. nanokvm-release is
always installed. Repository URLs must use http:// or https://. The output
directory must not already exist.
EOF
}

die() {
	echo "$PROGRAM: $*" >&2
	exit 2
}

need_value() {
	if [ "$#" -lt 2 ] || [ -z "$2" ]; then
		die "$1 requires a value"
	fi
}

valid_sha256() {
	[ "${#1}" -eq 64 ] || return 1
	case "$1" in *[!0-9A-Fa-f]*) return 1 ;; esac
}

valid_package() {
	package=$1
	[ "${#package}" -le 128 ] || return 1
	printf '%s\n' "$package" | grep -Eq '^[a-z0-9][a-z0-9+_.-]*$'
}

valid_repo() {
	repo=$1
	case "$repo" in
		http://?*|https://?*) ;;
		*) return 1 ;;
	esac
	case "$repo" in *[[:space:]]*) return 1 ;; esac
}

PROFILE=
BASE_ROOTFS=
BASE_SHA256=
BOOT_FIT=
BOOT_SHA256=
NANOKVM_REPO=
TUNED_REPO=
RUNTIME_NANOKVM_REPO=
RUNTIME_TUNED_REPO=
ALPINE_VERSION=3.24
ALPINE_MAIN=
ALPINE_COMMUNITY=
OUTPUT=
QEMU_STATIC=
VALIDATE_ONLY=0

PARSE_TMP=$(mktemp -d "${TMPDIR:-/tmp}/nanokvm-personal-request.XXXXXX")
PACKAGES_RAW=$PARSE_TMP/packages.raw
KEYS_RAW=$PARSE_TMP/keys.raw
: > "$PACKAGES_RAW"
: > "$KEYS_RAW"
BUILD_TMP=

cleanup() {
	if [ -n "${STAGE_ROOT:-}" ]; then
		umount "$STAGE_ROOT/sys" 2>/dev/null || true
		umount "$STAGE_ROOT/proc" 2>/dev/null || true
		umount "$STAGE_ROOT/dev" 2>/dev/null || true
	fi
	[ -z "$BUILD_TMP" ] || rm -rf "$BUILD_TMP"
	rm -rf "$PARSE_TMP"
}
trap cleanup EXIT INT TERM HUP

while [ "$#" -gt 0 ]; do
	case "$1" in
		--profile) need_value "$@"; PROFILE=$2; shift 2 ;;
		--base-rootfs) need_value "$@"; BASE_ROOTFS=$2; shift 2 ;;
		--base-sha256) need_value "$@"; BASE_SHA256=$2; shift 2 ;;
		--boot-fit) need_value "$@"; BOOT_FIT=$2; shift 2 ;;
		--boot-sha256) need_value "$@"; BOOT_SHA256=$2; shift 2 ;;
		--nanokvm-repo) need_value "$@"; NANOKVM_REPO=$2; shift 2 ;;
		--runtime-nanokvm-repo) need_value "$@"; RUNTIME_NANOKVM_REPO=$2; shift 2 ;;
		--runtime-tuned-repo) need_value "$@"; RUNTIME_TUNED_REPO=$2; shift 2 ;;
		--tuned-repo) need_value "$@"; TUNED_REPO=$2; shift 2 ;;
		--alpine-version) need_value "$@"; ALPINE_VERSION=$2; shift 2 ;;
		--alpine-main) need_value "$@"; ALPINE_MAIN=$2; shift 2 ;;
		--alpine-community) need_value "$@"; ALPINE_COMMUNITY=$2; shift 2 ;;
		--repo-key)
			need_value "$@"
			[ "$2" = "$(printf '%s' "$2" | tr -d '\r\n')" ] ||
				die "--repo-key contains a newline"
			printf '%s\n' "$2" >> "$KEYS_RAW"
			shift 2
			;;
		--package) need_value "$@"; printf '%s\n' "$2" >> "$PACKAGES_RAW"; shift 2 ;;
		--packages-file)
			need_value "$@"
			[ -f "$2" ] || die "package file does not exist: $2"
			while IFS= read -r line || [ -n "$line" ]; do
				line=$(printf '%s' "$line" | tr -d '\r')
				case "$line" in ''|'#'*) continue ;; esac
				printf '%s\n' "$line" >> "$PACKAGES_RAW"
			done < "$2"
			shift 2
			;;
		--output) need_value "$@"; OUTPUT=$2; shift 2 ;;
		--qemu-static) need_value "$@"; QEMU_STATIC=$2; shift 2 ;;
		--validate-only) VALIDATE_ONLY=1; shift ;;
		-h|--help) usage; exit 0 ;;
		--) shift; [ "$#" -eq 0 ] || die "positional arguments are not accepted" ;;
		-*) die "unknown option: $1" ;;
		*) die "positional arguments are not accepted: $1" ;;
	esac
done

case "$PROFILE" in
	stock) [ -z "$TUNED_REPO" ] || die "--tuned-repo is only valid with c906-scalar" ;;
	c906-scalar) [ -n "$TUNED_REPO" ] || die "c906-scalar requires --tuned-repo" ;;
	*) die "--profile must be stock or c906-scalar" ;;
esac
[ -n "$BASE_ROOTFS" ] || die "--base-rootfs is required"
valid_sha256 "$BASE_SHA256" || die "--base-sha256 must be 64 hexadecimal characters"
[ -n "$BOOT_FIT" ] || die "--boot-fit is required"
valid_sha256 "$BOOT_SHA256" || die "--boot-sha256 must be 64 hexadecimal characters"
[ -n "$NANOKVM_REPO" ] || die "--nanokvm-repo is required"
valid_repo "$NANOKVM_REPO" || die "invalid NanoKVM repository URL"
[ -z "$RUNTIME_NANOKVM_REPO" ] || valid_repo "$RUNTIME_NANOKVM_REPO" || die "invalid runtime NanoKVM URL"
[ -z "$RUNTIME_TUNED_REPO" ] || valid_repo "$RUNTIME_TUNED_REPO" || die "invalid runtime tuned URL"
[ -z "$TUNED_REPO" ] || valid_repo "$TUNED_REPO" || die "invalid tuned repository URL"
printf '%s\n' "$ALPINE_VERSION" | grep -Eq '^[0-9]+\.[0-9]+$' || die "invalid Alpine version"
[ -n "$OUTPUT" ] || die "--output is required"
for path_value in "$BASE_ROOTFS" "$BOOT_FIT" "$OUTPUT" "$QEMU_STATIC"; do
	[ "$path_value" = "$(printf '%s' "$path_value" | tr -d '\r\n')" ] ||
		die "file and directory paths must be single-line values"
done
[ -s "$KEYS_RAW" ] || die "at least one --repo-key is required"

ALPINE_MAIN=${ALPINE_MAIN:-https://dl-cdn.alpinelinux.org/alpine/v$ALPINE_VERSION/main}
ALPINE_COMMUNITY=${ALPINE_COMMUNITY:-https://dl-cdn.alpinelinux.org/alpine/v$ALPINE_VERSION/community}
valid_repo "$ALPINE_MAIN" || die "invalid Alpine main repository URL"
valid_repo "$ALPINE_COMMUNITY" || die "invalid Alpine community repository URL"

PACKAGES=$PARSE_TMP/packages
while IFS= read -r package; do
	valid_package "$package" || die "invalid APK package name: $package"
done < "$PACKAGES_RAW"
sort -u "$PACKAGES_RAW" > "$PACKAGES"
package_count=$(wc -l < "$PACKAGES" | tr -d ' ')
[ "$package_count" -le 256 ] || die "a request may contain at most 256 packages"

while IFS= read -r key; do
	[ -n "$key" ] || die "empty repository key path"
	key_name=${key##*/}
	case "$key_name" in
		*.pub) ;;
		*) die "APK public key filename must end in .pub: $key_name" ;;
	esac
	case "$key_name" in
		*[!A-Za-z0-9_.@+-]*) die "unsafe APK key filename: $key_name" ;;
	esac
done < "$KEYS_RAW"

REPOSITORIES=$PARSE_TMP/repositories
{
	[ -z "$TUNED_REPO" ] || printf '%s\n' "$TUNED_REPO"
	printf '%s\n' "$NANOKVM_REPO" "$ALPINE_MAIN" "$ALPINE_COMMUNITY"
} > "$REPOSITORIES"
BASE_REPOSITORIES=$PARSE_TMP/base-repositories
printf '%s\n' "$NANOKVM_REPO" "$ALPINE_MAIN" "$ALPINE_COMMUNITY" \
	> "$BASE_REPOSITORIES"

if [ "$VALIDATE_ONLY" -eq 1 ]; then
	echo "profile=$PROFILE"
	echo "alpine_version=$ALPINE_VERSION"
	echo "base_sha256=$(printf '%s' "$BASE_SHA256" | tr 'A-F' 'a-f')"
	echo "boot_sha256=$(printf '%s' "$BOOT_SHA256" | tr 'A-F' 'a-f')"
	echo "repositories:"
	sed 's/^/  /' "$REPOSITORIES"
	echo "requested_packages:"
	if [ -s "$PACKAGES" ]; then sed 's/^/  /' "$PACKAGES"; else echo "  (none)"; fi
	echo "implicit_package: nanokvm-release"
	exit 0
fi

[ "$(id -u)" -eq 0 ] || die "the image build must run as root"
[ -f "$BASE_ROOTFS" ] || die "base rootfs does not exist: $BASE_ROOTFS"
[ -f "$BOOT_FIT" ] || die "boot FIT does not exist: $BOOT_FIT"
[ -x "$UPDATE_BUILDER" ] || die "missing update builder: $UPDATE_BUILDER"
command -v chroot >/dev/null 2>&1 || die "chroot is required"
command -v mount >/dev/null 2>&1 || die "mount is required"
command -v python3 >/dev/null 2>&1 || die "python3 is required by the recovery builder"

actual=$(sha256sum "$BASE_ROOTFS")
actual=${actual%% *}
[ "$actual" = "$(printf '%s' "$BASE_SHA256" | tr 'A-F' 'a-f')" ] || die "base rootfs SHA256 mismatch"
actual=$(sha256sum "$BOOT_FIT")
actual=${actual%% *}
[ "$actual" = "$(printf '%s' "$BOOT_SHA256" | tr 'A-F' 'a-f')" ] || die "boot FIT SHA256 mismatch"

OUTPUT_PARENT=$(dirname -- "$OUTPUT")
mkdir -p "$OUTPUT_PARENT"
[ ! -e "$OUTPUT" ] || die "output already exists: $OUTPUT"
BUILD_TMP=$(mktemp -d "$OUTPUT_PARENT/.nanokvm-personal.XXXXXX")
STAGE_ROOT=$BUILD_TMP/root
BUNDLE=$BUILD_TMP/bundle
mkdir -p "$STAGE_ROOT"
tar --numeric-owner -xzf "$BASE_ROOTFS" -C "$STAGE_ROOT"
[ -f "$STAGE_ROOT/etc/alpine-release" ] || die "base input is not an Alpine root"
[ -e "$STAGE_ROOT/lib/ld-musl-riscv64.so.1" ] || die "base input is not Alpine riscv64"
installed_release=$(cat "$STAGE_ROOT/etc/alpine-release")
case "$installed_release" in
	"$ALPINE_VERSION".*|"$ALPINE_VERSION") ;;
	*) die "base Alpine release $installed_release does not match $ALPINE_VERSION" ;;
esac

mkdir -p "$STAGE_ROOT/etc/apk/keys"
KEY_NAMES=$PARSE_TMP/key-names
: > "$KEY_NAMES"
while IFS= read -r key; do
	[ -f "$key" ] || die "repository key does not exist: $key"
	key_name=${key##*/}
	if grep -qxF "$key_name" "$KEY_NAMES"; then
		die "duplicate APK key filename: $key_name"
	fi
	printf '%s\n' "$key_name" >> "$KEY_NAMES"
	install -m 0644 "$key" "$STAGE_ROOT/etc/apk/keys/$key_name"
done < "$KEYS_RAW"
if [ "$PROFILE" = c906-scalar ]; then
	# Complete configuration scripts with official Alpine userspace first.
	install -m 0644 "$BASE_REPOSITORIES" "$STAGE_ROOT/etc/apk/repositories"
else
	install -m 0644 "$REPOSITORIES" "$STAGE_ROOT/etc/apk/repositories"
fi

TARGET_PREFIX=
case "$(uname -m)" in
	riscv64) ;;
	*)
		if [ -z "$QEMU_STATIC" ]; then
			QEMU_STATIC=$(command -v qemu-riscv64-static || true)
		fi
		if [ -z "$QEMU_STATIC" ] || [ ! -x "$QEMU_STATIC" ]; then
			die "non-riscv64 builder requires --qemu-static"
		fi
		install -m 0755 "$QEMU_STATIC" "$STAGE_ROOT/usr/bin/qemu-riscv64-static"
		TARGET_PREFIX=/usr/bin/qemu-riscv64-static
		;;
esac

mkdir -p "$STAGE_ROOT/dev" "$STAGE_ROOT/proc" "$STAGE_ROOT/sys" "$STAGE_ROOT/run" "$STAGE_ROOT/tmp"
mount --bind /dev "$STAGE_ROOT/dev"
mount -t proc proc "$STAGE_ROOT/proc"
mount -t sysfs sysfs "$STAGE_ROOT/sys"

RESOLV_BACKUP=$BUILD_TMP/resolv.conf.original
if [ -e "$STAGE_ROOT/etc/resolv.conf" ] || [ -L "$STAGE_ROOT/etc/resolv.conf" ]; then
	cp -a "$STAGE_ROOT/etc/resolv.conf" "$RESOLV_BACKUP"
fi
rm -f "$STAGE_ROOT/etc/resolv.conf"
cp -L /etc/resolv.conf "$STAGE_ROOT/etc/resolv.conf"

run_target() {
	if [ -n "$TARGET_PREFIX" ]; then
		chroot "$STAGE_ROOT" "$TARGET_PREFIX" "$@"
	else
		chroot "$STAGE_ROOT" "$@"
	fi
}

run_target /sbin/apk --no-cache update
set -- nanokvm-release
while IFS= read -r package; do
	[ "$package" = nanokvm-release ] || set -- "$@" "$package"
done < "$PACKAGES"
run_target /sbin/apk --no-cache add "$@"

if [ "$PROFILE" = c906-scalar ]; then
	install -m 0644 "$REPOSITORIES" "$STAGE_ROOT/etc/apk/repositories"
	run_target /sbin/apk --no-cache update
	if [ -n "$TARGET_PREFIX" ]; then
		# The overlay changes code generation and pkgrel, while the matching
		# stock packages have already completed their configuration scripts.
		# Do not execute C906-specific /bin/sh under qemu-user.
		run_target /sbin/apk --no-cache upgrade --available --no-scripts
	else
		run_target /sbin/apk --no-cache upgrade --available
	fi
fi

# Build transport can stay local; installed repositories must be reachable by the device.
{
    [ -z "$TUNED_REPO" ] || printf '%s\n' "${RUNTIME_TUNED_REPO:-$TUNED_REPO}"
    printf '%s\n' "${RUNTIME_NANOKVM_REPO:-$NANOKVM_REPO}" "$ALPINE_MAIN" "$ALPINE_COMMUNITY"
} > "$REPOSITORIES"
install -m 0644 "$REPOSITORIES" "$STAGE_ROOT/etc/apk/repositories"

umount "$STAGE_ROOT/sys"
umount "$STAGE_ROOT/proc"
umount "$STAGE_ROOT/dev"
rm -f "$STAGE_ROOT/etc/resolv.conf"
[ ! -e "$RESOLV_BACKUP" ] || cp -a "$RESOLV_BACKUP" "$STAGE_ROOT/etc/resolv.conf"
[ -z "$TARGET_PREFIX" ] || rm -f "$STAGE_ROOT$TARGET_PREFIX"

INSTALLED=$PARSE_TMP/installed-packages
awk -F: '
	/^P:/ { package = substr($0, 3) }
	/^V:/ { print package "=" substr($0, 3) }
' "$STAGE_ROOT/lib/apk/db/installed" | LC_ALL=C sort > "$INSTALLED"

# These entry points are called by existing settings APIs. Refuse an incomplete
# release before it can be written to a card.
for path in usr/sbin/iw usr/sbin/nanokvm_update_edid etc/init.d/nanokvm-policy \
            usr/libexec/nanokvm/legacy/S50sshd usr/libexec/nanokvm/legacy/S38memory \
            usr/libexec/nanokvm/legacy/S34mssclamp kvmapp/system/bin/usb-audio-capture; do
    [ -x "$STAGE_ROOT/$path" ] || die "missing required runtime entry point: $path"
done
grep -q 'flavour=enhanced' "$STAGE_ROOT/etc/nanokvm-buildroot" ||
    die "accepted video runtime policy marker is missing"
python3 - "$STAGE_ROOT/usr/share/nanokvm/edid" <<'PY'
from pathlib import Path
import sys
profiles = list(Path(sys.argv[1]).glob('*.bin'))
if not profiles: raise SystemExit('no EDID profiles')
for path in profiles:
    data=path.read_bytes()
    if len(data) < 128 or len(data)%128 or data[:8] != bytes.fromhex('00ffffffffffff00'):
        raise SystemExit('invalid EDID structure: '+path.name)
    if any(sum(data[i:i+128])%256 for i in range(0,len(data),128)):
        raise SystemExit('invalid EDID checksum: '+path.name)
print('Validated EDID profiles:',len(profiles))
PY

# The APK-owned boot set is authoritative and matches the installed module ABI.
if [ -f "$STAGE_ROOT/usr/lib/nanokvm/boot/detect.sd" ]; then
    BOOT_FIT=$STAGE_ROOT/usr/lib/nanokvm/boot/detect.sd
    BOOT_SHA256=$(sha256sum "$BOOT_FIT")
    BOOT_SHA256=${BOOT_SHA256%% *}
    kernel_release=$(cat "$STAGE_ROOT/usr/lib/nanokvm/boot/kernel.release")
    [ -d "$STAGE_ROOT/lib/modules/$kernel_release" ] || die "kernel/module ABI mismatch"
    [ -x "$STAGE_ROOT/usr/sbin/nkos-board-probe" ] || die "board probe missing"
    [ -x "$STAGE_ROOT/usr/sbin/nkos-board-select" ] || die "board selector missing"
fi
ROOTFS=$STAGE_ROOT BOOT_FIT=$BOOT_FIT OUTPUT=$BUNDLE "$UPDATE_BUILDER"

cp "$PACKAGES" "$BUNDLE/requested-packages.txt"
cp "$INSTALLED" "$BUNDLE/installed-packages.txt"
cp "$REPOSITORIES" "$BUNDLE/repositories"
cp "$STAGE_ROOT/etc/apk/world" "$BUNDLE/apk-world.txt"
{
	echo "format=1"
	echo "profile=$PROFILE"
	echo "alpine_version=$ALPINE_VERSION"
	echo "base_rootfs=${BASE_ROOTFS##*/}"
	echo "base_rootfs_sha256=$(printf '%s' "$BASE_SHA256" | tr 'A-F' 'a-f')"
	echo "boot_fit=${BOOT_FIT##*/}"
	echo "boot_fit_sha256=$(printf '%s' "$BOOT_SHA256" | tr 'A-F' 'a-f')"
	echo "requested_package_count=$package_count"
	echo "implicit_package=nanokvm-release"
	echo "nanokvm_payload_profile=$(cat "$STAGE_ROOT/etc/nanokvm-build-profile" 2>/dev/null || echo unknown)"
	if [ "$PROFILE" = c906-scalar ] && [ -n "$TARGET_PREFIX" ]; then
		echo "tuned_install_mode=payload-only-after-stock-scripts"
	elif [ "$PROFILE" = c906-scalar ]; then
		echo "tuned_install_mode=native-with-scripts"
	else
		echo "tuned_install_mode=not-applicable"
	fi
} > "$BUNDLE/request-manifest.txt"
{
	while IFS= read -r key; do sha256sum "$key"; done < "$KEYS_RAW"
} | sed 's#  .*/#  #' | LC_ALL=C sort > "$BUNDLE/trusted-keys.sha256"

(
	cd "$BUNDLE"
	sha256sum alpine-rootfs.tar.gz boot-alpine.sd boot-alpine-recovery.sd \
		recovery-manifest.json \
		request-manifest.txt requested-packages.txt installed-packages.txt \
		repositories apk-world.txt trusted-keys.sha256 > SHA256SUMS
	sha256sum -c SHA256SUMS
)
mv "$BUNDLE" "$OUTPUT"
echo "Personal NanoKVM Alpine update bundle: $OUTPUT"
cat "$OUTPUT/SHA256SUMS"
