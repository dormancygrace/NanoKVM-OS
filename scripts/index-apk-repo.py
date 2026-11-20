#!/usr/bin/env python3
"""Validate assembled packages and create the unsigned Pages v3 index.

Only current OS packages and, of each pinned add-on nanokvm-release depends
on, the newest version these OS packages satisfy enter an OS index; only the
pinned add-ons the add-on index (--addons). Old core files and the other
pinned versions stay downloadable for cached indexes, but cannot be selected
anew.
The signing job adds the ECDSA production signature after this read-only-key job.
"""
import argparse
import importlib.util
import json
import operator
from pathlib import Path
import re
import subprocess

import addon_packages

spec = importlib.util.spec_from_file_location('assemble_apk_repo', Path(__file__).with_name('assemble-apk-repo.py'))
assembler = importlib.util.module_from_spec(spec)
spec.loader.exec_module(assembler)


SUFFIXES = {'alpha': 0, 'beta': 1, 'pre': 2, 'rc': 3}
OPERATORS = {'=': operator.eq, '>=': operator.ge, '<=': operator.le, '>': operator.gt, '<': operator.lt}


def version_key(text):
    """APK version order for the forms NanoKVM packages use: X.Y.Z, a
    pre-release suffix below its release (4.3.0_beta1 < 4.3.0), -rN."""
    match = re.fullmatch(r'(\d+(?:\.\d+)*)(?:_(alpha|beta|pre|rc)(\d*))?(?:-r(\d+))?', text)
    if not match:
        raise ValueError(f'Unsupported version: {text}')
    return (tuple(map(int, match[1].split('.'))), SUFFIXES.get(match[2], len(SUFFIXES)),
            int(match[3] or 0), int(match[4] or 0))


def satisfied(depends, records):
    """Whether the OS packages of records (a v2 index) satisfy every NanoKVM
    dependency of an add-on: nanokvm-os>=VERSION, which nanokvm-release
    provides, and the capabilities of the server (nanokvm-rustdesk-bridge=2
    and so on). Alpine packages come from Alpine's repositories and the other
    add-ons from the same selection, so they are not checked here."""
    provided = {}
    for record in records:
        for entry in [record['P'] + '=' + record['V'], *record.get('p', '').split()]:
            name, _, version = entry.partition('=')
            provided.setdefault(name, []).append(version)
    for dependency in depends.split():
        match = re.fullmatch(r'([^=<>~]+)(?:(>=|<=|=|>|<)(.+))?', dependency)
        if not match:
            raise ValueError(f'Unsupported add-on dependency: {dependency}')
        name, op, version = match.groups()
        if not name.startswith('nanokvm-') or name in addon_packages.ADDONS:
            continue
        if not any(op is None or (have and OPERATORS[op](version_key(have), version_key(version)))
                   for have in provided.get(name, [])):
            return False
    return True


def build_index(repo, keys, apk='apk', addons_only=False):
    repo, keys = Path(repo).resolve(), Path(keys).resolve()
    def run(*args):
        return subprocess.check_output([apk, '--keys-dir', str(keys), *args], text=True)
    core = {}
    if not addons_only:
        # Check the OS release's original indexes before replacing the v3 index.
        for name in assembler.INDEXES:
            run('verify', str(repo / name))
    addon_packages.verify_repository(repo, apk, keys)
    if not addons_only:
        core = assembler.index_packages((repo / 'APKINDEX.tar.gz').read_bytes())
    addons = addon_packages.manifest((repo / 'addons.json').read_bytes())
    newest = {}
    for filename in addons:
        match = assembler.ADDON_TAG.fullmatch(filename.removesuffix('.apk'))
        if not match:
            raise ValueError(f'Unsupported add-on package: {filename}')
        info = addon_packages.metadata(run('--allow-untrusted', 'adbdump', str(repo / filename)), filename)
        if addons_only:
            newest[filename] = (None, filename)
            continue
        # The OS folder keeps the retained versions of the BASE add-ons; its
        # index offers the newest one this release's OS packages satisfy, so
        # a channel whose head is older than an add-on's minimum keeps the
        # version that works with it.
        if satisfied(info.get('depends', ''),assembler.index_records((repo / 'APKINDEX.tar.gz').read_bytes())):
            version = version_key(info['version'])
            if match[1] not in newest or version > newest[match[1]][0]:
                newest[match[1]] = (version, filename)
    selected = sorted(set(core) | {filename for _, filename in newest.values()})
    pending = repo / 'Packages.pending.adb'
    description = 'NanoKVM OS add-ons' if addons_only else 'NanoKVM OS packages'
    run('--allow-untrusted', 'mkndx', '--description', description,
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
    parser.add_argument('--addons', action='store_true', help='the add-on repository: no OS packages')
    args = parser.parse_args()
    print(json.dumps(build_index(args.repo, args.keys, args.apk, args.addons)))


if __name__ == '__main__':
    main()
