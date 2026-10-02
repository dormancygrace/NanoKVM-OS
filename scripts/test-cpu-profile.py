#!/usr/bin/env python3
"""Check production CPU policy, generated defaults and optional real compiler output."""
import argparse
import json
from pathlib import Path
import subprocess
import tempfile
from nanokvm_cpu_profile import ROOT, PROFILE, flags, record, validate_flags

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--compiler')
a = p.parse_args()
user = flags()
kernel = flags('kernel')
for actual in (user, kernel):
    assert [f for f in actual if f.startswith('-O')] == ['-O2'], actual
    assert '-mtune=thead-c906' in actual and '-mno-fence-tso' in actual
assert '-mabi=lp64d' in user and '-mabi=lp64' in kernel
assert '-fno-tree-vectorize' in kernel and '-fno-tree-slp-vectorize' in kernel
kernel_isa = PROFILE['kernel_isa']
assert kernel_isa.split('_')[0] == 'rv64imac'
assert not any(x in kernel_isa for x in ('xtheadvector', 'xtheadfmemidx', 'xtheadfmv'))
config = (ROOT/'firmware/buildroot/configs/nanokvm_platform_defconfig').read_text()
assert 'CONFIG_CC_OPTIMIZE_FOR_SPEED=y' in (ROOT/'platform/uboot/defconfig').read_text()
assert 'BR2_OPTIMIZE_2=y' in config
assert 'BR2_TARGET_OPTIMIZATION="' + ' '.join(user) + '"' in config
patch = ROOT/'platform/native/cvi_mpi/0005-baseline-isa.patch'
if not patch.exists():
    patch = ROOT/'firmware/mpi/patches/0005-baseline-isa.patch'
assert '+  OPT_LEVEL := ' + ' '.join(user) + ' -mcmodel=medany' in patch.read_text()
# Source builds must consume the profile instead of keeping a local ISA copy.
for name in ('build-enhanced-capture.py', 'build-enhanced-mmf.py', 'build-enhanced-system.py',
             'build-enhanced-mpi-bin.py', 'build-enhanced-isp-vendor.py',
             'build-server-existing-libs.py', 'build-usb-audio.py'):
    text = (ROOT/'scripts'/name).read_text()
    assert 'from nanokvm_cpu_profile import' in text, name
    assert '-march=rv64' not in text, name
    assert '-Os' not in text and 'MinSizeRel' not in text, name
assert 'nanokvm_cpu_profile.py" kernel' in (ROOT/'platform/build.sh').read_text()
for kind in ('userspace', 'kernel', 'bootloader'):
    validate_flags(flags(kind), kind)
    for override in ('-Os', '-O3', '-march=rv64gc', '-mtune=generic', '-mabi=ilp32', '-mfence-tso'):
        try:
            validate_flags([*flags(kind), override], kind)
        except ValueError:
            pass
        else:
            raise AssertionError((kind, override))
for override in ('-ftree-vectorize', '-ftree-loop-vectorize', '-ftree-slp-vectorize'):
    try:
        validate_flags([*kernel, override], 'kernel')
    except ValueError:
        pass
    else:
        raise AssertionError(override)
if a.compiler:
    for kind in ('userspace', 'kernel', 'bootloader'):
        macros = subprocess.check_output([a.compiler, *flags(kind), '-dM', '-E', '-x', 'c', '-'], input='', text=True)
        assert '#define __riscv_xlen 64' in macros
        assert '#define __riscv_xtheadba ' in macros
        if kind != 'userspace':
            assert '__riscv_flen' not in macros and '__riscv_xtheadvector' not in macros
        else:
            assert '#define __riscv_flen 64' in macros
        with tempfile.TemporaryDirectory(prefix='nkos-cpu-profile-test-') as tmp:
            obj = Path(tmp)/'probe.o'
            subprocess.run([a.compiler, *flags(kind), '-x', 'c', '-c', '-', '-o', str(obj)],
                           input='unsigned long f(unsigned long a, unsigned long b) { return a + (b << 2); }',
                           text=True, check=True)
            assert obj.stat().st_size > 0
            manifest = Path(tmp)/'profile.json'
            record(manifest, a.compiler, kind)
            result = json.loads(manifest.read_text())
            assert result['flags'] == flags(kind)
            assert result['compiler_version'] == '16.2.0'
print('Shared C906 -O2 profiles, generated defaults and builder consumers pass')
