#!/bin/sh
# The Linux agent against a made-up /sys and /proc: what it names the parts
# and readings, how it converts the kernel's units, and that the report is
# the JSON the NanoKVM reads. Run from anywhere: sh test.sh [SHELL].
set -eu
here=$(cd "$(dirname "$0")" && pwd)
shell=${1:-sh}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
sys=$tmp/sys
proc=$tmp/proc
mkdir -p "$sys/class/hwmon/hwmon0" "$sys/class/hwmon/hwmon1/device" "$sys/class/hwmon/hwmon2" \
    "$sys/class/hwmon/hwmon3/device" "$sys/class/tty" "$sys/class/net" "$proc/sys/kernel"
put() { printf '%s\n' "$2" > "$1"; }
put "$sys/class/hwmon/hwmon0/name" k10temp
put "$sys/class/hwmon/hwmon0/temp1_input" 45123
put "$sys/class/hwmon/hwmon0/temp1_label" Tctl
put "$sys/class/hwmon/hwmon1/name" nvme
put "$sys/class/hwmon/hwmon1/device/model" 'Fast "Disk" 1TB                '
put "$sys/class/hwmon/hwmon1/temp1_input" 38850
put "$sys/class/hwmon/hwmon1/temp1_label" Composite
put "$sys/class/hwmon/hwmon2/name" it8686
put "$sys/class/hwmon/hwmon2/fan1_input" 912
put "$sys/class/hwmon/hwmon2/in0_input" 1234
put "$sys/class/hwmon/hwmon2/in0_label" Vcore
put "$sys/class/hwmon/hwmon2/pwm1" 128
put "$sys/class/hwmon/hwmon2/temp2_input" -5500
put "$sys/class/hwmon/hwmon2/power1_input" 65250000
put "$sys/class/hwmon/hwmon2/temp3_input" broken
# A second drive of the same model is told apart.
put "$sys/class/hwmon/hwmon3/name" nvme
put "$sys/class/hwmon/hwmon3/device/model" 'Fast "Disk" 1TB                '
put "$sys/class/hwmon/hwmon3/temp1_input" 40000
put "$proc/stat" 'cpu  100 0 100 800 0 0 0 0 0 0'
put "$proc/cpuinfo" 'model name	: Test CPU 9000'
printf 'MemTotal:       8388608 kB\nMemFree:        1000000 kB\nMemAvailable:   2097152 kB\n' > "$proc/meminfo"
put "$proc/sys/kernel/osrelease" 6.1.0-test

out=$(NANOKVM_SENSORS_SYS=$sys NANOKVM_SENSORS_PROC=$proc "$shell" "$here/nanokvm-sensors.sh" --once)
fail=0
want() {
    case "$out" in
        *"$1"*) ;;
        *) echo "missing: $1" >&2; fail=1 ;;
    esac
}
want '"agent":"nanokvm-sensors/'
want '{"hw":"Test CPU 9000 (k10temp)","type":"cpu","name":"Tctl","kind":"temperature","value":45.123}'
want '{"hw":"Fast \"Disk\" 1TB (nvme)","type":"storage","name":"Composite","kind":"temperature","value":38.850}'
want '{"hw":"Fast \"Disk\" 1TB (nvme) 3","type":"storage","name":"Temperature 1","kind":"temperature","value":40.000}'
want '{"hw":"it8686","type":"motherboard","name":"Fan 1","kind":"fan","value":912}'
want '"name":"Vcore","kind":"voltage","value":1.234}'
want '"name":"Fan control 1","kind":"control","value":50}'
want '"name":"Temperature 2","kind":"temperature","value":-5.500}'
want '"name":"Power 1","kind":"power","value":65.250}'
want '"name":"Memory used","kind":"load","value":75}'
want '"name":"Memory available","kind":"data","value":2.000}'
case "$out" in *broken*|*'Temperature 3'*) echo "an unreadable value got into the report" >&2; fail=1 ;; esac
if command -v python3 > /dev/null 2>&1; then
    printf '%s' "$out" | python3 -c 'import json, sys; r = json.load(sys.stdin); assert r["v"] == 1 and len(r["sensors"]) >= 10, r' ||
        { echo "the report is not the JSON expected" >&2; fail=1; }
fi
# The agents share the add-on's version.
version=$(sed -n 's/^VERSION=//p' "$here/nanokvm-sensors.sh")
grep -q "\"version\": \"$version\"" "$here/../addon.json" || { echo "VERSION=$version is not the add-on's" >&2; fail=1; }
[ "$fail" = 0 ] && echo "linux agent: ok ($shell)"
exit "$fail"
