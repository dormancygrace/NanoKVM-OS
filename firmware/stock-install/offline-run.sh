#!/bin/sh
set -eu
package=/root/.nanokvm-stock-package
/kvmapp/system/init.d/S95nanokvm restart
if ! sh "$package/migration/install.sh" "$package/migration" --yes; then
    echo 'Installation stopped; stock application has been restored.' >&2
    exit 1
fi
