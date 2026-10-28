#!/bin/sh
# Shared local entry point for the stock offline package and the SSH bootstrap.
set -eu
PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
fail() { echo "NanoKVM OS installation: $*" >&2; exit 1; }
usage() { echo "Usage: sh install.sh BUNDLE --check|--yes"; exit 2; }
[ "$#" = 2 ] || usage
bundle=$(cd "$1" && pwd) || fail "bundle directory is missing"
case "$2" in --check|--yes) mode=$2 ;; *) usage ;; esac
[ "$(id -u)" = 0 ] || fail "run as root"
[ "$(uname -m)" = riscv64 ] || fail "only SG2002 NanoKVM Cube/Lite/PCIe is supported"
[ ! -e /etc/alpine-release ] || fail "NanoKVM OS is already installed; use package updates"
[ -f /etc/kvm/hw ] && [ -f /kvmapp/version ] || fail "stock NanoKVM identity is missing"
case "$(cat /etc/kvm/hw)" in alpha|beta|pcie) ;; *) fail "unsupported stock board" ;; esac
[ -r /proc/device-tree/compatible ] || fail "device tree is unavailable"
tr '\000' '\n' </proc/device-tree/compatible | grep -Eq '^(sophgo,sg2002|sophgo,cv181x|sipeed,licheerv-nano)$' ||
    fail "not an SG2002 device tree"
for cmd in sha256sum awk tar df sync cmp find; do
    command -v "$cmd" >/dev/null || fail "required command is missing: $cmd"
done
# Verify before executing any payload program. This manifest travels inside the
# offline package, whose download is checked against the release SHA256SUMS.
for name in stock-layout recovery.sd boot.img.gz alpine-rootfs.tar.gz rootfs-kib target-version install.sh offline-run.sh; do
    [ -f "$bundle/$name" ] && [ ! -L "$bundle/$name" ] || fail "missing regular payload: $name"
    expected=$(awk -v wanted="$name" '$2==wanted && NF==2 {n++; hash=$1} END {if(n==1) print hash; else exit 1}' "$bundle/SHA256SUMS") ||
        fail "missing or duplicate checksum: $name"
    case "$expected" in *[!0-9a-f]*|'') fail "invalid checksum: $name" ;; esac
    [ "${#expected}" = 64 ] || fail "invalid checksum: $name"
    actual=$(sha256sum "$bundle/$name") || fail "cannot read payload: $name"
    [ "${actual%% *}" = "$expected" ] || fail "damaged installation bundle: $name"
done
mounted() {
    awk -v d="$1" -v p="$2" -v t="$3" '
        $1==d && $2==p && $3==t && ("," $4 ",") ~ /,rw,/ { found=1 }
        END { exit !found }' /proc/mounts
}
mounted /dev/mmcblk0p1 /boot vfat || fail "/boot must be the writable FAT partition 1"
mounted /dev/mmcblk0p3 /data exfat || fail "/data must be writable exFAT partition 3; detach the stock virtual disk first"
awk '$2=="/" && $3=="ext4" && ($1=="/dev/mmcblk0p2" || $1=="/dev/root") {found=1} END {exit !found}' /proc/mounts ||
    fail "the running root must be stock ext4 on partition 2"
case " $(cat /proc/cmdline) " in *' root=/dev/mmcblk0p2 '*) ;; *) fail "root boot device is not mmcblk0p2" ;; esac
# A mounted ISO or a raw data LUN can still be written by the USB host. Ask the
# operator to detach it; do not silently drop USB networking used for SSH.
for file in /sys/kernel/config/usb_gadget/*/functions/mass_storage.*/lun.*/file; do
    [ -e "$file" ] || continue
    medium=$(cat "$file") || fail "cannot inspect USB virtual media"
    [ -z "$medium" ] || fail "detach every USB virtual disk/image before installation"
done
plan=$("$bundle/stock-layout" plan /dev/mmcblk0) || fail "unsupported SD layout"
# Only the compiled helper emits these nine decimal integers, never shell code.
set -- $plan
[ "$#" = 9 ] || fail "invalid layout plan"
for part in 1 2 3; do
    [ "$(cat /sys/class/block/mmcblk0p$part/start)" = "$1" ] &&
    [ "$(cat /sys/class/block/mmcblk0p$part/size)" = "$2" ] || fail "kernel and MBR partition geometry differ"
    shift 2
done
root_kib=$(cat "$bundle/rootfs-kib")
case "$root_kib" in ''|*[!0-9]*) fail "invalid root size" ;; esac
[ "$(( $3 / 2 ))" -ge "$((root_kib + 131072))" ] || fail "new root lacks 128 MiB of free headroom"
free_kib() { df -Pk "$1" | awk 'NR==2 {print $4}'; }
need_boot=$(( ($(wc -c <"$bundle/recovery.sd") + 1023) / 1024 + 1024 ))
[ "$(free_kib /boot)" -ge "$need_boot" ] || fail "/boot lacks space for recovery beside the stock boot image (need $need_boot KiB)"
need_data=$(( $(du -sk "$bundle" | awk '{print $1}') + ($(wc -c </boot/boot.sd) + 1023) / 1024 + 8192 ))
[ "$(free_kib /data)" -ge "$need_data" ] || fail "/data needs $need_data KiB free"
[ -f /boot/boot.sd ] && [ ! -L /boot/boot.sd ] || fail "stock boot image is missing"
[ ! -e /boot/dbg.sd ] || fail "remove the existing debug boot override first"
stage=/data/.nanokvm-stock-install
[ ! -e "$stage" ] || fail "$stage already exists; inspect its status before starting another migration"
echo "Ready: keep partition 3; build a 64 MiB boot and $(( $3 / 2048 )) MiB root inside old partition 2."
echo "The stock OS and installed applications will be replaced. Network and SSH access settings are retained."
echo "The web login becomes admin/admin and requires a new password. Keep power connected."
[ "$mode" = --yes ] || exit 0
mkdir /run/nanokvm-stock-install.lock || fail "another installation is running"
trap 'rmdir /run/nanokvm-stock-install.lock 2>/dev/null || true' EXIT
mkdir "$stage" || fail "cannot create staging directory"
# Keep failed preparation available for diagnosis; only this fixed directory is
# owned by the installer. Never erase somebody else's directory on a retry.
printf 'preparing\n' >"$stage/status"
for name in boot.img.gz alpine-rootfs.tar.gz recovery.sd; do
    cp "$bundle/$name" "$stage/$name" || fail "cannot stage $name"
    cmp "$bundle/$name" "$stage/$name" || fail "staged $name differs"
done
"$bundle/stock-layout" save /dev/mmcblk0 >"$stage/mbr.old" || fail "cannot save partition table"
cp /boot/boot.sd "$stage/stock-boot.sd" || fail "cannot retain stock boot for pre-format abort"
# Construct a small, explicit access-settings archive. Do not import stock app
# databases, executables, service configuration or arbitrary symlink targets.
umask 077
settings=$(mktemp -d /tmp/nanokvm-settings.XXXXXX)
trap 'rm -f "$settings.paths"; rm -rf "$settings"; rmdir /run/nanokvm-stock-install.lock 2>/dev/null || true' EXIT
paths='etc/hostname etc/kvm/wifi.ssid etc/kvm/wifi.pass root/.ssh/authorized_keys
boot/eth.nodhcp boot/wifi.nodhcp boot/wifi.ssid boot/wifi.pass boot/hostname boot/hostname.prefix boot/hostname.suffix'
for path in $paths; do
    [ ! -L "/$path" ] || fail "settings symlink is unsupported: $path"
    [ -e "/$path" ] || continue
    [ -f "/$path" ] && [ "$(wc -c <"/$path")" -le 65536 ] || fail "invalid settings file: $path"
    mkdir -p "$settings/${path%/*}"
    cp -p "/$path" "$settings/$path"
done
for key in /etc/ssh/ssh_host_*; do
    [ -e "$key" ] || continue
    case "${key##*/}" in ssh_host_rsa_key|ssh_host_rsa_key.pub|ssh_host_ecdsa_key|ssh_host_ecdsa_key.pub|ssh_host_ed25519_key|ssh_host_ed25519_key.pub) ;; *) continue ;; esac
    [ -f "$key" ] && [ ! -L "$key" ] && [ "$(wc -c <"$key")" -le 65536 ] || fail "invalid SSH host key"
    mkdir -p "$settings/etc/ssh"
    cp -p "$key" "$settings/etc/ssh/"
done
umask 077
awk -F: '$1=="root" {if (NF!=9 || ++n!=1) exit 1; print} END {if(n!=1) exit 1}' /etc/shadow >"$stage/root.shadow" || fail "cannot preserve root password"
if [ -e /etc/kvm/ssh_stop ]; then echo off; else echo on; fi >"$stage/ssh-state"
# Record regular files only; tar directory entries must not replace the new
# root's /etc and /root directory permissions with staging-directory modes.
(cd "$settings" && find . -type f) >"$settings.paths"
set --
while IFS= read -r path; do set -- "$@" "$path"; done <"$settings.paths"
if [ "$#" -gt 0 ]; then
    tar --numeric-owner -cf "$stage/settings.tar" -C "$settings" "$@" || fail "cannot package settings"
else
    dd if=/dev/zero of="$stage/settings.tar" bs=1024 count=1 2>/dev/null || fail "cannot create empty settings archive"
fi
(cd "$stage" && sha256sum mbr.old stock-boot.sd root.shadow ssh-state settings.tar >transaction.sha256)
# A single complete activation record: recovery never reads half-prepared data.
printf 'prepared\n' >"$stage/status"
sync
(cd "$stage" && sha256sum -c transaction.sha256) || fail "staged settings failed readback"
[ "$("$bundle/stock-layout" state /dev/mmcblk0 "$stage/mbr.old")" = stock ] || fail "partition table changed"
cp "$stage/recovery.sd" /boot/.nanokvm-recovery.new || fail "cannot write recovery"
sync
cmp "$stage/recovery.sd" /boot/.nanokvm-recovery.new || fail "recovery readback failed"
mv -f /boot/.nanokvm-recovery.new /boot/boot.sd || fail "cannot activate recovery"
sync
echo "Recovery activated. Rebooting; installation continues without the SSH connection."
reboot
