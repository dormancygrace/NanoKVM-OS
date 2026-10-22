#!/usr/bin/env python3
"""S95 start: the device key and the hostname prefix, read by the shell."""
from pathlib import Path
import shutil
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
source = (root / 'kvmapp/system/init.d/S95nanokvm').read_text()
definitions, dispatch = source.split('case "$1" in\n', 1)
shells = [['sh']] + ([['busybox', 'sh']] if shutil.which('busybox') else [])

cases = [
    # base_uid, previous key, previous prefix -> key, prefix
    ('UID: d19506b3_1359c372\n', None, None, 'd19506b31359c372\n', 'kvm'),
    ('UID: d19506b3_1359c372\n', 'd19506b31359c372\n', 'kvm', 'd19506b31359c372\n', 'kvm'),
    ('UID: d19506b3_1359c372', 'stale\n', 'nanokvm\n', 'd19506b31359c372\n', 'kvm'),
    # As cut took them: the second field only, and the whole value without "_".
    ('UID: aa_bb_cc\n', None, None, 'aabb\n', 'kvm'),
    ('UID: abcdef\n', None, None, 'abcdefabcdef\n', 'kvm'),
]
for shell in shells:
    for uid, key, prefix, want_key, want_prefix in cases:
        with tempfile.TemporaryDirectory() as d:
            d = Path(d)
            (d / 'base_uid').write_text(uid)
            if key is not None:
                (d / 'device_key').write_text(key)
            if prefix is not None:
                (d / 'hostname.prefix').write_text(prefix)
            fixture = definitions + 'start_services() { :; }\ncase "$1" in\n' + dispatch
            for path, name in (('/sys/class/cvi-base/base_uid', 'base_uid'), ('/device_key', 'device_key'),
                               ('/boot/hostname.prefix', 'hostname.prefix'), ('/mnt/data', 'data')):
                fixture = fixture.replace(path, str(d / name))
            result = subprocess.run(shell + ['-s', 'start'], input=fixture, text=True, capture_output=True)
            assert result.returncode == 0, (shell, uid, result.stderr)
            assert (d / 'device_key').read_text() == want_key, (shell, uid, (d / 'device_key').read_text())
            assert (d / 'hostname.prefix').read_text() == want_prefix, (shell, prefix)
    print('PASS: device key and hostname prefix:', ' '.join(shell))
