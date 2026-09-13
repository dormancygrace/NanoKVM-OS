#!/usr/bin/env python3
import argparse, hashlib, json, re
from pathlib import Path

p = argparse.ArgumentParser()
p.add_argument('--build-log', type=Path, required=True)
p.add_argument('--build-dir', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
a = p.parse_args()

opt_re = re.compile(r'(?<!\S)-O(?:0|1|2|3|g|s|fast)(?!\S)')
families = {'wrapper': [], 'gcc-runtime': []}
for number, line in enumerate(a.build_log.read_text(errors='replace').splitlines(), 1):
    if ' -c ' not in line:
        continue
    # Configure probes and install commands can mention the cross-compiler and
    # the literal text "-c" without invoking a target compilation.
    command = line.lstrip()
    if command.startswith(('checking ', '/usr/bin/install ')):
        continue
    family = None
    if 'riscv64-buildroot-linux-musl-gcc' in line or 'riscv64-buildroot-linux-musl-g++' in line:
        family = 'wrapper'
    elif '/gcc/xgcc ' in line or '/gcc/xg++ ' in line:
        family = 'gcc-runtime'
    if family:
        families[family].append((number, line, opt_re.findall(line)))

generated = []
for path in a.build_dir.rglob('*'):
    if path.name not in ('compile_commands.json', 'build.ninja', 'flags.make'):
        continue
    relative = path.relative_to(a.build_dir)
    if relative.parts[0].startswith('host-'):
        continue
    for number, line in enumerate(path.read_text(errors='replace').splitlines(), 1):
        bad = re.findall(r'(?<!\S)-O(?:3|s|fast)(?!\S)', line)
        if bad:
            generated.append({'path': str(relative), 'line': number, 'flags': bad, 'text': line[:1000]})

violations = []
for family, records in families.items():
    for number, line, flags in records:
        if not flags or any(flag != '-O2' for flag in flags):
            violations.append({'family': family, 'line': number, 'flags': flags, 'text': line[:1000]})

report = {
    'status': 'pass' if not violations and not generated and len(families['wrapper']) >= 100 else 'fail',
    'policy': 'Every logged target C/C++ compile command uses only -O2 optimization; generated target build files contain no -O3, -Os, or -Ofast.',
    'build_log_sha256': hashlib.sha256(a.build_log.read_bytes()).hexdigest(),
    'compile_commands': {name: len(records) for name, records in families.items()},
    'violations': violations,
    'generated_residuals': generated,
}
a.output.write_text(json.dumps(report, indent=2) + '\n')
print(json.dumps({k: report[k] for k in ('status', 'compile_commands')}, indent=2))
if report['status'] != 'pass':
    raise SystemExit(1)
