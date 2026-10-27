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
         '-D__CV181X__', '-DOS_IS_LINUX', '-DNANOKVM_OS', '-DSENSOR_LONTIUM_LT6911']
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
    assert source.count('lt6911_get_csi_res(snapshot.width, snapshot.height, &') == 1
    assert source.count('vi_geometry.publish(') == 3
    assert 'vi_geometry.publish(width, height)' in function_body(source, 'static uint8_t detect_csi_res(')
    # The detector never takes vi_mutex (the gate is a leaf lock; vi_mutex is held for a whole read).
    assert 'vi_mutex' not in function_body(source, 'void* vi_subsystem_detection(void *)')
    assert 'vi_mutex' not in function_body(source, 'static uint8_t detect_csi_res(')

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
    # Stale: release, start again from a new snapshot, and do not count it as a failed try
    # (the two steps, and MJPEG's own output torn down while its frame was read outside vi_mutex).
    assert body.count('geometry_stale = true;') == 3
    assert 'while (geometry_stale ? ++stale_passes <= max_stale_passes : check_kvmv(try_num++))' in body
    assert body.count('IMG_MJPEG_INPUT_BLOCKED') == 3   # the early gate and the two steps
    assert 'nanokvm::admit_jpeg_step(vi_geometry, geometry)' in function_body(source, 'static nanokvm::JpegAdmission admit_capture_step')
    # kvmv_read_mjpeg_sink must reach the gated function, not a channel directly.
    sink = function_body(source, 'int kvmv_read_mjpeg_sink(')
    assert 'kvmv_read_img(width, height, VENC_MJPEG' in sink
    print('read_img gates ok')


def check_no_gop_setting():
    """The encoder runs NormalP with IDRs on request and a periodic safety IDR (venc_tuning_t); no GOP length or mode reaches libkvm or MMF."""
    source = (repo / 'support/sg2002/additional/kvm/src/kvm_vision.cpp').read_text()
    for name in ['set_h264_gop', 'gop_mode', 'kvmvenc_gop']:
        assert name not in source, name
    assert 'gop' not in function_body(source, 'static bool video_encoder_matches(')
    mmf = (repo / 'support/sg2002/additional/kvm_mmf/src/kvm_mmf.cpp').read_text()
    assert 'gop_mode' not in mmf and 'cfg->gop' not in mmf
    print('no gop setting ok')


def check_capture_mode_wiring():
    """The 4:2:0 capture mode: one rule, applied only at a capture start, followed by video encoders and never by JPEG or CPU readers; kvmv_read_img restarts the capture for it and reads again."""
    mmf = (repo / 'support/sg2002/additional/kvm_mmf/src/kvm_mmf.cpp').read_text()
    assert mmf.count('static bool _mmf_vi_yuv420_rule(') == 1 and mmf.count('_mmf_vi_yuv420_rule(') == 2
    # The wanted mode is written by the rule's follower and by the capture start only.
    assert mmf.count('vi_yuv420_wanted = ') == 3
    assert mmf.count('vi_yuv420_running = ') == 2
    assert 'vi_yuv420_wanted = want;' in function_body(mmf, 'static void _mmf_vi_yuv420_follow(')
    init = function_body(mmf, 'static CVI_S32 _mmf_init(void)')
    assert init.index('_mmf_vi_yuv420_start(stSize);') < init.index('SAMPLE_PLAT_VI_INIT(&stViConfig)')
    assert 'vi_yuv420_running ? PIXEL_FORMAT_NV21 : PIXEL_FORMAT_UYVY' in function_body(mmf, 'int mmf_vi_init(void)')
    # Followed by video encoders: their creation and the once-a-second recheck.
    venc = function_body(mmf, 'int mmf_add_venc_channel_v2(')
    assert venc.index('_mmf_vi_yuv420_follow(cfg);') < venc.index('_mmf_vi_direct_for_venc(ch, cfg);')
    assert mmf.count('_mmf_vi_yuv420_follow(') == 3
    # JPEG and CPU readers may read the mode, never change it.
    for reader in ['int mmf_enc_jpg_push_vi_with_quality(', 'int mmf_vi_frame_pop_nv21(',
                   'static int _mmf_vi_direct_yield(', 'int mmf_vi_frame_pop_jpeg(']:
        body = function_body(mmf, reader)
        for write in ['vi_yuv420_wanted', 'vi_yuv420_running =', '_mmf_vi_yuv420_follow(',
                      '_mmf_vi_yuv420_start(', 'restart']:
            assert write not in body, (reader, write)
    vision = (repo / 'support/sg2002/additional/kvm/src/kvm_vision.cpp').read_text()
    video = function_body(vision, 'int8_t frame_to_video(')
    pending = video.index('if (mmf_vi_yuv420_pending()) {')
    assert pending < video.index('mmf_venc_push_vi(') and video.index('init_venc_video(') < pending
    assert 'restart_capture_locked();' in video[pending:] and 'return IMG_CAPTURE_RESTARTED;' in video
    read = vision[vision.index('static int kvmv_read_img('):vision.index('static int kvmv_read_video(')]
    assert 'if (ret == IMG_CAPTURE_RESTARTED) continue;' in read
    assert vision.count('mmf_vi_yuv420_pending()') == 1
    print('capture mode wiring ok')


def check_vi_buffering_startup():
    mmf = (repo / 'support/sg2002/additional/kvm_mmf/src/kvm_mmf.cpp').read_text()
    init = function_body(mmf, 'static CVI_S32 _mmf_sys_init(')
    assert init.count('nanokvm_vi_configure_buffering(') == 1
    assert 'u32BlkCnt = buffering.source_buffers;' in init
    assert init.index('nanokvm_vi_configure_buffering(') < init.index('SAMPLE_COMM_SYS_Init(')
    vi = (args.mpi / 'sample/common/sample_common_vi.c').read_text()
    start = function_body(vi, 'CVI_S32 SAMPLE_COMM_VI_StartViChn(')
    depth = start.index('nanokvm_vi_startup_depth(')
    assert start.index('SAMPLE_COMM_VI_GetChnAttrBySns(') < depth < start.index('CVI_VI_SetChnAttr(')
    assert 'return CVI_FAILURE;' in start[depth:start.index('CVI_VI_SetChnAttr(')]
    assert start.index('CVI_VI_SetChnAttr(') < start.index('CVI_VI_EnableChn(')
    print('single startup buffering snapshot wiring ok')


def check_mjpeg_rate_pair():
    """Next to video the server reads MJPEG below the rate of its VPSS output. A server rate
    above it made the JPEG reader wait for VPSS frames and halved the video stream (1440p60:
    server 30 over VPSS 27 gave H.264 22 fps, 25 under 27 gave 45)."""
    import re
    go = (repo / 'server/service/stream/mjpeg/streamer.go').read_text()
    native = (repo / 'support/sg2002/additional/kvm/src/kvm_vision.cpp').read_text()
    # One pairing now applies at every encoded width. Keep the cross-language
    # headroom check when either side's limit changes.
    server = re.findall(r'mjpegNextTo\w*VideoFPS\s*=\s*(\d+)', go)
    vpss = re.findall(r'static constexpr int mjpeg_next_to_\w*video_fps = (\d+);', native)
    assert len(server) == len(vpss) == 1, (server, vpss)
    assert 0 < int(server[0]) < int(vpss[0]), (server, vpss)
    print('mjpeg rate pair ok')


def check_mjpeg_422_survives_path_switch():
    """MJPEG keeps 4:2:2 through the VI-direct path switch. An encoder keeps the input format of
    its first frame, so its first frame on the other path fails once by design (kvm_mmf). A
    single video failure gave up 4:2:2 until a GUI retry: MJPEG then read the video's QHD 4:2:0
    output (pale, and H.265 next to it fell from 45 to 32 fps)."""
    mmf = (repo / 'support/sg2002/additional/kvm_mmf/src/kvm_mmf.cpp').read_text()
    assert 'static void _mmf_vi_direct_recheck(uint64_t now = venc_now_us())' in mmf
    vision = (repo / 'support/sg2002/additional/kvm/src/kvm_vision.cpp').read_text()
    read = vision[vision.index('static int kvmv_read_img('):vision.index('static int kvmv_read_video(')]
    give_up = read.index('disable_mjpeg_422("video encoder failed with separate outputs");')
    guard = read.rindex('if (', 0, give_up)
    assert '++mjpeg_422_video_failures >= 3' in read[guard:give_up]
    assert read.count('mjpeg_422_video_failures = 0;') == 1
    assert read.index('mjpeg_422_video_failures = 0;') > give_up, 'reset after a video frame'
    assert 'mjpeg_422_video_failures = 0;' in function_body(vision, 'int set_mjpeg_chroma(')
    print('mjpeg 4:2:2 path switch ok')


check_read_img_gates()
check_no_gop_setting()
check_capture_mode_wiring()
check_vi_buffering_startup()
check_mjpeg_rate_pair()
check_mjpeg_422_survives_path_switch()

for case in ['quality', 'colour', 'sink', 'capture', 'policy', 'gate', 'pacing', 'teardown', 'park', 'hevc-direct',
             'vi-yuv420', 'vpss-demand']:
    binary = args.output.resolve() / case
    sources = [repo / f'firmware/probes/mjpeg-{case}-contract.cpp']
    if case == 'hevc-direct':
        sources = [repo / 'firmware/probes/hevc-direct-contract.cpp']
    if case in ('vi-yuv420', 'vpss-demand'):
        sources = [repo / f'firmware/probes/{case}-contract.cpp']
    if case == 'teardown':
        sources = [repo / 'firmware/probes/venc-teardown-contract.cpp']
    if case == 'pacing':
        sources = [repo / 'support/sg2002/additional/kvm/tests/capture_pacing_test.cpp']
    if case == 'capture':
        sources = [repo / 'firmware/probes/capture-contract.cpp',
                   repo / 'support/sg2002/additional/kvm/src/kvm_capture.cpp']
    elif case == 'policy':
        sources = [repo / 'support/sg2002/additional/kvm/tests/mjpeg_policy_test.cpp']
    elif case == 'gate':
        sources = [repo / 'support/sg2002/additional/kvm/tests/geometry_gate_test.cpp']
    elif case == 'park':
        sources = [repo / 'support/sg2002/additional/kvm/tests/capture_park_test.cpp']
    sources += [repo / 'firmware/mpi/nanokvm_vi_buffering.c']
    extra = ['-Wl,--wrap=fopen'] if case in ('colour', 'hevc-direct', 'vi-yuv420') else []
    if case == 'vpss-demand':
        extra += ['-Wl,--wrap=access', '-Wl,--wrap=fopen']
    subprocess.run(flags + extra + [str(path) for path in sources] + ['-o', str(binary)], check=True)
    if case == 'hevc-direct':
        for mode in ['switch', 'stride', 'invalid']:
            subprocess.run([str(binary), mode], check=True)
        continue
    if case == 'colour':
        for mode in ['new', 'old', 'input-failure', 'restore-failure', 'attr-failure']:
            subprocess.run([str(binary), mode], check=True)
        continue
    result = subprocess.run([str(binary)], capture_output=True, text=True)
    (args.output / (case + '.txt')).write_text(
        result.stdout + result.stderr + f'exit={result.returncode}\n')
    print(result.stdout + result.stderr, end='', flush=True)
    result.check_returncode()
    if case == 'vpss-demand':
        # Every configure emits exactly one complete decision, including the
        # malformed/missing property and old-layout fallbacks.
        decisions = [line for line in result.stderr.splitlines() if '] VI buffering ' in line]
        assert len(decisions) == 23, decisions
        for expected in ['cma_bytes=150994944 cma_read=ok buffers=4 depth=1 policy=shared-cma',
                         'cma_read=failed buffers=3 depth=0 policy=unknown-cma-size',
                         'cma_read=invalid buffers=3 depth=0 policy=unknown-cma-size',
                         'cma_read=ok buffers=3 depth=0 policy=small-cma',
                         'cma_read=ok buffers=3 depth=0 policy=fixed-layout']:
            assert any(expected in line for line in decisions), expected
