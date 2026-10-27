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
    if kind not in ('userspace', 'kernel', 'bootloader', 'audio', 'openssl'):
        raise ValueError('Unknown CPU build profile: ' + kind)
    if kind == 'kernel' and PROFILE.get('kernel_lto', 'none') != 'none':
        raise ValueError('Production GCC kernel requires kernel_lto=none')
    isa_kind = 'userspace' if kind in ('audio', 'openssl') else kind
    isa = PROFILE[isa_kind + '_isa']
    if kind == 'openssl':
        # No unconditional RVV instructions in general-purpose crypto code.
        # Vectorization/stringop switches alone do not restrict all expansions.
        isa = '_'.join(part for part in isa.split('_') if part != 'xtheadvector')
    result = [PROFILE.get(kind + '_optimization', PROFILE['optimization']),
              '-march=' + isa,
              '-mtune=' + PROFILE['tune'], '-mabi=' + PROFILE[isa_kind + '_abi']]
    if not PROFILE['fence_tso']:
        result.append('-mno-fence-tso')
    # The C906 performs misaligned loads and stores in hardware (the kernel's
    # boot probe measures them 4.3x faster than byte access). Kbuild adds
    # -mstrict-align for portable kernels; KCFLAGS come last and override it.
    if kind == 'kernel' and not PROFILE.get('kernel_strict_align', True):
        result.append('-mno-strict-align')
    # Keep handwritten/vendor RVV assembly; disable compiler autovectorization.
    result += ['-fno-tree-vectorize', '-fno-tree-slp-vectorize']
    if kind == 'openssl':
        # C906 memcpy and HTTPS measurements favor scalar inlining. Tree
        # vectorization flags alone do not control inline memory operations.
        result.append('-mstringop-strategy=scalar')
    return result


def validate_flags(actual, kind='userspace'):
    """Reject conflicting CPU/optimization options before writing metadata."""
    expected = flags(kind)
    for prefix in ('-O', '-march=', '-mtune=', '-mabi='):
        selected = [f for f in actual if f.startswith(prefix)]
        required = [f for f in expected if f.startswith(prefix)]
        if not selected or any(f not in required for f in selected):
            raise ValueError(f'{kind}: conflicting or missing {prefix} option: {selected}')
    for option in expected:
        if option not in actual:
            raise ValueError(f'{kind}: missing {option}')
    forbidden = {'-mfence-tso'}
    if kind == 'openssl':
        forbidden |= {'-mstringop-strategy=' + value for value in ('auto', 'vector', 'libcall')}
    if '-mno-strict-align' in expected:
        forbidden.add('-mstrict-align')
    forbidden |= {'-ftree-vectorize', '-ftree-loop-vectorize', '-ftree-slp-vectorize'}
    if any(f.startswith('-flto') for f in actual):
        raise ValueError(f'{kind}: LTO is outside the GCC production profile')
    if forbidden.intersection(actual):
        raise ValueError(f'{kind}: contradictory compiler options')


def record(output, compiler, kind='userspace', effective_flags=None):
    """Record the actual flag list supplied by the caller and compiler identity."""
    actual = list(effective_flags if effective_flags is not None else flags(kind))
    validate_flags(actual, kind)
    path = Path(output)
    path.parent.mkdir(parents=True, exist_ok=True)
    data = {
        'schema': 1, 'profile': PROFILE['name'], 'kind': kind,
        'compiler_family': 'gcc', 'lto': 'none',
        'profile_sha256': hashlib.sha256(PROFILE_FILE.read_bytes()).hexdigest(),
        'compiler_version': subprocess.check_output([str(compiler), '-dumpfullversion'], text=True).strip(),
        'compiler_target': subprocess.check_output([str(compiler), '-dumpmachine'], text=True).strip(),
        'flags': actual,
        'scope': 'NanoKVM-built source objects; excludes stock Alpine and prebuilt vendor objects'
    }
    path.write_text(json.dumps(data, indent=2) + '\n')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('kind', choices=('userspace', 'kernel', 'bootloader', 'audio', 'openssl'))
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
