#!/bin/sh
set -eu

repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
mkdir -p "$work/bin" "$work/boot" "$work/etc" "$work/run"

cat > "$work/bin/ip" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >> "$NANOKVM_TEST_IP_CALLS"
if [ "${NANOKVM_TEST_IP_SHOW_INET:-0}" = 1 ] && [ "$*" = 'a show dev eth0.100' ]; then
    printf '%s\n' 'inet 10.20.0.2/24'
fi
exit 0
EOF
cat > "$work/bin/udhcpc" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >> "$NANOKVM_TEST_UDHCPC_CALL"
EOF
cat > "$work/bin/arping" <<'EOF'
#!/bin/sh
exit 0
EOF
cat > "$work/bin/modprobe" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >> "$NANOKVM_TEST_MODPROBE_CALLS"
EOF
cat > "$work/bin/hostname" <<'EOF'
#!/bin/sh
printf '%s\n' "$1" > "$NANOKVM_TEST_HOSTNAME_CALL"
EOF
chmod 755 "$work/bin/ip" "$work/bin/udhcpc" "$work/bin/arping" "$work/bin/modprobe" "$work/bin/hostname"

run_eth() {
    NANOKVM_ETH_CONFIG=$work/boot/eth.nodhcp \
    NANOKVM_ETH_DISABLED=$work/boot/eth.disabled \
    NANOKVM_ETH_DHCP_PID=$work/run/udhcpc.eth0.pid \
    NANOKVM_ETH_VLAN=$work/boot/eth.vlan \
    NANOKVM_ETH_VLAN_STATE=$work/run/eth.vlan \
    NANOKVM_IP=$work/bin/ip \
    NANOKVM_MODPROBE=$work/bin/modprobe \
    NANOKVM_UDHCPC=$work/bin/udhcpc \
    NANOKVM_ARPING=$work/bin/arping \
    NANOKVM_DNS_MODE_FILE=$work/etc/dns.mode \
    NANOKVM_RESOLV_CONF=$work/etc/resolv.conf \
    NANOKVM_GATEWAY_PREFERENCE_FILE=$work/etc/gateway.preferred \
    NANOKVM_TEST_IP_SHOW_INET=${NANOKVM_TEST_IP_SHOW_INET:-0} \
    NANOKVM_TEST_IP_CALLS=$work/ip.calls \
    NANOKVM_TEST_MODPROBE_CALLS=$work/modprobe.calls \
    NANOKVM_TEST_UDHCPC_CALL=$work/udhcpc.call \
        sh "$repo/kvmapp/system/init.d/S30eth" "$1"
}

: > "$work/ip.calls"
touch "$work/boot/eth.disabled"
run_eth start
test ! -e "$work/udhcpc.call"
grep -Fqx 'addr flush dev eth0' "$work/ip.calls"
grep -Fqx 'link set dev eth0 down' "$work/ip.calls"
# `! cmd` never stops a set -e script, so fail explicitly.
if grep -Fqx 'link set dev eth0 up' "$work/ip.calls"; then
    echo 'Disabled Ethernet start brought eth0 up' >&2
    exit 1
fi

: > "$work/ip.calls"
rm "$work/boot/eth.disabled"
run_eth start
grep -Fqx 'link set dev eth0 up' "$work/ip.calls"
attempts=0
while [ ! -s "$work/udhcpc.call" ]; do
    attempts=$((attempts + 1))
    [ "$attempts" -lt 100 ] || exit 1
    sleep 0.01
done
grep -q -- '-i eth0' "$work/udhcpc.call"

: > "$work/ip.calls"
run_eth stop
grep -Fqx 'addr flush dev eth0' "$work/ip.calls"
grep -Fqx 'link set dev eth0 down' "$work/ip.calls"

printf '%s\n' '100' > "$work/boot/eth.vlan"
: > "$work/ip.calls"
: > "$work/udhcpc.call"
run_eth start
grep -Fqx 'link add link eth0 name eth0.100 type vlan id 100' "$work/ip.calls"
grep -Fqx '8021q' "$work/modprobe.calls"
grep -Fqx 'link set dev eth0.100 up' "$work/ip.calls"
grep -Fqx 'addr flush dev eth0.100' "$work/ip.calls"
if grep -Fqx 'addr flush dev eth0' "$work/ip.calls"; then
    echo 'VLAN start flushed the eth0 address' >&2
    exit 1
fi
attempts=0
while ! grep -q -- '-i eth0.100' "$work/udhcpc.call"; do
    attempts=$((attempts + 1))
    [ "$attempts" -lt 100 ] || exit 1
    sleep 0.01
done
test "$(cat "$work/run/eth.vlan")" = 'eth0.100'

: > "$work/ip.calls"
run_eth stop
grep -Fqx 'link delete dev eth0.100' "$work/ip.calls"
test ! -e "$work/run/eth.vlan"

printf '%s\n' '10.20.0.2/24 10.20.0.1' > "$work/boot/eth.nodhcp"
: > "$work/ip.calls"
dhcp_calls_before=$(wc -l < "$work/udhcpc.call")
NANOKVM_TEST_IP_SHOW_INET=1 run_eth start
grep -Fqx 'a add 10.20.0.2/24 brd + dev eth0.100' "$work/ip.calls"
# No gateway preference is saved, so S30eth uses its automatic metric.
grep -Fqx 'r add default via 10.20.0.1 dev eth0.100 metric 100' "$work/ip.calls"
test "$(wc -l < "$work/udhcpc.call")" -eq "$dhcp_calls_before"
run_eth stop

# Without a gateway: the address only, no default route and no DNS server.
printf '%s\n' '10.20.0.2/24 none' > "$work/boot/eth.nodhcp"
: > "$work/ip.calls"
: > "$work/etc/resolv.conf"
NANOKVM_TEST_IP_SHOW_INET=1 run_eth start
grep -Fqx 'a add 10.20.0.2/24 brd + dev eth0.100' "$work/ip.calls"
if grep -q 'r add default' "$work/ip.calls" || grep -q nameserver "$work/etc/resolv.conf"; then
    echo 'A gateway was configured for "none"' >&2
    exit 1
fi
run_eth stop
rm "$work/boot/eth.nodhcp"

rm "$work/boot/eth.vlan"
: > "$work/ip.calls"
: > "$work/udhcpc.call"
run_eth start
if grep -q 'link add link eth0' "$work/ip.calls"; then
    echo 'VLAN created without eth.vlan' >&2
    exit 1
fi
grep -Fqx 'addr flush dev eth0' "$work/ip.calls"
attempts=0
while ! grep -q -- '-i eth0 ' "$work/udhcpc.call"; do
    attempts=$((attempts + 1))
    [ "$attempts" -lt 100 ] || exit 1
    sleep 0.01
done
run_eth stop

printf '%s\n' '4095' > "$work/boot/eth.vlan"
: > "$work/ip.calls"
if run_eth start; then
    echo 'Invalid VLAN ID unexpectedly succeeded' >&2
    exit 1
fi
if grep -q 'link add link eth0' "$work/ip.calls"; then
    echo 'VLAN created for invalid ID 4095' >&2
    exit 1
fi
# A valid VLAN must remain configured while Ethernet is disabled.
printf '%s\n' '100' > "$work/boot/eth.vlan"

touch "$work/boot/eth.disabled"
: > "$work/ip.calls"
run_eth start
if grep -q 'link add link eth0' "$work/ip.calls"; then
    echo 'VLAN created while Ethernet is disabled' >&2
    exit 1
fi
rm "$work/boot/eth.disabled"
rm "$work/boot/eth.vlan"

printf '%s\n' 'old-host' > "$work/etc/hostname"
printf '%s\n' '127.0.0.1 localhost' '127.0.1.1 old-host' > "$work/etc/hosts"
printf '%s\n' 'device-uid' > "$work/uid"
touch "$work/boot/eth.disabled"
: > "$work/ip.calls"
NANOKVM_ID_BOOT=$work/boot \
NANOKVM_ID_ETC=$work/etc \
NANOKVM_ID_UID=$work/uid \
NANOKVM_ID_HOSTNAME=$work/bin/hostname \
NANOKVM_ID_IP=$work/bin/ip \
NANOKVM_TEST_IP_CALLS=$work/ip.calls \
NANOKVM_TEST_HOSTNAME_CALL=$work/hostname.call \
    sh "$repo/firmware/alpine/compat/S10uuid" start
grep -Fqx 'link set dev eth0 down' "$work/ip.calls"
grep -q '^link set dev eth0 address ' "$work/ip.calls"
if grep -Fqx 'link set dev eth0 up' "$work/ip.calls"; then
    echo 'S10uuid brought disabled eth0 up' >&2
    exit 1
fi

rm "$work/boot/eth.disabled"
: > "$work/ip.calls"
NANOKVM_ID_BOOT=$work/boot \
NANOKVM_ID_ETC=$work/etc \
NANOKVM_ID_UID=$work/uid \
NANOKVM_ID_HOSTNAME=$work/bin/hostname \
NANOKVM_ID_IP=$work/bin/ip \
NANOKVM_TEST_IP_CALLS=$work/ip.calls \
NANOKVM_TEST_HOSTNAME_CALL=$work/hostname.call \
    sh "$repo/firmware/alpine/compat/S10uuid" start
grep -Fqx 'link set dev eth0 up' "$work/ip.calls"

echo 'PASS: Ethernet disable preference controls boot and runtime administrative state'
