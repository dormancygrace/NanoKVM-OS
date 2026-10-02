#!/bin/bash
# Rebuild the released U-Boot (FIP LOADER_2ND) from prepared source and
# compare it with the release binary.
set -euo pipefail
export PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin
[ "$#" -ge 3 ] && [ "$#" -le 4 ] || { echo "Usage: $0 prepared-source new-output cross-prefix [jobs]" >&2; exit 2; }
here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../../.." && pwd)
source_dir=$(realpath "$1")
build_dir=$(realpath -m "$2")
cross_prefix=$3
jobs=${4:-8}
case "$jobs" in ''|*[!0-9]*) exit 2;; esac
[ "$jobs" -ge 1 ] && [ "$jobs" -le 64 ]
case "$build_dir" in "$source_dir"|"$source_dir"/*) echo 'Use an output outside the source tree' >&2; exit 2;; esac
[ ! -e "$build_dir" ] || { echo 'Use a new output directory' >&2; exit 2; }
[ "$("${cross_prefix}gcc" -dumpfullversion)" = 16.2.0 ]
[ "$(make -s -C "$source_dir" ubootversion)" = 2026.07 ]
mkdir -p "$build_dir/.clock"
# The release was built from Git (v2026.07 plus the three patches) without
# SOURCE_DATE_EPOCH, so the version string carries the Git description and
# the workstation time in +0300. Reproduce both.
printf '#!/bin/sh\nexec /bin/date -d @1788737047 "$@"\n' > "$build_dir/.clock/date"
chmod +x "$build_dir/.clock/date"
cp "$repo/firmware/uboot/nanokvm_enhanced_defconfig" "$build_dir/.config"
args=(-C "$source_dir" O="$build_dir" ARCH=riscv CROSS_COMPILE="$cross_prefix" LOCALVERSION=-00003-ga5149dd2536c)
PATH="$build_dir/.clock:$PATH" TZ=Etc/GMT-3 make "${args[@]}" olddefconfig
PATH="$build_dir/.clock:$PATH" TZ=Etc/GMT-3 make "${args[@]}" -j"$jobs"
# Identical results need GCC 16.2.0 with GNU Binutils 2.45.1; Binutils 2.47
# produces a different binary.
"${cross_prefix}ld" --version | head -n1
(cd "$build_dir" && sha256sum -c "$here/EXPECTED-SHA256SUMS")
echo 'u-boot.bin matches the U-Boot in the v2.0 release FIP.'
