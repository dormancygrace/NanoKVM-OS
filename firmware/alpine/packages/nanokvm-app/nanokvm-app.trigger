#!/bin/sh
# APK runs triggers after unpacking the transaction. Only restart an already
# running service on a live OpenRC system; never start one inside image builds.
set -eu
# The global APK commit hook coalesces all component changes into one plan.
[ ! -x /usr/sbin/nkos-apply-updates ] || exit 0
[ -f /run/openrc/softlevel ] || exit 0
command -v rc-service >/dev/null 2>&1 || exit 0
rc-service nanokvm-app status >/dev/null 2>&1 || exit 0
echo "Restarting NanoKVM after application component update"
exec rc-service nanokvm-app restart
