#!/bin/sh

set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)

START_SCRIPT="$ROOT/kvmapp/system/init.d/S95nanokvm"
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

echo 'Runtime-state path checks passed'
