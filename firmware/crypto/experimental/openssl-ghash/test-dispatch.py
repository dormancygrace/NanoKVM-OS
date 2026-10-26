#!/usr/bin/env python3
"""Exercise the real GHASH dispatcher with unavailable ISA/thread states."""
import argparse
from pathlib import Path
import subprocess
import tempfile
ROOT = Path(__file__).resolve().parents[4]
def added_file(patch, name):
    section=patch.split('+++ b/'+name+'\n',1)[1].split('\ndiff --git ',1)[0]
    return ''.join(line[1:] for line in section.splitlines(keepends=True) if line.startswith('+'))
def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--negative-control',action='store_true')
    a=parser.parse_args()
    patch=(ROOT/'firmware/crypto/openssl-c906/0010-ghash-c906.patch').read_text()
    source=added_file(patch,'crypto/modes/gcm_c906.c')
    with tempfile.TemporaryDirectory(prefix='ghash-dispatch-') as td:
        temp=Path(td); (temp/'crypto').mkdir()
        (temp/'crypto/modes.h').write_text('#include <stdint.h>\ntypedef uint64_t u64; typedef uint32_t u32; typedef uint8_t u8; typedef struct { u64 hi, lo; } u128;\n')
        (temp/'gcm_c906.c').write_text(source)
        exe=temp/'test'
        cmd=['cc','-O2','-Wall','-Wextra','-Werror','-fsanitize=address,undefined','-fno-omit-frame-pointer','-I'+td,str(Path(__file__).with_suffix('.c')),'-lcrypto','-o',str(exe)]
        subprocess.run(cmd,check=True);subprocess.run([str(exe)],check=True)
        if a.negative_control:
            changed=source.replace('if (control < 0 || (control & 3) != 2)','if (control < -100)')
            assert changed!=source
            (temp/'gcm_c906.c').write_text(changed)
            subprocess.run(cmd,check=True)
            run=subprocess.run([str(exe)],capture_output=True,text=True)
            if run.returncode==0: raise SystemExit('negative control unexpectedly passed')
            if 'Assertion' not in run.stderr: raise SystemExit(run.stderr)
            print('PASS: removing the thread permission check is detected')
if __name__=='__main__':main()
