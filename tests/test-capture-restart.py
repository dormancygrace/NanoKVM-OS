#!/usr/bin/env python3
"""Exercise production restart wrappers with forced reader/detector ordering."""
from pathlib import Path
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
s = (repo / 'support/additional/kvm/src/kvm_vision.cpp').read_text()
functions = s[s.index('static std::atomic<bool> capture_stopped'):s.index('static bool write_hdmi_signal_file')]
control = s[s.index('int kvmv_hdmi_control(uint8_t enabled)'):s.index('/* Caller holds vi_mutex. Parking')]
park = s[s.index('int kvmv_set_capture_parked('):s.index('int kvmv_capture_reclaim_pending(')]
retry = s[s.index('uint8_t check_kvmv('):s.index('/* A step that creates')]
# Run the actual native admission before any allocation, with the admitted
# media work replaced by a successful return (the MMF suite covers that work).
read_start = s.index('static int kvmv_read_img_locked(', s.index('static int kvmv_read_img(uint16_t'))
read = s[read_start:s.index('    uint8_t try_num = 0;', read_start)]
read = read.replace('{', '{\n    (void)_width; (void)_height; (void)_qlty;', 1)
read += '    (void)frame_undetact_count; pthread_mutex_unlock(&vi_mutex); return 0;\n}\n'
harness = (repo / 'tests/test-capture-restart.cpp').read_text()
with tempfile.TemporaryDirectory() as tmp:
    cpp = Path(tmp) / 'test.cpp'
    cpp.write_text(harness.replace('// PRODUCTION_FUNCTIONS', functions + retry + control + park + read))
    binary = Path(tmp) / 'test'
    subprocess.run(['g++', '-std=c++17', '-Wall', '-Wextra', '-Werror', '-pthread',
                    '-I' + str(repo / 'support/additional/kvm/include'),
                    str(cpp), '-o', str(binary)], check=True)
    subprocess.run([str(binary)], check=True, timeout=15)
print('PASS: capture restart/disable serialize readers, release resources, block stale detector/park, retry cleanup and re-enable')
