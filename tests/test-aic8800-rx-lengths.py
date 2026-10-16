#!/usr/bin/env python3
"""Exercise actual patched SDIO RX/scan functions under ASan/UBSan, without hardware.

Use --driver with the aic8800_fdrv directory after applying platform patches.
"""
import argparse
import subprocess
from pathlib import Path


def function(text, signature):
    start = text.index(signature)
    opening = text.index('{', start)
    level, end = 1, opening + 1
    while level:
        level += (text[end] == '{') - (text[end] == '}')
        end += 1
    return text[start:end]


p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--driver', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
p.add_argument('--prealloc', action='store_true', help='exercise the production preallocated RX path')
p.add_argument('--case', choices=['all', 'scan', 'transport', 'frames'], default='all')
a = p.parse_args()
a.output.mkdir(parents=True, exist_ok=True)
tx = (a.driver / 'aicwf_txrxif.c').read_text()
rx = (a.driver / 'rwnx_msg_rx.c').read_text()
functions = [function(tx, s) for s in (
    'static inline bool aicwf_e2a_msg_fits(',
    'static inline void aicwf_rx_frame_too_long(',
    'int aicwf_process_rxframes(')]
functions.insert(2, function(tx, 'static bool aicwf_another_ptk_1(' if a.prealloc else 'static bool aicwf_another_ptk('))
functions.append(function(rx, 'static inline int rwnx_rx_scanu_result_ind('))
structs = [function((a.driver / header).read_text(), signature) + ';'
           for header, signature in [('ipc_shared.h', 'struct ipc_e2a_msg {'),
                                     ('lmac_msg.h', 'struct scanu_result_ind {')]]
template = Path(__file__).with_suffix('.c').read_text()
source = template.replace('/* INSERT_STRUCTS */', '\n'.join(structs))
source = source.replace('/* INSERT_FUNCTIONS */', '\n\n'.join(functions))
target = a.output / 'rx-lengths.c'
target.write_text(source)
binary = a.output / 'rx-lengths'
subprocess.run(['cc'] + (['-DCONFIG_PREALLOC_RX_SKB'] if a.prealloc else []) + [ '-std=gnu11', '-Wall', '-Wextra', '-Werror',
                '-Wno-unused-parameter', '-Wno-unused-but-set-variable',
                '-fsanitize=address,undefined', '-fno-omit-frame-pointer', '-g',
                str(target), '-o', str(binary)], check=True)
subprocess.run([str(binary), a.case], check=True)
