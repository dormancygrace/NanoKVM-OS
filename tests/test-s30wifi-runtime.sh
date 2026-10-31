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
    # wlan0 is there unless a case removes it; the driver loader logs its
    # run and creates wlan0 unless DRIVER_FAILS is set.
    NET_DIR=$CASE_DIR/net/wlan0
    mkdir -p "$BOOT_DIR" "$ETC_DIR" "$KVM_DIR" "$BIN_DIR" "$NET_DIR"
    cat > "$BIN_DIR/S25wifimod" <<'EOF'
#!/bin/sh
printf 'S25wifimod args=%s\n' "$*" >> "$CALL_LOG"
[ "${DRIVER_FAILS:-0}" -eq 0 ] || exit 0
mkdir -p "$NANOKVM_WIFI_NET_DIR"
EOF
    chmod +x "$BIN_DIR/S25wifimod"
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

    # pidof: a process stays alive for $ALIVE_CHECKS more looks (counted in
    # a file), or for ever with ALIVE_CHECKS=forever.
    cat > "$BIN_DIR/pidof" <<'EOF'
#!/bin/sh
printf 'pidof args=%s\n' "$*" >> "$CALL_LOG"
[ "${ALIVE_CHECKS:-0}" = forever ] && exit 0
count=$(cat "$CALL_LOG.alive" 2>/dev/null || echo 0)
[ "$count" -lt "${ALIVE_CHECKS:-0}" ] || exit 1
echo $((count + 1)) > "$CALL_LOG.alive"
exit 0
EOF
    chmod +x "$BIN_DIR/pidof"
    for command in wpa_supplicant wpa_cli udhcpc hostapd udhcpd ifconfig ip killall chown
    do
        cat > "$BIN_DIR/$command" <<EOF
#!/bin/sh
printf '%s args=%s\n' '$command' "\$*" >> "\$CALL_LOG"
exit 0
EOF
        chmod +x "$BIN_DIR/$command"
    done
    chmod +x "$BIN_DIR/wpa_passphrase"
    # udhcpc writes its -p pid file.
    cat > "$BIN_DIR/udhcpc" <<'EOF'
#!/bin/sh
args=$*
while [ $# -gt 1 ]; do
    [ "$1" != -p ] || echo 4343 > "$2"
    shift
done
printf 'udhcpc args=%s\n' "$args" >> "$CALL_LOG"
EOF
    cat > "$BIN_DIR/iw" <<'EOF'
#!/bin/sh
printf 'iw args=%s\n' "$*" >> "$CALL_LOG"
printf '%s\n' '* 2412 MHz [1] (20.0 dBm)' '* 5180 MHz [36] (20.0 dBm)'
EOF
    chmod +x "$BIN_DIR/iw"
}

run_action() (
    export CALL_LOG WPA_FAIL ALIVE_CHECKS
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
    export NANOKVM_WIFI_DRIVER_LOADER="$BIN_DIR/S25wifimod"
    export NANOKVM_WIFI_DRIVER_WAIT=3
    export DRIVER_FAILS
    "$SCRIPT" "$@"
)

# event ID_STR: what wpa_cli -a runs when wlan0 associates with that block.
event() (
    WPA_ID_STR=$1
    export WPA_ID_STR
    run_action wlan0 CONNECTED
)

# With no credentials, boot must not launch idle WPA/DHCP processes or create
# persistent/runtime configuration.
WPA_FAIL=0
ALIVE_CHECKS=0

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
# Only the unrestricted fallback scans in the background, to return to the
# preferred band once it is back.
assert_count 'bgscan=' 1 "$RUN_DIR/wpa_supplicant.conf"
fallback=$(awk '/^network=\{/ { block = "" } { block = block $0 "\n" } /^}/ && block ~ /priority=1/ { printf "%s", block }' "$RUN_DIR/wpa_supplicant.conf")
case "$fallback" in *'bgscan="simple:30:-70:120"'*) ;; *) fail 'fallback block has no bgscan' ;; esac
case "$fallback" in *freq_list=*) fail 'fallback block is restricted' ;; esac

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
# Without a preferred block there is nothing to return to: no scanning.
assert_not_contains 'bgscan=' "$RUN_DIR/wpa_supplicant.conf"

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

hex() {
    printf '%s' "$1" | od -An -tx1 | tr -d ' \n'
}

# save_entry SSID PASS SECURITY HIDDEN AUTO: a saved network as the server writes it.
save_entry() {
    entry=$ETC_DIR/wifi.saved/$(hex "$1")
    mkdir -p "$entry"
    printf %s "$1" > "$entry/ssid"
    printf %s "$2" > "$entry/pass"
    printf %s "$3" > "$entry/security"
    printf %s "$4" > "$entry/hidden"
    printf %s "$5" > "$entry/auto"
}

wait_for() {
    tries=0
    until grep -F -- "$1" "$2" >/dev/null; do
        tries=$((tries + 1))
        [ "$tries" -le 50 ] || fail "$2 does not contain: $1"
        sleep 0.1
    done
}

# daemon NAME FILE [stubborn]: a running NAME (udhcpc, wpa_cli) whose pid is
# in FILE. Like them it removes FILE as it exits, here 0.5 s after SIGTERM;
# a stubborn one ignores SIGTERM. Started from a subshell, so that init reaps
# it like a daemon. Prints its pid.
daemon() {
    mkdir -p "$CASE_DIR/daemon"
    if [ "${3:-}" = stubborn ]; then
        cat > "$CASE_DIR/daemon/$1" <<'EOF'
#!/bin/sh
trap '' TERM
while :; do sleep 0.1; done
EOF
    else
        cat > "$CASE_DIR/daemon/$1" <<'EOF'
#!/bin/sh
trap 'sleep 0.5; rm -f "$PIDFILE"; exit 0' TERM
while :; do sleep 0.1; done
EOF
    fi
    chmod +x "$CASE_DIR/daemon/$1"
    # Not on this function's output: $(daemon ...) would wait for it.
    ( PIDFILE=$2 "$CASE_DIR/daemon/$1" </dev/null >/dev/null 2>&1 & echo $! > "$2" )
    pid=$(cat "$2")
    [ "$(cat "/proc/$pid/comm")" = "$1" ] || fail "test daemon is not named $1"
    echo "$pid"
}

network_block() {
    awk -v ssid="ssid=$1" '/^network=\{/ { block = "" } { block = block $0 "\n" } /^}/ && index(block, ssid) { printf "%s", block }' \
        "$RUN_DIR/wpa_supplicant.conf"
}

# A release with one network: it becomes a saved network that connects
# automatically, with private permissions.
new_case migrate_single
printf 'Office\n' > "$ETC_DIR/wifi.ssid"
printf 'secret123\n' > "$ETC_DIR/wifi.pass"
run_action start
entry=$ETC_DIR/wifi.saved/$(hex Office)
[ "$(cat "$entry/ssid")" = Office ] || fail 'migrated SSID'
[ "$(wc -c < "$entry/pass")" -eq 9 ] && [ "$(cat "$entry/pass")" = secret123 ] ||
    fail 'migrated password is not exactly the password'
[ "$(cat "$entry/security")" = personal ] || fail 'migrated security'
[ "$(cat "$entry/auto")" = 1 ] || fail 'migrated network does not connect automatically'
[ "$(stat -c '%a' "$ETC_DIR/wifi.saved")" = 700 ] || fail 'saved directory mode is not 0700'
[ "$(stat -c '%a' "$entry/pass")" = 600 ] || fail 'saved password mode is not 0600'
assert_count 'network={' 2 "$RUN_DIR/wpa_supplicant.conf"

# All saved networks that connect automatically are joined wherever the device
# is: the last chosen one with its band blocks first, the others below it. One
# that does not connect automatically is left out, and so is an entry that
# cannot be configured, without stopping the others.
new_case saved_networks
printf 'Office' > "$ETC_DIR/wifi.ssid"
printf 'secret123' > "$ETC_DIR/wifi.pass"
printf 2412 > "$ETC_DIR/wifi.freq_list_2.4"
printf 5180 > "$ETC_DIR/wifi.freq_list_5"
save_entry Office secret123 personal false 1
save_entry 'Cafe "Wi-Fi"' cafepass1 wpa2 false 1
save_entry Hotel hotelpass1 personal false 0
save_entry Guest '' open true 1
save_entry Broken brokenpass bogus false 1
mkdir -p "$ETC_DIR/wifi.saved/$(hex Renamed)"
printf Other > "$ETC_DIR/wifi.saved/$(hex Renamed)/ssid"
printf 1 > "$ETC_DIR/wifi.saved/$(hex Renamed)/auto"
run_action start 2>"$CASE_DIR/stderr"
config=$RUN_DIR/wpa_supplicant.conf
assert_count 'network={' 4 "$config"
case "$(network_block "$(hex Office)")" in *priority=2*) ;; *) fail 'last chosen network has no preferred block' ;; esac
case "$(network_block "$(hex 'Cafe "Wi-Fi"')")" in *proto=RSN*priority=0*) ;; *) fail 'saved WPA2 network missing' ;; esac
case "$(network_block "$(hex Guest)")" in *key_mgmt=NONE*scan_ssid=1*priority=0*) ;; *) fail 'saved hidden open network missing' ;; esac
assert_not_contains "ssid=$(hex Hotel)" "$config"
assert_not_contains "ssid=$(hex Broken)" "$config"
assert_not_contains "ssid=$(hex Other)" "$config"
assert_contains "Skipping saved WiFi network $(hex Broken)" "$CASE_DIR/stderr"
assert_contains 'wpa_supplicant args=-B -i wlan0' "$CALL_LOG"

# Every block names its saved network in id_str, for the event handler and
# the server.
case "$(network_block "$(hex Office)")" in *"id_str=\"$(hex Office)\""*) ;; *) fail 'block without id_str' ;; esac
assert_count 'id_str=' 4 "$config"
# wpa_cli runs this script on association events.
assert_contains "wpa_cli args=-B -i wlan0 -p $CASE_DIR/wpa_ctrl -P $RUN_DIR/wpa_cli.pid -a $(readlink -f "$SCRIPT")" "$CALL_LOG"

# The network chosen in this boot is joined without automatic connection;
# after a reboot (/run empty) it is not.
hex Hotel > "$RUN_DIR/selected"
run_action restart 2>/dev/null
case "$(network_block "$(hex Hotel)")" in *priority=0*) ;; *) fail 'network chosen in this boot was left out' ;; esac
rm "$RUN_DIR/selected"
run_action restart 2>/dev/null
assert_not_contains "ssid=$(hex Hotel)" "$config"

# The last chosen network without automatic connection: after a reboot the
# device joins the others; chosen again in this boot, it gets its band blocks.
new_case current_not_auto
printf 'Office' > "$ETC_DIR/wifi.ssid"
printf 'secret123' > "$ETC_DIR/wifi.pass"
save_entry Office secret123 personal false 0
save_entry Cafe cafepass1 personal false 1
run_action start
assert_not_contains "ssid=$(hex Office)" "$RUN_DIR/wpa_supplicant.conf"
assert_contains "ssid=$(hex Cafe)" "$RUN_DIR/wpa_supplicant.conf"
hex Office > "$RUN_DIR/selected"
run_action restart
case "$(network_block "$(hex Office)")" in *priority=2*) ;; *) fail 'chosen network has no preferred block' ;; esac

# A configured network without a password that is not open was never usable:
# it is not stored, and Wi-Fi idles as before.
new_case no_password
printf 'Office' > "$ETC_DIR/wifi.ssid"
run_action start
assert_absent "$ETC_DIR/wifi.saved"
assert_not_contains 'wpa_supplicant args=' "$CALL_LOG"

# Saved networks of which none connects automatically: idle, nothing started.
new_case none_automatic
save_entry Hotel hotelpass1 personal false 0
run_action start
assert_not_contains 'wpa_supplicant args=' "$CALL_LOG"
assert_absent "$RUN_DIR"

# Credentials provisioned through /boot replace a saved network of the same
# name and connect automatically.
new_case boot_replaces_saved
save_entry Office oldpass12 personal false 0
printf 'Office\n' > "$BOOT_DIR/wifi.ssid"
printf 'newpass12\n' > "$BOOT_DIR/wifi.pass"
run_action start
entry=$ETC_DIR/wifi.saved/$(hex Office)
[ "$(cat "$entry/pass")" = newpass12 ] || fail 'boot credentials did not replace the saved password'
[ "$(cat "$entry/auto")" = 1 ] || fail 'boot credentials do not connect automatically'
assert_contains 'wpa_passphrase args=Office stdin=newpass12' "$CALL_LOG"
[ "$(ls -A "$ETC_DIR/wifi.saved" | grep -c '^\.')" = 0 ] || fail 'temporary saved-network directory left behind'

# wpa_supplicant moves to another saved network by itself: DHCP starts again
# for it (udhcpc would keep the old network's address). The first
# association, a move between bands of the same network and a block without
# id_str do not touch DHCP.
new_case network_change_dhcp
printf 'Office' > "$ETC_DIR/wifi.ssid"
printf 'secret123' > "$ETC_DIR/wifi.pass"
save_entry Office secret123 personal false 1
save_entry Cafe cafepass1 personal false 1
run_action start
wait_for 'udhcpc args=' "$CALL_LOG" # started in the background
echo 4242 > "$RUN_DIR/wpa_cli.pid" # what wpa_cli -P writes
old=$(daemon udhcpc "$DHCP_PID")
: > "$CALL_LOG"
event "$(hex Office)"
event "$(hex Office)"
event ''
assert_not_contains 'udhcpc args=' "$CALL_LOG"
[ "$(cat "$RUN_DIR/associated")" = "$(hex Office)" ] || fail 'association not recorded'
event "$(hex Cafe)" > "$CASE_DIR/stdout"
# The old client has exited before the new one starts: exiting later, it
# would remove the new one's pid file, and the next change would find none.
[ ! -e "/proc/$old" ] || fail 'a new DHCP client started before the previous one exited'
wait_for 'udhcpc args=-S -B -O 121 -i wlan0' "$CALL_LOG"
assert_count 'udhcpc args=-S -B -O 121 -i wlan0' 1 "$CALL_LOG"
sleep 0.7
[ "$(cat "$DHCP_PID" 2>/dev/null)" = 4343 ] || fail "the new DHCP client's pid file was removed"
assert_contains 'requesting a new address' "$CASE_DIR/stdout"
[ "$(cat "$RUN_DIR/associated")" = "$(hex Cafe)" ] || fail 'new association not recorded'
# DISCONNECTED and events after stop (the handler's pid file is gone) do nothing.
# stop ends the handler and waits for it: a wpa_cli still exiting would remove
# the pid file of the next one.
# A client that ignores SIGTERM is killed, and only then replaced.
stubborn=$(daemon udhcpc "$DHCP_PID" stubborn)
: > "$CALL_LOG"
event "$(hex Office)" >/dev/null 2> "$CASE_DIR/stderr"
[ ! -e "/proc/$stubborn" ] || fail 'a DHCP client that ignores SIGTERM was left running'
assert_contains "udhcpc $stubborn did not exit; killing it" "$CASE_DIR/stderr"
wait_for 'udhcpc args=-S -B -O 121 -i wlan0' "$CALL_LOG"
handler=$(daemon wpa_cli "$RUN_DIR/wpa_cli.pid")
: > "$CALL_LOG"
run_action wlan0 DISCONNECTED
run_action stop
[ ! -e "/proc/$handler" ] || fail 'stop returned before the event handler exited'
assert_absent "$RUN_DIR/wpa_cli.pid"
assert_absent "$RUN_DIR/associated"
event "$(hex Office)"
assert_not_contains 'udhcpc args=' "$CALL_LOG"

# A static configuration has no DHCP client to start again.
new_case network_change_nodhcp
printf 'Office' > "$ETC_DIR/wifi.ssid"
printf 'secret123' > "$ETC_DIR/wifi.pass"
touch "$BOOT_DIR/wifi.nodhcp"
run_action start
echo 4242 > "$RUN_DIR/wpa_cli.pid"
event "$(hex Office)"
event "$(hex Cafe)"
assert_not_contains 'udhcpc args=' "$CALL_LOG"
echo 'S30wifi saved network tests passed'

# restart waits for the old wpa_supplicant before starting the new one, and
# removes its control socket only once it has gone.
new_case restart_waits
printf 'Home\n' > "$ETC_DIR/wifi.ssid"
printf 'secretpass\n' > "$ETC_DIR/wifi.pass"
mkdir -p "$CASE_DIR/wpa_ctrl"
: > "$CASE_DIR/wpa_ctrl/wlan0"
ALIVE_CHECKS=3 run_action restart
killed=$(grep -n 'killall args=wpa_supplicant' "$CALL_LOG" | head -n 1 | cut -d: -f1)
started=$(grep -n 'wpa_supplicant args=-B' "$CALL_LOG" | head -n 1 | cut -d: -f1)
last_look=$(grep -n 'pidof args=wpa_supplicant' "$CALL_LOG" | tail -n 1 | cut -d: -f1)
[ -n "$killed" ] && [ -n "$started" ] && [ -n "$last_look" ] || fail "restart did not stop and start wpa_supplicant"
[ "$(grep -c 'pidof args=wpa_supplicant' "$CALL_LOG")" -ge 4 ] || fail "stop did not wait for wpa_supplicant"
[ "$last_look" -lt "$started" ] || fail "the new wpa_supplicant started before the old one was gone"
assert_absent "$CASE_DIR/wpa_ctrl/wlan0"
assert_not_contains 'killall args=-9' "$CALL_LOG"

# One that never exits is killed, and its socket is left while it lives.
new_case restart_stuck
mkdir -p "$CASE_DIR/wpa_ctrl"
: > "$CASE_DIR/wpa_ctrl/wlan0"
ALIVE_CHECKS=forever run_action stop 2>/dev/null
assert_contains 'killall args=-9 wpa_supplicant' "$CALL_LOG"
assert_file "$CASE_DIR/wpa_ctrl/wlan0"
ALIVE_CHECKS=0
echo "S30wifi restart wait tests passed"

# Wi-Fi off: the driver is not loaded at boot (nanokvm-network), so there is
# no wlan0. start, stop and disable leave it so and succeed.
DRIVER_FAILS=0
for action in start stop disable; do
    new_case "driver_off_$action"
    : > "$ETC_DIR/wifi.disabled"
    rmdir "$NET_DIR"
    run_action "$action" || fail "$action without wlan0 failed"
    assert_not_contains 'S25wifimod' "$CALL_LOG"
    assert_absent "$NET_DIR"
done

# Turned on (the web switch removes wifi.disabled and restarts): the driver
# is loaded first and Wi-Fi starts on the new wlan0.
new_case driver_on_restart
printf 'Office' > "$ETC_DIR/wifi.ssid"
printf 'secret123' > "$ETC_DIR/wifi.pass"
rmdir "$NET_DIR"
run_action restart || fail "restart did not load the driver"
assert_count 'S25wifimod args=start' 1 "$CALL_LOG"
assert_contains 'ip args=link set dev wlan0 up' "$CALL_LOG"

# The access point for provisioning loads the driver too, also with Wi-Fi off.
new_case driver_ap
: > "$ETC_DIR/wifi.disabled"
rmdir "$NET_DIR"
run_action ap || fail "ap did not load the driver"
assert_count 'S25wifimod args=start' 1 "$CALL_LOG"
assert_contains 'hostapd args=' "$CALL_LOG"

# A driver that brings no wlan0: start fails after the wait, nothing runs.
new_case driver_missing
printf 'Office' > "$ETC_DIR/wifi.ssid"
printf 'secret123' > "$ETC_DIR/wifi.pass"
rmdir "$NET_DIR"
if DRIVER_FAILS=1 run_action start 2>/dev/null; then fail "start without wlan0 succeeded"; fi
assert_not_contains 'wpa_supplicant args=' "$CALL_LOG"
DRIVER_FAILS=0

# Loaded already (wlan0 there): no second load.
new_case driver_loaded
printf 'Office' > "$ETC_DIR/wifi.ssid"
printf 'secret123' > "$ETC_DIR/wifi.pass"
run_action restart
assert_not_contains 'S25wifimod' "$CALL_LOG"
echo "S30wifi driver loading tests passed"

