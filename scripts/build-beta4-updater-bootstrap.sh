#!/bin/bash
# Build the source-bound beta-4 GUI transport bootstrap. Never installs it.
set -euo pipefail
usage() { echo "usage: $0 PUBLIC_NANOKVM_OS_GIT BETA4_ROOTFS_EXT4 GO BUILDROOT_OUTPUT RELEASE_KEY OUTPUT_NKOS" >&2; exit 2; }
[ "$#" -eq 6 ] || usage
script_dir=$(realpath "$(dirname "$0")")
public_git=$(realpath "$1")
rootfs=$(realpath "$2")
go=$(realpath "$3")
buildroot=$(realpath "$4")
key=$(realpath "$5")
output=$(realpath -m "$6")
[ ! -e "$output" ] || { echo "output already exists" >&2; exit 2; }
[ "$(stat -c %s "$rootfs")" = 1610612736 ] || { echo "wrong beta-4 root partition size" >&2; exit 2; }
tag=v1.0.0-beta.4
commit=f0f65c1372e766d3db2807cbbe8bea06d847b9b8
[ "$(git -C "$public_git" rev-parse "$tag^{commit}")" = "$commit" ] || { echo "wrong public beta-4 tag" >&2; exit 2; }
base=022f1f4e6f7eba53f4fc9bee193c499b93a3a671641cce3e5ad451d8470f9d16
abi=f296357935b98b2d13a0930fd882a39d839bdff0543f817c8beb8d1b1bf215d6
updater_hash=02e606498fc04145fcba5e4d166d03c997a10914bf5ca0f454fbdbcba8bdea5d
[ "$(debugfs -R 'cat /etc/nkos-system-base' "$rootfs" 2>/dev/null)" = "$base" ] || { echo "wrong beta-4 system base" >&2; exit 2; }
[ "$(debugfs -R 'cat /kvmapp/version' "$rootfs" 2>/dev/null)" = 1.0.0-beta.4 ] || { echo "wrong beta-4 rootfs" >&2; exit 2; }
work=$(mktemp -d /tmp/nkos-beta4-bootstrap.XXXXXX)
cleanup() { case "$work" in /tmp/nkos-beta4-bootstrap.*) rm -rf -- "$work";; esac; }
trap cleanup EXIT
mkdir -p "$work/source" "$work/extract" "$work/build/app/dl_lib" "$work/build/app/web" "$work/build/system/usr/sbin"
git -C "$public_git" archive "$tag" | tar -x -C "$work/source"
git -C "$work/source" apply "$script_dir/legacy-beta4-helper-transport.patch"
debugfs -R "rdump /kvmapp/server/dl_lib $work/extract" "$rootfs" >/dev/null 2>&1
debugfs -R "rdump /kvmapp/server/web $work/extract" "$rootfs" >/dev/null 2>&1
debugfs -R "dump /usr/sbin/nkos-update $work/published-updater" "$rootfs" >/dev/null 2>&1
[ "$(sha256sum "$work/published-updater" | cut -d' ' -f1)" = "$updater_hash" ] || { echo "published updater mismatch" >&2; exit 2; }
cp -a "$work/extract/dl_lib/." "$work/build/app/dl_lib/"
cp -a "$work/extract/web/." "$work/build/app/web/"
go_root=$(GOROOT= "$go" env GOROOT)
cross="$buildroot/host/bin/riscv64-buildroot-linux-musl-"
lib="$work/build/app/dl_lib"
(
 cd "$work/source/server"
 GOOS=linux GOARCH=riscv64 GORISCV64=rva20u64 CGO_ENABLED=1 GOEXPERIMENT=boringcrypto \
 GOROOT="$go_root" GOTOOLCHAIN=local CC="${cross}gcc" \
 CGO_CFLAGS='-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector -mtune=thead-c906 -mno-fence-tso -mabi=lp64d' \
 CGO_LDFLAGS="-L$lib -Wl,-rpath-link,$lib -Wl,--enable-new-dtags -Wl,-rpath,\$ORIGIN/dl_lib" \
 "$go" build -trimpath -buildvcs=false -ldflags='-linkmode=external -buildid=' -o "$work/build/app/NanoKVM-Server" .
)
repo=$(realpath "$script_dir/..")
(
 cd "$repo/server"
 GOOS=linux GOARCH=riscv64 GORISCV64=rva20u64 CGO_ENABLED=0 GOROOT="$go_root" GOTOOLCHAIN=local \
 "$go" build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o "$work/build/system/usr/sbin/nkos-update" ./cmd/nkos-update
 "${cross}strip" --strip-debug "$work/build/app/NanoKVM-Server"
 printf '%s\n' "$base" >"$work/system-base"
 "$go" run ./cmd/nkos-package --app "$work/build/app" --system "$work/build/system" --system-base "$work/system-base" \
   --key "$key" --version 1.0.0-beta.4-bootstrap.1 --sequence 10 --output "$output"
)
python3 "$repo/scripts/verify-legacy-bootstrap.py" --public-source "$public_git" --tag "$tag" --package "$output" \
  --native-libs "$work/extract/dl_lib" --system-base "$base" --go "$go" --readelf "${cross}readelf"
echo "Hardware GUI/application health remains untested: $output"
