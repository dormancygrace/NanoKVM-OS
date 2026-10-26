#!/usr/bin/env python3
"""Check SM3 committed-prefix fallback using an independent compression core."""
import importlib.util
from pathlib import Path
import subprocess
import tempfile
ROOT=Path(__file__).resolve().parents[4]
spec=importlib.util.spec_from_file_location('transport',ROOT/'firmware/crypto/experimental/openssl-sg2002/test-transport.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
files={}
for name in ('0002-sg2002-transport.patch','0009-sm3-dma.patch'):
    files.update(m.added_files((ROOT/'firmware/crypto/openssl-c906'/name).read_text()))
with tempfile.TemporaryDirectory(prefix='nkos-sm3-test-') as tmp:
    d=Path(tmp)
    for name,text in files.items():
        p=d/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(text)
    m.apply_transport_updates(d)
    p=d/'include/internal/sm3.h';p.parent.mkdir(parents=True,exist_ok=True)
    p.write_text('#ifndef TEST_SM3_H\n#define TEST_SM3_H\n#include <stdint.h>\n#define SM3_CBLOCK 64\ntypedef struct { uint32_t A,B,C,D,E,F,G,H,Nl,Nh,data[16],num; } SM3_CTX;\n#endif\n')
    binary=d/'test'
    subprocess.run(['cc','-O1','-g','-Wall','-Wextra','-Werror','-fsanitize=address,undefined',
                    '-I'+str(d/'crypto/sm3'),'-I'+str(d/'include'),
                    str(Path(__file__).with_suffix('.c')),'-lcrypto','-o',str(binary)],check=True)
    subprocess.run([str(binary)],check=True)
