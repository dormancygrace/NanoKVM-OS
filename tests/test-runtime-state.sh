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

grep -F "[ -s /kvmapp/kvm/fps ] || printf '90' > /kvmapp/kvm/fps" "$START_SCRIPT" >/dev/null \
        || fail 'a missing FPS setting gets no default'
grep -F "[ -s /kvmapp/kvm/qlty ] || printf '15000' > /kvmapp/kvm/qlty" "$START_SCRIPT" >/dev/null \
        || fail 'a missing quality setting gets no default'
grep -F "[ -s /kvmapp/kvm/res ] || printf '0' > /kvmapp/kvm/res" "$START_SCRIPT" >/dev/null \
        || fail 'a missing resolution setting gets no default'
grep -F "[ -s /kvmapp/kvm/type ] || printf 'h265' > /kvmapp/kvm/type" "$START_SCRIPT" >/dev/null \
        || fail 'a missing codec setting gets no default'
# Exercise cleanup rather than require one particular spelling of rm.
python3 - "$START_SCRIPT" <<'PY'
from pathlib import Path
import shutil, subprocess, sys, tempfile
source = Path(sys.argv[1]).read_text().split('case "$1" in\n', 1)[0]
for shell in [['sh']] + ([['busybox', 'sh']] if shutil.which('busybox') else []):
    with tempfile.TemporaryDirectory() as directory:
        settings = Path(directory) / 'kvm'
        fixture = source.replace('/kvmapp/kvm', str(settings)) + '\nvideo_settings\n'
        subprocess.run(shell, input=fixture, text=True, check=True)
        assert (settings / 'fps').read_text() == '90'
        (settings / 'fps').write_text('60')
        (settings / 'fps.apk-new').write_text('90')
        outside = Path(directory) / 'keep'
        outside.write_text('keep')
        (settings / 'type.apk-new').symlink_to(outside)
        (settings / 'qlty.apk-new').symlink_to(Path(directory) / 'absent')
        subprocess.run(shell, input=fixture, text=True, check=True)
        assert not list(settings.glob('*.apk-new')), 'stale .apk-new entries kept'
        assert (settings / 'fps').read_text() == '60', 'user setting changed'
        assert outside.read_text() == 'keep', 'symlink target changed'
        subprocess.run(shell, input=fixture, text=True, check=True)
        print('PASS video defaults, cleanup and user settings:', shell)
PY
if grep -E '^app +file +/kvmapp/kvm/' "$ROOT/platform/packages.list" >/dev/null; then
        fail 'a package owns a video setting again'
fi

echo 'Runtime-state path checks passed'
