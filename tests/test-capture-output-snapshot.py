#!/usr/bin/env python3
"""Exercise the production frame check after another reader selects its output."""
from pathlib import Path
import subprocess
import tempfile
repo = Path(__file__).resolve().parents[1]
s = (repo / 'support/sg2002/additional/kvm/src/kvm_vision.cpp').read_text()
start = s.index('if (native_result != 0 || native_width !=')
condition = s[start + 4:s.index(') {', start)]
source = r'''#include <cassert>
struct Size { int width, height; };
struct Config { int vpss_width, vpss_height; };
bool rejected(int native_result, int native_width, int native_height, int native_format,
              int capture_format, Size output, Config kvmv_cfg) {
    (void)output; (void)kvmv_cfg;
    return CONDITION;
}
int main() {
    // JPEG captured at FHD; video selects QHD during its unlocked wait.
    assert(!rejected(0, 1920, 1080, 1, 1, {1920,1080}, {2560,1440}));
    assert(rejected(0, 2560, 1440, 1, 1, {1920,1080}, {2560,1440}));
    assert(rejected(-1, 1920, 1080, 1, 1, {1920,1080}, {1920,1080}));
    assert(rejected(0, 1920, 1080, 2, 1, {1920,1080}, {1920,1080}));
}
'''.replace('CONDITION', condition)
with tempfile.TemporaryDirectory() as tmp:
    p = Path(tmp) / 'check.cpp'
    p.write_text(source)
    binary = p.with_suffix('')
    subprocess.run(['g++', '-std=c++17', '-Wall', '-Wextra', '-Werror', str(p), '-o', str(binary)], check=True)
    subprocess.run([str(binary)], check=True)
print('Capture output snapshot: concurrent selection and invalid frames passed')
