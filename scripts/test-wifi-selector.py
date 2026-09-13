#!/usr/bin/env python3
"""Exercise the boot selector with synthetic SDIO sysfs and a recording modprobe."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

loader = Path(__file__).resolve().parents[1] / 'firmware/buildroot/board/enhanced/init.d/S25wifimod'
cases = [
    ('aic-primary', ['sdio:c07v5449d0145'], ['aic8800_fdrv']),
    ('aic-secondary', ['sdio:c07v544Ad0146'], ['aic8800_fdrv']),
    ('d80-primary', ['sdio:c07vC8A1d0082'], ['aic8800_fdrv']),
    ('d80-secondary', ['sdio:c07vC8A1d0182'], ['aic8800_fdrv']),
    ('d80-functions', ['sdio:c07vC8A1d0082', 'sdio:c07vC8A1d0182'], ['aic8800_fdrv']),
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
        mock.write_text(
            '#!/bin/sh\n'
            'printf "%s\\n" "$1" >> "$CALLS"\n'
            '[ "${FAIL:-0}" = 0 ] || exit "$FAIL"\n'
            'mkdir -p "$NANOKVM_MODULES_DIR/$1"\n'
        )
        mock.chmod(0o755)
        calls = root / 'calls'
        modules = root / 'modules'
        modules.mkdir()
        env = dict(
            os.environ,
            NANOKVM_SDIO_DEVICES_DIR=str(root),
            NANOKVM_MODULES_DIR=str(modules),
            NANOKVM_MODPROBE=str(mock),
            CALLS=str(calls),
        )
        result = subprocess.run(['sh', str(loader), 'start'], env=env, capture_output=True, text=True)
        actual = calls.read_text().splitlines() if calls.exists() else []
        assert result.returncode == 0 and actual == expected, (name, result, actual)
        result = subprocess.run(['sh', str(loader), 'start'], env=env, capture_output=True, text=True)
        actual = calls.read_text().splitlines() if calls.exists() else []
        assert result.returncode == 0 and actual == expected, (name + '-idempotent', result, actual)
        if expected:
            for child in modules.iterdir():
                child.rmdir()
            result = subprocess.run(['sh', str(loader), 'start'], env=dict(env, FAIL='1'), capture_output=True)
            assert result.returncode != 0, 'Failed module load must propagate'
        print(json.dumps({'case': name, 'modules': actual, 'pass': True}))
