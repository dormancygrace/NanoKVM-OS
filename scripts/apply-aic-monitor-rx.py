#!/usr/bin/env python3
"""Apply the reviewed monitor RX NULL fix, accepting a fully patched tree."""
import argparse
from pathlib import Path
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--source', type=Path, required=True)
a = p.parse_args()
patch = Path(__file__).resolve().parents[1] / 'firmware/wifi/aic8800/monitor-rx-null.patch'
args = ['patch', '--batch', '--fuzz=0', '-d', str(a.source.resolve()), '-p1']
data = patch.read_bytes()
if subprocess.run(args + ['--reverse', '--force', '--dry-run'], input=data,
                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
    print('AIC monitor RX fix already applied')
else:
    # Preflight every hunk before modifying a source with unexpected contents.
    subprocess.run(args + ['--forward', '--dry-run'], input=data, check=True)
    subprocess.run(args + ['--forward'], input=data, check=True)
