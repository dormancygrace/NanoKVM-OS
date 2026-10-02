#!/bin/bash
# NanoKVM OS platform build: toolchain, Linux kernel and modules, U-Boot, FIP,
# initramfs and the boot.sd images, from the inputs pinned in sources.lock.
#
#   platform/build.sh [-o OUTPUT] [-j JOBS] [STEP...]
#
# Steps, in order (default: all of them):
#   fetch      download and verify every input in sources.lock
#   toolchain  Buildroot 2026.08: cross toolchain, host tools, initramfs userland
#   kernel     Linux 7.2.6 with kernel/*.patch and kernel/config
#   modules    out-of-tree modules; stage /lib/modules like nanokvm-kmod-sg2002
#   uboot      U-Boot 2026.07 with uboot/*.patch and uboot/defconfig
#   fip        fip.bin: fip/base-fip.bin with the new U-Boot
#   initramfs  initramfs from boot/initramfs.list and the Buildroot userland
#   boot       board device trees and boot.sd images like nanokvm-kernel-sg2002
#   verify     compare the outputs with expected.sha256
# Not in the default list:
#   source     write the corresponding-source archive of the outputs
#
# Results are in OUTPUT/images (default: build/platform/images).
set -euo pipefail
export PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin LC_ALL=C TZ=UTC
umask 022
here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
out=${NANOKVM_PLATFORM_OUT:-$repo/build/platform}
jobs=$(nproc)
usage() { sed -n '2,23p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }
while getopts o:j:h opt; do
    case $opt in o) out=$OPTARG ;; j) jobs=$OPTARG ;; *) usage ;; esac
done
shift $((OPTIND - 1))
out=$(realpath -m "$out")
steps=("$@")
[ "${#steps[@]}" -gt 0 ] || steps=(fetch toolchain kernel modules uboot fip initramfs boot verify)
# Keep builds inside OUTPUT from finding an enclosing Git checkout.
export GIT_CEILING_DIRECTORIES=$out

version=$(sed -n 's/^NANOKVM_VERSION=//p' "$repo/firmware/alpine/release.env")
release=7.2.6-nanokvm-os-r1
# Build times recorded in the binaries. The kernel keeps the v2.0 value so
# that it stays identical to the released kernel.
kernel_timestamp='Sat Sep 19 13:51:57 UTC 2026'
uboot_epoch=1788737047
rtl8733bs_epoch=1788607804
isa='-march=rv64imac_zicsr_zifencei_zacas_zabha_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync -mtune=thead-c906 -mno-fence-tso -fno-tree-vectorize -fno-tree-slp-vectorize'
boards=(detect alpha beta pcie lite)

dl=$out/downloads
br=$out/buildroot
bo=$out/buildroot-output
host=$bo/host/bin
cross=$host/riscv64-buildroot-linux-musl-
img=$out/images
ksrc=$out/kernel/src
kbuild=$out/kernel/build

log() { printf '\n== %s\n' "$*"; }
lock() { awk -v n="$1" '$1 == n' "$here/sources.lock"; }

# Git object id of a file or directory, independent of transport.
object_id() (
    if [ -f "$1" ]; then git hash-object --no-filters "$1"; exit; fi
    scratch=$(mktemp -d)
    trap 'rm -rf "$scratch"' EXIT
    export GIT_DIR=$scratch GIT_WORK_TREE=$1 GIT_INDEX_FILE=$scratch/index
    git init -q
    git -c core.autocrlf=false add -A -f .
    git write-tree
)

# Check that a Git archive in downloads/ holds the pinned objects.
check_git_archive() (
    local name=$1 spec path id
    scratch=$(mktemp -d)
    trap 'rm -rf "$scratch"' EXIT
    tar -xf "$dl/$name.tar" -C "$scratch"
    read -r _ _ _ specs <<< "$(lock "$name")"
    for spec in $specs; do
        path=${spec%%=*} id=${spec#*=}
        [ "$(object_id "$scratch/$path")" = "$id" ] || { echo "$name: $path does not match $id" >&2; exit 1; }
    done
)

fetch() {
    local name url pin specs file clone paths
    mkdir -p "$dl"
    while read -r name url pin specs; do
        case $name in ''|'#'*) continue ;; esac
        if [[ $url != *.git ]]; then
            file=$dl/${url##*/}
            if ! echo "$pin  $file" | sha256sum -c --quiet 2>/dev/null; then
                curl -fL --retry 3 -o "$file.part" "$url"
                echo "$pin  $file.part" | sha256sum -c --quiet
                mv "$file.part" "$file"
            fi
        elif ! [ -f "$dl/$name.tar" ] || ! check_git_archive "$name" 2>/dev/null; then
            clone=$(mktemp -d)
            paths=()
            for spec in $specs; do [ "${spec%%=*}" = . ] || paths+=("${spec%%=*}"); done
            git -C "$clone" init -q
            git -C "$clone" fetch -q --depth 1 --filter=blob:none "$url" "$pin"
            if [ "${#paths[@]}" -gt 0 ]; then
                git -C "$clone" sparse-checkout set --no-cone "${paths[@]/#//}"
            fi
            git -C "$clone" checkout -q FETCH_HEAD
            git -C "$clone" archive --format=tar -o "$dl/$name.tar" HEAD -- "${paths[@]}"
            rm -rf "$clone"
            check_git_archive "$name"
        fi
        echo "$name: verified"
    done < "$here/sources.lock"
}

# Unpack one path of a Git archive to a new directory, resolving symbolic
# links as the v2.0 module build did.
unpack_git() (
    local name=$1 path=$2 dest=$3
    scratch=$(mktemp -d)
    trap 'rm -rf "$scratch"' EXIT
    tar -xf "$dl/$name.tar" -C "$scratch"
    mkdir -p "$dest"
    cp -rL "$scratch/$path/." "$dest/"
)

# Apply <dir>/*.patch in name order, then record the prepared tree in a
# separate <tree>.git, so a change can be saved as the next patch (README).
# The repository stays outside the tree to keep version strings unchanged.
apply_patches() {
    local tree=$1 patch
    for patch in "$2"/*.patch; do
        GIT_CEILING_DIRECTORIES=$(dirname "$tree") git -C "$tree" apply --whitespace=nowarn "$patch"
    done
    rm -rf "$tree.git"
    git --git-dir="$tree.git" --work-tree="$tree" init -q
    git --git-dir="$tree.git" --work-tree="$tree" -c core.autocrlf=false add -A -f
    git --git-dir="$tree.git" --work-tree="$tree" -c gc.auto=0 -c user.name=platform -c user.email=platform@localhost \
        commit -q -m "${tree##*/}: upstream with ${2#"$here"/}/*.patch"
}

toolchain() {
    if [ -e "$bo/.platform-toolchain" ]; then echo "already built in $bo"; return; fi
    # An interrupted run continues incrementally; delete OUTPUT/buildroot* to start over.
    if [ ! -e "$br/.platform-patched" ]; then
        rm -rf "$br" "$bo"
        mkdir -p "$br"
        tar -xf "$dl/buildroot-2026.08.tar.xz" -C "$br" --strip-components=1
        for patch in "$repo"/firmware/buildroot/source-patches/*.patch; do patch -s -d "$br" -p1 < "$patch"; done
        touch "$br/.platform-patched"
    fi
    if [ ! -e "$bo/.config" ]; then
        make -C "$br" O="$bo" BR2_EXTERNAL="$repo/firmware/buildroot" nanokvm_enhanced_defconfig
        # No compiler cache; host Python with lzma for fiptool.
        "$br/utils/config" --file "$bo/.config" --disable CCACHE \
            --enable PACKAGE_HOST_PYTHON3 --enable PACKAGE_HOST_PYTHON3_XZ
        make -C "$br" O="$bo" olddefconfig
    fi
    mkdir -p "$dl/buildroot"
    BR2_DL_DIR=$dl/buildroot make -C "$br" O="$bo" toolchain host-dtc host-kmod host-python3 \
        host-uboot-tools host-zstd busybox e2fsprogs f2fs-tools
    [ "$("${cross}gcc" -dumpfullversion)" = 16.2.0 ]
    touch "$bo/.platform-toolchain"
}

kernel() {
    rm -rf "$out/kernel"
    mkdir -p "$out/kernel" "$kbuild"
    tar -xf "$dl/linux-7.2.6.tar.xz" -C "$out/kernel"
    mv "$out/kernel/linux-7.2.6" "$ksrc"
    apply_patches "$ksrc" "$here/kernel"
    cp "$here/kernel/config" "$kbuild/.config"
    # Host pahole, rustc and bindgen would be recorded in .config; ignore them.
    local make=(make -C "$ksrc" O="$kbuild" ARCH=riscv CROSS_COMPILE="$cross" LOCALVERSION= "KCFLAGS=$isa"
                PAHOLE=nkos-no-pahole RUSTC=nkos-no-rustc BINDGEN=nkos-no-bindgen)
    KBUILD_BUILD_USER=nanokvm KBUILD_BUILD_HOST=builder KBUILD_BUILD_VERSION=1 \
        KBUILD_BUILD_TIMESTAMP=$kernel_timestamp "${make[@]}" olddefconfig
    cmp "$here/kernel/config" "$kbuild/.config"
    KBUILD_BUILD_USER=nanokvm KBUILD_BUILD_HOST=builder KBUILD_BUILD_VERSION=1 \
        KBUILD_BUILD_TIMESTAMP=$kernel_timestamp "${make[@]}" -j"$jobs" Image modules
    [ "$(cat "$kbuild/include/config/kernel.release")" = "$release" ]
    mkdir -p "$img"
    cp "$kbuild/arch/riscv/boot/Image" "$img/Image"
}

modules() {
    local m=$out/modules src name module symbols=() maps target
    rm -rf "$m" "${img:?}/lib"
    src=$m/sources
    unpack_git osdrv . "$src/osdrv"
    unpack_git aic8800 src/SDIO/driver_fw/driver/aic8800 "$src/wifi"
    unpack_git cryptodev . "$src/cryptodev"
    cp -r "$here/modules/sg2002-aes" "$src/aes"
    unpack_git sipeed-sdk osdrv/extdrv/wireless/rtl8733bs "$m/rtl8733bs"
    apply_patches "$src/osdrv" "$here/modules/osdrv"
    apply_patches "$src/wifi" "$here/modules/aic8800"
    apply_patches "$src/cryptodev" "$here/modules/cryptodev"
    apply_patches "$m/rtl8733bs" "$here/modules/rtl8733bs"

    # __FILE__ is kept in stripped modules; map paths as the v2.0 build did.
    maps="-ffile-prefix-map=$ksrc=./linux -ffile-prefix-map=$kbuild=./linux-build -ffile-prefix-map=$src=./modules -ffile-prefix-map=$bo=./toolchain"
    build() {
        local dir=$1; shift
        (cd "$dir" && PWD=$dir make -C "$ksrc" O="$kbuild" ARCH=riscv CROSS_COMPILE="$cross" \
            "KCFLAGS=$isa $maps" M="$dir" "$@" -j"$jobs" modules)
    }
    for name in sys base cif vi vpss vcodec jpeg cvi_vc_drv ive dwa rgn snsr_i2c; do
        module=$src/osdrv/interdrv/$name
        build "$module" CVIARCH=CV181X CVIARCH_L=cv181x "KBUILD_EXTRA_SYMBOLS=${symbols[*]}"
        symbols+=("$module/Module.symvers")
    done
    build "$src/wifi" CONFIG_PLATFORM_UBUNTU=n CONFIG_SDIO_BT=y CONFIG_AIC8800_BTLPM_SUPPORT=n CONFIG_USE_FW_REQUEST=y
    build "$src/aes"
    build "$src/cryptodev"
    # Vendor diagnostics use __DATE__ and __TIME__.
    KBUILD_BUILD_USER=nanokvm KBUILD_BUILD_HOST=builder SOURCE_DATE_EPOCH=$rtl8733bs_epoch \
        make -C "$ksrc" O="$kbuild" ARCH=riscv CROSS_COMPILE="$cross" M="$m/rtl8733bs" \
        "KCFLAGS=$isa -ffile-prefix-map=$ksrc=./linux -ffile-prefix-map=$kbuild=./linux-build -ffile-prefix-map=$m/rtl8733bs=./rtl8733bs" \
        CONFIG_PLATFORM_I386_PC=n \
        'USER_EXTRA_CFLAGS=-DCONFIG_LITTLE_ENDIAN -DCONFIG_IOCTL_CFG80211 -DRTW_USE_CFG80211_STA_EVENT' \
        -j"$jobs" modules

    # Staged like the package: in-tree modules as built, extra modules stripped.
    make -C "$ksrc" O="$kbuild" ARCH=riscv CROSS_COMPILE="$cross" INSTALL_MOD_PATH="$img" DEPMOD=true modules_install
    rm -f "$img/lib/modules/$release/build" "$img/lib/modules/$release/source"
    mkdir -p "$img/lib/modules/$release/extra"
    while IFS= read -r -d '' module; do
        target=$img/lib/modules/$release/extra/${module##*/}
        [ ! -e "$target" ] || { echo "Duplicate module name: $target" >&2; exit 1; }
        cp "$module" "$target"
        "${cross}strip" --strip-debug "$target"
    done < <(find "$src/osdrv/interdrv" "$src/wifi" "$src/aes" "$src/cryptodev" "$m/rtl8733bs" -name '*.ko' -print0)
    "$bo/host/sbin/depmod" -b "$img" -e -F "$kbuild/System.map" "$release"
}

uboot() {
    local u=$out/uboot
    rm -rf "$u"
    mkdir -p "$u/build" "$img"
    tar -xjf "$dl/u-boot-2026.07.tar.bz2" -C "$u"
    mv "$u/u-boot-2026.07" "$u/src"
    apply_patches "$u/src" "$here/uboot"
    cp "$here/uboot/defconfig" "$u/build/.config"
    local make=(make -C "$u/src" O="$u/build" ARCH=riscv CROSS_COMPILE="$cross" LOCALVERSION=-nanokvm-os)
    SOURCE_DATE_EPOCH=$uboot_epoch "${make[@]}" olddefconfig
    SOURCE_DATE_EPOCH=$uboot_epoch "${make[@]}" -j"$jobs"
    cp "$u/build/u-boot.bin" "$img/u-boot.bin"
}

fip() {
    local f=$out/fip
    rm -rf "$f"
    mkdir -p "$f"
    tar -xf "$dl/sipeed-sdk.tar" -C "$f" fsbl/plat/cv181x/fiptool.py
    # FSBL enters U-Boot at 0x80200000 after a 32-byte loader header. The base
    # FIP keeps BL2, BLCP, DDR parameters and OpenSBI byte for byte.
    "$host/python3" - "$f/fsbl/plat/cv181x/fiptool.py" "$here/fip/base-fip.bin" "$img/u-boot.bin" "$f" <<'PY'
import binascii, importlib.util, lzma, struct, subprocess, sys
from pathlib import Path
tool, base, uboot, work = map(Path, sys.argv[1:])
text_base = 0x80200000
raw = work / 'u-boot-raw.bin'
raw.write_bytes(struct.pack('<I4sIIQII', 0, b'BL33', 0, 32 + uboot.stat().st_size, text_base - 32, 0, 0) + uboot.read_bytes())
subprocess.run([sys.executable, str(tool), 'genfip', '--OLD_FIP', str(base), '--LOADER_2ND', str(raw),
                '--compress', 'lzma', str(work / 'fip.bin')], check=True, stdout=subprocess.DEVNULL)
spec = importlib.util.spec_from_file_location('fiptool', tool)
fiptool = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fiptool)
def parts(path):
    f = fiptool.FIP()
    f.read_fip(str(path))
    return {k: bytes(v.content) for table in (f.body1, f.body2) for k, v in table.items()}
old, new = parts(base), parts(work / 'fip.bin')
for part in ('BL2', 'BLCP', 'DDR_PARAM', 'BLCP_2ND', 'MONITOR'):
    assert old[part] == new[part], part
loader = new['LOADER_2ND']
fields = struct.unpack('<I4sIIQII', loader[:32])
assert fields[4] + 32 == text_base
assert fields[2] == (0xcafe0000 | binascii.crc_hqx(loader[12:fields[3]], 0))
assert lzma.LZMADecompressor(format=lzma.FORMAT_ALONE).decompress(loader[32:]) == uboot.read_bytes()
PY
    cp "$f/fip.bin" "$img/fip.bin"
}

initramfs() {
    local i=$out/initramfs
    rm -rf "$i"
    mkdir -p "$i" "$img"
    sed -e "s|@TARGET@|$bo/target|g" -e "s|@BOOT@|$here/boot|g" "$here/boot/initramfs.list" > "$i/initramfs.list"
    # Every shared library that the programs need must be in the list.
    local file lib
    for file in $(awk '$1 == "file" { print $3 }' "$i/initramfs.list"); do
        "${cross}readelf" -h "$file" > /dev/null 2>&1 || continue
        for lib in $("${cross}readelf" -d "$file" | sed -n 's/.*(NEEDED).*\[\(.*\)\]/\1/p'); do
            grep -q "^file /lib/$lib " "$i/initramfs.list" || { echo "initramfs lacks /lib/$lib for $file" >&2; exit 1; }
        done
    done
    "$kbuild/usr/gen_init_cpio" -t 0 "$i/initramfs.list" > "$i/initramfs.cpio"
    "$host/zstd" -q -f -19 -T1 "$i/initramfs.cpio" -o "$img/initramfs.cpio.zst"
}

boot() {
    local b=$out/boot profile mode name dtb
    rm -rf "$b" "${img:?}/boot" "${img:?}/dtb"
    mkdir -p "$b" "$img/boot" "$img/dtb"
    "$host/zstd" -q -f -19 -T1 "$img/Image" -o "$b/Image.zst"
    for profile in "${boards[@]}"; do
        "${cross}cpp" -P -nostdinc -undef -D__DTS__ -x assembler-with-cpp \
            -I"$ksrc/arch/riscv/boot/dts/sophgo" -I"$ksrc/include" -I"$ksrc/scripts/dtc/include-prefixes" \
            "$repo/firmware/boards/sg2002-nanokvm-$profile.dts" > "$b/$profile.dts"
        "$host/dtc" -q -I dts -O dtb -o "$b/$profile.dtb" "$b/$profile.dts"
        for mode in cma fixed; do
            name=$profile
            [ "$mode" = cma ] || name=$profile-fixed
            dtb=$img/dtb/$name.dtb
            cp "$b/$profile.dtb" "$dtb"
            # Same kernel and modules; only the 64 MiB video pool backend differs.
            [ "$("$host/fdtget" -t x "$dtb" /reserved-memory/ion size)" = 4000000 ]
            if [ "$mode" = fixed ]; then
                "$host/fdtput" -t s "$dtb" /reserved-memory/ion compatible ion-region
                "$host/fdtput" -d "$dtb" /reserved-memory/ion reusable
                "$host/fdtput" -d "$dtb" /cvitek-ion/heap-carveout nanokvm,cma-backend
            fi
            "$host/fdtput" -t s "$dtb" / nanokvm,video-memory-mode "$mode"
            mkdir "$b/$name"
            cp "$b/Image.zst" "$img/initramfs.cpio.zst" "$b/$name/"
            cp "$dtb" "$b/$name/board.dtb"
            sed "s/@PROFILE@/$profile/" "$here/boot/boot.its" > "$b/$name/boot.its"
            (cd "$b/$name" && SOURCE_DATE_EPOCH=0 "$host/mkimage" -f boot.its boot.sd > /dev/null)
            [ "$(stat -c %s "$b/$name/boot.sd")" -lt $((16 * 1024 * 1024)) ]
            cp "$b/$name/boot.sd" "$img/boot/$name.sd"
            (cd "$img/boot" && sha256sum "$name.sd" > "$name.sha256")
        done
    done
    echo "$release" > "$img/boot/kernel.release"
}

verify() {
    (cd "$img" && sha256sum --quiet -c "$here/expected.sha256")
    echo "Kernel, all modules and device trees match NanoKVM OS v2.0."
    (cd "$img" && find . -type f ! -name SHA256SUMS -print0 | LC_ALL=C sort -z | xargs -0 sha256sum > SHA256SUMS)
    echo "Checksums of every output: $img/SHA256SUMS"
}

source_archive() {
    local name=NanoKVM-OS-$version-platform-source stage pkg
    [ -z "$(git -C "$repo" status --porcelain)" ] || { echo 'Commit local changes first' >&2; exit 2; }
    stage=$(mktemp -d "$out/.source.XXXXXX")
    mkdir -p "$stage/$name/upstream/buildroot-packages" "$stage/$name/NanoKVM-OS"
    while read -r pkg url _; do
        case $pkg in ''|'#'*) continue ;; esac
        if [[ $url == *.git ]]; then cp "$dl/$pkg.tar" "$stage/$name/upstream/"
        else cp "$dl/${url##*/}" "$stage/$name/upstream/"; fi
    done < "$here/sources.lock"
    # GPL userland of the initramfs, as downloaded and checked by Buildroot.
    for pkg in busybox e2fsprogs util-linux f2fs-tools; do
        cp -r "$dl/buildroot/$pkg" "$stage/$name/upstream/buildroot-packages/"
    done
    git -C "$repo" archive HEAD LICENSE platform firmware/boards firmware/buildroot firmware/alpine/release.env |
        tar -x -C "$stage/$name/NanoKVM-OS"
    git -C "$repo" rev-parse HEAD > "$stage/$name/SOURCE-COMMIT"
    cp "$here/README.md" "$stage/$name/README.md"
    (cd "$stage/$name" && find . -type f ! -name SHA256SUMS -print0 | LC_ALL=C sort -z | xargs -0 sha256sum > SHA256SUMS)
    tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$(git -C "$repo" log -1 --format=%ct)" \
        -C "$stage" -cf - "$name" | "$host/xz" -T1 -6 > "$out/$name.tar.xz"
    rm -rf "$stage"
    (cd "$out" && sha256sum "$name.tar.xz")
}

for step in "${steps[@]}"; do
    case $step in
        fetch|toolchain|kernel|modules|uboot|fip|initramfs|boot|verify) log "$step"; "$step" ;;
        source) log source; source_archive ;;
        *) usage ;;
    esac
done
