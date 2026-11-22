#!/bin/sh
# Install nkos-safe-update on a NanoKVM OS device. Run it outside any trial.
set -eu
cd "$(dirname "$0")"
install -m 0755 nkos-safe-update /usr/sbin/nkos-safe-update
install -m 0755 nkos-safe-update.initd /etc/init.d/nkos-safe-update
install -d /usr/lib/nkos-safe-update
install -m 0644 ktrial-demote.sh ktrial-watch.sh boot.its /usr/lib/nkos-safe-update/
rc-update add nkos-safe-update default >/dev/null
sync
/usr/sbin/nkos-safe-update status
