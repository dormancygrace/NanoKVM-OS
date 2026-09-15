#!/bin/sh
set -eu

repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
mkdir -p "$work/bin" "$work/boot" "$work/etc" "$work/kvm" "$work/run"

cat > "$work/bin/ok" <<'EOF'
#!/bin/sh
exit 0
EOF
cat > "$work/bin/ip" <<'EOF'
#!/bin/sh
exit 0
EOF
cat > "$work/bin/udhcpc" <<'EOF'
#!/bin/sh
printf '%s\n' "$@" > "$NANOKVM_TEST_UDHCPC_ARGS"
EOF
chmod 755 "$work/bin/ok" "$work/bin/ip" "$work/bin/udhcpc"

printf '%s\n' 'nano-kvm.test' > "$work/hostname"

run_eth() {
    NANOKVM_HOSTNAME_FILE=$work/hostname \
    NANOKVM_ETH_CONFIG=$work/boot/eth.nodhcp \
    NANOKVM_ETH_DHCP_PID=$work/run/udhcpc.eth0.pid \
    NANOKVM_IP=$work/bin/ip \
    NANOKVM_UDHCPC=$work/bin/udhcpc \
    NANOKVM_TEST_UDHCPC_ARGS=$work/eth.args \
        sh "$repo/kvmapp/system/init.d/S30eth" start
    wait_for "$work/eth.args"
}

run_wifi() {
    printf '%s\n' test > "$work/etc/wifi.ssid"
    : > "$work/etc/wifi.pass"
    printf '%s\n' open > "$work/etc/wifi.security"
    NANOKVM_HOSTNAME_FILE=$work/hostname \
    NANOKVM_WIFI_BOOT_DIR=$work/boot \
    NANOKVM_WIFI_ETC_DIR=$work/etc \
    NANOKVM_WIFI_KVM_DIR=$work/kvm \
    NANOKVM_WIFI_RUN_DIR=$work/run/wifi \
    NANOKVM_WIFI_DHCP_PID=$work/run/udhcpc.wlan0.pid \
    NANOKVM_IP=$work/bin/ip \
    NANOKVM_CHOWN=$work/bin/ok \
    NANOKVM_WPA_SUPPLICANT=$work/bin/ok \
    NANOKVM_UDHCPC=$work/bin/udhcpc \
    NANOKVM_TEST_UDHCPC_ARGS=$work/wifi.args \
        sh "$repo/kvmapp/system/init.d/S30wifi" start
    wait_for "$work/wifi.args"
}

wait_for() {
    path=$1
    attempts=0
    while [ ! -s "$path" ]; do
        attempts=$((attempts + 1))
        [ "$attempts" -lt 100 ] || return 1
        sleep 0.01
    done
}

assert_identity() {
    path=$1
    grep -Fqx -- '-x' "$path"
    grep -Fqx -- 'hostname:nano-kvm.test' "$path"
    grep -Fqx -- '-F' "$path"
    grep -Fqx -- 'nano-kvm.test' "$path"
}

run_eth
run_wifi
assert_identity "$work/eth.args"
assert_identity "$work/wifi.args"

printf '%s\n' 'bad hostname' > "$work/hostname"
rm -f "$work/eth.args" "$work/wifi.args"
run_eth
run_wifi
! grep -Fqx -- '-x' "$work/eth.args"
! grep -Fqx -- '-F' "$work/eth.args"
! grep -Fqx -- '-x' "$work/wifi.args"
! grep -Fqx -- '-F' "$work/wifi.args"

echo 'PASS: Ethernet and Wi-Fi advertise valid DHCP option 12/81 hostnames only'
