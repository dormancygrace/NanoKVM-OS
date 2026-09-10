#!/bin/sh
# One real-browser window. CPU percentages come from monotonic wall time.
set -eu
pid=$(pidof NanoKVM-Server)
[ -n "$pid" ]
printf 'BEGIN\n'
uname -r
cat /proc/sys/kernel/random/boot_id
cat /proc/uptime
cat /sys/class/net/wlan0/statistics/tx_bytes
cat /sys/class/net/wlan0/statistics/tx_packets
cat /sys/class/net/wlan0/statistics/rx_bytes
head -n 5 /proc/meminfo
cat /sys/kernel/debug/ion/cvi_carveout_heap_dump/summary 2>/dev/null | head -3 || true
/root/.cpu-accounting-probe "$pid" 20 &
probe=$!
for i in $(seq 1 20); do
    printf 'FPS '
    cat /run/nanokvm/now_fps
    printf '\n'
    sleep 1
done
wait "$probe"
printf 'END\n'
cat /proc/uptime
cat /sys/class/net/wlan0/statistics/tx_bytes
cat /sys/class/net/wlan0/statistics/tx_packets
cat /sys/class/net/wlan0/statistics/rx_bytes
head -n 5 /proc/meminfo
cat /sys/kernel/debug/ion/cvi_carveout_heap_dump/summary 2>/dev/null | head -3 || true
cat /proc/cvitek/venc
