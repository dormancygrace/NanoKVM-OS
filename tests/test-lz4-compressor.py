#!/usr/bin/env python3
"""Exercise the actual imported kernel compressor against independent liblz4."""
from pathlib import Path
import argparse
import os
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--oracle', default='liblz4.so.1')
parser.add_argument('--output', type=Path, help='Retain build in the selected task output directory')
args = parser.parse_args()


def check(directory):
    directory.mkdir(parents=True, exist_ok=True)
    patch = (ROOT / 'platform/kernel/0054-lz4-update-compressor-1.10.patch').read_text()
    sources = ['lib/lz4/lz4_110.h', 'lib/lz4/lz4_compress_110.c']
    extracted = set()
    for part in patch.split('diff --git ')[1:]:
        name = part.splitlines()[0].split(' b/')[1]
        if name not in sources:
            continue
        assert '\nnew file mode 100644\n' in part, name
        content = '\n'.join(line[1:] for line in part.splitlines()
                            if line.startswith('+') and not line.startswith('+++')) + '\n'
        target = directory / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(content)
        extracted.add(name)
    assert extracted == set(sources)
    scalar = ROOT / 'platform/kernel/0056-lz4-c906-scalar-paths.patch'
    selected = ''.join('diff --git ' + part for part in scalar.read_text().split('diff --git ')[1:]
                       if part.splitlines()[0].split(' b/')[1] == 'lib/lz4/lz4_110.h')
    assert selected
    subprocess.run(['patch', '--batch', '--fuzz=0', '-p1', '-d', str(directory)],
                   input=selected, text=True, check=True)
    # Old candidate tools must fail explicitly before creating partial outputs.
    bench = ROOT / 'firmware/alpine/bench/c906-lz4'
    for tool in ['variants.py', 'build.py']:
        output = directory / ('retired-' + tool)
        arguments = [str(directory), str(output)] if tool == 'variants.py' else [
            '--kernel', str(directory), '--kernel-build', str(directory),
            '--output', str(output), '--cross', 'unused-', '--cflags=-O3']
        result = subprocess.run(['python3', str(bench / tool), *arguments], text=True, capture_output=True)
        assert result.returncode == 2 and 'retired for the LZ4 1.10' in result.stderr, result.stderr
        assert not output.exists()
    include = directory / 'include/linux'
    include.mkdir(parents=True, exist_ok=True)
    for name in ['kernel.h', 'limits.h', 'lz4.h', 'module.h', 'string.h']:
        (include / name).write_text('/* Supplied by the injected native compatibility header. */\n')
    flags = ['-std=gnu11', '-O2', '-g', '-fno-strict-aliasing', '-Wall', '-Wextra', '-Werror']
    if os.environ.get('NANOKVM_SANITIZE') == '1':
        flags += ['-fsanitize=address,undefined', '-fno-omit-frame-pointer', '-fno-sanitize-recover=all']
    library = directory / 'kernel-lz4.so'
    subprocess.run(['cc', *flags, '-fPIC', '-shared', '-Wl,-Bsymbolic-functions',
                    '-include', str(ROOT / 'tests/lz4-compressor/compat.h'),
                    '-I' + str(include.parent),
                    str(directory / 'lib/lz4/lz4_compress_110.c'), '-o', str(library)], check=True)
    # The wrapper must expose exactly the existing seven C entrypoints. The
    # kernel's EXPORT_SYMBOL subset is separately checked by the real Kbuild.
    symbols = subprocess.check_output(['nm', '-D', '--defined-only', str(library)], text=True)
    assert {line.split()[-1] for line in symbols.splitlines()} == {
        'LZ4_compress_fast', 'LZ4_compress_default', 'LZ4_compress_destSize',
        'LZ4_resetStream', 'LZ4_loadDict', 'LZ4_saveDict', 'LZ4_compress_fast_continue'}
    test = directory / 'contract'
    subprocess.run(['cc', *flags, str(ROOT / 'tests/lz4-compressor/contract.c'),
                    '-ldl', '-o', str(test)], check=True)
    subprocess.run([str(test), str(library.resolve()), args.oracle], check=True)


if args.output:
    check(args.output.resolve())
else:
    with tempfile.TemporaryDirectory(prefix='lz4-compressor-') as tmp:
        check(Path(tmp))
