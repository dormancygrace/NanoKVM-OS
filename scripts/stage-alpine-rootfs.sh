#!/bin/sh
# Stage a bootable NanoKVM Alpine root from a running NanoKVM OS instance.
# This is deliberately a direct port tool. APK packaging reuses the same file set.
set -eu

TARGET=${TARGET:-/mnt/alpine-test}
SOURCE=${SOURCE:-/}
PORT_DIR=${PORT_DIR:-/tmp/nanokvm-alpine-port}
ALPINE_RELEASE=${ALPINE_RELEASE:-3.24}
KERNEL_RELEASE=${KERNEL_RELEASE:-$(uname -r)}

die() {
    echo "stage-alpine-rootfs: $*" >&2
    exit 1
}

[ "$(id -u)" -eq 0 ] || die "must run as root"
[ -f "$TARGET/etc/alpine-release" ] || die "$TARGET is not an Alpine root"
[ -d "$SOURCE/usr/lib/modules/$KERNEL_RELEASE" ] ||
    die "missing source modules for $KERNEL_RELEASE"
[ -d "$PORT_DIR/openrc" ] || die "missing $PORT_DIR/openrc"

for directory in dev proc sys run tmp boot data root/.ssh; do
    mkdir -p "$TARGET/$directory"
done
mounted=
bind_mount() {
    source_path=$1
    target_path=$2
    fs_type=${3:-}
    if mountpoint -q "$target_path"; then
        return
    fi
    if [ -n "$fs_type" ]; then
        mount -t "$fs_type" "$source_path" "$target_path"
    else
        mount --bind "$source_path" "$target_path"
    fi
    mounted="$target_path $mounted"
}
cleanup() {
    for path in $mounted; do
        umount "$path" 2>/dev/null || true
    done
}
trap cleanup EXIT INT TERM

bind_mount /dev "$TARGET/dev"
bind_mount proc "$TARGET/proc" proc
bind_mount sysfs "$TARGET/sys" sysfs
cp "$SOURCE/etc/resolv.conf" "$TARGET/etc/resolv.conf"

cp "$PORT_DIR/repositories" "$TARGET/etc/apk/repositories"
chroot "$TARGET" /sbin/apk update
chroot "$TARGET" /sbin/apk add \
    alpine-base bash ca-certificates coreutils eudev exfatprogs f2fs-tools \
    hostapd i2c-tools iproute2 kmod libgcc libstdc++ openssh \
    util-linux-misc wpa_supplicant

rm -rf "$TARGET/lib/modules/$KERNEL_RELEASE"
mkdir -p "$TARGET/lib/modules"
cp -a "$SOURCE/usr/lib/modules/$KERNEL_RELEASE" "$TARGET/lib/modules/"
chroot "$TARGET" /sbin/depmod "$KERNEL_RELEASE"

mkdir -p "$TARGET/lib/firmware"
if [ -d "$SOURCE/usr/lib/firmware" ]; then
    cp -a "$SOURCE/usr/lib/firmware/." "$TARGET/lib/firmware/"
fi
if [ -d "$SOURCE/lib/firmware" ]; then
    cp -a "$SOURCE/lib/firmware/." "$TARGET/lib/firmware/"
fi
for directory in fw_vcodec nanokvm; do
    if [ -d "$SOURCE/usr/share/$directory" ]; then
        mkdir -p "$TARGET/usr/share/$directory"
        cp -a "$SOURCE/usr/share/$directory/." "$TARGET/usr/share/$directory/"
    fi
done

# Seed board defaults onto the persistent exFAT volume. Existing files win.
if [ -d "$SOURCE/mnt/data" ] && mountpoint -q "$SOURCE/data"; then
    for source_file in "$SOURCE"/mnt/data/*; do
        [ -f "$source_file" ] || continue
        destination="$SOURCE/data/$(basename "$source_file")"
        [ -e "$destination" ] || cp -a "$source_file" "$destination"
    done
fi

rm -rf "$TARGET/kvmapp"
cp -a "$SOURCE/kvmapp" "$TARGET/kvmapp"
mkdir -p "$TARGET/etc/kvm" "$TARGET/mnt/cfg"
[ ! -d "$SOURCE/etc/kvm" ] || cp -a "$SOURCE/etc/kvm/." "$TARGET/etc/kvm/"
[ ! -d "$SOURCE/mnt/cfg" ] || cp -a "$SOURCE/mnt/cfg/." "$TARGET/mnt/cfg/"
[ ! -f "$SOURCE/etc/nanokvm-buildroot" ] ||
    cp "$SOURCE/etc/nanokvm-buildroot" "$TARGET/etc/nanokvm-buildroot"

mkdir -p "$TARGET/usr/libexec/nanokvm/legacy"
for script in S02config S03usbdev S10uuid S15kvmhwd S25wifimod \
              S29qdisc S30eth S30wifi S30usbnet S95nanokvm; do
    [ -x "$SOURCE/etc/init.d/$script" ] ||
        die "missing source init script $script"
    cp "$SOURCE/etc/init.d/$script"        "$TARGET/usr/libexec/nanokvm/legacy/$script"
done
chmod 0755 "$TARGET/usr/libexec/nanokvm/legacy/"*
install -m 0755 "$PORT_DIR/compat/wpa-passphrase-compat" \
    "$TARGET/usr/libexec/nanokvm/wpa-passphrase-compat"
for script in S02config S03usbdev S10uuid S15kvmhwd S25wifimod \
              S29qdisc S30eth S30wifi S30usbnet S95nanokvm; do
    ln -snf "/usr/libexec/nanokvm/legacy/$script" "$TARGET/etc/init.d/$script"
done

for service in nanokvm-storage nanokvm-modules nanokvm-board \
               nanokvm-network nanokvm-usb nanokvm-app; do
    install -m 0755 "$PORT_DIR/openrc/$service" "$TARGET/etc/init.d/$service"
done

cat > "$TARGET/etc/fstab" <<'EOF'
/dev/mmcblk0p1 /boot vfat rw,nosuid,nodev,noexec 0 0
/dev/mmcblk0p3 /data exfat rw,nosuid,nodev,noexec 0 0
EOF

sed -i '/^tty[1-6]::respawn:/d; /^ttyS0::respawn:/d' "$TARGET/etc/inittab"
printf '%s\n' 'ttyS0::respawn:/sbin/getty -L 115200 ttyS0 vt100' >> "$TARGET/etc/inittab"
printf '%s\n' 'nanokvm-alpine' > "$TARGET/etc/hostname"
printf '%s\n' \
    'NAME="NanoKVM Alpine"' \
    'VERSION="0.1"' \
    'ALPINE_VERSION="3.24"' \
    'BUILD_PROFILE="stock"' > "$TARGET/etc/nanokvm-release"
printf '%s\n' stock > "$TARGET/etc/nanokvm-build-profile"

if [ -r "$SOURCE/root/.ssh/authorized_keys" ]; then
    cp "$SOURCE/root/.ssh/authorized_keys" "$TARGET/root/.ssh/authorized_keys"
    chmod 0700 "$TARGET/root/.ssh"
    chmod 0600 "$TARGET/root/.ssh/authorized_keys"
fi
chroot "$TARGET" /usr/bin/ssh-keygen -A

for service in devfs dmesg procfs sysfs; do
    chroot "$TARGET" /sbin/rc-update add "$service" sysinit
done
for service in bootmisc hostname hwdrivers localmount modules sysctl udev udev-trigger; do
    [ -x "$TARGET/etc/init.d/$service" ] &&
        chroot "$TARGET" /sbin/rc-update add "$service" boot
done
for service in nanokvm-storage nanokvm-modules nanokvm-board \
               nanokvm-network nanokvm-usb sshd nanokvm-app; do
    chroot "$TARGET" /sbin/rc-update add "$service" default
done
chroot "$TARGET" /sbin/rc-update add mount-ro shutdown

chroot "$TARGET" /sbin/apk fix
chroot "$TARGET" /sbin/apk audit --system >/tmp/nanokvm-alpine-apk-audit.txt || true

echo "NanoKVM Alpine root staged at $TARGET"
echo "Kernel/modules: $KERNEL_RELEASE"
echo "APK packages: $(chroot "$TARGET" /sbin/apk info | wc -l)"
du -sh "$TARGET" 2>/dev/null || true
