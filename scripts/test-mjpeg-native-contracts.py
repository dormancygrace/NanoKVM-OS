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


def function_body(source, signature, end='\n}\n'):
    start = source.index(signature)
    return source[start:source.index(end, start)]


def check_read_img_gates():
    """Structure of the MJPEG guard in kvmv_read_img: one geometry snapshot per pass, the steps that create or submit to the JPEG channel under a lease on it, and no other path from the detector to the size."""
    source = (repo / 'support/sg2002/additional/kvm/src/kvm_vision.cpp').read_text()
    assert '#include "geometry_gate.hpp"' in source
    # The geometry is owned by the gate: kvmv_cfg has no copy, so no other access exists.
    assert 'kvmv_cfg.vi_width' not in source and 'kvmv_cfg.vi_height' not in source
    cfg = source[source.index('struct kvmv_cfg_t {'):source.index('struct kvmv_data_t {')]
    assert 'uint16_t vi_width' not in cfg and 'uint16_t vi_height' not in cfg
    assert 'mjpeg_input_blocked(' not in source, 'the live-size helper is gone'
    # The detector publishes only through the gate: lt6911_get_csi_res fills locals in
    # detect_csi_res, and the three publish sites are the detector-side writers.
    assert source.count('lt6911_get_csi_res(&') == 1
    assert source.count('vi_geometry.publish(') == 3
    assert 'vi_geometry.publish(width, height)' in function_body(source, 'static uint8_t detect_csi_res()')
    # The detector never takes vi_mutex (the gate is a leaf lock; vi_mutex is held for a whole read).
    assert 'vi_mutex' not in function_body(source, 'void* vi_subsystem_detection(void *)')
    assert 'vi_mutex' not in function_body(source, 'static uint8_t detect_csi_res()')

    start = source.index('int kvmv_read_img(uint16_t _width')
    body = source[start:source.index('int kvmv_read_video(', start)]
    assert body.index('pthread_mutex_timedlock(&vi_mutex') < body.index('do {')
    # One snapshot per pass, first in the loop; every later use derives from it.
    snap = body.index('vi_geometry.snapshot()')
    assert body.count('vi_geometry.') == 1 and body.index('do {') < snap
    first = body.index('nanokvm::mjpeg_input_allowed(geometry.width, geometry.height)')
    assert snap < first < body.index('nanokvm::stream_size(geometry.width')
    for step in ['cam->set_resolution(', 'mmf_enc_jpg_deinit(0)', 'cam->close_format(', 'mmf_vi_frame_pop_native(', 'cam->read()']:
        assert first < body.index(step), 'early gate after ' + step
    # The two steps that configure and submit run under a lease on that snapshot.
    assert body.count('admit_capture_step(_type, geometry)') == 2
    resize, encode = body.index('resize_step = admit_capture_step'), body.index('encode_step = admit_capture_step')
    assert resize < body.index('cam->set_resolution(')
    for step in ['frame_to_jpeg(', 'img->to_jpeg(']:
        assert encode < body.index(step), 'encode lease after ' + step
    # The encode lease belongs to the loop body, so it outlives frame_to_jpeg/to_jpeg.
    assert 'encode_step.lease.release' not in body and 'std::move(encode_step' not in body
    # Stale: release, start again from a new snapshot, and do not count it as a failed try.
    assert body.count('geometry_stale = true;') == 2
    assert 'while (geometry_stale ? ++stale_passes <= max_stale_passes : check_kvmv(try_num++))' in body
    assert body.count('IMG_MJPEG_INPUT_BLOCKED') == 3   # the early gate and the two steps
    assert 'nanokvm::admit_jpeg_step(vi_geometry, geometry)' in function_body(source, 'static nanokvm::JpegAdmission admit_capture_step')
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
