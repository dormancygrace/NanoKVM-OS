#!/bin/sh
# Export the tested Alpine root into the package payload boundaries.
set -eu

ROOTFS=${ROOTFS:-/}
PROFILE=${PROFILE:-stock}
OUTPUT=${OUTPUT:-work/alpine/payloads/$PROFILE}
BOOT_BUILD=${BOOT_BUILD:-build/f2fs/boot-pcie-f2fs-root}
KERNEL_RELEASE=${KERNEL_RELEASE:-$(uname -r)}

case "$PROFILE" in stock|c906-scalar) ;; *) echo "invalid PROFILE" >&2; exit 2 ;; esac
[ -f "$ROOTFS/etc/alpine-release" ] || { echo "not an Alpine root: $ROOTFS" >&2; exit 1; }
[ -d "$ROOTFS/lib/modules/$KERNEL_RELEASE" ] || { echo "missing module tree" >&2; exit 1; }
[ -f "$BOOT_BUILD/boot.sd" ] || { echo "missing boot FIT: $BOOT_BUILD/boot.sd" >&2; exit 1; }

rm -rf "$OUTPUT"
for part in base kernel-sg2002 kmod-sg2002 firmware-sg2002 app release; do
    mkdir -p "$OUTPUT/$part"
done

copy_path() {
    source=$1
    destination=$2
    [ -e "$source" ] || [ -L "$source" ] || return 0
    mkdir -p "$(dirname "$destination")"
    cp -a "$source" "$destination"
}

for service in "$ROOTFS"/etc/init.d/nanokvm-*; do
    [ -f "$service" ] || continue
    copy_path "$service" "$OUTPUT/base/etc/init.d/$(basename "$service")"
done
for script in S02config S03usbdev S10uuid S15kvmhwd S25wifimod \
              S29qdisc S30eth S30wifi S30usbnet S95nanokvm; do
    copy_path "$ROOTFS/etc/init.d/$script" "$OUTPUT/base/etc/init.d/$script"
done
copy_path "$ROOTFS/usr/libexec/nanokvm" "$OUTPUT/base/usr/libexec/nanokvm"
install -D -m 0755 "$ROOT/firmware/alpine/compat/nanokvm-stage-update" \
    "$OUTPUT/base/usr/sbin/nanokvm-stage-update"

# Keep the four names used by the existing application and control APIs. The
# implementation lives once under /usr/libexec; /etc/init.d contains links so
# installing the Alpine base package cannot leave a second implementation in
# the application payload.
for script in S03usbdev S30eth S30wifi S95nanokvm; do
    canonical=$ROOTFS/usr/libexec/nanokvm/legacy/$script
    if [ ! -f "$canonical" ]; then
        for candidate in "$ROOTFS/etc/init.d/$script" \
                         "$ROOTFS/kvmapp/system/init.d/$script"; do
            if [ -f "$candidate" ]; then
                canonical=$candidate
                break
            fi
        done
    fi
    [ -f "$canonical" ] || {
        echo "missing Alpine compatibility script: $script" >&2
        exit 1
    }
    mkdir -p "$OUTPUT/base/usr/libexec/nanokvm/legacy" "$OUTPUT/base/etc/init.d"
    install -m 0755 "$canonical" \
        "$OUTPUT/base/usr/libexec/nanokvm/legacy/$script"
    ln -snf "/usr/libexec/nanokvm/legacy/$script" \
        "$OUTPUT/base/etc/init.d/$script"
done
copy_path "$ROOTFS/etc/nanokvm-release" "$OUTPUT/release/etc/nanokvm-release"
copy_path "$ROOTFS/etc/nanokvm-build-profile" "$OUTPUT/release/etc/nanokvm-build-profile"

mkdir -p "$OUTPUT/kernel-sg2002/usr/lib/nanokvm/boot"
for file in boot.sd Image.zst board.dtb; do
    copy_path "$BOOT_BUILD/$file" "$OUTPUT/kernel-sg2002/usr/lib/nanokvm/boot/$file"
done
(
    cd "$OUTPUT/kernel-sg2002/usr/lib/nanokvm/boot"
    sha256sum boot.sd Image.zst board.dtb > SHA256SUMS
)

mkdir -p "$OUTPUT/kmod-sg2002/lib/modules"
cp -a "$ROOTFS/lib/modules/$KERNEL_RELEASE" "$OUTPUT/kmod-sg2002/lib/modules/"

copy_path "$ROOTFS/lib/firmware" "$OUTPUT/firmware-sg2002/lib/firmware"
copy_path "$ROOTFS/usr/share/fw_vcodec" "$OUTPUT/firmware-sg2002/usr/share/fw_vcodec"
copy_path "$ROOTFS/usr/share/nanokvm" "$OUTPUT/firmware-sg2002/usr/share/nanokvm"
copy_path "$ROOTFS/kvmapp" "$OUTPUT/app/kvmapp"
# Update transactions and locks are runtime state, never package payload.
rm -rf "$OUTPUT/app/kvmapp/.os-update"

echo "$OUTPUT"
du -sh "$OUTPUT"/*
