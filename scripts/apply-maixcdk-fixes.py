#!/usr/bin/env python3
"""Apply or verify the reviewed MaixCDK fixes on v4.11.3 sources."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--source', type=Path, required=True)
p.add_argument('--check', action='store_true', help='Verify without changing sources')
a = p.parse_args()
policy = Path(__file__).resolve().parents[1] / 'firmware/maixcdk'
files = json.loads((policy / 'source-policy.json').read_text())['files']
source = a.source.resolve()
sha = lambda path: hashlib.sha256(path.read_bytes()).hexdigest()
pending = []
# Validate every input before changing any file.
for relative, hashes in files.items():
    digest = sha(source / relative)
    if digest == hashes['patched_sha256']:
        continue
    if digest != hashes['base_sha256']:
        raise SystemExit('MaixCDK source differs from reviewed baseline: ' + relative)
    if a.check:
        raise SystemExit('Apply MaixCDK fixes before building: ' + relative)
    pending.append(policy / 'patches' / hashes['patch'])
args = ['patch', '--batch', '--fuzz=0', '--forward', '-d', str(source), '-p1']
for patch in pending:
    subprocess.run(args + ['--dry-run'], input=patch.read_bytes(), check=True)
for patch in pending:
    subprocess.run(args, input=patch.read_bytes(), check=True)
for relative, hashes in files.items():
    if sha(source / relative) != hashes['patched_sha256']:
        raise SystemExit('MaixCDK patched hash mismatch: ' + relative)
print('MaixCDK I2C and system-file fixes verified')
