#!/bin/sh
# The syslog migration in nanokvm-base's post-install and post-upgrade, run
# against a private copy of its paths: only Alpine's default is replaced.
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
BASE="$ROOT/firmware/alpine/packages/nanokvm-base"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM

generated='# NanoKVM: the live system log in RAM
mkdir -p /run/log
SYSLOGD_OPTS="-t -O /run/log/messages -s 512 -b 1"'

for hook in post-install post-upgrade; do
    script="$work/$hook.sh"
    # The syslog block alone, with its paths in the test directory and no
    # running OpenRC (no service restart).
    awk '/^# The live system log stays in RAM/{f=1} f{print} /^unset syslog_conf syslog_marker syslog_changed/{if(f) exit}' \
        "$BASE/nanokvm-base.$hook" |
        sed -e "s|/etc/conf.d/syslog|$work/conf.d/syslog|g" \
            -e "s|/run/openrc/softlevel|$work/no-softlevel|g" \
            -e "s|/var/log/messages|$work/var-log/messages|g" > "$script"
    grep -q 'syslog_changed=yes' "$script"

    run() { rm -rf "$work/conf.d" "$work/var-log"; mkdir -p "$work/var-log"; : > "$work/var-log/messages"
            [ -z "$1" ] || { mkdir -p "$work/conf.d"; printf '%s\n' "$1" > "$work/conf.d/syslog"; }
            sh -eu "$script"; }
    expect() { [ "$(cat "$work/conf.d/syslog")" = "$1" ] || { echo "$hook: $2: got: $(cat "$work/conf.d/syslog")" >&2; exit 1; }; }

    # No file, and Alpine's default (with or without comments): generated.
    run ''; expect "$generated" 'missing file'
    run 'SYSLOGD_OPTS="-t"'; expect "$generated" 'Alpine default'
    run '# value for syslogd
SYSLOGD_OPTS="-t"'; expect "$generated" 'Alpine default with a comment'
    # The generated file stays, edited or not, and so does any other.
    run "$generated"; expect "$generated" 'generated file'
    edited="$(printf '%s\n' "$generated" | sed 's/-s 512 -b 1/-s 128 -b 2 -R 192.0.2.1/')"
    run "$edited"; expect "$edited" 'edited generated file'
    run 'SYSLOGD_OPTS="-t -R 192.0.2.1"'; expect 'SYSLOGD_OPTS="-t -R 192.0.2.1"' 'user setting'
done

# An upgrade outside a running system removes the card log it replaced; one
# that keeps the user's setting leaves it.
script="$work/post-upgrade.sh"
rm -rf "$work/conf.d" "$work/var-log"; mkdir -p "$work/var-log"; : > "$work/var-log/messages"
sh -eu "$script"
[ ! -e "$work/var-log/messages" ] || { echo 'upgrade kept the card log after the move' >&2; exit 1; }
mkdir -p "$work/conf.d"; printf '%s\n' 'SYSLOGD_OPTS="-t -R 192.0.2.1"' > "$work/conf.d/syslog"; : > "$work/var-log/messages"
sh -eu "$script"
[ -e "$work/var-log/messages" ] || { echo 'upgrade removed the card log of a user setting' >&2; exit 1; }

echo 'syslog RAM migration tests passed'
