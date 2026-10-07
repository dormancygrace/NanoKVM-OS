#!/bin/bash
# NanoKVM OS build: everything in the SD card image, from the inputs pinned in
# sources.lock.
#
#   platform/build.sh [-o OUTPUT] [-j JOBS] [-k KEY | -d] [STEP...]
#
# Steps, in order (default: all of them):
#   fetch      download and verify every input in sources.lock
#   toolchain  Buildroot 2026.08: cross toolchain, host tools, initramfs userland
#   kernel     Linux 7.2.9 with kernel/*.patch and kernel/config
#   modules    out-of-tree modules; stage /lib/modules like nanokvm-kmod-sg2002
#   uboot      U-Boot 2026.07 with uboot/*.patch and uboot/defconfig
#   opensbi    OpenSBI 1.9: SG2002 FW_DYNAMIC with embedded M-mode DT
#   fip        fip.bin: stock FSBL/DDR with new OpenSBI and U-Boot
#   initramfs  initramfs from boot/initramfs.list and the Buildroot userland
#   boot       board device trees and boot.sd images like nanokvm-kernel-sg2002
#   native     SOPHGO media libraries, libkvm_mmf and libkvm
#   system     kvm_system board service
#   server     NanoKVM-Server and update/USB internet helpers
#   web        web UI
#   tools      devmem, nanokvm_update_edid, EDID profiles, board probe, USB audio
#   firmware   Wi-Fi, regulatory and video codec firmware
#   verify     compare the outputs with expected.sha256
#   payloads   contents of the seven nanokvm-* packages, from packages.list
#   packages   signed APK repository of the seven packages
#   rootfs     Alpine 3.24 root file system with nanokvm-release
#   image      SD card image
# Not in the default list:
#   source     write the corresponding-source archive of the outputs
#   clean      delete OUTPUT/apk-builder and OUTPUT/rootfs, whose files belong
#              to subordinate IDs (see packages)
#
# Checked outputs are in OUTPUT/images (default: build/platform/images), the
# APK repository and the SD card image in OUTPUT/release. The packages step
# signs (RSA256) with the key named by -k (an abuild .rsa private key with
# its .rsa.pub next to it), which must be one of firmware/alpine/keys:
# the image trusts those, and nanokvm-keys owns them. For local tests -d uses
# a test key in OUTPUT/keys instead, created on the first run; such an image
# also trusts that key, so never publish it.
set -euo pipefail
export PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin LC_ALL=C TZ=UTC
umask 022
here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
out=${NANOKVM_PLATFORM_OUT:-$repo/build/platform}
jobs=$(nproc)
key=
devkey=
usage() { sed -n '2,39p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }
while getopts o:j:k:dh opt; do
    case $opt in
        o) out=$OPTARG ;; j) jobs=$OPTARG ;; k) key=$(realpath "$OPTARG") ;; d) devkey=1 ;; *) usage ;;
    esac
done
[ -z "$key" ] || [ -z "$devkey" ] || { echo "-k and -d exclude each other" >&2; exit 2; }
shift $((OPTIND - 1))
out=$(realpath -m "$out")
steps=("$@")
[ "${#steps[@]}" -gt 0 ] || steps=(fetch toolchain kernel modules uboot opensbi fip initramfs boot
    native system server web tools firmware verify payloads packages rootfs image)
# Fail before a long build, not at the signing steps.
case " ${steps[*]} " in
    *" packages "* | *" rootfs "*)
        [ -n "$key$devkey" ] || {
            echo "APK signing key required: -k path/to/key.rsa, or -d for a local test key" >&2
            exit 2
        }
        # Devices trust the keys of nanokvm-keys; a release key must be one of them.
        [ -z "$key" ] || cmp -s "$key.pub" "$repo/firmware/alpine/keys/$(basename "$key" .rsa).rsa.pub" || {
            echo "$key.pub is not in firmware/alpine/keys under the same name; add it there," >&2
            echo "and publish it in nanokvm-keys before signing with it (firmware/alpine/README.md)" >&2
            exit 2
        } ;;
esac
# Keep builds inside OUTPUT from finding an enclosing Git checkout.
export GIT_CEILING_DIRECTORIES=$out

version=$(sed -n 's/^NANOKVM_VERSION=//p' "$repo/firmware/alpine/release.env")
# Official Alpine packages; the c906-scalar overlay is not built here.
image_version=$(sed -n 's/^NANOKVM_IMAGE_VERSION=//p' "$repo/firmware/alpine/release.env")
profile=stock
release=7.2.9-nanokvm-os-r1
# Build times recorded in the binaries. The kernel keeps the v2.0 value so
# that it stays identical to the released kernel.
kernel_timestamp='Sat Sep 19 13:51:57 UTC 2026'
uboot_epoch=1788737047
rtl8733bs_epoch=1788607804
isa=$(python3 "$repo/scripts/nanokvm_cpu_profile.py" kernel)
boards=(detect alpha beta pcie lite)

dl=$out/downloads
br=$out/buildroot
bo=$out/buildroot-output
host=$bo/host/bin
cross=$host/riscv64-buildroot-linux-musl-
img=$out/images
rel=$out/release
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
            if ! { [ -f "$file" ] && echo "$pin  $file" | sha256sum -c --quiet > /dev/null 2>&1; }; then
                curl -fsSL --retry 3 -o "$file.part" "$url"
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

# Fingerprint of everything the toolchain step builds from: the pinned
# Buildroot archive, firmware/buildroot (source patches, defconfig, external
# recipes, package patches, BusyBox configuration) and this step's recipe.
toolchain_id() {
    { lock buildroot; object_id "$repo/firmware/buildroot"; cat "$here/cpu-profile.json"; declare -f toolchain; } | sha256sum | cut -d' ' -f1
}

toolchain_stale() {
    echo "$1 was built from different toolchain inputs" >&2
    echo "  recorded: $(cat "$1")" >&2
    echo "  current:  $2" >&2
    echo "Delete $br and $bo, or use a new output directory (-o)." >&2
    exit 1
}

toolchain() {
    local id
    id=$(toolchain_id)
    if [ -e "$bo/.platform-toolchain" ]; then
        [ "$(cat "$bo/.platform-toolchain")" = "$id" ] || toolchain_stale "$bo/.platform-toolchain" "$id"
        echo "already built in $bo"
        return
    fi
    # An interrupted run of the same inputs continues incrementally.
    if [ -e "$br/.platform-patched" ]; then
        [ "$(cat "$br/.platform-patched")" = "$id" ] || toolchain_stale "$br/.platform-patched" "$id"
    else
        rm -rf "$br" "$bo"
        mkdir -p "$br"
        tar -xf "$dl/buildroot-2026.08.tar.xz" -C "$br" --strip-components=1
        for patch in "$repo"/firmware/buildroot/source-patches/*.patch; do patch -s -d "$br" -p1 < "$patch"; done
        echo "$id" > "$br/.platform-patched"
    fi
    local make=(make -C "$br" O="$bo" BR2_EXTERNAL="$repo/firmware/buildroot" BR2_JLEVEL="$jobs")
    if [ ! -e "$bo/.config" ]; then
        "${make[@]}" nanokvm_platform_defconfig
        "${make[@]}" olddefconfig
    fi
    mkdir -p "$dl/buildroot"
    # host-cmake builds json-c and miniz; host f2fs-tools, mtools and
    # dosfstools write the SD image.
    BR2_DL_DIR=$dl/buildroot "${make[@]}" toolchain host-dtc host-kmod host-patchelf \
        host-python3 host-uboot-tools host-zstd host-cmake host-f2fs-tools host-mtools \
        host-dosfstools busybox e2fsprogs f2fs-tools
    [ "$("${cross}gcc" -dumpfullversion)" = 16.2.0 ]
    python3 "$repo/scripts/nanokvm_cpu_profile.py" userspace --record "$bo/cpu-profile.json" --compiler "${cross}gcc"
    echo "$id" > "$bo/.platform-toolchain"
}

kernel() {
    rm -rf "$out/kernel"
    mkdir -p "$out/kernel" "$kbuild"
    tar -xf "$dl/linux-7.2.9.tar.xz" -C "$out/kernel"
    mv "$out/kernel/linux-7.2.9" "$ksrc"
    apply_patches "$ksrc" "$here/kernel"
    python3 "$repo/scripts/nanokvm_cpu_profile.py" kernel --record "$out/kernel/cpu-profile.json" --compiler "${cross}gcc"
    cp "$here/kernel/config" "$kbuild/.config"
    # Host pahole, rustc and bindgen would be recorded in .config; ignore them.
    local make=(make -C "$ksrc" O="$kbuild" ARCH=riscv CROSS_COMPILE="$cross" CC="${cross}gcc" LOCALVERSION= "KCFLAGS=$isa"
                PAHOLE=nkos-no-pahole RUSTC=nkos-no-rustc BINDGEN=nkos-no-bindgen)
    KBUILD_BUILD_USER=nanokvm KBUILD_BUILD_HOST=builder KBUILD_BUILD_VERSION=1 \
        KBUILD_BUILD_TIMESTAMP=$kernel_timestamp "${make[@]}" olddefconfig
    cmp "$here/kernel/config" "$kbuild/.config"
    grep -qx "CONFIG_LTO_NONE=y" "$kbuild/.config"
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
        (cd "$dir" && PWD=$dir make -C "$ksrc" O="$kbuild" ARCH=riscv CROSS_COMPILE="$cross" CC="${cross}gcc" \
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
    # Name every module in sorted order; a directory scan would make the
    # order of the modules.* indexes depend on the file system.
    local mods=$img/lib/modules/$release list
    mapfile -t list < <(cd "$mods" && find kernel extra -name '*.ko' | LC_ALL=C sort | sed "s|^|$mods/|")
    "$bo/host/sbin/depmod" -b "$img" -e -F "$kbuild/System.map" "$release" "${list[@]}"
}

uboot() {
    local u=$out/uboot
    rm -rf "$u"
    mkdir -p "$u/build" "$img"
    tar -xjf "$dl/u-boot-2026.07.tar.bz2" -C "$u"
    mv "$u/u-boot-2026.07" "$u/src"
    apply_patches "$u/src" "$here/uboot"
    cp "$here/uboot/defconfig" "$u/build/.config"
    local boot_flags boot_isa boot_abi flag
    boot_flags=$(python3 "$repo/scripts/nanokvm_cpu_profile.py" bootloader)
    for flag in $boot_flags; do
        case "$flag" in
            -march=*) boot_isa=${flag#-march=} ;;
            -mabi=*) boot_abi=${flag#-mabi=} ;;
        esac
    done
    # Architecture Makefile flags follow KCFLAGS; set its inputs as well.
    local make=(make -C "$u/src" O="$u/build" ARCH=riscv CROSS_COMPILE="$cross" LOCALVERSION=-nanokvm-os
                "RISCV_MARCH=$boot_isa" "ABI=$boot_abi" "KCFLAGS=$boot_flags")
    SOURCE_DATE_EPOCH=$uboot_epoch "${make[@]}" olddefconfig
    SOURCE_DATE_EPOCH=$uboot_epoch "${make[@]}" -j"$jobs"
    cp "$u/build/u-boot.bin" "$img/u-boot.bin"
    python3 "$repo/scripts/nanokvm_cpu_profile.py" bootloader --record "$u/cpu-profile.json" --compiler "${cross}gcc"
}

opensbi() {
    local o=$out/opensbi
    rm -rf "$o" "${img:?}/opensbi"
    mkdir -p "$o/build" "$img/opensbi"
    unpack_git opensbi . "$o/src"
    apply_patches "$o/src" "$here/opensbi"
    cp "$here/opensbi/defconfig" "$o/src/platform/generic/configs/nanokvm_defconfig"
    "$host/dtc" -I dts -O dtb -o "$o/build/sg2002.dtb" "$here/opensbi/sg2002.dts"
    # Portable scalar instructions: C906's draft vector ISA is not RVV 1.0.
    # Keep upstream -O2 and separate RO/RW firmware PMP regions.
    SOURCE_DATE_EPOCH=1782907200 make -C "$o/src" O="$o/build" -j"$jobs" \
        CROSS_COMPILE="$cross" \
        PLATFORM=generic PLATFORM_DEFCONFIG=nanokvm_defconfig \
        PLATFORM_RISCV_XLEN=64 PLATFORM_RISCV_ISA=rv64imac_zicsr_zifencei PLATFORM_RISCV_ABI=lp64 \
        FW_TEXT_START=0x80000000 FW_DYNAMIC=y FW_JUMP=n FW_PAYLOAD=n \
        FW_FDT_PATH="$o/build/sg2002.dtb" FW_FDT_PADDING=0 FW_DYNAMIC_FDT_ADDR=0x80100000 \
        REPRODUCIBLE=y OPENSBI_VERSION_GIT=
    "$host/python3" "$repo/scripts/nanokvm-opensbi-manifest.py" \
        --elf "$o/build/platform/generic/firmware/fw_dynamic.elf" \
        --binary "$o/build/platform/generic/firmware/fw_dynamic.bin" \
        --dtb "$o/build/sg2002.dtb" --nm "${cross}nm" --compiler "${cross}gcc" \
        --config "$o/build/platform/generic/kconfig/.config" --fdtget "$host/fdtget" \
        --output "$img/opensbi/build-manifest.json"
    cp "$o/build/platform/generic/firmware/fw_dynamic.bin" "$img/opensbi/"
    cp "$o/build/sg2002.dtb" "$img/opensbi/"
}

fip() {
    local f=$out/fip
    rm -rf "$f"
    mkdir -p "$f"
    tar -xf "$dl/sipeed-sdk.tar" -C "$f" fsbl/plat/cv181x/fiptool.py
    "$host/python3" "$repo/scripts/nanokvm-fip.py" build \
        --tool "$f/fsbl/plat/cv181x/fiptool.py" --base "$here/fip/base-fip.bin" \
        --monitor "$img/opensbi/fw_dynamic.bin" --uboot "$img/u-boot.bin" \
        --opensbi-manifest "$img/opensbi/build-manifest.json" \
        --output "$f/fip.bin" --report "$f/manifest.json"
    cp "$f/fip.bin" "$img/fip.bin"
    cp "$f/manifest.json" "$img/fip-manifest.json"
}

initramfs() {
    local i=$out/initramfs
    rm -rf "$i"
    mkdir -p "$i" "$img"
    # Buildroot strips and removes build-directory RPATHs only in a full
    # build; do the same for the copies that go into the initramfs.
    local type name src rest file lib
    sed -e "s|@TARGET@|$bo/target|g" -e "s|@BOOT@|$here/boot|g" "$here/boot/initramfs.list" |
    while read -r type name src rest; do
        case $type in ''|'#'*) continue ;; esac
        if [ "$type" = file ]; then
            mkdir -p "$i/root${name%/*}"
            cp "$src" "$i/root$name"
            if "${cross}readelf" -h "$i/root$name" > /dev/null 2>&1; then
                "$host/patchelf" --remove-rpath "$i/root$name"
                "${cross}strip" --remove-section=.comment --remove-section=.note "$i/root$name"
            fi
            src=$i/root$name
        fi
        echo "$type $name $src $rest"
    done > "$i/initramfs.list"
    # Every shared library that the programs need must be in the list.
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
    rm -rf "$b" "${img:?}/boot" "${img:?}/dtb" "${img:?}/boot-fit"
    mkdir -p "$b" "$img/boot" "$img/dtb" "$img/boot-fit"
    "$host/zstd" -q -f -19 -T1 "$img/Image" -o "$b/Image.zst"
    for profile in "${boards[@]}"; do
        "${cross}cpp" -P -nostdinc -undef -D__DTS__ -x assembler-with-cpp \
            -I"$ksrc/arch/riscv/boot/dts/sophgo" -I"$ksrc/include" -I"$ksrc/scripts/dtc/include-prefixes" \
            "$repo/firmware/boards/sg2002-nanokvm-$profile.dts" > "$b/$profile.dts"
        "$host/dtc" -q -I dts -O dtb -o "$b/$profile.dtb" "$b/$profile.dts"
        for mode in cma fixed uhd; do
            name=$profile
            [ "$mode" = cma ] || name=$profile-$mode
            dtb=$img/dtb/$name.dtb
            cp "$b/$profile.dtb" "$dtb"
            # Same kernel and modules; only the video pool differs: 128 MiB
            # reusable CMA, a 64 MiB fixed carveout that Linux never uses, or
            # a 128 MiB fixed carveout for 3840x2160 (CMA cannot always migrate
            # borrowed pages back for the UHD encoder buffers).
            [ "$("$host/fdtget" -t x "$dtb" /reserved-memory/ion size)" = 8000000 ]
            if [ "$mode" != cma ]; then
                [ "$mode" = uhd ] || "$host/fdtput" -t x "$dtb" /reserved-memory/ion size 4000000
                "$host/fdtput" -t s "$dtb" /reserved-memory/ion compatible ion-region
                "$host/fdtput" -d "$dtb" /reserved-memory/ion reusable
                "$host/fdtput" -d "$dtb" /cvitek-ion/heap-carveout nanokvm,cma-backend
            fi
            "$host/fdtput" -t s "$dtb" / nanokvm,video-memory-mode "$mode"
            # One size for every device tree, so the FIT images share a layout.
            "$host/dtc" -q -I dtb -O dtb -S 32768 -o "$dtb.pad" "$dtb"
            mv "$dtb.pad" "$dtb"
            [ "$(stat -c %s "$dtb")" = 32768 ]
            mkdir "$b/$name"
            cp "$b/Image.zst" "$img/initramfs.cpio.zst" "$b/$name/"
            cp "$dtb" "$b/$name/board.dtb"
            cp "$here/boot/boot.its" "$b/$name/boot.its"
            (cd "$b/$name" && SOURCE_DATE_EPOCH=0 "$host/mkimage" -f boot.its boot.sd > /dev/null)
            [ "$(stat -c %s "$b/$name/boot.sd")" -lt $((16 * 1024 * 1024)) ]
            cp "$b/$name/boot.sd" "$img/boot-fit/$name.sd"
        done
    done
    # The package carries one FIT template and the device trees (about 10 MiB
    # instead of 15 images of 9 MiB); compose-fit rebuilds an image on the
    # device and checks it against the hash of the image built here.
    python3 "$here/boot/fit-layout.py" "$img/boot-fit" "$img/dtb" "$img/boot"
    install -m 0644 "$here/boot/compose-fit" "$img/boot/compose-fit"
    echo "$release" > "$img/boot/kernel.release"
}

# SOPHGO media libraries (cvi_mpi with native/cvi_mpi/*.patch), the MMF
# wrapper and the capture library: the 19 libraries of /kvmapp/server/dl_lib.
native() {
    local n=$out/native lib
    rm -rf "$n" "${img:?}/native"
    mkdir -p "$n/src" "$img/native"
    unpack_git cvi-mpi . "$n/src/cvi_mpi"
    for lib in sensors json-c miniz inih; do unpack_git "$lib" . "$n/src/$lib"; done
    apply_patches "$n/src/json-c" "$here/native/json-c"
    apply_patches "$n/src/cvi_mpi" "$here/native/cvi_mpi"
    apply_patches "$n/src/sensors" "$repo/firmware/sensor/patches"
    local env=(NANOKVM_MPI_SOURCE="$n/src/cvi_mpi" NANOKVM_OSDRV_SOURCE="$out/modules/sources/osdrv"
               NANOKVM_KERNEL_SOURCE="$ksrc" NANOKVM_BUILDROOT_OUTPUT="$bo" NANOKVM_CMAKE="$host/cmake"
               JOBS="$jobs")
    env "${env[@]}" bash "$repo/scripts/build-enhanced-mpi.sh"
    env "${env[@]}" "$host/python3" "$repo/scripts/build-enhanced-isp-vendor.py"
    env "${env[@]}" NANOKVM_JSON_C_SOURCE="$n/src/json-c" NANOKVM_MINIZ_SOURCE="$n/src/miniz" \
        NANOKVM_MPI_THIRDPARTY_OUTPUT="$n/bin" "$host/python3" "$repo/scripts/build-enhanced-mpi-bin.py"
    env "${env[@]}" NANOKVM_SENSOR_SOURCE="$n/src/sensors" NANOKVM_INIH_SOURCE="$n/src/inih" \
        NANOKVM_MMF_OUTPUT="$n/mmf" "$host/python3" "$repo/scripts/build-enhanced-mmf.py"
    env "${env[@]}" NANOKVM_MMF_OUTPUT="$n/mmf" NANOKVM_CAPTURE_OUTPUT="$n/capture" \
        "$host/python3" "$repo/scripts/build-enhanced-capture.py"
    # The vendor makefiles always compile with -g; ship the libraries without it.
    for lib in "$n"/src/cvi_mpi/lib/*.so; do
        "${cross}strip" --strip-debug -o "$img/native/${lib##*/}" "$lib"
    done
    cp "$n/mmf/libkvm_mmf.so" "$n/capture/libkvm.so" "$img/native/"
    [ "$(find "$img/native" -name '*.so' | wc -l)" = 19 ]
}

# kvm_system board service from MaixCDK with native/maixcdk/*.patch.
system() {
    local s=$out/system
    rm -rf "$s" "${img:?}/system"
    mkdir -p "$s" "$img/system"
    unpack_git maixcdk . "$s/maixcdk"
    apply_patches "$s/maixcdk" "$here/native/maixcdk"
    NANOKVM_MAIXCDK_SOURCE=$s/maixcdk NANOKVM_BUILDROOT_OUTPUT=$bo NANOKVM_SYSTEM_OUTPUT=$s/out \
        "$host/python3" "$repo/scripts/build-enhanced-system.py"
    cp "$s/out/kvm_system" "$img/system/"
}

# NanoKVM-Server with the NanoKVM Go runtime (firmware/cpu/sysmon-runtime),
# and the static update helpers. Go modules are checked against go.sum.
server() {
    local s=$out/server
    rm -rf "$s" "${img:?}/server"
    mkdir -p "$s" "$img/server" "$dl/go-mod"
    tar -xzf "$dl/go1.27.1.linux-amd64.tar.gz" -C "$s"
    "$host/python3" "$repo/firmware/cpu/sysmon-runtime/prepare.py" --base "$s/go" --output "$s/goroot" > /dev/null
    GOMODCACHE=$dl/go-mod GOPATH=$s/gopath GOCACHE=$s/gocache GOFLAGS=-mod=readonly GOTOOLCHAIN=local \
        "$host/python3" "$repo/scripts/build-server-existing-libs.py" --libraries "$img/native" \
        --buildroot-output "$bo" --go "$s/goroot/bin/go" --output "$s/out"
    cp "$s/out/NanoKVM-Server.stripped" "$img/server/NanoKVM-Server"
    cp "$s/out/nkos-update" "$s/out/nkos-apply-updates" "$s/out/nkos-usb-internet" "$img/server/"
}

# Web UI. npm packages are checked against pnpm-lock.yaml.
web() {
    local w=$out/web node
    node=$(lock node | awk '{ print $2 }')
    node=${node##*/}
    rm -rf "$w" "${img:?}/web"
    mkdir -p "$w/src" "$dl/pnpm-store" "$img"
    tar -xJf "$dl/$node" -C "$w"
    tar -xzf "$dl/exe.linux-x64-12.8.1.tgz" -C "$w"
    (cd "$repo/web" && tar --exclude=./node_modules --exclude=./dist -cf - .) | tar -xf - -C "$w/src"
    (
        export PATH=$w/${node%.tar.xz}/bin:$PATH
        cd "$w/src"
        "$w/package/pnpm" install --frozen-lockfile --store-dir "$dl/pnpm-store"
        "$w/package/pnpm" build
    )
    cp -r "$w/src/dist" "$img/web"
}

# Board tools: BusyBox devmem, nanokvm_update_edid and the EDID profiles,
# nkos-board-probe and usb-audio-capture.
tools() {
    local t=$out/tools e py=$host/python3
    rm -rf "$t" "${img:?}/tools"
    mkdir -p "$t" "$img/tools/edid" "$img/tools/usb-audio"
    NANOKVM_BUILDROOT_OUTPUT=$bo BUSYBOX_SOURCE_ARCHIVE=$dl/busybox-1.36.1.tar.bz2 JOBS=$jobs \
        sh "$repo/scripts/build-busybox-devmem.sh" "$t/devmem"
    cp "$t/devmem/devmem" "$t/devmem/busybox-LICENSE" "$img/tools/"

    # As Buildroot builds a target package: TARGET_CFLAGS, then its strip.
    e=$t/edid
    cp -r "$repo/tools/nanokvm_update_edid" "$e"
    make -C "$e" CC="${cross}gcc" RISCV_FLAGS= LDFLAGS=-ztext \
        CFLAGS="$(python3 "$repo/scripts/nanokvm_cpu_profile.py" userspace) -D_LARGEFILE_SOURCE -D_LARGEFILE64_SOURCE -D_FILE_OFFSET_BITS=64 -g0 -Wall -Wextra -Werror"
    "${cross}strip" --remove-section=.comment --remove-section=.note \
        -o "$img/tools/nanokvm_update_edid" "$e/nanokvm_update_edid"
    "$py" "$repo/scripts/build-qhd-edid.py" --input "$e/E21_NanoKVM.bin" --output "$e/NanoKVM-QHD30.bin"
    "$py" "$repo/scripts/build-monitor-edids.py" --input "$e/E21_NanoKVM.bin" --output "$e/monitor-profiles"
    "$py" "$repo/firmware/probes/edid120/build_experimental_edid.py" \
        --input "$e/NanoKVM-QHD30.bin" --output "$e/NanoKVM-720p120.bin" --force
    "$py" "$repo/firmware/probes/edid120/build_experimental_qhd40.py" \
        --input "$e/NanoKVM-720p120.bin" --output "$e/NanoKVM-QHD40.bin" --force
    "$py" "$repo/firmware/probes/edid120/build_experimental_fhd_high.py" --rate 75 \
        --input "$e/NanoKVM-QHD40.bin" --output "$e/NanoKVM-final-video-profiles.bin" --force
    "$py" "$repo/firmware/probes/edid120/build_final_monitor_profiles.py" \
        --input "$e/NanoKVM-final-video-profiles.bin" --output "$e/monitor-profiles"
    "$py" "$repo/scripts/build-portrait-edid.py" --input "$e/E21_NanoKVM.bin" --output "$e/NanoKVM-portrait-1080x1920.bin"
    "$py" "$repo/scripts/build-portrait-edid.py" --profile hd --input "$e/E21_NanoKVM.bin" --output "$e/NanoKVM-portrait-720x1280.bin"
    "$py" "$repo/scripts/build-portrait-edid.py" --profile h264 --input "$e/E21_NanoKVM.bin" --output "$e/NanoKVM-portrait-1296x2304.bin"
    "$py" "$repo/scripts/build-portrait-edid.py" --profile max --input "$e/E21_NanoKVM.bin" --output "$e/NanoKVM-portrait-1440x2560.bin"
    "$py" "$repo/scripts/build-portrait-edid.py" --rates --input "$e/E21_NanoKVM.bin" --output "$e/portrait-rates"
    cp "$e/NanoKVM-QHD30.bin" "$e"/NanoKVM-portrait-*.bin "$e"/portrait-rates/NanoKVM-portrait-*.bin "$e"/monitor-profiles/NanoKVM-monitor-*.bin \
        "$e"/monitor-profiles/NanoKVM-cube-monitor-*.bin "$img/tools/edid/"
    cp "$e/monitor-profiles/NanoKVM-monitor-auto.bin" "$img/tools/edid/NanoKVM-final-video-profiles.bin"
    cp "$e/E21_NanoKVM.bin" "$img/tools/edid/NanoKVM-stock.bin"

    local cpu_flags
    read -r -a cpu_flags <<< "$(python3 "$repo/scripts/nanokvm_cpu_profile.py" userspace)"
    python3 "$repo/scripts/nanokvm_cpu_profile.py" userspace --record "$t/cpu-profile.json" --compiler "${cross}gcc"
    "${cross}gcc" "${cpu_flags[@]}" -static -Wall -Wextra -Werror "$repo/firmware/boards/nkos-board-probe.c" -o "$t/nkos-board-probe"
    "${cross}strip" -o "$img/tools/nkos-board-probe" "$t/nkos-board-probe"

    unpack_git tinyalsa . "$t/tinyalsa"
    "$py" "$repo/scripts/build-usb-audio.py" --output "$t/usb-audio" --buildroot-output "$bo" \
        --kernel-source "$ksrc" --kernel-output "$kbuild" --tinyalsa "$t/tinyalsa" \
        --opus-archive "$dl/opus-1.6.1.tar.gz" --jobs "$jobs"
    cp "$t/usb-audio/usb-audio-capture" "$img/tools/"
    cp "$t/usb-audio/share/usb-audio/"* "$img/tools/usb-audio/"
}

# Firmware files: AIC8800 Wi-Fi firmware from the Radxa package that the
# driver comes from, the wireless regulatory database and the video codec.
firmware() {
    local f=$img/firmware name dir
    rm -rf "$f"
    mkdir -p "$f/lib/firmware/aic8800_sdio" "$f/fw_vcodec" "$out/firmware"
    # The driver looks in aic8800_sdio/<chip>; AIC8801 and D80 share one directory.
    for dir in aic8800 aic8800D80 aic8800D80N aic8800D80X2 aic8800DC; do
        rm -rf "$out/firmware/$dir"
        unpack_git aic8800 "src/SDIO/driver_fw/fw/$dir" "$out/firmware/$dir"
    done
    rm -f "$out/firmware/aic8800D80/aic8800D80.7z"
    cp -r "$out/firmware/aic8800" "$f/lib/firmware/aic8800_sdio/aic8800_and_aic8800D80"
    for name in "$out"/firmware/aic8800D80/*; do
        [ ! -e "$f/lib/firmware/aic8800_sdio/aic8800_and_aic8800D80/${name##*/}" ] || {
            echo "AIC firmware name in both aic8800 and aic8800D80: ${name##*/}" >&2; exit 1; }
        cp "$name" "$f/lib/firmware/aic8800_sdio/aic8800_and_aic8800D80/"
    done
    for dir in aic8800D80N aic8800D80X2 aic8800DC; do
        cp -r "$out/firmware/$dir" "$f/lib/firmware/aic8800_sdio/$dir"
    done
    # The stock driver path, kept as in earlier releases.
    cp "$out"/firmware/aic8800/* "$f/lib/firmware/"
    tar -xJf "$dl/wireless-regdb-2026.09.03.tar.xz" -C "$out/firmware"
    cp "$out"/firmware/wireless-regdb-2026.09.03/regulatory.db{,.p7s} "$f/lib/firmware/"
    unpack_git sipeed-sdk ramdisk/rootfs/common_musl_riscv64/usr/share/fw_vcodec "$f/fw_vcodec"
    mkdir -p "$f/licenses"
    tar -xf "$dl/aic8800.tar" -O debian/copyright > "$f/licenses/aic8800-copyright"
    cp "$out/firmware/wireless-regdb-2026.09.03/LICENSE" "$f/licenses/wireless-regdb-LICENSE"
}

# Assemble the seven package payloads from packages.list.
payloads() {
    local p=$out/payloads gen pkg type path mode src dest file
    rm -rf "$p"
    gen=$out/payloads-gen
    rm -rf "$gen"
    mkdir -p "$gen/kvm"
    # Written below once the application payload is complete.
    : > "$gen/enhanced-stage-manifest.json"
    printf '+kvmapp/kvm\n' > "$gen/protected-paths"
    printf 'Alpine 3.24; SG2002/C906; flavour=%s\n' enhanced > "$gen/nanokvm-buildroot"
    printf '%s\n' "${version#v}" > "$gen/version"
    printf '%s' 50 > "$gen/kvm/fps"
    printf '%s' 15000 > "$gen/kvm/qlty"
    printf '%s' 0 > "$gen/kvm/res"
    printf '%s' h265 > "$gen/kvm/type"
    printf 'NAME="NanoKVM OS"\nVERSION="%s"\nALPINE_VERSION="3.24"\nBUILD_PROFILE="%s"\n' "$version" "$profile" > "$gen/nanokvm-release"
    printf '%s\n' "$profile" > "$gen/nanokvm-build-profile"
    resolve() {
        case $1 in
            @*) echo "$img/${1#@}" ;;
            %*) echo "$gen/${1#%}" ;;
            *) echo "$repo/$1" ;;
        esac
    }
    while read -r pkg type path mode src; do
        case $pkg in ''|'#'*) continue ;; esac
        dest=$p/$pkg$path
        case $type in
            file)
                file=$(resolve "$src")
                [ -f "$file" ] || { echo "packages.list: missing $src" >&2; exit 1; }
                install -D -m "$mode" "$file" "$dest" ;;
            tree)
                file=$(resolve "$src")
                [ -d "$file" ] || { echo "packages.list: missing $src" >&2; exit 1; }
                [ -z "$(find "$file" -type l)" ] || { echo "packages.list: links below $src" >&2; exit 1; }
                mkdir -p "$dest"
                (cd "$file" && find . -type d) | while read -r dir; do mkdir -p "$dest/$dir"; done
                (cd "$file" && find . -type f) | while read -r name; do install -m "$mode" "$file/$name" "$dest/$name"; done ;;
            dir) mkdir -p "$dest"; chmod "$mode" "$dest" ;;
            link) mkdir -p "$(dirname "$dest")"; ln -sfn "$src" "$dest" ;;
            *) echo "packages.list: unknown type $type" >&2; exit 1 ;;
        esac
    done < "$here/packages.list"
    # What the application payload was built from, for support requests.
    "$host/python3" - "$p/app/kvmapp" "$version" "$(git -C "$repo" rev-parse HEAD 2>/dev/null || echo unknown)" \
        "$([ -z "$(git -C "$repo" status --porcelain 2>/dev/null)" ] && echo false || echo true)" <<'PY'
import hashlib, json, sys
from pathlib import Path
root, version, commit, dirty = Path(sys.argv[1]), sys.argv[2], sys.argv[3], sys.argv[4] == 'true'
manifest = root / 'enhanced-stage-manifest.json'
files = {str(p.relative_to(root)): hashlib.sha256(p.read_bytes()).hexdigest()
         for p in sorted(root.rglob('*')) if p.is_file() and not p.is_symlink() and p != manifest}
manifest.write_text(json.dumps({'version': version, 'source_dirty': dirty, 'source_base_commit': commit,
                                'files': files}, indent=2) + '\n')
PY
}

# The packages, rootfs and image steps create files of several owners and run
# riscv64 programs. build.sh runs them as root of a user namespace instead of
# host root: newuidmap maps the subordinate IDs of /etc/subuid and /etc/subgid,
# and the namespace's own binfmt_misc (Linux 6.7 or newer) starts qemu.
in_userns() (
    local user sub_u sub_g
    user=$(id -un)
    sub_u=$(awk -F: -v u="$user" '$1 == u { print $2; exit }' /etc/subuid 2>/dev/null)
    sub_g=$(awk -F: -v u="$user" '$1 == u { print $2; exit }' /etc/subgid 2>/dev/null)
    if [ -z "$sub_u" ] || [ -z "$sub_g" ] || ! command -v newuidmap > /dev/null || ! command -v newgidmap > /dev/null; then
        echo "The $1 step needs newuidmap and newgidmap (package uidmap) and subordinate" >&2
        echo "IDs for $user in /etc/subuid and /etc/subgid; see platform/README.md." >&2
        exit 1
    fi
    local sync pid="" status=0 ready_fd mapped_fd deadline
    sync=$(mktemp -d)
    cleanup_userns() {
        if [ -n "$pid" ]; then
            kill "$pid" 2>/dev/null || true
            wait "$pid" 2>/dev/null || true
        fi
        rm -rf "$sync"
    }
    trap cleanup_userns EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    mkfifo "$sync/ready" "$sync/mapped"
    # Opening read/write avoids an unbounded FIFO open if unshare fails early.
    exec {ready_fd}<>"$sync/ready"
    exec {mapped_fd}<>"$sync/mapped"
    unshare --user --mount --pid --fork --kill-child \
        bash -c 'echo > "$1/ready"; read -r _ < "$1/mapped"; shift; exec "$@"' sh "$sync" \
        env NANOKVM_USERNS=1 bash "$here/build.sh" -o "$out" -j "$jobs" ${key:+-k "$key"} ${devkey:+-d} "$1" &
    pid=$!
    deadline=$((SECONDS + 30))
    until read -r -t 0.1 -u "$ready_fd" _; do
        if ! kill -0 "$pid" 2>/dev/null; then
            wait "$pid" || status=$?
            pid=
            echo "User namespace exited before reporting ready" >&2
            [ "$status" -ne 0 ] || status=1
            exit "$status"
        fi
        if [ "$SECONDS" -ge "$deadline" ]; then
            echo "Timed out waiting for user namespace startup" >&2
            exit 1
        fi
    done
    newuidmap "$pid" 0 "$(id -u)" 1 1 "$sub_u" 65535 || exit $?
    newgidmap "$pid" 0 "$(id -g)" 1 1 "$sub_g" 65535 || exit $?
    echo >&"$mapped_fd"
    wait "$pid" || status=$?
    pid=
    exit "$status"
)

qemu=$out/qemu/qemu-riscv64-static
register_qemu() {
    local bfm
    if [ ! -x "$qemu" ]; then
        rm -rf "$out/qemu"
        mkdir -p "$out/qemu"
        (cd "$out/qemu" && "${cross}ar" x "$dl/qemu-user-static_8.2.2+ds-0ubuntu1.18_amd64.deb" &&
            "$host/zstd" -q -d -c data.tar.zst | tar -x ./usr/bin/qemu-riscv64-static)
        mv "$out/qemu/usr/bin/qemu-riscv64-static" "$qemu"
    fi
    # The mounts of the chroot steps stay in this namespace.
    mount --make-rprivate /
    bfm=$(mktemp -d)
    mount -t binfmt_misc binfmt_misc "$bfm"
    # F: the kernel opens qemu now, so it need not exist inside the chroot.
    printf '%s%s:F' ':nkos-riscv64:M::\x7fELF\x02\x01\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x02\x00\xf3\x00:\xff\xff\xff\xff\xff\xff\xff\x00\xff\xff\xff\xff\xff\xff\xff\xff\xfe\xff\xff\xff:' \
        "$qemu" > "$bfm/register"
}

# A fresh Alpine 3.24 minirootfs in $1.
alpine_tree() {
    rm -rf "$1"
    mkdir -p "$1"
    tar --numeric-owner -xzf "$dl/alpine-minirootfs-3.24.2-riscv64.tar.gz" -C "$1"
}

# Run a command in the Alpine tree $1 with /dev, /proc, /sys, the host DNS
# configuration and, in $BIND (SOURCE:TARGET), one more directory.
in_chroot() (
    local root=$1
    shift
    mkdir -p "$root/dev" "$root/proc" "$root/sys"
    mount --rbind /dev "$root/dev"
    mount -t proc proc "$root/proc"
    mount --rbind /sys "$root/sys"
    if [ -n "${BIND:-}" ]; then
        mkdir -p "$root${BIND#*:}"
        mount --bind "${BIND%%:*}" "$root${BIND#*:}"
    fi
    # A lazy unmount also detaches the submounts of /dev and /sys.
    trap 'rc=$?; umount -l "$root/dev"; umount -l "$root/proc"; umount -l "$root/sys"
          [ -z "${BIND:-}" ] || umount -l "$root${BIND#*:}"; exit $rc' EXIT
    cp -L /etc/resolv.conf "$root/etc/resolv.conf"
    chroot "$root" "$@"
)

# The APK signing key: -k KEY, or with -d a local test key created on the first
# run. Never a key nobody chose: an image trusts it for every repository.
signing_key() {
    local tmp
    if [ -n "$key" ]; then
        keyfile=$key
    elif [ -z "$devkey" ]; then
        echo "APK signing key required: -k path/to/key.rsa, or -d for a local test key" >&2
        exit 2
    else
        echo "Signing with a local test key: do not publish these packages or this image" >&2
        # Only a key this option created; never another key left in OUTPUT/keys.
        keyfile=
        [ ! -d "$out/keys" ] || keyfile=$(find "$out/keys" -name 'nanokvm-test-*.rsa' | LC_ALL=C sort | head -n 1)
        if [ -z "$keyfile" ]; then
            mkdir -p "$out/keys"
            tmp=$out/keys/new.rsa
            openssl genrsa -out "$tmp" 4096 2> /dev/null
            openssl rsa -in "$tmp" -pubout -out "$tmp.pub" 2> /dev/null
            keyfile=$out/keys/nanokvm-test-$(sha256sum < "$tmp.pub" | cut -c1-8).rsa
            mv "$tmp" "$keyfile"
            mv "$tmp.pub" "$keyfile.pub"
            echo "Created APK signing key $keyfile"
        fi
    fi
    [ -f "$keyfile.pub" ] || { echo "APK signing key: $keyfile.pub is missing" >&2; exit 1; }
    keyname=$(basename "$keyfile" .rsa)
}

# Build the seven packages with abuild in an Alpine riscv64 tree.
packages() (
    local b=$out/apk-builder
    register_qemu
    signing_key
    alpine_tree "$b"
    in_chroot "$b" /sbin/apk --no-cache add alpine-sdk coreutils musl-utils
    mkdir -p "$b/build/src/scripts" "$b/build/src/firmware/alpine" "$b/root/.abuild"
    cp "$repo/scripts/build-alpine-packages.sh" "$b/build/src/scripts/"
    cp -r "$repo/firmware/alpine/packages" "$repo/firmware/alpine/release.env" "$b/build/src/firmware/alpine/"
    cp -a "$out/payloads" "$b/build/payloads"
    # A test image trusts the test key too; nanokvm-keys owns it there.
    [ -z "$devkey" ] || install -D -m 0644 "$keyfile.pub" "$b/build/payloads/keys/etc/apk/keys/$keyname.rsa.pub"
    trap 'rm -f "$b/root/.abuild/$keyname.rsa"' EXIT
    install -m 0600 "$keyfile" "$b/root/.abuild/$keyname.rsa"
    install -m 0644 "$keyfile.pub" "$b/root/.abuild/$keyname.rsa.pub"
    install -m 0644 "$keyfile.pub" "$b/etc/apk/keys/$keyname.rsa.pub"
    printf 'PACKAGER="NanoKVM OS"\nPACKAGER_PRIVKEY="/root/.abuild/%s.rsa"\n' "$keyname" > "$b/root/.abuild/abuild.conf"
    # -F: abuild runs as root of the namespace. -d: the packages compile
    # nothing, so their dependencies are not installed in the builder.
    in_chroot "$b" /usr/bin/env -i HOME=/root PATH=/usr/sbin:/usr/bin:/sbin:/bin \
        SOURCE_DATE_EPOCH="$(git -C "$repo" log -1 --format=%ct)" PAYLOAD_ROOT=/build/payloads \
        REPODEST=/build/repo SRCDEST=/build/distfiles ABUILD_FLAGS='-F -d' \
        /bin/sh /build/src/scripts/build-alpine-packages.sh stock
    rm -f "$b/root/.abuild/$keyname.rsa"
    rm -rf "${rel:?}/apk"
    mkdir -p "$rel/apk"
    cp -a "$b/build/repo/stock/." "$rel/apk/"
    cp "$keyfile.pub" "$rel/apk/$keyname.rsa.pub"
)

# Alpine 3.24 root file system: the minirootfs with nanokvm-release from the
# packages step and its dependencies from the Alpine mirror.
rootfs() {
    local r=$out/rootfs/root pub path
    register_qemu
    signing_key
    rm -rf "$out/rootfs"
    alpine_tree "$r"
    # apk must trust the keys before it can install nanokvm-keys; it then takes
    # over these identical files (/etc/apk is not a protected path).
    install -m 0644 "$keyfile.pub" "$r/etc/apk/keys/$keyname.rsa.pub"
    for pub in "$repo"/firmware/alpine/keys/*.pub; do install -m 0644 "$pub" "$r/etc/apk/keys/"; done
    printf '%s\n' /mnt/nanokvm-apk/recipes https://dl-cdn.alpinelinux.org/alpine/v3.24/main \
        https://dl-cdn.alpinelinux.org/alpine/v3.24/community > "$r/etc/apk/repositories"
    BIND=$rel/apk:/mnt/nanokvm-apk in_chroot "$r" /sbin/apk --no-cache add nanokvm-release
    rmdir "$r/mnt/nanokvm-apk"
    # The repositories of the device: NanoKVM OS releases and Alpine.
    printf '%s\n' https://nkos.pesin.pro/repos/nanokvm https://dl-cdn.alpinelinux.org/alpine/v3.24/main \
        https://dl-cdn.alpinelinux.org/alpine/v3.24/community \
        '@edgecommunity https://dl-cdn.alpinelinux.org/alpine/edge/community' > "$r/etc/apk/repositories"
    rm -f "$r/etc/resolv.conf"
    # The settings APIs call these; refuse an incomplete root.
    for path in usr/sbin/nkos-usb-internet etc/init.d/nanokvm-usb-internet usr/sbin/iw usr/sbin/nanokvm_update_edid etc/init.d/nanokvm-policy \
                usr/libexec/nanokvm/legacy/S50sshd usr/libexec/nanokvm/legacy/S38memory \
                usr/libexec/nanokvm/legacy/S34mssclamp kvmapp/system/bin/usb-audio-capture \
                usr/sbin/nkos-board-probe usr/sbin/nkos-board-select; do
        [ -x "$r/$path" ] || { echo "rootfs: missing $path" >&2; exit 1; }
    done
    [ -d "$r/lib/modules/$(cat "$r/usr/lib/nanokvm/boot/kernel.release")" ] || {
        echo "rootfs: boot.sd and modules differ" >&2; exit 1; }
    # Every trusted key belongs to a package (nanokvm-keys, alpine-keys), so
    # that updates can add and remove keys; this also catches an .apk-new.
    for pub in "$r"/etc/apk/keys/*; do
        awk -v name="${pub##*/}" '/^F:/ { dir = substr($0, 3) }
            /^R:/ && dir == "etc/apk/keys" && substr($0, 3) == name { found = 1 }
            END { exit !found }' "$r/lib/apk/db/installed" || {
            echo "rootfs: no package owns /etc/apk/keys/${pub##*/}" >&2; exit 1; }
    done
    for path in dev proc sys mnt/nanokvm-apk; do
        ! mountpoint -q "$r/$path" || { echo "rootfs: $path is still mounted" >&2; exit 1; }
    done
    awk -F: '/^P:/ { p = substr($0, 3) } /^V:/ { print p "=" substr($0, 3) }' "$r/lib/apk/db/installed" |
        LC_ALL=C sort > "$rel/installed-packages.txt"
    tar --numeric-owner -czf "$rel/alpine-rootfs.tar.gz" --exclude='./boot/*' --exclude='./data/*' \
        --exclude='./dev/*' --exclude='./proc/*' --exclude='./sys/*' --exclude='./run/*' --exclude='./tmp/*' \
        -C "$r" .
}

clean() {
    rm -rf "${out:?}/apk-builder" "$out/rootfs"
}

# The SD card image: boot partition with fip.bin and the detect boot.sd,
# F2FS root partition from the rootfs step.
image() {
    rm -rf "${rel:?}/image"
    PATH=$bo/host/sbin:$host:$PATH "$host/python3" "$repo/scripts/build-alpine-sd-image.py" \
        --rootfs-archive "$rel/alpine-rootfs.tar.gz" --fip "$img/fip.bin" --f2fs-tools "$bo/host/sbin" \
        --output "$rel/image" --version "$version" --image-version "$image_version"
}

verify() {
    local unlisted
    # Every listed output must exist and match, and every output must be listed.
    (cd "$img" && sha256sum --quiet --strict -c "$here/expected.sha256") || {
        echo "Outputs are missing or differ from expected.sha256" >&2; exit 1; }
    unlisted=$(cd "$img" && find . -type f ! -name SHA256SUMS | sed 's|^\./||' | LC_ALL=C sort |
        LC_ALL=C comm -23 - <(awk '$1 !~ /^#/ && NF { print $2 }' "$here/expected.sha256" | LC_ALL=C sort))
    if [ -n "$unlisted" ]; then
        printf 'Outputs not in expected.sha256:\n%s\n' "$unlisted" >&2
        exit 1
    fi
    echo "All $(awk '$1 !~ /^#/ && NF' "$here/expected.sha256" | wc -l) outputs match expected.sha256."
    (cd "$img" && find . -type f ! -name SHA256SUMS -print0 | LC_ALL=C sort -z | xargs -0 sha256sum > SHA256SUMS)
    echo "Checksums of every output: $img/SHA256SUMS"
}

source_archive() {
    local name=NanoKVM-OS-$version-source stage pkg url
    [ -z "$(git -C "$repo" status --porcelain)" ] || { echo 'Commit local changes first' >&2; exit 2; }
    stage=$(mktemp -d "$out/.source.XXXXXX")
    mkdir -p "$stage/$name/upstream/buildroot-packages" "$stage/$name/NanoKVM-OS"
    # The GPL-licensed inputs of the image. The rest of sources.lock is
    # fetched from its upstream by build.sh and is not redistributed here.
    for pkg in buildroot linux u-boot osdrv aic8800 cryptodev sipeed-sdk opensbi busybox; do
        url=$(lock "$pkg" | awk '{ print $2 }')
        if [[ $url == *.git ]]; then cp "$dl/$pkg.tar" "$stage/$name/upstream/"
        else cp "$dl/${url##*/}" "$stage/$name/upstream/"; fi
    done
    # GPL userland of the initramfs, as downloaded and checked by Buildroot.
    for pkg in busybox e2fsprogs util-linux f2fs-tools; do
        cp -r "$dl/buildroot/$pkg" "$stage/$name/upstream/buildroot-packages/"
    done
    # This repository: the application (GPL-3.0) and every build recipe.
    git -C "$repo" archive HEAD | tar -x -C "$stage/$name/NanoKVM-OS"
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
        fetch|toolchain|kernel|modules|uboot|opensbi|fip|initramfs|boot|native|system|server|web|tools|firmware|verify|payloads)
            log "$step"; "$step" ;;
        packages|rootfs|image|clean)
            if [ "${NANOKVM_USERNS:-0}" = 1 ]; then log "$step"; "$step"; else in_userns "$step"; fi ;;
        source) log source; source_archive ;;
        *) usage ;;
    esac
done
