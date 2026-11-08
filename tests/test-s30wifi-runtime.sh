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
    for command in wpa_supplicant wpa_cli udhcpc hostapd udhcpd ifconfig ip killall chown iw nft logger
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
    export NANOKVM_NFT="$BIN_DIR/nft"
    export NANOKVM_LOGGER="$BIN_DIR/logger"
    # The stub iw reports no country in force: do not wait for one.
    export NANOKVM_WIFI_REG_WAIT=0
    "$SCRIPT" "$@"
)

# iw with a regulatory domain (DE) and the channels it allows: 12 and 13
# without IR, 52 with radar detection, 144 disabled.
fake_iw_de() {
    cat > "$BIN_DIR/iw" <<'EOF'
#!/bin/sh
printf 'iw args=%s\n' "$*" >> "$CALL_LOG"
case "$*" in
    'reg get')
        printf '%s\n' global 'country DE: DFS-ETSI' \
            '	(2400 - 2483 @ 40), (N/A, 20), (N/A)' \
            '	(5250 - 5350 @ 80), (N/A, 20), (0 ms), DFS, AUTO-BW' '' \
            'phy#0' 'country 99: DFS-UNSET'
        ;;
    'phy phy0 info')
        printf '%s\n' 'Wiphy phy0' '	Band 1:' '		Frequencies:' \
            '			* 2412.0 MHz [1] (20.0 dBm)' \
            '			* 2437.0 MHz [6] (20.0 dBm)' \
            '			* 2467.0 MHz [12] (20.0 dBm) (no IR)' \
            '			* 2472.0 MHz [13] (20.0 dBm) (no IR)' \
            '	Band 2:' '		Frequencies:' \
            '			* 5180.0 MHz [36] (20.0 dBm)' \
            '			* 5200.0 MHz [40] (20.0 dBm)' \
            '			* 5260.0 MHz [52] (20.0 dBm) (no IR, radar detection)' \
            '			* 5720.0 MHz [144] (disabled)'
        ;;
esac
exit 0
EOF
    chmod +x "$BIN_DIR/iw"
}

# The administrator's access point on 5 GHz at channel CHANNEL.
user_ap() {
    printf 'Lab\n' > "$ETC_DIR/wifi.ap.ssid"
    printf 'secret123\n' > "$ETC_DIR/wifi.ap.pass"
    printf '5\n' > "$ETC_DIR/wifi.ap.band"
    printf '%s\n' "$1" > "$ETC_DIR/wifi.ap.channel"
    : > "$ETC_DIR/wifi.ap.enabled"
    # ap_start removes source rules until there is none left.
    cat > "$BIN_DIR/ip" <<'EOF'
#!/bin/sh
printf 'ip args=%s\n' "$*" >> "$CALL_LOG"
case "$*" in *'rule del'*) exit 1 ;; esac
exit 0
EOF
    chmod +x "$BIN_DIR/ip"
}

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

# Without a country (or with an invalid one) the world domain is set and the
# configuration names none, as before.
new_case country_world
printf Office > "$ETC_DIR/wifi.ssid"
printf secret123 > "$ETC_DIR/wifi.pass"
printf 'de\n' > "$ETC_DIR/wifi.country"
run_action start
assert_contains 'iw args=reg set 00' "$CALL_LOG"
assert_not_contains 'country=' "$RUN_DIR/wpa_supplicant.conf"

# The country is in force before wpa_supplicant starts, which gets it too.
new_case country_station
fake_iw_de
printf Office > "$ETC_DIR/wifi.ssid"
printf secret123 > "$ETC_DIR/wifi.pass"
printf 'DE\n' > "$ETC_DIR/wifi.country"
run_action start
assert_contains 'country=DE' "$RUN_DIR/wpa_supplicant.conf"
[ "$(sed -n 2p "$RUN_DIR/wpa_supplicant.conf")" = country=DE ] || fail 'country is not global'
grep -n -e '^iw args=reg set DE$' -e '^wpa_supplicant args=' "$CALL_LOG" | head -1 |
    grep -q 'iw args=reg set DE' || fail 'wpa_supplicant started before the country was set'

# The access point advertises the country; a stored channel the country does
# not allow (52, radar detection) is chosen again among the allowed ones.
new_case country_access_point
fake_iw_de
printf 'DE\n' > "$ETC_DIR/wifi.country"
user_ap 52
run_action start
assert_contains 'country_code=DE' "$RUN_DIR/hostapd.conf"
assert_contains 'ieee80211d=1' "$RUN_DIR/hostapd.conf"
assert_contains 'hw_mode=a' "$RUN_DIR/hostapd.conf"
assert_contains 'channel=36' "$RUN_DIR/hostapd.conf"
[ "$(cat "$RUN_DIR/ap-band")" = 5 ] || fail 'the access point band in use is not 5'
grep -n -e '^iw args=reg set DE$' -e '^hostapd args=' "$CALL_LOG" | head -1 |
    grep -q 'iw args=reg set DE' || fail 'hostapd started before the country was set'

# An allowed channel stays.
new_case country_access_point_channel
fake_iw_de
printf 'DE\n' > "$ETC_DIR/wifi.country"
user_ap 40
run_action start
assert_contains 'channel=40' "$RUN_DIR/hostapd.conf"

# The world domain allows no access point on 5 GHz: it runs on 2.4 GHz, with
# no country_code, says why, and keeps the saved band for when a country is
# set.
new_case world_access_point_5ghz
cat > "$BIN_DIR/iw" <<'EOF'
#!/bin/sh
printf 'iw args=%s\n' "$*" >> "$CALL_LOG"
[ "$*" != 'phy phy0 info' ] || printf '%s\n' \
    '			* 2412.0 MHz [1] (20.0 dBm)' \
    '			* 5180.0 MHz [36] (20.0 dBm) (no IR)'
exit 0
EOF
user_ap 40
run_action start 2> "$CASE_DIR/stderr"
assert_contains 'hw_mode=g' "$RUN_DIR/hostapd.conf"
assert_contains 'channel=1' "$RUN_DIR/hostapd.conf"
assert_not_contains 'country_code=' "$RUN_DIR/hostapd.conf"
assert_contains 'hostapd args=' "$CALL_LOG"
assert_contains 'The 5 GHz access point runs on 2.4 GHz: no Wi-Fi country is set' "$CASE_DIR/stderr"
assert_contains 'logger args=-t nanokvm-wifi The 5 GHz access point runs on 2.4 GHz' "$CALL_LOG"
[ "$(cat "$RUN_DIR/ap-band")" = 2.4 ] || fail 'the band in use is not 2.4'
[ "$(cat "$ETC_DIR/wifi.ap.band")" = 5 ] || fail 'the saved band was rewritten'

# A country that allows no 5 GHz access point (none of its channels without
# radar or IR): 2.4 GHz too.
new_case country_without_5ghz_access_point
cat > "$BIN_DIR/iw" <<'EOF'
#!/bin/sh
printf 'iw args=%s\n' "$*" >> "$CALL_LOG"
case "$*" in
    'reg get') printf '%s\n' global 'country XX: DFS-ETSI' ;;
    'phy phy0 info') printf '%s\n' '* 2437.0 MHz [6] (20.0 dBm)' \
        '* 5260.0 MHz [52] (20.0 dBm) (no IR, radar detection)' ;;
esac
exit 0
EOF
printf 'XX\n' > "$ETC_DIR/wifi.country"
user_ap 0
run_action start 2> "$CASE_DIR/stderr"
assert_contains 'hw_mode=g' "$RUN_DIR/hostapd.conf"
assert_contains 'channel=6' "$RUN_DIR/hostapd.conf"
assert_contains 'country_code=XX' "$RUN_DIR/hostapd.conf"
assert_contains 'the Wi-Fi country XX allows none of its channels' "$CASE_DIR/stderr"

# iw with DE in force that takes only the world domain: any other country
# fails with IW_SET_STATUS, or (0) is accepted and never comes into force, as
# a country the regulatory database lacks.
fake_iw_unapplied() {
    printf 'DE\n' > "$CASE_DIR/iw-domain"
    cat > "$BIN_DIR/iw" <<EOF
#!/bin/sh
printf 'iw args=%s\n' "\$*" >> "\$CALL_LOG"
case "\$*" in
    'reg set 00') printf '00\n' > "$CASE_DIR/iw-domain" ;;
    'reg set '*) exit ${IW_SET_STATUS:-0} ;;
    'reg get') printf '%s\n' global "country \$(cat "$CASE_DIR/iw-domain"): DFS-ETSI" ;;
    'phy phy0 info') printf '%s\n' '* 2437.0 MHz [6] (20.0 dBm)' '* 5180.0 MHz [36] (20.0 dBm)' ;;
esac
exit 0
EOF
    chmod +x "$BIN_DIR/iw"
}

# A country that does not come into force is never advertised: the world
# domain is set instead, and a 5 GHz access point runs on 2.4 GHz as without
# a country, saying why.
new_case country_unapplied_access_point
IW_SET_STATUS=0 fake_iw_unapplied
printf 'IQ\n' > "$ETC_DIR/wifi.country"
user_ap 36
run_action start 2> "$CASE_DIR/stderr"
assert_contains 'iw args=reg set IQ' "$CALL_LOG"
assert_contains 'iw args=reg set 00' "$CALL_LOG"
[ "$(cat "$CASE_DIR/iw-domain")" = 00 ] || fail 'the previous domain stayed in force'
assert_not_contains 'country_code=' "$RUN_DIR/hostapd.conf"
assert_not_contains 'ieee80211d=' "$RUN_DIR/hostapd.conf"
assert_contains 'hw_mode=g' "$RUN_DIR/hostapd.conf"
assert_contains 'hostapd args=' "$CALL_LOG"
[ "$(cat "$RUN_DIR/ap-band")" = 2.4 ] || fail 'the band in use is not 2.4'
assert_contains 'The Wi-Fi country IQ did not come into force; using the world domain 00' "$CASE_DIR/stderr"
assert_contains 'logger args=-t nanokvm-wifi The Wi-Fi country IQ did not come into force' "$CALL_LOG"
assert_contains 'The 5 GHz access point runs on 2.4 GHz: the Wi-Fi country IQ is not in force' "$CASE_DIR/stderr"

# iw failing to set the country: wpa_supplicant is not given it either.
new_case country_unapplied_station
IW_SET_STATUS=1 fake_iw_unapplied
printf Office > "$ETC_DIR/wifi.ssid"
printf secret123 > "$ETC_DIR/wifi.pass"
printf 'IQ\n' > "$ETC_DIR/wifi.country"
run_action start 2> "$CASE_DIR/stderr"
assert_contains 'iw args=reg set 00' "$CALL_LOG"
assert_not_contains 'country=' "$RUN_DIR/wpa_supplicant.conf"
assert_contains "wpa_supplicant args=-B -i wlan0 -c $RUN_DIR/wpa_supplicant.conf" "$CALL_LOG"
assert_contains 'The Wi-Fi country IQ did not come into force' "$CASE_DIR/stderr"

echo 'S30wifi runtime-state tests passed'
