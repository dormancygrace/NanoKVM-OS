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

assert_count() {
    pattern=$1
    expected=$2
    file=$3
    actual=$(grep -Fc -- "$pattern" "$file" || true)
    [ "$actual" = "$expected" ] || fail "$file has $actual occurrences of $pattern; expected $expected"
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
    mkdir -p "$BOOT_DIR" "$ETC_DIR" "$KVM_DIR" "$BIN_DIR"
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

    for command in wpa_supplicant udhcpc hostapd udhcpd ifconfig ip killall chown
    do
        cat > "$BIN_DIR/$command" <<EOF
#!/bin/sh
printf '%s args=%s\n' '$command' "\$*" >> "\$CALL_LOG"
exit 0
EOF
        chmod +x "$BIN_DIR/$command"
    done
    chmod +x "$BIN_DIR/wpa_passphrase"
    cat > "$BIN_DIR/iw" <<'EOF'
#!/bin/sh
printf 'iw args=%s\n' "$*" >> "$CALL_LOG"
printf '%s\n' '* 2412 MHz [1] (20.0 dBm)' '* 5180 MHz [36] (20.0 dBm)'
EOF
    chmod +x "$BIN_DIR/iw"
}

run_action() (
    action=$1
    export CALL_LOG WPA_FAIL
    export NANOKVM_WIFI_BOOT_DIR="$BOOT_DIR"
    export NANOKVM_WIFI_ETC_DIR="$ETC_DIR"
    export NANOKVM_WIFI_KVM_DIR="$KVM_DIR"
    export NANOKVM_WIFI_RUN_DIR="$RUN_DIR"
    export NANOKVM_WIFI_AP_FLAG="$AP_FLAG"
    export NANOKVM_WIFI_UID_FILE="$UID_FILE"
    export NANOKVM_WIFI_DHCP_PID="$DHCP_PID"
    export NANOKVM_WPA_PASSPHRASE="$BIN_DIR/wpa_passphrase"
    export NANOKVM_WPA_SUPPLICANT="$BIN_DIR/wpa_supplicant"
    export NANOKVM_UDHCPC="$BIN_DIR/udhcpc"
    export NANOKVM_HOSTAPD="$BIN_DIR/hostapd"
    export NANOKVM_UDHCPD="$BIN_DIR/udhcpd"
    export NANOKVM_IFCONFIG="$BIN_DIR/ifconfig"
    export NANOKVM_IP="$BIN_DIR/ip"
    export NANOKVM_KILLALL="$BIN_DIR/killall"
    export NANOKVM_CHOWN="$BIN_DIR/chown"
    export NANOKVM_IW="$BIN_DIR/iw"
    export NANOKVM_WIFI_PHY_LINK="$PHY_LINK"
    "$SCRIPT" "$action"
)

# With no credentials, boot must not launch idle WPA/DHCP processes or create
# persistent/runtime configuration.
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
assert_contains "udhcpc args=-B -O 121 -i wlan0 -t 10 -T 1 -A 5 -b -p $DHCP_PID" "$CALL_LOG"

# A static Wi-Fi configuration suppresses the DHCP client only.
new_case nodhcp
printf 'Office\n' > "$ETC_DIR/wifi.ssid"
printf 'secret123\n' > "$ETC_DIR/wifi.pass"
touch "$BOOT_DIR/wifi.nodhcp"
run_action start
assert_contains 'wpa_supplicant args=-B -i wlan0' "$CALL_LOG"
assert_not_contains 'udhcpc args=' "$CALL_LOG"

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

# AP configuration and DHCP leases stay in tmpfs. Entering AP mode only removes
# a wlan0 default route and uses the valid `ip addr flush` spelling.
new_case ap
printf 'NanoKVM\n' > "$KVM_DIR/ap.ssid"
printf '87654321\n' > "$KVM_DIR/ap.pass"
run_action ap
assert_file "$RUN_DIR/hostapd.conf"
assert_file "$RUN_DIR/udhcpd.conf"
[ "$(stat -c '%a' "$RUN_DIR/hostapd.conf")" = 600 ] || fail 'hostapd config mode is not 0600'
[ "$(stat -c '%a' "$RUN_DIR/udhcpd.conf")" = 600 ] || fail 'udhcpd config mode is not 0600'
assert_contains "pidfile $RUN_DIR/udhcpd.pid" "$RUN_DIR/udhcpd.conf"
assert_contains "lease_file $RUN_DIR/udhcpd.leases" "$RUN_DIR/udhcpd.conf"
assert_contains 'ip args=route del default dev wlan0' "$CALL_LOG"
assert_contains 'ip args=addr flush dev wlan0' "$CALL_LOG"
assert_not_contains 'ip args=add flush' "$CALL_LOG"
assert_file "$AP_FLAG"

# Stop must preserve the USB network DHCP server and remove ephemeral Wi-Fi state.
new_case stop
mkdir -p "$RUN_DIR"
for file in udhcpd.pid udhcpd.leases wpa_supplicant.conf hostapd.conf udhcpd.conf
do
    : > "$RUN_DIR/$file"
done
printf 'not-a-pid\n' > "$DHCP_PID"
touch "$AP_FLAG"
run_action stop
assert_contains 'killall args=hostapd' "$CALL_LOG"
assert_not_contains 'killall args=udhcpd' "$CALL_LOG"
assert_contains 'killall args=wpa_supplicant' "$CALL_LOG"
for file in udhcpc.wlan0.pid udhcpd.pid udhcpd.leases wpa_supplicant.conf hostapd.conf udhcpd.conf
do
    assert_absent "$RUN_DIR/$file"
done
assert_absent "$AP_FLAG"

# Disabled state survives restart without deleting the saved connection.
new_case disabled
printf 'Office' > "$ETC_DIR/wifi.ssid"
printf 'secret123' > "$ETC_DIR/wifi.pass"
touch "$ETC_DIR/wifi.disabled"
run_action restart
assert_contains 'ip args=link set dev wlan0 down' "$CALL_LOG"
assert_not_contains 'wpa_supplicant args=' "$CALL_LOG"
assert_contains 'secret123' "$ETC_DIR/wifi.pass"
rm "$ETC_DIR/wifi.disabled"
run_action restart
assert_contains 'wpa_supplicant args=' "$CALL_LOG"

# Hidden open networks and restricted bands generate an explicit safe profile.
new_case hidden_open
printf 'Hidden "network"' > "$ETC_DIR/wifi.ssid"
: > "$ETC_DIR/wifi.pass"
printf open > "$ETC_DIR/wifi.security"
printf true > "$ETC_DIR/wifi.hidden"
printf '5180 5200' > "$ETC_DIR/wifi.freq_list"
run_action start
assert_contains 'ssid=48696464656e20226e6574776f726b22' "$RUN_DIR/wpa_supplicant.conf"
assert_contains 'key_mgmt=NONE' "$RUN_DIR/wpa_supplicant.conf"
assert_contains 'scan_ssid=1' "$RUN_DIR/wpa_supplicant.conf"
assert_contains 'freq_list=5180 5200' "$RUN_DIR/wpa_supplicant.conf"
assert_not_contains 'wpa_passphrase args=' "$CALL_LOG"
assert_not_contains 'psk=' "$RUN_DIR/wpa_supplicant.conf"

# A preferred band is a wpa_supplicant priority, not a scan restriction: the
# preferred network block has exact 5 GHz frequencies and the lower-priority
# fallback still accepts the same SSID on 2.4 GHz.
new_case preferred_band
printf Office > "$ETC_DIR/wifi.ssid"
printf secret123 > "$ETC_DIR/wifi.pass"
printf 5 > "$ETC_DIR/wifi.band_preference"
printf 2412 > "$ETC_DIR/wifi.freq_list_2.4"
printf '5180 5200' > "$ETC_DIR/wifi.freq_list_5"
run_action start
assert_count 'network={' 2 "$RUN_DIR/wpa_supplicant.conf"
assert_contains 'freq_list=5180 5200' "$RUN_DIR/wpa_supplicant.conf"
assert_contains 'priority=2' "$RUN_DIR/wpa_supplicant.conf"
assert_contains 'priority=1' "$RUN_DIR/wpa_supplicant.conf"

# Existing profiles have no preference field. Capability discovery makes their
# default preference 5 GHz without persisting or reconnecting during a read.
new_case legacy_default_5ghz
printf Office > "$ETC_DIR/wifi.ssid"
printf secret123 > "$ETC_DIR/wifi.pass"
run_action start
assert_count 'network={' 2 "$RUN_DIR/wpa_supplicant.conf"
assert_contains 'freq_list=5180' "$RUN_DIR/wpa_supplicant.conf"
assert_contains 'priority=2' "$RUN_DIR/wpa_supplicant.conf"

# A radio without 5 GHz has no preferred block, but the fallback remains
# usable even though the persisted/default preference is still 5 GHz.
new_case only_24ghz
printf Office > "$ETC_DIR/wifi.ssid"
printf secret123 > "$ETC_DIR/wifi.pass"
printf 5 > "$ETC_DIR/wifi.band_preference"
printf 2412 > "$ETC_DIR/wifi.freq_list_2.4"
: > "$ETC_DIR/wifi.freq_list_5"
run_action start
assert_count 'network={' 1 "$RUN_DIR/wpa_supplicant.conf"
assert_not_contains 'freq_list=' "$RUN_DIR/wpa_supplicant.conf"
assert_contains 'priority=1' "$RUN_DIR/wpa_supplicant.conf"

new_case invalid_frequencies
printf 'Office' > "$ETC_DIR/wifi.ssid"
printf 'secret123' > "$ETC_DIR/wifi.pass"
printf '5180\nkey_mgmt=NONE' > "$ETC_DIR/wifi.freq_list"
if run_action start; then fail 'invalid frequency config accepted'; fi
assert_not_contains 'wpa_supplicant args=' "$CALL_LOG"


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
