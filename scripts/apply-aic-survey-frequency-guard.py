#!/usr/bin/env python3
"""Apply the SDIO survey-frequency bounds fix to a reviewed AIC source tree."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--source', type=Path, required=True)
a = p.parse_args()
policy = Path(__file__).resolve().parents[1] / 'firmware/wifi/aic8800'
patch = policy / 'survey-frequency-guard.patch'
hashes = json.loads((policy / 'sdio-source.json').read_text())['survey_frequency_file']
source = a.source.resolve()
target = source / 'aic8800_fdrv/rwnx_msg_rx.c'
data = patch.read_bytes()
args = ['patch', '--batch', '--fuzz=0', '--forward', '-d', str(source), '-p1']
digest = lambda: hashlib.sha256(target.read_bytes()).hexdigest()
compat = json.loads((policy / 'linux-7.3-source.json').read_text())['files']
compat_hash = compat['aic8800_fdrv/rwnx_msg_rx.c']['patched_sha256']
if digest() in (hashes['patched_sha256'], compat_hash):
    print('AIC survey frequency guard already applied')
else:
    if digest() != hashes['base_sha256']:
        raise SystemExit('AIC survey source differs from the reviewed baseline')
    subprocess.run(args + ['--dry-run'], input=data, check=True)
    subprocess.run(args, input=data, check=True)
    if digest() != hashes['patched_sha256']:
        raise SystemExit('AIC survey patched source hash mismatch')
    print('AIC survey frequency guard applied')
