#!/usr/bin/env python3
"""Fault injection of real JPEG and capture code on the host; no hardware."""
import argparse
from pathlib import Path
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--mpi', type=Path, required=True)
parser.add_argument('--sensor', type=Path, required=True)
parser.add_argument('--output', type=Path, required=True)
parser.add_argument('--sanitize', action='store_true', help='Enable address/undefined sanitizers')
args = parser.parse_args()
repo = Path(__file__).resolve().parents[1]
args.output.mkdir(parents=True, exist_ok=False)
# Match musl target char signedness. Glibc fortify is unrelated to these host
# lifetime tests; target production builds keep their normal compiler flags.
flags = ['g++', '-std=gnu++17', '-pthread', '-O1', '-g', '-funsigned-char',
         '-U_FORTIFY_SOURCE', '-D_FORTIFY_SOURCE=0', '-Wall', '-Wextra', '-Werror',
         '-ffunction-sections', '-fdata-sections', '-Wl,--gc-sections',
         '-D__CV181X__', '-DOS_IS_LINUX', '-DNANOKVM_ENHANCED', '-DSENSOR_LONTIUM_LT6911']
if args.sanitize:
    flags += ['-fsanitize=address,undefined', '-fno-omit-frame-pointer']
flags += [f'-DSENSOR{i}_TYPE=LONTIUM_LT6911_2M_60FPS_8BIT' for i in range(3)]
includes = [args.sensor / 'common', args.mpi / 'include', args.mpi / 'include/isp/cv181x',
            args.mpi / 'sample/common', args.mpi / 'component/panel/cv181x',
            repo / 'support/sg2002/additional/kvm/include',
            repo / 'support/sg2002/additional/kvm_mmf/include', repo / 'firmware/mpi']
flags += ['-I' + str(path.resolve()) for path in includes]


def check_read_img_gates():
    """The MJPEG gate must run under vi_mutex, on every pass, before any channel is configured or submitted to."""
    source = (repo / 'support/sg2002/additional/kvm/src/kvm_vision.cpp').read_text()
    start = source.index('int kvmv_read_img(uint16_t _width')
    body = source[start:source.index('int kvmv_read_video(', start)]
    gate = 'mjpeg_input_blocked(_type)'
    first, second = body.index(gate), body.index(gate, body.index(gate) + 1)
    assert body.index('pthread_mutex_timedlock(&vi_mutex') < first, 'gate before vi_mutex'
    assert body.index('do {') < first, 'gate outside the retry loop'
    for step in ['cam->set_resolution(', 'mmf_enc_jpg_deinit(0)', 'cam->close_format(', 'mmf_vi_frame_pop_native(', 'cam->read()']:
        assert first < body.index(step), 'gate after ' + step
    for step in ['frame_to_jpeg(', 'img->to_jpeg(']:
        assert second < body.index(step), 'second gate after ' + step
    assert body.count('IMG_MJPEG_INPUT_BLOCKED') == 2
    # kvmv_read_mjpeg_sink must reach the gated function, not a channel directly.
    sink = source[source.index('int kvmv_read_mjpeg_sink('):]
    assert 'kvmv_read_img(width, height, VENC_MJPEG' in sink[:sink.index('free_kvmv_data')]
    print('read_img gates ok')


check_read_img_gates()

for case in ['quality', 'sink', 'capture', 'policy', 'gate']:
    binary = args.output.resolve() / case
    sources = [repo / f'firmware/probes/mjpeg-{case}-contract.cpp']
    if case == 'capture':
        sources = [repo / 'firmware/probes/capture-contract.cpp',
                   repo / 'support/sg2002/additional/kvm/src/kvm_capture.cpp']
    elif case == 'policy':
        sources = [repo / 'support/sg2002/additional/kvm/tests/mjpeg_policy_test.cpp']
    elif case == 'gate':
        sources = [repo / 'support/sg2002/additional/kvm/tests/geometry_gate_test.cpp']
    subprocess.run(flags + [str(path) for path in sources] + ['-o', str(binary)], check=True)
    result = subprocess.run([str(binary)], capture_output=True, text=True)
    (args.output / (case + '.txt')).write_text(
        result.stdout + result.stderr + f'exit={result.returncode}\n')
    print(result.stdout + result.stderr, end='', flush=True)
    result.check_returncode()
