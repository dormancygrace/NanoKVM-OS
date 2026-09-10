#!/bin/sh
# Buildroot post-build hook. Board assets must match the kernel shipped in boot/.
set -eu
target=$(realpath "$1")
: "${NANOKVM_APP_STAGE:?Set NANOKVM_APP_STAGE to the assembled application directory}"
: "${NANOKVM_BOARD_ASSETS:?Set NANOKVM_BOARD_ASSETS to the verified boot and module directory}"
NANOKVM_SENSOR_DATA=$(dirname "$0")/sensor-data
[ "$target" != / ] && [ -d "$target/usr/lib" ] || exit 1
for file in server/NanoKVM-Server server/dl_lib/libkvm.so kvm_system/kvm_system version; do
    test -f "$NANOKVM_APP_STAGE/$file"
done
flavour=${2:-compatibility}
if [ "$flavour" = enhanced ]; then
    test "$(cat "$NANOKVM_BOARD_ASSETS/kernel.release")" = 7.2.4-nanokvm-enhanced
    # A new version label must never silently package the old 5.10 modules.
    test -f "$NANOKVM_BOARD_ASSETS/usr/lib/modules/7.2.4-nanokvm-enhanced/extra/cvi_mipi_rx.ko"
    test -f "$NANOKVM_BOARD_ASSETS/usr/lib/modules/7.2.4-nanokvm-enhanced/modules.dep"
    python3 "$(dirname "$0")/verify-board-assets.py" "$NANOKVM_BOARD_ASSETS"
    for executable in server/NanoKVM-Server kvm_system/kvm_system; do
        readelf -l "$NANOKVM_APP_STAGE/$executable" | grep -q '/lib/ld-musl-riscv64.so.1'
    done
else
    test -f "$NANOKVM_BOARD_ASSETS/mnt/system/ko/soph_sys.ko"
    test -f "$NANOKVM_APP_STAGE/system/ko/soph_mipi_rx.ko"
fi
test -f "$NANOKVM_SENSOR_DATA/sensor_cfg.ini.LT"
# The new base owns its tools and libc. The OTA compatibility bundle is only
# needed for older installed systems and must not overwrite Buildroot packages.
mkdir -p "$target/kvmapp" "$target/mnt/system" "$target/mnt/cfg" "$target/mnt/data" "$target/etc/kvm" "$target/boot" "$target/data"
rsync -a --delete --delete-excluded --exclude=/system/modern/ --exclude=/system/bin/ --exclude=/system/share/ "$NANOKVM_APP_STAGE/" "$target/kvmapp/"
rm -f "$target/kvmapp/kvm_new_app" "$target/kvmapp/kvm_new_img"
rsync -a "$NANOKVM_BOARD_ASSETS/mnt/system/" "$target/mnt/system/"
if [ "$flavour" = enhanced ]; then
    rsync -a "$NANOKVM_BOARD_ASSETS/usr/" "$target/usr/"
fi
if [ "$flavour" != enhanced ]; then install -m644 "$NANOKVM_APP_STAGE/system/ko/soph_mipi_rx.ko" "$target/mnt/system/ko/soph_mipi_rx.ko"; fi
cp -a "$NANOKVM_SENSOR_DATA/." "$target/mnt/data/"
cp "$target/mnt/data/sensor_cfg.ini.LT" "$target/mnt/data/sensor_cfg.ini"
for name in S00kmod S01fs S03usbdev S15kvmhwd S25wifimod S30eth S30wifi S50avahi-daemon S50sshd S80dnsmasq S95nanokvm S96picoclaw; do
    install -m755 "$NANOKVM_APP_STAGE/system/init.d/$name" "$target/etc/init.d/$name"
done
# HID-only is a selectable template, not a second boot service.
rm -f "$target/etc/init.d/S03usbhid"
# This image is already integrated; first-boot migration would reset settings,
# replace the receiver module and reboot based on obsolete MD5 allowlists.
touch "$target/etc/kvm/frame_detact"
# High-frequency telemetry lives on tmpfs, including for a fresh image.
for name in now_fps state width height wifi_state; do
    rm -f "$target/kvmapp/kvm/$name"
    ln -s "/run/nanokvm/$name" "$target/kvmapp/kvm/$name"
done
# Do not carry credentials, machine identity or host keys into distributable images.
test ! -e "$target/etc/ssh/ssh_host_ed25519_key"
test ! -e "$target/etc/kvm/pwd"
printf '%s\n' "Buildroot 2026.08; SG2002/C906; flavour=$flavour" > "$target/etc/nanokvm-buildroot"
# Catch accidental replacement of the generic loader by the vendor archive.
[ "$(readlink "$target/usr/lib/ld-musl-riscv64.so.1")" = libc.so ]
if [ "$flavour" != enhanced ]; then
    test -f "$target/usr/lib/ld-musl-riscv64xthead.so.1"
    test -f "$target/usr/lib/ld-musl-riscv64v0p7_xthead.so.1"
fi

if [ "$flavour" = enhanced ]; then
    install -m755 "$(dirname "$0")/../../../server/service/network/scripts/S02ipv6" "$target/etc/init.d/S02ipv6"
    # Memory is a base service; Tailscale installs its boot script on user enable.
    # Linux scheduling profiles remain opt-in; S95 retains the tested Go timer policy.
    install -m755 "$(dirname "$0")/enhanced/tools/nanokvm-wifi-tx-policy" "$target/usr/sbin/nanokvm-wifi-tx-policy"
    install -m755 "$(dirname "$0")/enhanced/tools/nanokvm-wifi-tx-live" "$target/usr/sbin/nanokvm-wifi-tx-live"
    install -m755 "$NANOKVM_APP_STAGE/system/bin/nkos-update" "$target/usr/sbin/nkos-update"
    for name in S13nanokvm-watchdog S38memory S94sg2002aes S94nanokvm-update; do
        install -m755 "$NANOKVM_APP_STAGE/system/init.d/$name" "$target/etc/init.d/$name"
    done
    release_version=$(cat "$NANOKVM_APP_STAGE/version")
    printf '%s\n' "$release_version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'
    display_version=$(printf '%s' "$release_version" | sed 's/-beta\./ beta-/')
    cat > "$target/usr/lib/os-release" <<EOF
NAME="NanoKVM OS"
ID=nanokvm-os
VERSION="$display_version"
VERSION_ID="$release_version"
PRETTY_NAME="NanoKVM OS v$display_version"
EOF
    printf 'NanoKVM OS v%s\n' "$display_version" > "$target/etc/issue"
    # Remove stale updater artifacts from incremental Buildroot targets too.
    rm -f "$target/etc/nanokvm/image-updates-only" "$target/etc/kvm/update-nanokvm.py"
    rm -f "$target/kvmapp/system/update-components.sh" "$target/kvmapp/system/update-nanokvm.py"
    # A kernel DCO module alone cannot provide a usable OpenVPN client.
    test -x "$target/usr/sbin/openvpn"
    # Buildroot owns this rebuilt tool; preserve the historical application path.
    test -x "$target/usr/sbin/nanokvm_update_edid"
    mkdir -p "$target/kvmapp/system/tool"
    ln -sfn /usr/sbin/nanokvm_update_edid "$target/kvmapp/system/tool/nanokvm_update_edid"
    # GCC's auto-load helper embeds the host sysroot; it is unused without target GDB.
    if [ ! -x "$target/usr/bin/gdb" ]; then
        rm -f "$target/usr/lib"/libstdc++.so.*-gdb.py
    fi
    # Ship nftables tools without enabling a packet-filtering policy at boot.
    rm -f "$target/etc/init.d/S35iptables" "$target/etc/init.d/S35nftables"
    # These scripts target the old LicheeRV PMU/framebuffer and absent Maix apps.
    rm -f "$target/etc/init.d/S00pmu" "$target/etc/init.d/S04fb"         "$target/etc/profile.d/app_store.sh" "$target/etc/rc.local"
    for script in "$(dirname "$0")"/enhanced/init.d/*; do
        install -m755 "$script" "$target/etc/init.d/$(basename "$script")"
        install -m755 "$script" "$target/kvmapp/system/init.d/$(basename "$script")"
    done
    # Mainline DWC2 is already peripheral in DT; vendor role procfs is absent.
    for name in S03usbdev S03usbhid; do
        sed -i '/echo device > \/proc\/cviusb\/otg_role/d; /echo host > \/proc\/cviusb\/otg_role/d' "$target/kvmapp/system/init.d/$name"
    done
    python3 "$(dirname "$0")/enhanced/usb-network-hooks.py" "$target/kvmapp/system/init.d"
    cp "$target/kvmapp/system/init.d/S03usbdev" "$target/etc/init.d/S03usbdev"
    for name in S03usbdev S03usbhid; do
        # Templates are kept under kvmapp for runtime profile selection.
        test -x "$target/kvmapp/system/init.d/$name"
    done
fi
