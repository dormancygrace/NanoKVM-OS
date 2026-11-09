#!/usr/bin/env python3
import json
import os
from pathlib import Path
import subprocess
import tempfile

loader = Path(__file__).resolve().parents[1] / 'kvmapp/system/init.d/S25wifimod'
cases = [
    ('aic-primary', ['sdio:c07v5449d0145'], 'aic8800_fdrv'),
    ('aic-secondary', ['sdio:c07v544Ad0146'], 'aic8800_fdrv'),
    ('aic-func2', ['sdio:c07v5449d0146'], 'aic8800_fdrv'),
    ('aic8800dc', ['sdio:c07vC8A1dC08D'], 'aic8800_fdrv'),
    ('d80n', ['sdio:c07vC8A1d9081'], 'aic8800_fdrv'),
    ('d40wn', ['sdio:c07vC8A1d9086'], 'aic8800_fdrv'),
    ('d80x2', ['sdio:c07vC8A1d2082'], 'aic8800_fdrv'),
    ('d80-primary', ['sdio:c07vC8A1d0082'], 'aic8800_fdrv'),
    ('d80-secondary', ['sdio:c07vC8A1d0182'], 'aic8800_fdrv'),
    ('d80-functions', ['sdio:c07vC8A1d0082', 'sdio:c07vC8A1d0182'], 'aic8800_fdrv'),
    ('rtl-b733', ['sdio:c07v024CdB733'], '8733bs'),
    ('rtl-b73a', ['sdio:c07v024CdB73A'], '8733bs'),
    ('unknown', ['sdio:c07v0001d0001'], None),
    ('unknown-aic-device', ['sdio:c07vC8A1d9087'], None),
    ('absent', [], None),
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
        # No supported card fails at once: S30wifi must not wait for wlan0.
        assert (result.returncode == 0) == (expected is not None), (name, result)
        assert actual == ([expected] if expected else []), (name, actual)
        if expected:
            result = subprocess.run(['sh', str(loader), 'start'], env=dict(env, FAIL='1'), capture_output=True)
            assert result.returncode != 0, 'Failed module load must propagate'
        print(json.dumps({'case': name, 'module': expected, 'pass': True}))
