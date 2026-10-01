#!/usr/bin/env python3
"""Build and run a host ASan/UBSan probe of real MaixCDK system helpers."""
import argparse
import os
from pathlib import Path
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--source', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
a = p.parse_args()
repo = Path(__file__).resolve().parents[1]
basic = a.source.resolve() / 'components/basic'
out = a.output.resolve()
out.mkdir(parents=True, exist_ok=True)
(out / 'global_config.h').write_text('#pragma once\n#define PROJECT_ID "nanokvm-maix-sys-probe"\n')
(out / 'global_build_info_version.h').write_text('#pragma once\n')
sources = [repo / 'firmware/probes/maix-system-contract.cpp']
sources += [basic / 'src' / ('maix_' + name + '.cpp') for name in ('sys', 'fs', 'err', 'log')]
executable = out / 'maix-system-contract'
subprocess.run(['g++', '-std=gnu++17', '-O1', '-g', '-DPLATFORM_MAIXCAM',
    '-Wall', '-Wextra', '-Werror=return-type', '-ffunction-sections', '-fdata-sections',
    '-fsanitize=address,undefined', '-fno-omit-frame-pointer', '-fno-pie', '-no-pie',
    '-I' + str(out), '-I' + str(basic / 'include'), *map(str, sources),
    '-Wl,--gc-sections', '-Wl,--wrap=fopen,--wrap=fclose', '-o', str(executable)], check=True)
subprocess.run([str(executable)], env=dict(os.environ,
    ASAN_OPTIONS='detect_leaks=1:halt_on_error=1', UBSAN_OPTIONS='halt_on_error=1'), check=True)
