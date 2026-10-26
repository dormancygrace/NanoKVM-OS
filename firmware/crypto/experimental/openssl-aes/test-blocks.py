#!/usr/bin/env python3
"""Exercise CBC/CTR/ECB state continuation, with a software-backed fake DMA."""
import importlib.util
from pathlib import Path
import subprocess
import tempfile
ROOT=Path(__file__).resolve().parents[4]
spec=importlib.util.spec_from_file_location('transport',ROOT/'firmware/crypto/experimental/openssl-sg2002/test-transport.py')
module=importlib.util.module_from_spec(spec);spec.loader.exec_module(module)
files={}
for name in ('0002-sg2002-transport.patch','0005-aes-dma.patch'):
    files.update(module.added_files((ROOT/'firmware/crypto/openssl-c906'/name).read_text()))
with tempfile.TemporaryDirectory(prefix='nkos-aes-test-') as tmp:
    d=Path(tmp)
    for name,text in files.items():
        p=d/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(text)
    binary=d/'test'
    subprocess.run(['cc','-O1','-g','-Wall','-Wextra','-Werror',
                    '-fsanitize=address,undefined','-I'+str(d/'crypto'),'-I'+str(d/'include'),
                    str(Path(__file__).with_suffix('.c')),'-lcrypto','-o',str(binary)],check=True)
    subprocess.run([str(binary)],check=True)
    source=d/'crypto/sg2002_cipher.c'
    original=source.read_text()
    broken=original.replace('if (mode == SG2002_MODE_CTR) {',
                            'if (mode == SG2002_MODE_CTR && counter_bytes == 4) {')
    assert broken != original
    source.write_text(broken)
    subprocess.run(['cc','-O1','-g','-Wall','-Wextra','-Werror',
                    '-fsanitize=address,undefined','-I'+str(d/'crypto'),'-I'+str(d/'include'),
                    str(Path(__file__).with_suffix('.c')),'-lcrypto','-o',str(binary)],check=True)
    bad=subprocess.run([str(binary)],capture_output=True)
    assert bad.returncode != 0 and b'low+r->length/b' in bad.stderr, bad.stderr
    print('PASS negative control: relying on a full-width hardware counter is rejected')
