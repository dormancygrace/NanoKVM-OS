#!/usr/bin/env python3
"""Validate assembled packages and create the unsigned Pages v3 index.

Only current OS packages and retained optional add-ons enter the index. Old
core files stay downloadable for cached indexes, but cannot be selected anew.
The signing job adds both production signatures after this read-only-key job.
"""
import argparse
import importlib.util
import json
from pathlib import Path
import re
import subprocess

import addon_packages

spec = importlib.util.spec_from_file_location('assemble_apk_repo', Path(__file__).with_name('assemble-apk-repo.py'))
assembler = importlib.util.module_from_spec(spec)
spec.loader.exec_module(assembler)


def build_index(repo, keys, apk='apk'):
    repo, keys = Path(repo).resolve(), Path(keys).resolve()
    def run(*args):
        return subprocess.check_output([apk, '--keys-dir', str(keys), *args], text=True)
    # Check the OS release's original indexes before replacing the v3 index.
    for name in assembler.INDEXES:
        run('verify', str(repo / name))
    addon_packages.verify_repository(repo, apk, keys)
    core = assembler.index_packages((repo / 'APKINDEX.tar.gz').read_bytes())
    addons = addon_packages.manifest((repo / 'addons.json').read_bytes())
    for filename in addons:
        match = assembler.ADDON_TAG.fullmatch(filename.removesuffix('.apk'))
        if not match:
            raise ValueError(f'Unsupported add-on package: {filename}')
        dump = run('--allow-untrusted', 'adbdump', str(repo / filename))
        fields = dict(re.findall(r'^  (name|version|arch): (.+)$', dump, re.M))
        expected = {'name': match[1], 'version': match[2] + '-r' + match[3], 'arch': 'riscv64'}
        if fields != expected:
            raise ValueError(f'Add-on metadata mismatch: {filename}: {fields}')
    selected = sorted(set(core) | set(addons))
    pending = repo / 'Packages.pending.adb'
    run('--allow-untrusted', 'mkndx', '--description', 'NanoKVM OS packages and independent add-ons',
        '--output', str(pending), *[str(repo / name) for name in selected])
    # A missing/injected package must not silently enter the resulting index.
    dump = run('--allow-untrusted', 'adbdump', str(pending))
    actual = re.findall(r'^  - name: (.+)\n    version: (.+)$', dump, re.M)
    if sorted(n + '-' + v + '.apk' for n, v in actual) != selected:
        raise ValueError('Generated index differs from selected package inventory')
    return selected


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repo', type=Path, required=True)
    parser.add_argument('--keys', type=Path, required=True)
    parser.add_argument('--apk', default='apk')
    args = parser.parse_args()
    print(json.dumps(build_index(args.repo, args.keys, args.apk)))


if __name__ == '__main__':
    main()
