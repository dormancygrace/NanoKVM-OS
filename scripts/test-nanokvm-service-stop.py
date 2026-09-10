#!/usr/bin/env python3
"""Exercise real S95 stop/restart control flow with no process or device access.

Emit a self-contained shell fixture to run the same cases under target BusyBox.
Only start_services and external process/filesystem commands are doubled.
"""
import argparse
from pathlib import Path
import shlex
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--script', type=Path, default=Path(__file__).resolve().parents[1] / 'kvmapp/system/init.d/S95nanokvm')
parser.add_argument('--emit-shell', type=Path)
args = parser.parse_args()
source = args.script.read_text()
definitions, dispatch = source.split('case "$1" in\n', 1)
mock = r'''
server_up=1
system_up=1
slept=0
[ "$scenario" != absent ] || { server_up=0; system_up=0; }
pidof() {
    case "$1" in
        NanoKVM-Server) [ "$server_up" = 1 ];;
        kvm_system) [ "$system_up" = 1 ];;
        *) exit 90;;
    esac
}
killall() {
    printf '%s %s\n' "$1" "$2" >> "$log"
    case "$scenario:$2" in
        server-stuck:NanoKVM-Server|system-stuck:kvm_system) return 0;;
        signal-denied:NanoKVM-Server) return 1;;
        kill-needed:NanoKVM-Server) [ "$1" = -KILL ] || return 0;;
        delayed:NanoKVM-Server) return 0;;
    esac
    case "$2" in
        NanoKVM-Server) server_up=0;;
        kvm_system) system_up=0;;
        *) exit 91;;
    esac
}
sleep() {
    slept=$((slept + 1))
    if [ "$scenario" = delayed ] && [ "$slept" -eq 3 ]; then server_up=0; fi
    [ "$slept" -le 12 ] || exit 92
    return 0
}
rm() { printf 'remove runtime paths\n' >> "$log"; }
start_services() { printf 'start services\n' >> "$log"; }
sync() { printf 'sync\n' >> "$log"; }
'''
normal = ['-TERM NanoKVM-Server', '-TERM kvm_system', 'remove runtime paths']
stuck = ['-TERM NanoKVM-Server', '-KILL NanoKVM-Server']
cases = [
    ('normal', 'restart', 0, normal + ['start services', 'sync']),
    ('normal', 'stop', 0, normal),
    ('absent', 'restart', 0, ['remove runtime paths', 'start services', 'sync']),
    ('delayed', 'restart', 0, normal + ['start services', 'sync']),
    ('kill-needed', 'restart', 0, stuck + ['-TERM kvm_system', 'remove runtime paths', 'start services', 'sync']),
    ('server-stuck', 'restart', 1, stuck),
    ('server-stuck', 'stop', 1, stuck),
    ('signal-denied', 'restart', 1, stuck),
    ('system-stuck', 'restart', 1, ['-TERM NanoKVM-Server', '-TERM kvm_system', '-KILL kvm_system']),
]
fixture = '''#!/bin/sh
set -u
testdir=$(mktemp -d /tmp/nanokvm-init-test.XXXXXX) || exit 1
trap 'rm -f "$testdir/calls" "$testdir/stdout" "$testdir/stderr"; rmdir "$testdir"' EXIT
log="$testdir/calls"
run_case() (
'''+definitions+'\n'+mock+'\ncase "$1" in\n'+dispatch+'\n)\n'
for scenario, action, status, calls in cases:
    expected = shlex.quote('\n'.join(calls))
    fixture += f'''
scenario={scenario}
: > "$log"
run_case {action} > "$testdir/stdout" 2> "$testdir/stderr"
actual_status=$?
if [ "$actual_status" -ne {status} ] || [ "$(cat "$log")" != {expected} ]; then
    echo 'FAIL {scenario}/{action}: unexpected exit or side effects' >&2
    cat "$log" "$testdir/stdout" "$testdir/stderr" >&2
    exit 1
fi
'''
    if status:
        fixture += '''if grep -q 'OK' "$testdir/stdout" || ! grep -q 'Cannot stop' "$testdir/stderr"; then
    echo 'FAIL stop failure reporting' >&2; exit 1
fi
'''
    fixture += f"echo 'PASS {scenario}/{action}'\n"
if args.emit_shell:
    args.emit_shell.write_text(fixture)
else:
    subprocess.run(['sh', '-n'], input=fixture, text=True, check=True)
    subprocess.run(['sh'], input=fixture, text=True, check=True)
