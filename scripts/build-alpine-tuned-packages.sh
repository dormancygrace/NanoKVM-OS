#!/bin/sh
# Build the small C906 scalar Alpine package overlay for NanoKVM.
#
# Run this script inside an Alpine v3.24 riscv64 rootfs. On the x86_64
# builder VM that rootfs is entered through qemu-riscv64-static and binfmt.
# The script never edits the checked-out aports tree: each APKBUILD is copied
# into a private work directory before the pkgrel and flags are changed.
set -eu

APORTS_COMMIT=${APORTS_COMMIT:-92921ccb60bf1bea0d4666b2c0533ce22c5fae9e}
APORTS_URL=${APORTS_URL:-https://gitlab.alpinelinux.org/alpine/aports.git}
WORK_DIR=${WORK_DIR:-/tmp/nanokvm-alpine-c906}
APORTS_DIR=${APORTS_DIR:-$WORK_DIR/aports}
OUTPUT_DIR=${OUTPUT_DIR:-$WORK_DIR/repository}
SRCDEST=${SRCDEST:-$WORK_DIR/distfiles}
KEYS_DIR=${KEYS_DIR:-$WORK_DIR/keys}
SIGN_KEY=${SIGN_KEY:-${PACKAGER_PRIVKEY:-}}
PACKAGER_NAME=${PACKAGER_NAME:-NanoKVM Alpine <packages@nanokvm.local>}
CUSTOM_REPOSITORY=${CUSTOM_REPOSITORY:-https://packages.nanokvm.invalid/alpine/v3.24/c906-scalar}
ALPINE_REPOSITORY=${ALPINE_REPOSITORY:-https://dl-cdn.alpinelinux.org/alpine/v3.24}
JOBS=${JOBS:-$(getconf _NPROCESSORS_ONLN 2>/dev/null || printf '%s' 1)}

# Keep this list deliberately small. These are the packages exercised by the
# first C906 comparison (busybox gzip, coreutils sha256sum, compression and
# OpenSSL). musl and apk-tools remain the official Alpine builds.
PACKAGES="${PACKAGES:-busybox coreutils zlib lz4 zstd xz openssl}"

# This is scalar-only C906 tuning. Do not add V, GCV, v0p7 or xtheadvector:
# C906's legacy XTheadVector is incompatible with standard RVV 1.0.
C906_FLAGS=${C906_FLAGS:-'-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync -mtune=thead-c906 -mno-fence-tso -mabi=lp64d'}

if [ "${1:-}" = --help ]; then
	cat <<'EOF'
Usage: SIGN_KEY=/path/key.rsa [WORK_DIR=/writable/path] \
  scripts/build-alpine-tuned-packages.sh

  SIGN_KEY=/path/key.rsa WORK_DIR=/existing/work \
    OUTPUT_DIR=/existing/repository \
    scripts/build-alpine-tuned-packages.sh --finalize-only

Run as an unprivileged Alpine abuild user in an Alpine v3.24 riscv64 rootfs.
EOF
	exit 0
fi

FINALIZE_ONLY=0
if [ "${1:-}" = --finalize-only ]; then
	FINALIZE_ONLY=1
	shift
fi
if [ "$#" -ne 0 ]; then
	echo "build-alpine-tuned-packages: unexpected argument: $1" >&2
	exit 2
fi

die() {
	echo "build-alpine-tuned-packages: $*" >&2
	exit 2
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "required command is missing: $1"
}

march_option=
for flag in $C906_FLAGS; do
	case "$flag" in
		-march=*) march_option=${flag#-march=} ;;
	esac
done
march_base=${march_option%%_*}
march_base=${march_base#rv64}
case "$march_base" in
	*v*) die "C906_FLAGS contains a vector ISA: $C906_FLAGS" ;;
esac
case "$C906_FLAGS" in
	*xtheadvector*|*zve*|*zvl*|*v0p7*)
		die "C906_FLAGS contains a vector ISA: $C906_FLAGS" ;;
esac

[ -r /etc/apk/arch ] || die "run inside an Alpine rootfs"
[ "$(sed -n '1p' /etc/apk/arch)" = riscv64 ] ||
	die "target architecture is $(sed -n '1p' /etc/apk/arch), expected riscv64"

need apk
need abuild
need git
need tar
need sha256sum

[ -n "$SIGN_KEY" ] ||
	die "set SIGN_KEY to the private APK signing key (do not commit it)"
[ -f "$SIGN_KEY" ] || die "signing key does not exist: $SIGN_KEY"

if [ "$FINALIZE_ONLY" -eq 1 ]; then
	[ -d "$OUTPUT_DIR" ] || die "finalize output does not exist: $OUTPUT_DIR"
else
	[ ! -e "$OUTPUT_DIR" ] ||
		die "output already exists; choose a fresh OUTPUT_DIR: $OUTPUT_DIR"
fi
mkdir -p "$WORK_DIR" "$OUTPUT_DIR/riscv64" "$SRCDEST" "$KEYS_DIR"

if [ ! -d "$APORTS_DIR/.git" ]; then
	git clone "$APORTS_URL" "$APORTS_DIR"
fi
git -C "$APORTS_DIR" fetch --quiet --no-tags origin "$APORTS_COMMIT" || true
git -C "$APORTS_DIR" cat-file -e "$APORTS_COMMIT^{commit}" 2>/dev/null ||
	die "pinned aports commit is unavailable: $APORTS_COMMIT"
actual_commit=$(git -C "$APORTS_DIR" rev-parse "$APORTS_COMMIT^{commit}")
[ "$actual_commit" = "$APORTS_COMMIT" ] || die "unexpected aports commit: $actual_commit"
SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-$(git -C "$APORTS_DIR" show -s --format=%ct "$APORTS_COMMIT")}
case "$SOURCE_DATE_EPOCH" in
	''|*[!0-9]*) die "SOURCE_DATE_EPOCH must be an unsigned integer" ;;
esac
export SOURCE_DATE_EPOCH

# abuild signs every APK with PACKAGER_PRIVKEY. apk mkndx below signs the
# repository index with the same key. The public key is copied into a private
# build trust directory; the private key never enters OUTPUT_DIR. Keeping this
# directory under WORK_DIR lets the normal unprivileged abuild user run the
# script without writing /etc/apk/keys.
export PACKAGER="$PACKAGER_NAME"
export PACKAGER_PRIVKEY="$SIGN_KEY"
export REPODEST="$OUTPUT_DIR"
export PKGDEST="$OUTPUT_DIR/riscv64"
export SRCDEST
export CARCH=riscv64
export JOBS

key_name=$(basename "$SIGN_KEY")
export PACKAGER_PUBKEY="$KEYS_DIR/${key_name}.pub"
if [ ! -e "$KEYS_DIR/${key_name}.pub" ]; then
	if command -v openssl >/dev/null 2>&1; then
		openssl rsa -in "$SIGN_KEY" -pubout 2>/dev/null > "$KEYS_DIR/${key_name}.pub" ||
			die "cannot derive public key from $SIGN_KEY"
	else
		die "openssl is required to derive the APK public key"
	fi
fi

if [ "$FINALIZE_ONLY" -eq 0 ]; then
	rm -rf "$WORK_DIR/ports"
	mkdir -p "$WORK_DIR/ports"
	: > "$WORK_DIR/pkgrel.tsv"

	for package in $PACKAGES; do
	case "$package" in
		busybox|coreutils|zlib|lz4|zstd|xz|openssl) ;;
		*) die "package is outside the approved tuning set: $package" ;;
	esac

	port="$WORK_DIR/ports/$package"
	mkdir -p "$port"
	git -C "$APORTS_DIR" archive "$APORTS_COMMIT" "main/$package" |
		tar -xf - -C "$port" --strip-components=2
	[ -f "$port/APKBUILD" ] || die "missing APKBUILD for $package"

	pkgrel=$(sed -n 's/^pkgrel=\([0-9][0-9]*\)$/\1/p' "$port/APKBUILD" | sed -n '1p')
	[ -n "$pkgrel" ] || die "cannot parse pkgrel for $package"
	next_pkgrel=$((pkgrel + 1))
	sed -i "s/^pkgrel=$pkgrel$/pkgrel=$next_pkgrel/" "$port/APKBUILD"

	cat >> "$port/APKBUILD" <<EOF

# NanoKVM c906-scalar overlay, generated by scripts/build-alpine-tuned-packages.sh.
# Keep the recipe's Alpine hardening and optimization flags, then select the
# C906 scalar ISA. The !check policy avoids executing target test binaries
# under qemu-user; final functional tests run on the physical C906.
_nanokvm_c906_flags='$C906_FLAGS'
CFLAGS="\${CFLAGS:+\$CFLAGS }\${_nanokvm_c906_flags}"
CXXFLAGS="\${CXXFLAGS:+\$CXXFLAGS }\${_nanokvm_c906_flags}"
export CFLAGS CXXFLAGS
options="\${options:+\$options }!check"
EOF

	printf '%s\t%s\t%s\n' "$package" "$pkgrel" "$next_pkgrel" \
		>> "$WORK_DIR/pkgrel.tsv"

		(
		cd "$port"
		# The source hashes in the pinned recipe are unchanged; checksum only
		# validates the recipe before abuild starts.
		abuild checksum
		abuild -r
		)
	done
else
	[ -s "$WORK_DIR/pkgrel.tsv" ] ||
		die "missing build metadata for finalize-only: $WORK_DIR/pkgrel.tsv"
fi

# abuild normally writes to PKGDEST. Some abuild versions still add a repo
# directory below REPODEST, so collect those files into the one target index
# directory. The output directory is caller-owned and is never populated from
# a previous build by this script.
find "$OUTPUT_DIR" -type f -name '*.apk' ! -path "$OUTPUT_DIR/riscv64/*" \
	-exec cp -f {} "$OUTPUT_DIR/riscv64/" \;
find "$OUTPUT_DIR" -type f -name '*.apk' -print | sort > "$WORK_DIR/apk-files.txt"
[ -s "$WORK_DIR/apk-files.txt" ] || die "abuild produced no APK files"

# Alpine v3.24 target images still consume the signed APKINDEX.tar.gz emitted
# by abuild. Keep the apk-tools v3 Packages.adb alongside it for newer clients.
abuild_index="$OUTPUT_DIR/ports/riscv64/APKINDEX.tar.gz"
[ -f "$abuild_index" ] || die "abuild produced no signed APKINDEX.tar.gz"
cp "$abuild_index" "$OUTPUT_DIR/riscv64/APKINDEX.tar.gz"
apk_count=$(find "$OUTPUT_DIR/riscv64" -maxdepth 1 -type f -name '*.apk' | wc -l)
index_count=$(tar -xOzf "$OUTPUT_DIR/riscv64/APKINDEX.tar.gz" APKINDEX |
	grep -c '^P:')
[ "$index_count" -eq "$apk_count" ] ||
	die "APKINDEX contains $index_count packages, expected $apk_count"

index="$OUTPUT_DIR/riscv64/Packages.adb"
apk --keys-dir "$KEYS_DIR" --sign-key "$SIGN_KEY" mkndx \
	--output "$index" \
	--description "NanoKVM Alpine v3.24 c906-scalar $APORTS_COMMIT" \
	"$OUTPUT_DIR/riscv64"/*.apk

# Exact package identities are read from .PKGINFO, not inferred from filenames
# (subpackage names can contain hyphens). This is the handoff consumed by the
# image builder and the benchmark log.
manifest="$OUTPUT_DIR/manifest.tsv"
printf 'pkgname\tversion\tarch\tapk\tsha256\n' > "$manifest"
for file in "$OUTPUT_DIR/riscv64"/*.apk; do
	name=$(tar -xOf "$file" .PKGINFO | sed -n 's/^pkgname[[:space:]]*=[[:space:]]*//p' | sed -n '1p')
	version=$(tar -xOf "$file" .PKGINFO | sed -n 's/^pkgver[[:space:]]*=[[:space:]]*//p' | sed -n '1p')
	arch=$(tar -xOf "$file" .PKGINFO | sed -n 's/^arch[[:space:]]*=[[:space:]]*//p' | sed -n '1p')
	case "$arch" in riscv64|noarch) ;; *) arch= ;; esac
	if [ -z "$name" ] || [ -z "$version" ] || [ -z "$arch" ]; then
		die "invalid .PKGINFO in $file"
	fi
	hash=$(sha256sum "$file" | sed -n 's/ .*//p')
	printf '%s\t%s\t%s\t%s\t%s\n' "$name" "$version" "$arch" \
		"$(basename "$file")" "$hash" >> "$manifest"
done

# Verify both signatures before handing the repository to the image builder.
for file in "$OUTPUT_DIR/riscv64"/*.apk; do
	apk --keys-dir "$KEYS_DIR" verify "$file" >/dev/null
done
apk --keys-dir "$KEYS_DIR" --repositories-file /dev/null \
	--repository "$OUTPUT_DIR" \
	policy busybox >/dev/null

cat > "$OUTPUT_DIR/repositories" <<EOF
$CUSTOM_REPOSITORY
$ALPINE_REPOSITORY/main
$ALPINE_REPOSITORY/community
EOF

printf '%s\n' "$APORTS_COMMIT" > "$OUTPUT_DIR/aports-commit"
printf '%s\n' "$C906_FLAGS" > "$OUTPUT_DIR/c906-scalar-flags"
cp "$WORK_DIR/pkgrel.tsv" "$OUTPUT_DIR/pkgrel.tsv"
(
	cd "$OUTPUT_DIR"
	sha256sum riscv64/Packages.adb > Packages.adb.sha256
	sha256sum riscv64/APKINDEX.tar.gz > APKINDEX.tar.gz.sha256
)
echo "signed Alpine c906-scalar repository: $OUTPUT_DIR"
echo "aports commit: $APORTS_COMMIT"
echo "packages: $(find "$OUTPUT_DIR/riscv64" -maxdepth 1 -type f -name '*.apk' | wc -l)"
