#!/bin/sh
set -eu

repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM

persistent=$work/etc/kvm/cron/crontabs
spool=$work/var/spool/cron/crontabs
mkdir -p "$spool"
printf '%s\n' '* * * * * echo migrated' > "$spool/root"

NANOKVM_CRON_DIR=$persistent NANOKVM_CRON_SPOOL=$spool NANOKVM_CRON_OWNER= \
    sh "$repo/kvmapp/system/init.d/S49persistent-cron" start

test -L "$spool"
test "$(readlink "$spool")" = "$persistent"
test "$(cat "$persistent/root")" = '* * * * * echo migrated'
test "$(stat -c '%a' "$persistent")" = 700
test "$(stat -c '%a' "$persistent/root")" = 600

printf '%s\n' '* * * * * echo persistent' > "$spool/root"
rm -rf "$work/var/spool"
mkdir -p "$(dirname "$spool")"
NANOKVM_CRON_DIR=$persistent NANOKVM_CRON_SPOOL=$spool NANOKVM_CRON_OWNER= \
    sh "$repo/kvmapp/system/init.d/S49persistent-cron" start
test "$(cat "$spool/root")" = '* * * * * echo persistent'

echo 'PASS: cron spool survives recreation through persistent storage'
