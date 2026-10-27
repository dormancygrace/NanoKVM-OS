#!/usr/bin/env python3
"""Verify exact SHA chaining-state continuation after partial DMA completion."""
import argparse
import importlib.util
from pathlib import Path
import subprocess
import tempfile

ROOT=Path(__file__).resolve().parents[4]
spec=importlib.util.spec_from_file_location('transport_test',ROOT/'firmware/crypto/experimental/openssl-sg2002/test-transport.py')
module=importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument("--negative-control", action="store_true")
args=parser.parse_args()
files={}
for name in ('0002-sg2002-transport.patch','0003-sha256-dma.patch'):
    files.update(module.added_files((ROOT/'firmware/crypto/openssl-c906'/name).read_text()))
with tempfile.TemporaryDirectory(prefix='nkos-sha256-test-') as tmp:
    d=Path(tmp)
    for name,text in files.items():
        p=d/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(text)
    binary=d/'test'
    command=['cc','-O1','-g','-Wall','-Wextra','-Werror','-fsanitize=address,undefined',
             '-I'+str(d/'crypto/sha'),'-I'+str(d/'include'),
             str(Path(__file__).with_suffix('.c')),'-lcrypto','-o',str(binary)]
    subprocess.run(command,check=True)
    subprocess.run([str(binary)],check=True)
    if args.negative_control:
        source=d/'crypto/sha/sha256_sg2002.c'
        original=source.read_text()
        for name,old,new in (
            ('request cap','blocks - completed < DMA_BLOCKS ? blocks - completed : DMA_BLOCKS','blocks - completed'),
            ('tail completion','completed += n;','completed += n - 1;'),
        ):
            assert original.count(old)==1
            source.write_text(original.replace(old,new))
            subprocess.run(command,check=True)
            result=subprocess.run([str(binary)],capture_output=True,text=True)
            assert result.returncode!=0 and 'Assertion' in result.stderr, name+' was not detected'
            print('PASS negative control: '+name)
