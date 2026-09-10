#!/bin/sh
set -eu
n=0
seconds=${1:?duration}
case "$seconds" in ''|*[!0-9]*) exit 2;; esac
[ "$seconds" -ge 1 ] && [ "$seconds" -le 180 ]
while [ "$n" -lt "$seconds" ]; do
 printf 'NKCORE_SAMPLE %s ' "$n"
 cut -d ' ' -f 1 /proc/uptime
 /usr/sbin/c906ctl status
 head -n 1 /proc/stat
 p=$(pidof NanoKVM-Server || true)
 [ -z "$p" ] || cat /proc/"$p"/stat
 printf 'VIDEO_FPS %s\n' "$(cat /run/nanokvm/now_fps 2>/dev/null || echo 0)"
 sed -n '/MemAvailable:/p; /CmaFree:/p' /proc/meminfo
 n=$((n+1)); sleep 1
done
echo NKCORE_SAMPLE_END
