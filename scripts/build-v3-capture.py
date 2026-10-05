#!/usr/bin/env python3
"""Build and inspect the native capture helper; never execute vendor code."""
from pathlib import Path
import hashlib, json, os, subprocess

REPO = Path(__file__).resolve().parents[1]
PLATFORM = Path(os.environ.get('NK_V3_PLATFORM', REPO.parent / 'work/v2.1-b1-20261004/platform'))
OUT = REPO / 'work/v3/native-capture-build'
BASELINE = 'a53b25579ab87cc98f85323b4743deb0d4da907b'
ABI = {
    'kvmv_init': 'void(*)(uint8_t)',
    'kvmv_deinit': 'void(*)(void)',
    'kvmv_hdmi_control': 'int(*)(uint8_t)',
    'kvmv_hdmi_signal_active': 'uint8_t(*)(void)',
    'set_h264_gop': 'void(*)(uint8_t)',
    'set_h265_gop_mode': 'int8_t(*)(uint8_t)',
    'get_h265_gop_mode': 'uint8_t(*)(void)',
    'set_mjpeg_chroma': 'int(*)(uint8_t)',
    'get_mjpeg_chroma_status': 'uint8_t(*)(void)',
    'set_frame_detact': 'void(*)(uint8_t)',
    'kvmv_request_keyframe': 'void(*)(void)',
    'kvmv_edid_maintenance': 'int(*)(uint8_t)',
    'kvmv_read_mjpeg_sink': 'int(*)(uint16_t,uint16_t,uint16_t,kvmv_video_sink,uintptr_t)',
    'kvmv_read_video_sink': 'int(*)(uint16_t,uint16_t,uint8_t,uint16_t,uint8_t,uint8_t,kvmv_video_sink,uintptr_t)',
}

def command(args):
    subprocess.run([str(arg) for arg in args], cwd=REPO, check=True)

def main():
    source = REPO / 'server-rust/native/capture'
    cc = PLATFORM / 'buildroot-output/host/bin/riscv64-buildroot-linux-musl-gcc'
    cxx = PLATFORM / 'buildroot-output/host/bin/riscv64-buildroot-linux-musl-g++'
    libs = PLATFORM / 'server/out/dl_lib'
    if not all(path.is_file() for path in [cc, cxx, libs / 'libkvm.so', source / 'main.c', source / 'nk-capture-worker.c']):
        raise RuntimeError('Set NK_V3_PLATFORM to the matched read-only build artifacts')
    symbols = subprocess.check_output(['nm', '-D', '--defined-only', str(libs / 'libkvm.so')], text=True)
    exported = {line.split()[-1] for line in symbols.splitlines() if line.split()}
    missing = sorted(set(ABI) - exported)
    if missing: raise RuntimeError('Missing capture ABI: ' + ', '.join(missing))
    header = subprocess.check_output(['git', 'show', BASELINE + ':support/sg2002/additional/kvm/include/kvm_vision.h'], cwd=REPO)
    OUT.mkdir(parents=True, exist_ok=True)
    with (REPO / 'docs/experiments/v3.0/actions.md').open('a') as log:
        log.write('\nNative helper before reproducible build:14 exact function-pointer ABI assertions against immutable baseline header and matched exported symbols; strict host/target C compilation and target dynamic libkvm link. Inspect ELF only, never run helper or load native libraries. Fixed runtime fd3, RPATH-compatible retained library locations; package/install/activation still pending.\n')
    (OUT / 'baseline-kvm-vision.h').write_bytes(header)
    (OUT / 'abi.cpp').write_text('#include "baseline-kvm-vision.h"\n#include "nk-capture-abi.h"\n#include <type_traits>\n' + ''.join(
        f'static_assert(std::is_same_v<decltype(&{name}),{kind}>,"{name} ABI");\n' for name,kind in ABI.items()))
    generic = ['-march=rv64gc', '-mabi=lp64d', '-fno-tree-vectorize', '-fno-tree-slp-vectorize']
    strict = ['-Wall', '-Wextra', '-Werror', '-fstack-protector-strong', '-ffile-prefix-map=' + str(REPO) + '=/nanokvm-v3']
    for compiler, name, flags in [('c++', 'host', []), (cxx, 'riscv64', generic)]:
        command([compiler, '-std=c++17', *strict, *flags, '-I' + str(source), '-c', OUT / 'abi.cpp', '-o', OUT / (name + '-abi.o')])
    for compiler, name, flags in [('cc', 'host', []), (cc, 'riscv64', generic)]:
        for file in ['nk-capture-worker.c', 'main.c']:
            command([compiler, '-std=c11', '-O2', *strict, *flags, '-c', source / file, '-o', OUT / (name + '-' + file + '.o')])
    binary = OUT / 'nanokvm-capture-worker'
    command([cc, *generic, '-Wl,--as-needed', '-Wl,--no-undefined',
             OUT / 'riscv64-nk-capture-worker.c.o', OUT / 'riscv64-main.c.o',
             '-L' + str(libs), '-Wl,-rpath-link,' + str(libs),
             '-Wl,-rpath,/usr/lib/nanokvm:/kvmapp/server/dl_lib', '-lkvm', '-o', binary])
    description = subprocess.check_output(['file', str(binary)], text=True).strip()
    dynamic = subprocess.check_output(['readelf', '-d', str(binary)], text=True)
    program = subprocess.check_output(['readelf', '-l', str(binary)], text=True)
    assert 'libkvm.so' in dynamic and '/lib/ld-musl-riscv64.so.1' in program
    result = dict(baseline=BASELINE, baseline_header_sha256=hashlib.sha256(header).hexdigest(),
                  function_abi=ABI, matched_library_sha256=hashlib.sha256((libs / 'libkvm.so').read_bytes()).hexdigest(),
                  helper=dict(sha256=hashlib.sha256(binary.read_bytes()).hexdigest(), bytes=binary.stat().st_size,
                              description=description, dynamic_entries=[line.strip() for line in dynamic.splitlines() if 'NEEDED' in line or 'RUNPATH' in line]),
                  vendor_code_executed=False, installed=False, runtime_wired=False)
    (REPO / 'docs/experiments/v3.0/native-capture-build.json').write_text(json.dumps(result, indent=2) + '\n')
    with (REPO / 'docs/experiments/v3.0/actions.md').open('a') as log:
        log.write('\nNative helper build result:14 ABI assertions compile host/target, all14 exported functions present; strict C and dynamic target libkvm linkage pass. ELF hashes/dependencies recorded in native-capture-build.json; helper not executed, installed, packaged or runtime-wired.\n')
    print(description)
    print('14 ABI types and dynamic helper build pass; vendor code not executed')

if __name__ == '__main__': main()
