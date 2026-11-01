#!/bin/sh

set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
SCRIPT=$ROOT/kvmapp/system/init.d/S30wifi
TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/nanokvm-s30wifi.XXXXXX")
trap 'rm -rf "$TMP_ROOT"' EXIT HUP INT TERM

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

assert_file() {
    [ -f "$1" ] || fail "missing file: $1"
}

assert_absent() {
    [ ! -e "$1" ] || fail "unexpected path: $1"
}

assert_contains() {
    pattern=$1
    file=$2
    grep -F -- "$pattern" "$file" >/dev/null || fail "$file does not contain: $pattern"
}

assert_not_contains() {
    pattern=$1
    file=$2
    if grep -F -- "$pattern" "$file" >/dev/null; then
        fail "$file contains: $pattern"
    fi
}

new_case() {
    name=$1
    CASE_DIR=$TMP_ROOT/$name
    BOOT_DIR=$CASE_DIR/boot
    ETC_DIR=$CASE_DIR/etc/kvm
    KVM_DIR=$CASE_DIR/kvm
    RUN_DIR=$CASE_DIR/run
    AP_FLAG=$CASE_DIR/wifiap
    UID_FILE=$CASE_DIR/base_uid
    PHY_LINK=$CASE_DIR/phy80211
    DHCP_PID=$RUN_DIR/udhcpc.wlan0.pid
    CALL_LOG=$CASE_DIR/calls.log
    BIN_DIR=$CASE_DIR/bin
    # wlan0 is there, so no driver is loaded.
    NET_DIR=$CASE_DIR/net/wlan0
    mkdir -p "$BOOT_DIR" "$ETC_DIR" "$KVM_DIR" "$BIN_DIR" "$NET_DIR"
    mkdir -p "$CASE_DIR/phy0"
    ln -s "$CASE_DIR/phy0" "$PHY_LINK"
    printf 'test-device-uid\n' > "$UID_FILE"
    : > "$CALL_LOG"

    cat > "$BIN_DIR/wpa_passphrase" <<'EOF'
#!/bin/sh
read -r password
printf 'wpa_passphrase args=%s stdin=%s\n' "$*" "$password" >> "$CALL_LOG"
[ "${WPA_FAIL:-0}" -eq 0 ] || exit 1
printf '%s\n' 'network={' "    ssid=\"$1\"" "    #psk=\"$password\"" \
    '    psk=0123456789abcdef' '}'
EOF
    chmod +x "$BIN_DIR/wpa_passphrase"

    # No process is ever running.
    cat > "$BIN_DIR/pidof" <<'EOF'
#!/bin/sh
printf 'pidof args=%s\n' "$*" >> "$CALL_LOG"
exit 1
EOF
    chmod +x "$BIN_DIR/pidof"
    for command in wpa_supplicant wpa_cli udhcpc hostapd udhcpd ifconfig ip killall chown iw
    do
        cat > "$BIN_DIR/$command" <<EOF
#!/bin/sh
printf '%s args=%s\n' '$command' "\$*" >> "\$CALL_LOG"
exit 0
EOF
        chmod +x "$BIN_DIR/$command"
    done
}

run_action() (
    export CALL_LOG WPA_FAIL
    export NANOKVM_PIDOF="$BIN_DIR/pidof"
    export NANOKVM_WPA_CTRL_DIR="$CASE_DIR/wpa_ctrl"
    export NANOKVM_HOSTAPD_CTRL_DIR="$CASE_DIR/hostapd_ctrl"
    export NANOKVM_WIFI_BOOT_DIR="$BOOT_DIR"
    export NANOKVM_WIFI_ETC_DIR="$ETC_DIR"
    export NANOKVM_WIFI_KVM_DIR="$KVM_DIR"
    export NANOKVM_WIFI_RUN_DIR="$RUN_DIR"
    export NANOKVM_WIFI_AP_FLAG="$AP_FLAG"
    export NANOKVM_WIFI_UID_FILE="$UID_FILE"
    export NANOKVM_WIFI_DHCP_PID="$DHCP_PID"
    export NANOKVM_WPA_PASSPHRASE="$BIN_DIR/wpa_passphrase"
    export NANOKVM_WPA_SUPPLICANT="$BIN_DIR/wpa_supplicant"
    export NANOKVM_WPA_CLI="$BIN_DIR/wpa_cli"
    export NANOKVM_UDHCPC="$BIN_DIR/udhcpc"
    export NANOKVM_HOSTAPD="$BIN_DIR/hostapd"
    export NANOKVM_UDHCPD="$BIN_DIR/udhcpd"
    export NANOKVM_IFCONFIG="$BIN_DIR/ifconfig"
    export NANOKVM_IP="$BIN_DIR/ip"
    export NANOKVM_KILLALL="$BIN_DIR/killall"
    export NANOKVM_CHOWN="$BIN_DIR/chown"
    export NANOKVM_IW="$BIN_DIR/iw"
    export NANOKVM_WIFI_PHY_LINK="$PHY_LINK"
    export NANOKVM_WIFI_NET_DIR="$NET_DIR"
    "$SCRIPT" "$@"
)

# With no credentials, boot must not launch idle WPA/DHCP processes or create
# persistent/runtime configuration.
WPA_FAIL=0

new_case no_credentials
run_action start
assert_contains 'ip args=link set dev wlan0 up' "$CALL_LOG"
assert_not_contains 'wpa_supplicant args=' "$CALL_LOG"
assert_not_contains 'udhcpc args=' "$CALL_LOG"
assert_absent "$RUN_DIR"

# Boot credentials are imported once, tightened to mode 0600, and used to build
# a mode-0600 runtime config without the plaintext comment emitted by
# wpa_passphrase. The password is provided on stdin, not in argv.
new_case sta
printf 'Office WiFi\n' > "$BOOT_DIR/wifi.ssid"
printf 'secret123\n' > "$BOOT_DIR/wifi.pass"
run_action start
assert_absent "$BOOT_DIR/wifi.ssid"
assert_absent "$BOOT_DIR/wifi.pass"
assert_file "$ETC_DIR/wifi.ssid"
assert_file "$ETC_DIR/wifi.pass"
[ "$(stat -c '%a' "$ETC_DIR/wifi.ssid")" = 600 ] || fail 'SSID mode is not 0600'
[ "$(stat -c '%a' "$ETC_DIR/wifi.pass")" = 600 ] || fail 'password mode is not 0600'
[ "$(stat -c '%a' "$RUN_DIR")" = 700 ] || fail 'runtime directory mode is not 0700'
[ "$(stat -c '%a' "$RUN_DIR/wpa_supplicant.conf")" = 600 ] || fail 'WPA config mode is not 0600'
assert_contains 'ctrl_interface=/var/run/wpa_supplicant' "$RUN_DIR/wpa_supplicant.conf"
assert_contains 'psk=0123456789abcdef' "$RUN_DIR/wpa_supplicant.conf"
assert_contains 'key_mgmt=WPA-PSK SAE' "$RUN_DIR/wpa_supplicant.conf"
assert_contains 'ieee80211w=1' "$RUN_DIR/wpa_supplicant.conf"
assert_contains 'sae_password=736563726574313233' "$RUN_DIR/wpa_supplicant.conf"
assert_not_contains '#psk=' "$RUN_DIR/wpa_supplicant.conf"
assert_not_contains 'secret123' "$RUN_DIR/wpa_supplicant.conf"
assert_contains 'wpa_passphrase args=Office WiFi stdin=secret123' "$CALL_LOG"
assert_contains "wpa_supplicant args=-B -i wlan0 -c $RUN_DIR/wpa_supplicant.conf" "$CALL_LOG"
assert_contains "udhcpc args=-S -B -O 121 -i wlan0 -t 10 -T 1 -A 5 -b -p $DHCP_PID" "$CALL_LOG"

# Invalid credentials must not leave a partial config or launch networking.
new_case invalid_credentials
printf 'Office\n' > "$ETC_DIR/wifi.ssid"
printf 'short\n' > "$ETC_DIR/wifi.pass"
WPA_FAIL=1
if run_action start; then
    fail 'wpa_passphrase failure was ignored'
fi
WPA_FAIL=0
assert_absent "$RUN_DIR/wpa_supplicant.conf"
assert_not_contains 'wpa_supplicant args=' "$CALL_LOG"
assert_not_contains 'udhcpc args=' "$CALL_LOG"

# Advertised protection maps to an explicit protocol, never legacy WPA+SAE.
for security in wpa wpa2 wpa3 wpa2-wpa3
do
    new_case "security_$security"
    printf Office > "$ETC_DIR/wifi.ssid"
    printf secret123 > "$ETC_DIR/wifi.pass"
    printf %s "$security" > "$ETC_DIR/wifi.security"
    run_action start
    case "$security" in
        wpa)
            assert_contains 'proto=WPA' "$RUN_DIR/wpa_supplicant.conf"
            assert_not_contains 'SAE' "$RUN_DIR/wpa_supplicant.conf"
            assert_not_contains 'sae_password=' "$RUN_DIR/wpa_supplicant.conf" ;;
        wpa2)
            assert_contains 'proto=RSN' "$RUN_DIR/wpa_supplicant.conf"
            assert_contains 'key_mgmt=WPA-PSK' "$RUN_DIR/wpa_supplicant.conf"
            assert_not_contains 'SAE' "$RUN_DIR/wpa_supplicant.conf" ;;
        wpa3)
            assert_contains 'proto=RSN' "$RUN_DIR/wpa_supplicant.conf"
            assert_contains 'key_mgmt=SAE' "$RUN_DIR/wpa_supplicant.conf"
            assert_contains 'ieee80211w=2' "$RUN_DIR/wpa_supplicant.conf"
            assert_not_contains 'psk=' "$RUN_DIR/wpa_supplicant.conf" ;;
        wpa2-wpa3)
            assert_contains 'proto=RSN' "$RUN_DIR/wpa_supplicant.conf"
            assert_contains 'key_mgmt=WPA-PSK SAE' "$RUN_DIR/wpa_supplicant.conf" ;;
    esac
done
echo 'S30wifi runtime-state tests passed'
