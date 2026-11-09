#!/bin/sh

set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)

START_SCRIPT="$ROOT/kvmapp/system/init.d/S95nanokvm"
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
        subprocess.run(shell, input=fixture, text=True, check=True)
        assert (settings / 'fps').read_text() == '60', 'user setting changed'
        print('PASS video defaults and user settings:', shell)
PY

echo 'Runtime-state path checks passed'
