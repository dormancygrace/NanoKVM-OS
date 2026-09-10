#!/usr/bin/env python3
"""Test actual SDIO synchronous queue/send/RX functions with deterministic events.

Hardware IO, scheduling and locking are doubles. No radio device is opened.
--reproduce-old-timeout deliberately exercises a freed response buffer on an
unfixed source; use only on the host under ASan, expecting a nonzero exit.
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
p.add_argument('--reproduce-old-timeout', action='store_true')
a = p.parse_args()
a.output.mkdir(parents=True, exist_ok=True)
tx = (a.driver / 'rwnx_msg_tx.c').read_text()
cmds = (a.driver / 'rwnx_cmds.c').read_text()
header = (a.driver / 'rwnx_cmds.h').read_text()
flags = header[header.index('#define RWNX_CMD_FLAG_NONBLOCK'):header.index('#define RWNX_CMD_MAX_QUEUED')]
functions = []
for signature in ('struct rwnx_cmd *rwnx_cmd_malloc(', 'void rwnx_cmd_free(',
                  'static void rwnx_msg_free(', 'static int rwnx_send_msg('):
    functions.append(function(tx, signature))
for signature in ('static void cmd_complete(', 'static int cmd_detach_waiter(',
                  'static int cmd_mgr_queue(', 'static int cmd_mgr_run_callback(',
                  'static int cmd_mgr_msgind(', 'static void cmd_mgr_drain('):
    if signature == 'static int cmd_detach_waiter(' and signature not in cmds:
        continue  # The baseline negative control predates this helper.
    functions.append(function(cmds, signature))
template = Path(__file__).with_suffix('.c').read_text()
source = template.replace('/* INSERT_FLAGS */', flags).replace('/* INSERT_FUNCTIONS */', '\n\n'.join(functions))
target = a.output / 'timeout-contract.c'
target.write_text(source)
command = ['cc', '-std=gnu11', '-Wall', '-Wextra', '-Werror', '-Wno-unused-parameter',
           '-fsanitize=address,undefined', '-fno-omit-frame-pointer', '-g']
if a.reproduce_old_timeout:
    command.append('-DREPRODUCE_OLD_TIMEOUT')
subprocess.run(command + [str(target), '-o', str(a.output / 'timeout-contract')], check=True)
subprocess.run([str(a.output / 'timeout-contract')], check=True)
