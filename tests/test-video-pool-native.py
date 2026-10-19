#!/usr/bin/env python3
"""Run the native capture admission function against simulated Device Trees."""
from pathlib import Path
import re
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
source = (repo / 'support/sg2002/additional/kvm/src/kvm_vision.cpp').read_text()
body = source[source.index('static bool res_fits_video_memory'):source.index('uint8_t check_res')]
constants = '\n'.join(re.findall(r'^#define (?:uhd_ion_mib|qhd_ion_mib) .*$', source, re.M))
with tempfile.TemporaryDirectory() as tmp:
    root = Path(tmp)
    node = root / 'ion'
    node.mkdir()
    body = body.replace('/proc/device-tree/reserved-memory/ion', str(node))
    cpp = root / 'test.cpp'
    cpp.write_text('#include <cstdint>\n#include <cstdio>\n#include <cstdlib>\n#include <unistd.h>\n' + constants + '\n' + body +
                   '\nint main(int c, char **v) { if(c!=4)return 2; return res_fits_video_memory(atoi(v[1]),atoi(v[2])) == (atoi(v[3])!=0) ? 0 : 1; }\n')
    binary = root / 'test'
    subprocess.run(['g++', '-std=c++17', '-Wall', '-Wextra', '-Werror', str(cpp), '-o', str(binary)], check=True)
    count = 0
    for mib in (40, 56, 104, 128, 144):
        for reusable in (False, True):
            (node / 'size').write_bytes((mib << 20).to_bytes(4, 'big'))
            marker = node / 'reusable'
            marker.unlink(missing_ok=True)
            if reusable:
                marker.touch()
            for w, h in ((1920,1080), (2560,1440), (3840,2160), (1440,2560)):
                want = (w<=1920 and h<=1080) or (mib>=54 if max(w,h)<=2560 else mib >= (144 if reusable else 104))
                subprocess.run([str(binary), str(w), str(h), str(int(want))], check=True)
                count += 1
    print(f'{count} native video pool cases passed')
