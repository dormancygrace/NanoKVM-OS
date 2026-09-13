#!/bin/sh
# The supplied heartbeat script must already be present on the device.
# Redirect inside the child: BusyBox nohup redirects terminal stdout to nohup.out.
set -eu
script=${1:?path to heartbeat.sh}
token=${2:?unique heartbeat token}
seconds=${3:-300}
case "$token" in ''|*[!a-zA-Z0-9-]*) exit 2;; esac
case "$seconds" in ''|*[!0-9]*) exit 2;; esac
[ -r "$script" ]
[ "${#token}" -le 48 ]
[ "$seconds" -ge 5 ] && [ "$seconds" -le 3600 ]
nohup sh -c 'exec sh "$@" >/dev/ttyS0 2>&1' sh "$script" "$token" "$seconds" </dev/null >/dev/null 2>&1 &
printf '%s\n' "$!"
