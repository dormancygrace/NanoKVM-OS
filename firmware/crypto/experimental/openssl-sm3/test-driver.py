#!/usr/bin/env python3
"""Check SM3 descriptor and completion handling extracted from the real driver."""
from pathlib import Path
import subprocess
import re
import tempfile
ROOT=Path(__file__).resolve().parents[4]
source=(ROOT/'platform/modules/sg2002-aes/crypto_algorithms.inc').read_text()
names=('crypto_is_hash','crypto_b64_char','crypto_validate','crypto_prepare_descriptor','crypto_finish_locked','crypto_clean_locked')
functions=[]
for name in names:
    match=re.search(r'(?m)^static (?:int|bool|void) '+name+r'\(',source)
    assert match is not None, name
    start=match.start();body=source.index('{',match.end());depth=1;end=body+1
    while depth:
        depth+=(source[end]=='{')-(source[end]=='}');end+=1
    functions.append(source[start:end])
with tempfile.TemporaryDirectory(prefix='nkos-sm3-driver-') as tmp:
    p=Path(tmp);(p/'driver.inc').write_text('\n'.join(functions))
    subprocess.run(['cc','-O1','-g','-Wall','-Wextra','-Werror','-fsanitize=address,undefined',
                    '-I'+tmp,'-I'+str(ROOT/'platform/modules/sg2002-aes'),
                    str(Path(__file__).with_suffix('.c')),'-o',str(p/'test')],check=True)
    subprocess.run([str(p/'test')],check=True)
