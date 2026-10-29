#!/usr/bin/env python3
"""Run the production FPS reader of kvm_system against an absent, empty,
normal and over-long /run/nanokvm/now_fps under ASan/UBSan."""
from pathlib import Path
import subprocess
import tempfile

repo = Path(__file__).resolve().parents[1]
main = repo / 'support/sg2002/kvm_system/main'
source = (main / 'lib/system_state/system_state.cpp').read_text()


def function(signature):
    start = source.index(signature)
    return source[start:source.index('\n}\n', start) + 3]


with tempfile.TemporaryDirectory(prefix='nkos-stream-fps-') as directory:
    out = Path(directory)
    fps = out / 'now_fps'
    code = function('int read_integer_file(const char* path, int fallback)') + \
        function('void kvm_update_stream_fps(void)').replace('/run/nanokvm/now_fps', str(fps))
    (out / 'test.cpp').write_text(r"""
#include "config.h"
#include <cassert>
kvm_sys_state_t kvm_sys_state;
""" + code + r"""
static void write(const char *path, const char *text) {
    FILE *fp = fopen(path, "w");
    assert(fp);
    fputs(text, fp);
    fclose(fp);
}
int main(int, char **argv) {
    const char *path = argv[1];
    remove(path);
    kvm_sys_state.now_fps = 33;
    kvm_update_stream_fps();            // no file: the server is not encoding
    assert(kvm_sys_state.now_fps == 0);
    write(path, "60\n");
    kvm_update_stream_fps();
    assert(kvm_sys_state.now_fps == 60);
    write(path, "");
    kvm_update_stream_fps();
    assert(kvm_sys_state.now_fps == 0);
    // Longer than the old 10-byte buffer.
    write(path, "59                                        \n");
    kvm_update_stream_fps();
    assert(kvm_sys_state.now_fps == 59);
    puts("kvm_system FPS file: absent, normal, empty, long pass");
}
""")
    includes = [main / 'include', repo / 'support/sg2002/additional/kvm/include']
    includes += sorted(p for p in (main / 'lib').iterdir() if p.is_dir())
    subprocess.run(['g++', '-std=c++17', '-O1', '-g', '-pthread',
                    '-fsanitize=address,undefined', '-fno-omit-frame-pointer', '-no-pie',
                    *['-I' + str(p) for p in includes], str(out / 'test.cpp'),
                    '-o', str(out / 'test')], check=True)
    subprocess.run([str(out / 'test'), str(fps)], check=True, timeout=10)
