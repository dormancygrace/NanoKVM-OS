#!/bin/sh
# Run detached from SSH with stdin=/dev/null, stdout/stderr=/dev/ttyS0.
# Bounded userspace liveness only; it cannot prove interrupt/NMI availability.
set -eu
token=${1:?heartbeat token}; seconds=${2:-300}
case "$token" in ''|*[!a-zA-Z0-9-]*) exit 2;; esac
case "$seconds" in ''|*[!0-9]*) exit 2;; esac
[ "$seconds" -ge 5 ] && [ "$seconds" -le 3600 ]
i=0
while [ "$i" -lt "$seconds" ]; do
 read -r uptime idle < /proc/uptime
 fps=0
 if [ -r /run/nanokvm/now_fps ]; then read -r fps < /run/nanokvm/now_fps || :; fi
 printf 'NKHB %s %s %s fps=%s\n' "$token" "$i" "$uptime" "${fps:-0}"
 i=$((i+1));sleep 1
done
printf 'NKHB_END %s\n' "$token"
