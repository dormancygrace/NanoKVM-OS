#!/bin/sh
# Read-only preflight for the manual Enhanced app delta; NEVER installs/restarts.
set -eu
PATH=/usr/sbin:/usr/bin:/sbin:/bin
stage=$(CDPATH= cd -- "${1:-$(dirname -- "$0")}" && pwd -P)
fail() { echo "$*" >&2; exit 1; }
grep -q 'flavour=enhanced' /etc/nanokvm-buildroot || fail 'Enhanced rootfs required'
test ! -e /kvmapp/kvm_new_app || fail 'Legacy migration is pending'
test -x "$stage/NanoKVM-Server" || fail 'Missing executable server'
test -s "$stage/web/index.html" || fail 'Missing web index'
for name in S13nanokvm-watchdog S38memory S94sg2002aes S95nanokvm S98tailscaled; do
    test -x "$stage/init.d/$name" || fail "Missing script: $name"
    sh -n "$stage/init.d/$name"
done
filesystem() { df -P "$1" | awk 'NR == 2 {print $1}'; }
stage_fs=$(filesystem "$stage")
test -n "$stage_fs" || fail 'Cannot identify staging filesystem'
test "$stage_fs" = "$(filesystem /kvmapp/server)" || fail 'Stage on the application SD filesystem, not tmpfs'
test "$stage_fs" = "$(filesystem /etc/init.d)" || fail 'Init scripts are on a different filesystem'
test -L /tmp/server && test "$(readlink /tmp/server)" = /kvmapp/server || fail 'Expected Enhanced SD-backed runtime path'
test -x /lib/ld-musl-riscv64.so.1 || fail 'Missing Enhanced musl loader'
# musl --list loads/relocates DSOs, then exits before application constructors
# and entry point. It validates linkage, not the media driver's runtime ABI.
/lib/ld-musl-riscv64.so.1 --library-path /kvmapp/server/dl_lib --list "$stage/NanoKVM-Server"
echo 'PASS: SD staging, shell syntax and installed-library linkage; nothing installed or restarted'
