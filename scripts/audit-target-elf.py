#!/usr/bin/env python3
"""Audit target ELF dependency closure and reject OpenSSL 3 consumers."""
import argparse
import json
import re
import subprocess
from pathlib import Path

p = argparse.ArgumentParser()
p.add_argument('--target', type=Path, required=True)
p.add_argument('--readelf', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
a = p.parse_args()
target = a.target.resolve()
if not target.is_dir() or not a.readelf.is_file():
    p.error('target/readelf input is unavailable')

providers = {path.name for path in target.rglob('*')}
needed_re = re.compile(r'\(NEEDED\).*\[(.+?)\]')
elfs = {}
invalid_interpreters = {}
for path in sorted(target.rglob('*')):
    if not path.is_file() or path.is_symlink():
        continue
    try:
        with path.open('rb') as stream:
            if stream.read(4) != b'\x7fELF':
                continue
    except OSError:
        continue
    dynamic = subprocess.run([str(a.readelf), '-d', str(path)], text=True,
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if dynamic.returncode:
        raise SystemExit(f'readelf failed for {path}: {dynamic.stderr}')
    dependencies = needed_re.findall(dynamic.stdout)
    elfs[str(path.relative_to(target))] = dependencies
    headers = subprocess.run([str(a.readelf), '-lW', str(path)], text=True,
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if headers.returncode or headers.stderr.strip():
        invalid_interpreters[str(path.relative_to(target))] = headers.stderr.strip()
    elif re.search(r'^\s*INTERP\s', headers.stdout, re.MULTILINE):
        interpreter = re.search(r'Requesting program interpreter: ([^\]]+)\]', headers.stdout)
        if not interpreter or interpreter.group(1) != '/lib/ld-musl-riscv64.so.1':
            invalid_interpreters[str(path.relative_to(target))] = (
                interpreter.group(1) if interpreter else 'missing or empty PT_INTERP')

missing = {path: sorted(name for name in names if name not in providers)
           for path, names in elfs.items()
           if any(name not in providers for name in names)}
openssl3 = {path: names for path, names in elfs.items()
            if any(name in ('libssl.so.3', 'libcrypto.so.3') for name in names)}
openssl3_files = sorted(str(path.relative_to(target)) for path in target.rglob('*')
                        if path.name in ('libssl.so.3', 'libcrypto.so.3'))
openssl4 = {path: names for path, names in elfs.items()
            if any(name in ('libssl.so.4', 'libcrypto.so.4') for name in names)}
report = {
    'status': 'pass' if not missing and not openssl3 and not openssl3_files and not invalid_interpreters else 'fail',
    'elf_count': len(elfs),
    'needed_objects': sum(bool(names) for names in elfs.values()),
    'provided_basenames': len(providers),
    'missing': missing,
    'invalid_interpreters': invalid_interpreters,
    'openssl3_consumers': openssl3,
    'openssl3_files': openssl3_files,
    'openssl4_consumers': openssl4,
}
a.output.write_text(json.dumps(report, indent=2) + '\n')
print(json.dumps({key: report[key] for key in
                  ('status', 'elf_count', 'needed_objects', 'missing',
                   'openssl3_consumers', 'openssl3_files')}, indent=2))
if report['status'] != 'pass':
    raise SystemExit(1)
