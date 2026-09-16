#!/bin/sh
# One-shot RAM-only AIC8801 smoke test. Run on the UART console, not over Wi-Fi.
set -u

release=7.2.5-nanokvm-os-r3
installed=/usr/lib/modules/$release/extra/aic8800_fdrv.ko
candidate=/tmp/aic8800_fdrv_survey_guard.ko
expected_installed=7b470b4011abc05ecfaef2fc5ea53c7e06261f5fe1162fc1b0885f8ab903b171
expected_candidate=2377201b1adddea4f5e1fbdc0094bceeedd931cab1e76eb34d55163e668f0d5b
changed=0

hash_file() {
    sha256sum "$1" | cut -d ' ' -f 1
}

wait_for_wifi() {
    count=0
    while [ "$count" -lt 45 ]; do
        if [ "$(cat /sys/class/net/wlan0/carrier 2>/dev/null)" = 1 ] &&
            iw dev wlan0 link 2>/dev/null | grep -q 'freq: 5' &&
            ip -4 addr show dev wlan0 | grep -q 'inet '; then
            iw dev wlan0 link
            ip -4 addr show dev wlan0 | grep 'inet '
            return 0
        fi
        sleep 1
        count=$((count + 1))
    done
    return 1
}

restore() {
    result=$?
    trap - EXIT HUP INT TERM
    if [ "$changed" = 1 ]; then
        echo 'RESTORE_START'
        /etc/init.d/S30wifi stop || true
        if [ -d /sys/module/aic8800_fdrv ]; then
            if ! rmmod aic8800_fdrv; then
                echo 'RESTORE_FAILED: could not unload test module'
                exit 1
            fi
        fi
        if ! modprobe aic8800_fdrv; then
            echo 'RESTORE_FAILED: could not load installed module'
            exit 1
        fi
        if ! /etc/init.d/S30wifi start || ! wait_for_wifi; then
            echo 'RESTORE_FAILED: Wi-Fi did not reconnect'
            exit 1
        fi
        echo 'RESTORE_OK'
    fi
    printf 'AIC_GUARD_TEST_END status=%s\n' "$result"
    exit "$result"
}

trap restore EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

echo 'AIC_GUARD_TEST_START'
[ "$(uname -r)" = "$release" ] || { echo 'wrong kernel'; exit 1; }
[ "$(hash_file "$installed")" = "$expected_installed" ] || {
    echo 'installed module differs from reviewed baseline'; exit 1;
}
[ "$(hash_file "$candidate")" = "$expected_candidate" ] || {
    echo 'test module hash mismatch'; exit 1;
}
cat /sys/bus/sdio/devices/*/modalias | grep -q 'v5449d0145' || {
    echo 'not the expected AIC8801 device'; exit 1;
}
before_boot=$(cat /proc/sys/kernel/random/boot_id)
printf 'kernel=%s boot_id=%s\n' "$release" "$before_boot"

changed=1
/etc/init.d/S30wifi stop || exit 1
rmmod aic8800_fdrv || exit 1
insmod "$candidate" || exit 1
echo 'PATCHED_MODULE_LOADED'
/etc/init.d/S30wifi start || exit 1
wait_for_wifi || { echo 'patched Wi-Fi did not reconnect'; exit 1; }
iw dev wlan0 survey dump || exit 1
ping -c 3 -W 2 192.168.4.1 || exit 1
[ "$(cat /proc/sys/kernel/random/boot_id)" = "$before_boot" ] || {
    echo 'unexpected device reboot'; exit 1;
}
echo 'PATCHED_MODULE_OK'
