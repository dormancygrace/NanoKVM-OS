#!/usr/bin/env python3
"""Apply the reviewed BSP ownership patch to an exact source baseline."""
import argparse
import hashlib
import json
from pathlib import Path
import shutil
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--source', type=Path, required=True)
a = p.parse_args()
policy = Path(__file__).resolve().parents[1] / 'firmware/wifi/aic8800'
pin = json.loads((policy / 'sdio-source.json').read_text())
src = a.source.resolve()
f = src / 'aic8800_bsp/aicsdio.c'
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
if sha(f) == pin['base_aicsdio_sha256']:
    with (policy / 'sdio-ownership.patch').open('rb') as patch:
        subprocess.run(['patch','-d',str(src),'-p1','--forward'],stdin=patch,check=True)
elif sha(f) != pin['patched_aicsdio_sha256']:
    raise SystemExit('AIC BSP source does not match the reviewed baseline or patch')
if sha(f) != pin['patched_aicsdio_sha256']:
    raise SystemExit('Patched source hash mismatch')
header = policy / 'aicbsp_sdio_ids.h'
if sha(header) != pin['header_sha256']:
    raise SystemExit('SDIO policy header hash mismatch')
shutil.copyfile(header, src / 'aic8800_bsp/aicbsp_sdio_ids.h')
print('AIC BSP exact SDIO ownership policy applied')
