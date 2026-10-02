#!/usr/bin/env python3
"""Check source preparation, idempotence and rejection before writes."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess
import tempfile

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--maix-source', type=Path, required=True, help='Unpatched v4.11.3 checkout')
a = p.parse_args()
repo = Path(__file__).resolve().parents[1]
work = repo / 'work'
work.mkdir(exist_ok=True)
sha = lambda path: hashlib.sha256(path.read_bytes()).hexdigest()
def run(script, source, *extra, ok=True):
    result = subprocess.run(['python3', str(repo / 'scripts' / script),
                             '--source', str(source), *extra],
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    if ok and result.returncode:
        raise AssertionError(result.stdout.decode())
    if not ok:
        assert result.returncode, 'Unexpectedly accepted invalid source'
def snapshot(root, paths):
    return {path: sha(root / path) for path in paths}

with tempfile.TemporaryDirectory(prefix='source-patches-', dir=work) as directory:
    root = Path(directory)
    policy = json.loads((repo / 'firmware/maixcdk/source-policy.json').read_text())['files']
    maix = root / 'maix'
    for path, hashes in policy.items():
        dest = maix / path
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(a.maix_source / path, dest)
        assert sha(dest) == hashes['base_sha256']
    # A late invalid input must not cause an earlier I2C patch to be written.
    sys_file = maix / 'components/basic/src/maix_sys.cpp'
    original = sys_file.read_bytes()
    sys_file.write_bytes(original + b'/* unexpected change */\n')
    before = snapshot(maix, policy)
    run('apply-maixcdk-fixes.py', maix, ok=False)
    assert snapshot(maix, policy) == before
    sys_file.write_bytes(original)
    run('apply-maixcdk-fixes.py', maix, '--check', ok=False)
    run('apply-maixcdk-fixes.py', maix)
    before = snapshot(maix, policy)
    run('apply-maixcdk-fixes.py', maix)
    run('apply-maixcdk-fixes.py', maix, '--check')
    assert snapshot(maix, policy) == before

print('PASS: MaixCDK preparation in ignored work/, repeat application, required fixes and invalid input rejection')
