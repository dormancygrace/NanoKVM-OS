#!/usr/bin/env python3
"""Shared flags for NanoKVM-owned C/C++ builds, not official Alpine packages.

Kernel ISA retains the already qualified Kbuild profile, including extensions
used by its architecture alternatives. It must not inherit userspace FP/vector
flags. The Go compiler and prebuilt vendor objects are outside this policy.
"""
import argparse
import hashlib
import json
from pathlib import Path
import shlex
import subprocess

ROOT = Path(__file__).resolve().parents[1]
PROFILE_FILE = ROOT / 'platform/cpu-profile.json'
PROFILE = json.loads(PROFILE_FILE.read_text())


def flags(kind='userspace'):
    if kind not in ('userspace', 'kernel', 'bootloader'):
        raise ValueError('Unknown CPU build profile: ' + kind)
    result = [PROFILE['optimization'], '-march=' + PROFILE[kind + '_isa'],
              '-mtune=' + PROFILE['tune'], '-mabi=' + PROFILE[kind + '_abi']]
    if not PROFILE['fence_tso']:
        result.append('-mno-fence-tso')
    if kind != 'userspace':
        result += ['-fno-tree-vectorize', '-fno-tree-slp-vectorize']
    return result


def record(output, compiler, kind='userspace', effective_flags=None):
    """Record the actual flag list supplied by the caller and compiler identity."""
    path = Path(output)
    path.parent.mkdir(parents=True, exist_ok=True)
    data = {
        'schema': 1, 'profile': PROFILE['name'], 'kind': kind,
        'profile_sha256': hashlib.sha256(PROFILE_FILE.read_bytes()).hexdigest(),
        'compiler_version': subprocess.check_output([str(compiler), '-dumpfullversion'], text=True).strip(),
        'compiler_target': subprocess.check_output([str(compiler), '-dumpmachine'], text=True).strip(),
        'flags': list(effective_flags if effective_flags is not None else flags(kind)),
        'scope': 'NanoKVM-built source objects; excludes stock Alpine and prebuilt vendor objects'
    }
    path.write_text(json.dumps(data, indent=2) + '\n')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('kind', choices=('userspace', 'kernel', 'bootloader'))
    p.add_argument('--record', type=Path)
    p.add_argument('--compiler')
    args = p.parse_args()
    if args.record:
        if not args.compiler:
            p.error('--record requires --compiler')
        record(args.record, args.compiler, args.kind)
    else:
        print(shlex.join(flags(args.kind)))


if __name__ == '__main__':
    main()
