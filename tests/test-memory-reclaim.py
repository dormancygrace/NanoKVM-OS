#!/usr/bin/env python3
"""Exercise production reclaim selection against real-shaped active swap tables."""
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
source = (root / 'kvmapp/system/init.d/S38memory').read_text()
function = re.search(r'^VM_SYSCTL=.*?^apply_reclaim\(\) \{.*?^\}\n', source, re.S | re.M).group(0)
shells = [['sh']] + ([['busybox', 'sh']] if shutil.which('busybox') else [])
header = 'Filename Type Size Used Priority\n'
cases = [
    ('0', ['/dev/zram0'], '100', '0'),
    ('1', ['/dev/zram0'], '100', '0'),  # stale configured SD state
    ('0', ['/dev/zram0', '/swapfile'], '60', '3'),
    ('1', ['/dev/zram0', '/swapfile'], '60', '3'),
    ('0', ['/dev/zram0', '/dev/mmcblk0p3'], '60', '3'),
    ('0', ['/dev/zram0', '/data/user.swap'], '60', '3'),
    ('1', ['/swapfile'], '60', '3'),
    ('0', [], '60', '3'),
    ('0', None, '60', '3'),  # unreadable table: conservative fallback
]
for shell in shells:
    for sd, swaps, swappiness, cluster in cases:
        with tempfile.TemporaryDirectory() as directory:
            d = Path(directory)
            table = d / 'swaps'
            if swaps is not None:
                table.write_text(header + ''.join(f'{name} partition 65536 0 100\n' for name in swaps))
            fixture = f'NANOKVM_VM_SYSCTL={d}\nNANOKVM_PROC_SWAPS={table}\nSD_ENABLED={sd}\n{function}'
            subprocess.run(shell, input=fixture + 'apply_reclaim\n', text=True, check=True)
            assert (d / 'swappiness').read_text() == swappiness + '\n', (shell, sd, swaps)
            assert (d / 'page-cluster').read_text() == cluster + '\n', (shell, sd, swaps)
            # Exercise the actual stop dispatch with device operations doubled.
            stop = re.search(r'^    stop\) (.*?) ;;$', source, re.M).group(1)
            table.write_text(header)
            script = fixture + 'disable_device() { :; }\n' + stop + '\n'
            subprocess.run(shell, input=script, text=True, check=True)
            assert (d / 'swappiness').read_text() == '60\n'
            assert (d / 'page-cluster').read_text() == '3\n'
    # Refused writes must remain nonfatal even with the production set -e.
    fixture = f'set -eu\nNANOKVM_VM_SYSCTL=/nonexistent\nNANOKVM_PROC_SWAPS=/nonexistent\n{function}apply_reclaim\necho done\n'
    assert subprocess.run(shell, input=fixture, text=True, capture_output=True, check=True).stdout == 'done\n'
    print('PASS active swaps, foreign swap, stale config, stop and refused writes:', shell)
