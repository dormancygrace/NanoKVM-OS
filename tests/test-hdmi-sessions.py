#!/usr/bin/env python3
"""Run real receiver/session code with controlled I2C faults and scheduling."""
from pathlib import Path
import os
import subprocess
import tempfile

root = Path(__file__).resolve().parents[1]
vision = (root / 'support/sg2002/additional/kvm/src/kvm_vision.cpp').read_text()
def function(source, marker):
    start = source.index(marker)
    brace = source.index('{', start)
    depth = 1
    for end in range(brace + 1, len(source)):
        depth += (source[end] == '{') - (source[end] == '}')
        if not depth:
            return source[start:end + 1]
    raise AssertionError(marker)

with tempfile.TemporaryDirectory(prefix='nanokvm-hdmi-session-') as name:
    tmp = Path(name)
    source = (root / 'tests/test-hdmi-sessions.cpp').read_text()
    source = source.replace('// PRODUCTION_DIAGNOSTICS', function(vision, 'static void log_receiver_diagnostics('))
    source = source.replace('// PRODUCTION_POLL', function(vision, 'static bool poll_hdmi_receiver('))
    source = source.replace('// PRODUCTION_CONTROL',
                            function(vision, 'int kvmv_edid_maintenance(') + '\n' +
                            function(vision, 'int kvmv_hdmi_control('))
    cpp = tmp / 'sessions.cpp'
    cpp.write_text(source)
    flags = ['-Wall', '-Wextra', '-Werror', '-g']
    if os.environ.get('NANOKVM_SANITIZE'):
        flags += ['-fsanitize=address,undefined', '-fno-omit-frame-pointer']
    lock = f'-DNANOKVM_RECEIVER_LOCK_PATH="{tmp / "receiver.lock"}"'
    subprocess.run(['g++', '-std=c++17', *flags, '-pthread', lock,
                    '-I' + str(root / 'support/sg2002/additional/kvm/include'),
                    str(cpp), '-o', str(tmp / 'sessions')], check=True)
    subprocess.run([str(tmp / 'sessions')], check=True, timeout=20)
    subprocess.run(['gcc', '-std=gnu11', *flags, lock,
                    str(root / 'tests/test-edid-i2c.c'), '-o', str(tmp / 'edid')],
                   check=True)
    subprocess.run([str(tmp / 'edid')], check=True, timeout=10)
    subprocess.run(['g++', '-std=c++17', *flags,
                    '-I' + str(root / 'support/sg2002/additional/kvm/include'),
                    str(root / 'tests/test-hdmi-i2c.cpp'), '-o', str(tmp / 'transport')],
                   check=True)
    subprocess.run([str(tmp / 'transport')], check=True, timeout=10)
print('PASS: native receiver/maintenance and production EDID I2C helpers')
