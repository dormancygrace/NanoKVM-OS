#!/usr/bin/env python3
"""Verify exact SHA chaining-state continuation after partial DMA completion."""
import importlib.util
from pathlib import Path
import subprocess
import tempfile

ROOT=Path(__file__).resolve().parents[4]
spec=importlib.util.spec_from_file_location('transport_test',ROOT/'firmware/crypto/experimental/openssl-sg2002/test-transport.py')
module=importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
files={}
for name in ('0002-sg2002-transport.patch','0003-sha256-dma.patch'):
    files.update(module.added_files((ROOT/'firmware/crypto/openssl-c906'/name).read_text()))
with tempfile.TemporaryDirectory(prefix='nkos-sha256-test-') as tmp:
    d=Path(tmp)
    for name,text in files.items():
        p=d/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(text)
    binary=d/'test'
    subprocess.run(['cc','-O1','-g','-Wall','-Wextra','-Werror','-fsanitize=address,undefined',
                    '-I'+str(d/'crypto/sha'),'-I'+str(d/'include'),
                    str(Path(__file__).with_suffix('.c')),'-lcrypto','-o',str(binary)],check=True)
    subprocess.run([str(binary)],check=True)
