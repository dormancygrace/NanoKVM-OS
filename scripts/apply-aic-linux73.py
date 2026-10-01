#!/usr/bin/env python3
"""Apply the reviewed SDIO cfg80211 Linux 7.3 compatibility backport."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--source', type=Path, required=True)
a = p.parse_args()
policy = Path(__file__).resolve().parents[1] / 'firmware/wifi/aic8800'
source = a.source.resolve()
files = json.loads((policy / 'linux-7.3-source.json').read_text())['files']
sha = lambda path: hashlib.sha256(path.read_bytes()).hexdigest()
states = []
for relative, hashes in files.items():
    digest = sha(source / relative)
    if digest == hashes['base_sha256']:
        states.append('base')
    elif digest == hashes['patched_sha256']:
        states.append('patched')
    else:
        raise SystemExit('AIC Linux 7.3 source differs from reviewed baseline: ' + relative)
if all(state == 'patched' for state in states):
    print('AIC Linux 7.3 compatibility already applied')
elif all(state == 'base' for state in states):
    data = (policy / 'linux-7.3-sdio.patch').read_bytes()
    args = ['patch', '--batch', '--fuzz=0', '--forward', '-d', str(source), '-p1']
    subprocess.run(args + ['--dry-run'], input=data, check=True)
    subprocess.run(args, input=data, check=True)
else:
    raise SystemExit('AIC Linux 7.3 source is partly patched')
for relative, hashes in files.items():
    if sha(source / relative) != hashes['patched_sha256']:
        raise SystemExit('AIC Linux 7.3 patched hash mismatch: ' + relative)
