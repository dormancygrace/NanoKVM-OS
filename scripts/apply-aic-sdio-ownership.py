#!/usr/bin/env python3
"""Apply the reviewed NanoKVM SDIO ownership and firmware-path policy."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--source', type=Path, required=True)
a = p.parse_args()
repo = Path(__file__).resolve().parents[1]
policy = repo / 'firmware/wifi/aic8800'
pin = json.loads((policy / 'sdio-source.json').read_text())
src = a.source.resolve()
f = src / 'aic8800_bsp/aicsdio.c'
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
if sha(f) == pin['base_aicsdio_sha256']:
    with (policy / 'sdio-ownership.patch').open('rb') as patch:
        subprocess.run(['patch','-d',str(src),'-p1','--forward'],stdin=patch,check=True)
elif sha(f) not in (pin['patched_aicsdio_sha256'],
                    pin['clock_limit_files']['aic8800_bsp/aicsdio.c']['patched_sha256']):
    raise SystemExit('AIC BSP source does not match the reviewed baseline or patch')
if sha(f) not in (pin['patched_aicsdio_sha256'],
                  pin['clock_limit_files']['aic8800_bsp/aicsdio.c']['patched_sha256']):
    raise SystemExit('Patched source hash mismatch')
header = policy / 'aicbsp_sdio_ids.h'
if sha(header) != pin['header_sha256']:
    raise SystemExit('SDIO policy header hash mismatch')
shutil.copyfile(header, src / 'aic8800_bsp/aicbsp_sdio_ids.h')

firmware_files = pin['firmware_path_files']
states = []
for relative, hashes in firmware_files.items():
    digest = sha(src / relative)
    if digest == hashes['base_sha256']:
        states.append('base')
    elif digest == hashes['patched_sha256']:
        states.append('patched')
    else:
        raise SystemExit('AIC firmware-path source hash mismatch: ' + relative)
if all(state == 'base' for state in states):
    with (policy / 'firmware-request-path.patch').open('rb') as patch:
        subprocess.run(['patch', '-d', str(src), '-p1', '--forward'], stdin=patch, check=True)
elif not all(state == 'patched' for state in states):
    raise SystemExit('AIC firmware-path source is partly patched')
for relative, hashes in firmware_files.items():
    if sha(src / relative) != hashes['patched_sha256']:
        raise SystemExit('AIC firmware-path patched hash mismatch: ' + relative)

firmware_header = policy / 'aicbsp_firmware_path.h'
if sha(firmware_header) != pin['firmware_path_header_sha256']:
    raise SystemExit('AIC firmware-path policy header hash mismatch')
shutil.copyfile(firmware_header, src / 'aic8800_bsp/aicbsp_firmware_path.h')

clock_files = pin['clock_limit_files']
states = []
for relative, hashes in clock_files.items():
    digest = sha(src / relative)
    if digest == hashes['base_sha256']:
        states.append('base')
    elif digest == hashes['patched_sha256']:
        states.append('patched')
    else:
        raise SystemExit('AIC SDIO-clock source hash mismatch: ' + relative)
if all(state == 'base' for state in states):
    with (repo / 'firmware/aic8800/sdio-clock-limit-candidate.patch').open('rb') as patch:
        subprocess.run(['patch', '-d', str(src), '-p6', '--forward'], stdin=patch, check=True)
elif not all(state == 'patched' for state in states):
    raise SystemExit('AIC SDIO-clock source is partly patched')
for relative, hashes in clock_files.items():
    if sha(src / relative) != hashes['patched_sha256']:
        raise SystemExit('AIC SDIO-clock patched hash mismatch: ' + relative)

# Apply the transport-independent monitor RX fix to the SDIO driver too.
subprocess.run(['python3', str(repo / 'scripts/apply-aic-monitor-rx.py'),
                '--source', str(src)], check=True)
subprocess.run(['python3', str(repo / 'scripts/apply-aic-survey-frequency-guard.py'),
                '--source', str(src)], check=True)

print('AIC BSP ownership, firmware-path, SDIO-clock, monitor RX and survey frequency policy applied')
