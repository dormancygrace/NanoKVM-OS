set -eu
[ "$(cat /etc/kvm/hw)" = pcie ]
[ "$(cat /sys/class/gpio/gpiochip480/label)" = 3020000.gpio ]
for n in 503 505; do
    [ "$(cat /sys/class/gpio/gpio$n/direction)" = out ]
    [ "$(cat /sys/class/gpio/gpio$n/value)" = 0 ]
done
changed=''
cleanup() {
    result=$?
    trap - EXIT
    for n in $changed; do
        if [ ! -d /sys/class/gpio/gpio$n ]; then
            echo "$n" > /sys/class/gpio/export || result=1
        fi
        echo low > /sys/class/gpio/gpio$n/direction || result=1
        printf 'restored GPIO%s direction=%s value=%s\n' "$n" "$(cat /sys/class/gpio/gpio$n/direction)" "$(cat /sys/class/gpio/gpio$n/value)"
        [ "$(cat /sys/class/gpio/gpio$n/value)" = 0 ] || result=1
    done
    exit "$result"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
chmod 700 /mnt/data/gpio-atx-probe
for n in 503 505; do
    changed="$changed $n"
    echo "$n" > /sys/class/gpio/unexport
done
/mnt/data/gpio-atx-probe --confirm-disconnected
