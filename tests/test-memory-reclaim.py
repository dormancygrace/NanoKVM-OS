#!/usr/bin/env python3
"""S38memory: reclaim policy for the swap it set up (ZRAM alone or with SD)."""
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
source = (root / 'kvmapp/system/init.d/S38memory').read_text()
function = re.search(r'^VM_SYSCTL=.*?^apply_reclaim\(\) \{.*?^\}\n', source, re.S | re.M).group(0)
shells = [['sh']] + ([['busybox', 'sh']] if shutil.which('busybox') else [])

cases = [
    # SD_ENABLED, zram active -> swappiness, page-cluster
    ('0', True, '100', '0'),
    ('1', True, '60', '3'),
    ('0', False, '60', '3'),
    ('1', False, '60', '3'),
]
for shell in shells:
    for sd, zram, swappiness, cluster in cases:
        with tempfile.TemporaryDirectory() as d:
            d = Path(d)
            fixture = f'''NANOKVM_VM_SYSCTL={d}
active() {{ {'true' if zram else 'false'}; }}
SD_ENABLED={sd}
{function}apply_reclaim
'''
            subprocess.run(shell, input=fixture, text=True, check=True)
            assert (d / 'swappiness').read_text() == swappiness + '\n', (shell, sd, zram)
            assert (d / 'page-cluster').read_text() == cluster + '\n', (shell, sd, zram)
    # A refused write does not fail the caller.
    fixture = f'NANOKVM_VM_SYSCTL=/nonexistent\nactive() {{ true; }}\nSD_ENABLED=0\n{function}apply_reclaim\necho done\n'
    assert subprocess.run(shell, input=fixture, text=True, capture_output=True, check=True).stdout == 'done\n'
    print('PASS: reclaim policy for ZRAM and SD swap:', ' '.join(shell))
