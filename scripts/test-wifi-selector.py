#!/usr/bin/env python3
"""Exercise the boot selector with synthetic SDIO sysfs and a recording modprobe."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

loader = Path(__file__).resolve().parents[1] / 'firmware/buildroot/board/enhanced/init.d/S25wifimod'
cases = [
    ('aic', ['sdio:c07v5449d0145'], ['aic8800_fdrv']),
    ('rtl-b733', ['sdio:c07v024CdB733'], ['8733bs']),
    ('rtl-b73a', ['sdio:c07v024CdB73A'], ['8733bs']),
    ('mixed-duplicate', ['sdio:c07v024CdB733', 'sdio:c07v024CdB73A', 'sdio:c07v5449d0145'], ['8733bs', 'aic8800_fdrv']),
    ('unknown', ['sdio:c07v0001d0001'], []),
    ('absent', [], []),
]
for name, aliases, expected in cases:
    with tempfile.TemporaryDirectory(prefix='nkos-wifi-selector-') as d:
        root = Path(d)
        for i, alias in enumerate(aliases):
            dev = root / str(i)
            dev.mkdir()
            (dev / 'modalias').write_text(alias + '\n')
        mock = root / 'modprobe'
        mock.write_text('#!/bin/sh\nprintf "%s\\n" "$1" >> "$CALLS"\nexit "${FAIL:-0}"\n')
        mock.chmod(0o755)
        calls = root / 'calls'
        env = dict(os.environ, NANOKVM_SDIO_DEVICES_DIR=str(root), NANOKVM_MODPROBE=str(mock), CALLS=str(calls))
        result = subprocess.run(['sh', str(loader), 'start'], env=env, capture_output=True, text=True)
        actual = calls.read_text().splitlines() if calls.exists() else []
        assert result.returncode == 0 and actual == expected, (name, result, actual)
        if expected:
            result = subprocess.run(['sh', str(loader), 'start'], env=dict(env, FAIL='1'), capture_output=True)
            assert result.returncode != 0, 'Failed module load must propagate'
        print(json.dumps({'case': name, 'modules': actual, 'pass': True}))
