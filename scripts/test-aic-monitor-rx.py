#!/usr/bin/env python3
"""Check monitor RX patch application against a supplied vendor source file."""
import argparse
from pathlib import Path
import subprocess
import tempfile

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--source-file', type=Path, required=True)
a = p.parse_args()
script = Path(__file__).with_name('apply-aic-monitor-rx.py')
with tempfile.TemporaryDirectory() as td:
    dest = Path(td) / 'aic8800_fdrv/rwnx_rx.c'
    dest.parent.mkdir()
    original = a.source_file.read_bytes()
    dest.write_bytes(original)
    cmd = ['python3', str(script), '--source', td]
    subprocess.run(cmd, check=True)
    patched = dest.read_bytes()
    assert patched != original, 'Use an unpatched baseline for this test'
    assert b'if (!skb_monitor)' in patched
    assert b'status &= ~RX_STAT_FORWARD;' in patched
    subprocess.run(cmd, check=True)
    assert dest.read_bytes() == patched
    dest.write_bytes(b'/* unsupported source */\n')
    before = dest.read_bytes()
    assert subprocess.run(cmd, stdout=subprocess.DEVNULL,
                          stderr=subprocess.DEVNULL).returncode != 0
    assert dest.read_bytes() == before
print('PASS: apply, repeat without changes, reject unknown source')
