#!/bin/sh
set -eu
incoming=${1:?missing package handoff}
case "$incoming" in /root/.nanokvm-stock-incoming.*) ;; *) exit 2 ;; esac
package=/root/.nanokvm-stock-package
lock=/run/nanokvm-stock-package.lock
locked=0
owned=0
cleanup() {
    # Only this invocation's handoff and promoted package are disposable.
    # The resumable transaction on /data is never removed here.
    rm -rf "$incoming"
    if [ "$owned" = 1 ]; then rm -rf "$package"; fi
    if [ "$locked" = 1 ]; then rmdir "$lock"; fi
}
trap cleanup EXIT
/kvmapp/system/init.d/S95nanokvm restart
mkdir "$lock" || { echo 'Another offline installer is running.' >&2; exit 1; }
locked=1
[ ! -e /run/nanokvm-stock-install.lock ] || {
    echo 'A migration is already running; stock application has been restored.' >&2; exit 1;
}
# A handoff from an earlier kernel boot cannot have a live controller. Keep
# current-boot handoffs: another upload may be waiting to acquire this lock.
boot_id=$(cat /proc/sys/kernel/random/boot_id 2>/dev/null) || boot_id=unknown
case "$boot_id" in ''|*[!0-9a-f-]*) boot_id=unknown ;; esac
[ "${#boot_id}" = 36 ] || boot_id=unknown
for stale in /root/.nanokvm-stock-incoming.*; do
    [ "$boot_id" != unknown ] || break
    [ "$stale" != "$incoming" ] && [ -d "$stale" ] && [ ! -L "$stale" ] || continue
    stamp=${stale##*/.nanokvm-stock-incoming.}
    stamp=${stamp%.*}
    case "$stamp" in ''|*[!0-9a-f-]*) continue ;; esac
    [ "${#stamp}" = 36 ] && [ "$stamp" != "$boot_id" ] || continue
    rm -rf "$stale"
done
# No controller or migration uses the old package. A refused preflight or a
# power cycle may have left it behind; replace it after restoring the stock UI.
rm -rf "$package"
mv "$incoming/package" "$package"
owned=1
if ! sh "$package/migration/install.sh" "$package/migration" --yes; then
    echo 'Installation stopped; stock application has been restored.' >&2
    exit 1
fi
