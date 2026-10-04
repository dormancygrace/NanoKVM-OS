#!/usr/bin/env python3
"""Audit effective target C compiler options saved by Kbuild (kernel or U-Boot)."""
import argparse
import json
from pathlib import Path
import shlex
from nanokvm_cpu_profile import flags

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('root', type=Path)
p.add_argument('--kind', choices=('kernel', 'bootloader'), default='kernel')
p.add_argument('--output', type=Path)
a = p.parse_args()
expected = flags(a.kind)
prefixes = ('-O', '-march=', '-mtune=', '-mabi=')
required = {prefix: next(f for f in expected if f.startswith(prefix)) for prefix in prefixes}
checked = 0
failures = []
architecture_overrides = []
for path in sorted(a.root.rglob('.*.cmd')):
    lines = path.read_text(errors='replace').splitlines()
    if not lines or 'riscv64-buildroot-linux-musl-gcc' not in lines[0]:
        continue
    args = shlex.split(lines[0])
    if '-c' not in args or not any(f.endswith('.c') for f in args):
        continue
    checked += 1
    actual = {prefix: next((f for f in reversed(args) if f.startswith(prefix)), None)
              for prefix in prefixes}
    selected = dict(required)
    relative = str(path.relative_to(a.root))
    # vDSO code runs in userspace. Kbuild selects its ABI and the separate CFI
    # variant explicitly; do not replace that architecture policy with KCFLAGS.
    if a.kind == 'kernel' and relative.startswith(('arch/riscv/kernel/vdso/', 'arch/riscv/kernel/vdso_cfi/')):
        selected['-march='] = required['-march='].split('_xthead', 1)[0]
        if relative.startswith('arch/riscv/kernel/vdso_cfi/'):
            selected['-march='] += '_zicfilp_zicfiss'
        architecture_overrides.append({'file': relative, 'isa': selected['-march=']})
    mismatch = {key: {'expected': selected[key], 'actual': value}
                for key, value in actual.items() if value != selected[key]}
    fence = [f for f in args if f in ('-mfence-tso', '-mno-fence-tso')]
    if not fence or fence[-1] != '-mno-fence-tso':
        mismatch['fence_tso'] = fence
    for enable, disable in (('-ftree-vectorize', '-fno-tree-vectorize'),
                            ('-ftree-slp-vectorize', '-fno-tree-slp-vectorize')):
        options = [f for f in args if f in (enable, disable)]
        if not options or options[-1] != disable:
            mismatch[enable] = options
    loop_options = [f for f in args if f in ('-ftree-vectorize', '-fno-tree-vectorize',
                                            '-ftree-loop-vectorize', '-fno-tree-loop-vectorize')]
    if not loop_options or loop_options[-1] not in ('-fno-tree-vectorize', '-fno-tree-loop-vectorize'):
        mismatch['loop_vectorization'] = loop_options
    lto_flags = [f for f in args if f.startswith('-flto')]
    if lto_flags:
        mismatch['lto'] = lto_flags
    if mismatch:
        failures.append({'file': str(path.relative_to(a.root)), 'mismatch': mismatch})
report = {'kind': a.kind, 'target_c_commands': checked, 'failures': failures, 'architecture_overrides': architecture_overrides,
          'scope': 'Compiled target C objects recorded by Kbuild; excludes assembly, host tools and prebuilt blobs'}
if a.output:
    a.output.parent.mkdir(parents=True, exist_ok=True)
    a.output.write_text(json.dumps(report, indent=2) + '\n')
print(json.dumps(report, indent=2))
raise SystemExit(1 if failures or not checked else 0)
