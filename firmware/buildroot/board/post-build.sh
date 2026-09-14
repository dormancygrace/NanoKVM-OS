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
s01fs_source="$(dirname "$0")/../../../kvmapp/system/init.d/S01fs"
test -f "$s01fs_source"
test -f "$NANOKVM_APP_STAGE/system/init.d/S01fs"
s01fs_hash=$(sha256sum "$s01fs_source" | cut -d' ' -f1)
test "$(sha256sum "$NANOKVM_APP_STAGE/system/init.d/S01fs" | cut -d' ' -f1)" = "$s01fs_hash"
flavour=${2:-compatibility}
if [ "$flavour" = enhanced ]; then
    kernel_release=$(cat "$NANOKVM_BOARD_ASSETS/kernel.release")
    case "$kernel_release" in *[!a-zA-Z0-9._+-]*|"") exit 1 ;; esac
    # A new version label must never silently package the old 5.10 modules.
    test -f "$NANOKVM_BOARD_ASSETS/usr/lib/modules/$kernel_release/extra/cvi_mipi_rx.ko"
    test -f "$NANOKVM_BOARD_ASSETS/usr/lib/modules/$kernel_release/modules.dep"
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
    # Remove modules disabled since the previous incremental build.
    rsync -a --delete "$NANOKVM_BOARD_ASSETS/usr/lib/modules/" "$target/usr/lib/modules/"
fi
if [ "$flavour" != enhanced ]; then install -m644 "$NANOKVM_APP_STAGE/system/ko/soph_mipi_rx.ko" "$target/mnt/system/ko/soph_mipi_rx.ko"; fi
cp -a "$NANOKVM_SENSOR_DATA/." "$target/mnt/data/"
cp "$target/mnt/data/sensor_cfg.ini.LT" "$target/mnt/data/sensor_cfg.ini"
for name in S00kmod S01fs S03usbdev S12temperature S13cpufreq S15kvmhwd S25wifimod S29qdisc S30eth S30wifi S50avahi-daemon S50sshd S80dnsmasq S95nanokvm S96picoclaw S97nkos-addons; do
    install -m755 "$NANOKVM_APP_STAGE/system/init.d/$name" "$target/etc/init.d/$name"
done
# Incremental Buildroot trees retain files from deselected packages.
# chrony is the sole system clock daemon in new images.
if [ -x "$target/usr/sbin/chronyd" ]; then
    rm -f "$target/etc/init.d/S49ntp" "$target/etc/init.d/S49ntpd" \
        "$target/usr/sbin/ntpd" "$target/usr/bin/ntpdate" "$target/usr/bin/ntpq" "$target/etc/ntp.conf"
fi
# HID-only is a selectable template, not a second boot service.
rm -f "$target/etc/init.d/S03usbhid"
# This image is already integrated; first-boot migration would reset settings,
# replace the receiver module and reboot based on obsolete MD5 allowlists.
touch "$target/etc/kvm/frame_detact"
# Fresh images keep SSH disabled until the user enables it in the web UI.
# S50sshd also honours /boot/start_ssh_once for one-boot recovery access.
touch "$target/etc/kvm/ssh_stop"
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
    # Audio playback invokes this helper from the application tree.
    mkdir -p "$target/kvmapp/system/bin" "$target/kvmapp/system/share/usb-audio"
    install -m755 "$NANOKVM_APP_STAGE/system/bin/usb-audio-capture" "$target/kvmapp/system/bin/usb-audio-capture"
    cp -a "$NANOKVM_APP_STAGE/system/share/usb-audio/." "$target/kvmapp/system/share/usb-audio/"

    for name in S00nkos-system-update S99nkos-system-confirm S13nanokvm-watchdog S38memory S94sg2002aes S94nanokvm-update; do
        install -m755 "$NANOKVM_APP_STAGE/system/init.d/$name" "$target/etc/init.d/$name"
    done
    python3 "$(dirname "$0")/system-update-base.py" "$target" "$NANOKVM_BOARD_ASSETS"
    release_version=$(cat "$NANOKVM_APP_STAGE/version")
    printf '%s\n' "$release_version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'
    : "${NANOKVM_RELEASE_SEQUENCE:?Set NANOKVM_RELEASE_SEQUENCE to the positive signed release sequence}"
    printf '%s\n' "$NANOKVM_RELEASE_SEQUENCE" | grep -Eq '^[1-9][0-9]*$'
    # The addon ABI is an explicit compatibility contract.  It must remain
    # stable when the OS release label changes (for example beta.3 -> beta.4).
    # Keep this input under source control instead of deriving it from a label.
    base_abi_file="$(dirname "$0")/enhanced/apk-base-abi.version"
    test -f "$base_abi_file"
    base_abi_version=$(cat "$base_abi_file")
    printf '%s\n' "$base_abi_version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'
    # APK addons live in their own root.  The marker records the exact base
    # providers that the full image transaction must install in its fresh APK
    # database; it is metadata, never a dummy provider package.
    test -x "$target/usr/sbin/nkos-addons"
    mkdir -p "$target/opt/nkos/addons" "$target/opt/nkos/etc/apk/keys" \
        "$target/opt/nkos/etc/apk/protected_paths.d" "$target/opt/nkos/etc/apk/repositories.d" \
        "$target/opt/nkos/etc/nkos"
    # Incremental Buildroot targets must not carry stale APK state into a new
    # image.  The native host apk records this image's immutable providers
    # below, before any addon transaction can run.
    rm -rf "$target/opt/nkos/lib/apk/db" "$target/opt/nkos/etc/apk/world"
    mkdir -p "$target/opt/nkos/lib/apk/db"
    install -m644 "$(dirname "$0")/overlay/opt/nkos/etc/apk/arch" "$target/opt/nkos/etc/apk/arch"
    install -m644 "$(dirname "$0")/overlay/opt/nkos/etc/apk/repositories" "$target/opt/nkos/etc/apk/repositories"
    cp -a "$(dirname "$0")/overlay/opt/nkos/etc/apk/keys/." "$target/opt/nkos/etc/apk/keys/"
    cp -a "$(dirname "$0")/overlay/opt/nkos/etc/apk/protected_paths.d/." "$target/opt/nkos/etc/apk/protected_paths.d/"
    cp -a "$(dirname "$0")/overlay/opt/nkos/etc/apk/repositories.d/." "$target/opt/nkos/etc/apk/repositories.d/"
    cat > "$target/opt/nkos/etc/nkos/base-abi.env" <<EOF
base_abi_provider=nkos-base-abi
base_abi_version=$base_abi_version
server_api_provider=nkos-server-api
server_api_version=1
features=nkos-feature-static-riscv64=1,nkos-feature-shell=1,nkos-feature-private-libs-riscv64=1
EOF
    chmod 644 "$target/opt/nkos/etc/nkos/base-abi.env"
    # Keep the immutable capability contract beside the runtime.  It contains
    # only the image ABI/API/capabilities and inline public trust material;
    # addon world, catalog and index state remain independently replaceable.
    contract_template="$(dirname "$0")/../../addons/contract.json"
    key_source="$(dirname "$0")/overlay/opt/nkos/etc/apk/keys/nanokvm-os-packages-rsa4096.pem"
    test -f "$contract_template"
    test -f "$key_source"
    mkdir -p "$target/usr/share/nkos"
    python3 - "$contract_template" "$target/usr/share/nkos/addons-contract.json" "$base_abi_version" <<'PY'
import json
import os
import sys

source, destination, base_abi = sys.argv[1:4]
with open(source, encoding="utf-8") as stream:
    contract = json.load(stream)
if contract.get("format") != 1 or contract.get("base_abi") != "<image release version>":
    raise SystemExit("invalid addon contract template identity")
if any(field in contract for field in ("world", "packages", "catalog", "index_sha256")):
    raise SystemExit("immutable addon contract contains repository transaction state")
contract["base_abi"] = base_abi
temporary = destination + ".new"
with open(temporary, "w", encoding="utf-8") as stream:
    json.dump(contract, stream, indent=2)
    stream.write("\n")
    stream.flush()
    os.fsync(stream.fileno())
os.replace(temporary, destination)
PY
    python3 - "$target/usr/share/nkos/addons-contract.json" "$key_source" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as stream:
    contract = json.load(stream)
with open(sys.argv[2], encoding="utf-8") as stream:
    public_key = stream.read()
keys = contract.get("trust", {}).get("keys", [])
if len(keys) != 1 or keys[0].get("public_key_pem") != public_key:
    raise SystemExit("contract trust key does not match image APK trust key")
PY
    if [ -n "${HOST_DIR:-}" ]; then
        NKOS_HOST_APK="$HOST_DIR/bin/apk" \
            "$(dirname "$0")/initialize-nkos-apk-db.sh" "$target" "$base_abi_version" \
            "$target/usr/share/nkos/addons-contract.json"
    else
        "$(dirname "$0")/initialize-nkos-apk-db.sh" "$target" "$base_abi_version" \
            "$target/usr/share/nkos/addons-contract.json"
    fi
    # The immutable image carries the explicitly selected positive release
    # sequence in its installed identity.  The updater capability state starts
    # at sequence 0 because no replaceable updater package is installed yet.
    mkdir -p "$target/kvmapp/.os-update"
    cat > "$target/kvmapp/.os-update/installed.json" <<EOF
{"version":"$release_version","sequence":$NANOKVM_RELEASE_SEQUENCE}
EOF
    cat > "$target/kvmapp/.os-update/updater.json" <<'EOF'
{"capability":2,"sequence":0}
EOF
    chmod 600 "$target/kvmapp/.os-update/installed.json" "$target/kvmapp/.os-update/updater.json"
    # Providers are fileless virtual records created by native apk; no
    # installable ABI-provider package is present in the repository.
    test -s "$target/opt/nkos/lib/apk/db/installed"
    test -s "$target/opt/nkos/etc/apk/world"
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
    test -x "$target/usr/sbin/openvpn3"
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
        [ "$(basename "$script")" = S01fs ] && continue
        install -m755 "$script" "$target/etc/init.d/$(basename "$script")"
        install -m755 "$script" "$target/kvmapp/system/init.d/$(basename "$script")"
    done
    # S01fs is owned by kvmapp.  Keep both runtime copies byte-identical after
    # all board-service overlays, including retained application stages.
    install -m755 "$NANOKVM_APP_STAGE/system/init.d/S01fs" "$target/etc/init.d/S01fs"
    install -m755 "$NANOKVM_APP_STAGE/system/init.d/S01fs" "$target/kvmapp/system/init.d/S01fs"
    test "$(sha256sum "$target/etc/init.d/S01fs" | cut -d' ' -f1)" = "$s01fs_hash"
    test "$(sha256sum "$target/kvmapp/system/init.d/S01fs" | cut -d' ' -f1)" = "$s01fs_hash"
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

# Remove ownership-recorded stale optional payloads from incremental outputs.
python3 "$(dirname "$0")/prune-optional.py" "$target" \
    --build-dir "${BUILD_DIR:?Buildroot BUILD_DIR is required}" \
    --config "${BR2_CONFIG:?Buildroot BR2_CONFIG is required}"
