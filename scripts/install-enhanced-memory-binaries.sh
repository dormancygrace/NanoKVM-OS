#!/bin/sh
# Run on NanoKVM with new binaries staged on its SD root filesystem.
# chmod must run BEFORE replacing BusyBox, because chmod is a BusyBox applet.
set -eu
PATH=/usr/sbin:/usr/bin:/sbin:/bin
stage=${1:-/root/.memory-install}
test -s "$stage/busybox.new"
chmod 755 "$stage/busybox.new"
"$stage/busybox.new" true
"$stage/busybox.new" swapon --help 2>&1 | grep -q -- '-p'
filesystem() { df -P "$1" | awk 'NR == 2 {print $1}'; }
stage_fs=$(filesystem "$stage")
test -n "$stage_fs"
test "$stage_fs" = "$(filesystem /usr/bin)"
restart=0
if [ -e "$stage/NanoKVM-Server.new" ]; then
    test -s "$stage/NanoKVM-Server.new"
    test "$stage_fs" = "$(filesystem /kvmapp/server)"
    chmod 755 "$stage/NanoKVM-Server.new"
    mv "$stage/NanoKVM-Server.new" /kvmapp/server/NanoKVM-Server
    restart=1
fi
mv "$stage/busybox.new" /usr/bin/busybox
/usr/bin/busybox true
sync
if [ "$restart" = 1 ]; then /etc/init.d/S95nanokvm restart; fi
