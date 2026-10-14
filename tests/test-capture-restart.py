#!/usr/bin/env python3
"""Exercise production restart wrappers with forced reader/detector ordering."""
from pathlib import Path
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
s = (repo / 'support/sg2002/additional/kvm/src/kvm_vision.cpp').read_text()
functions = s[s.index('static bool restart_capture_now()'):s.index('static bool write_hdmi_signal_file')]
retry = s[s.index('uint8_t check_kvmv('):s.index('/* A step that creates')]
harness = (repo / 'tests/test-capture-restart.cpp').read_text()
with tempfile.TemporaryDirectory() as tmp:
    cpp = Path(tmp) / 'test.cpp'
    cpp.write_text(harness.replace('// PRODUCTION_FUNCTIONS', functions + retry))
    binary = Path(tmp) / 'test'
    subprocess.run(['g++', '-std=c++17', '-Wall', '-Wextra', '-Werror', '-pthread',
                    '-I' + str(repo / 'support/sg2002/additional/kvm/include'),
                    str(cpp), '-o', str(binary)], check=True)
    subprocess.run([str(binary)], check=True, timeout=15)
print('PASS: restart waits for frame/JPEG lease, retires pending video, preserves park/resume and retries without deadlock')
