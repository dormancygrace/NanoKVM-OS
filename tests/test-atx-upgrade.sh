#!/bin/sh
# The ATX block of nanokvm-base's post-upgrade, run against a private copy of
# /etc/kvm: an updated Cube keeps ATX on; nothing else is written.
set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
HOOK="$ROOT/firmware/alpine/packages/nanokvm-base/nanokvm-base.post-upgrade"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM

script="$work/atx.sh"
awk '/^# ATX switch \(added/{f=1} f{print} /^# ATX switch end/{if(f) exit}' "$HOOK" |
    sed "s|/etc/kvm|$work/kvm|g" > "$script"
grep -q "$work/kvm/atx" "$script"

# run PROFILE HW EXISTING: board-profile, hw and an existing atx ("" = absent).
run() {
    rm -rf "$work/kvm"; mkdir -p "$work/kvm"
    [ -z "$1" ] || printf '%s\n' "$1" > "$work/kvm/board-profile"
    [ -z "$2" ] || printf '%s\n' "$2" > "$work/kvm/hw"
    [ -z "$3" ] || printf '%s\n' "$3" > "$work/kvm/atx"
    sh -eu "$script"
}
expect() {
    got=$(cat "$work/kvm/atx" 2>/dev/null || echo absent)
    [ "$got" = "$1" ] || { echo "ATX upgrade: $2: got $got, want $1" >&2; exit 1; }
}

run beta beta '';   expect on     'Cube (beta) updated'
run alpha alpha ''; expect on     'Cube (alpha) updated'
run '' beta '';     expect on     'Cube from an image without board-profile'
run lite beta '';   expect absent 'Lite updated: no ATX before'
run pcie pcie '';   expect absent 'PCIe: the server default (on) applies'
run '' '' '';       expect absent 'unknown board'
run beta beta off;  expect off    'a Cube switched off stays off'
run lite beta on;   expect on     'a Lite switched on stays on'
echo "ATX upgrade migration: ok"
