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
# No controller or migration uses the old package. A refused preflight or a
# power cycle may have left it behind; replace it after restoring the stock UI.
rm -rf "$package"
mv "$incoming/package" "$package"
owned=1
if ! sh "$package/migration/install.sh" "$package/migration" --yes; then
    echo 'Installation stopped; stock application has been restored.' >&2
    exit 1
fi
