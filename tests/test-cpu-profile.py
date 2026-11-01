#!/usr/bin/env python3
"""Check production CPU flag policy, its validator and the Kbuild flag-order audit."""
from pathlib import Path
import subprocess
import tempfile
import sys
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'scripts'))
from nanokvm_cpu_profile import ROOT, PROFILE, flags, validate_flags  # noqa: E402

user = flags()
kernel = flags('kernel')
for actual, optimization in ((user, '-O2'), (kernel, '-O3'), (flags('audio'), '-O2'), (flags('bootloader'), '-O2'), (flags('openssl'), '-O3')):
    assert [f for f in actual if f.startswith('-O')] == [optimization], actual
    assert '-mtune=thead-c906' in actual and '-mno-fence-tso' in actual
assert '-mabi=lp64d' in user and '-mabi=lp64' in kernel
# Only the kernel and its modules drop -mstrict-align; userspace never had it.
assert '-mno-strict-align' in kernel and '-mno-strict-align' not in user
assert '-mno-strict-align' not in flags('bootloader') and '-mno-strict-align' not in flags('audio')
for actual in (user, kernel, flags('audio'), flags('openssl')):
    assert '-fno-tree-vectorize' in actual and '-fno-tree-slp-vectorize' in actual
assert flags('audio') == user
assert '-mstringop-strategy=scalar' in flags('openssl')
assert all('xtheadvector' not in flag for flag in flags('openssl'))
assert PROFILE['kernel_lto'] == 'none'
kernel_isa = PROFILE['kernel_isa']
assert kernel_isa.split('_')[0] == 'rv64imac'
assert not any(x in kernel_isa for x in ('xtheadvector', 'xtheadfmemidx', 'xtheadfmv'))
for kind in ('userspace', 'kernel', 'bootloader', 'audio', 'openssl'):
    validate_flags(flags(kind), kind)
    overrides = ['-Os', '-O2' if kind in ('kernel', 'openssl') else '-O3', '-flto', '-march=rv64gc', '-mtune=generic', '-mabi=ilp32', '-mfence-tso']
    if kind == 'kernel':
        overrides.append('-mstrict-align')
    if kind == 'openssl':
        overrides.extend(['-mstringop-strategy=vector', '-march=' + PROFILE['userspace_isa']])
    for override in overrides:
        try:
            validate_flags([*flags(kind), override], kind)
        except ValueError:
            pass
        else:
            raise AssertionError((kind, override))
for override in ('-ftree-vectorize', '-ftree-loop-vectorize', '-ftree-slp-vectorize'):
    try:
        validate_flags([*user, override], 'userspace')
    except ValueError:
        pass
    else:
        raise AssertionError(override)
# Kbuild may put generic flags before our override, or a driver override after
# it. Check the order using actual saved-command syntax.
with tempfile.TemporaryDirectory(prefix='nkos-kbuild-flags-') as tmp:
    root = Path(tmp)
    command = root/'.probe.o.cmd'
    for extra, success in (('', True), ('-O1', False), ('-march=rv64gc', False), ('-mstrict-align', False),
                           ('-ftree-loop-vectorize', False), ('-flto=auto', False)):
        command.write_text('savedcmd_probe.o := riscv64-buildroot-linux-musl-gcc -Os '
                           + ' '.join(kernel) + ' ' + extra + ' -c probe.c -o probe.o\n')
        result = subprocess.run(['python3', str(ROOT/'scripts/audit-kbuild-profile.py'), str(root)],
                                capture_output=True, text=True)
        assert (result.returncode == 0) == success, (extra, result.stdout, result.stderr)
print('C906 GCC kernel/modules/OpenSSL -O3, userspace/audio -O2 and unchanged bootloader policy pass')
