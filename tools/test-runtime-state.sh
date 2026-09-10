#!/bin/sh

set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
PERSISTENT_PATTERN='/kvmapp/kvm/(now_fps|wifi_state|width|height|state)'

fail() {
        echo "FAIL: $*" >&2
        exit 1
}

if grep -IErn "$PERSISTENT_PATTERN" \
        "$ROOT/server" "$ROOT/support" "$ROOT/kvmapp" >/dev/null
then
        grep -IErn "$PERSISTENT_PATTERN" \
                "$ROOT/server" "$ROOT/support" "$ROOT/kvmapp" >&2
        fail 'transient telemetry still uses persistent application storage'
fi

START_SCRIPT="$ROOT/kvmapp/system/init.d/S95nanokvm"
grep -F 'mkdir -p /run/nanokvm' "$START_SCRIPT" >/dev/null \
        || fail 'runtime directory is not recreated on every service start'
grep -F "printf '0' > /run/nanokvm/now_fps" "$START_SCRIPT" >/dev/null \
        || fail 'runtime FPS state is not initialized'
grep -F "printf '0' > /run/nanokvm/state" "$START_SCRIPT" >/dev/null \
        || fail 'runtime HDMI state is not initialized'
grep -F "printf '0' > /run/nanokvm/wifi_state" "$START_SCRIPT" >/dev/null \
        || fail 'runtime Wi-Fi state is not initialized'
grep -F "printf '1920' > /run/nanokvm/width" "$START_SCRIPT" >/dev/null \
        || fail 'runtime HDMI width is not initialized'
grep -F "printf '1080' > /run/nanokvm/height" "$START_SCRIPT" >/dev/null \
        || fail 'runtime HDMI height is not initialized'

echo 'Runtime-state path checks passed'
