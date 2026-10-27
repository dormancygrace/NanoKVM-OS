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
    # Exercise the actual integration guards without the draft-vector compiler
    # macro: vectors are encoded explicitly and enabled by runtime hwprobe.
    integration = patch.split('+++ b/crypto/modes/gcm128.c\n', 1)[1].split('diff --git ', 1)[0]
    guards = [line[1:] for line in integration.splitlines() if line.startswith('+#if ')]
    assert len(guards) == 2
    def included(guard, definitions):
        probe = guard + '\nGHASH_INTEGRATED\n#endif\n'
        result = subprocess.run(['cc', '-E', '-P', '-x', 'c', *definitions, '-'],
                                input=probe, capture_output=True, text=True, check=True)
        return 'GHASH_INTEGRATED' in result.stdout
    target = ['-D__riscv=1', '-D__riscv_xlen=64', '-D__riscv_xtheadbb=1']
    for guard in guards:
        assert included(guard, target), 'scalar C906 baseline must include runtime dispatch'
        for extra in ['-DOPENSSL_NO_ASM=1', '-DFIPS_MODULE=1']:
            assert not included(guard, target + [extra])
        assert not included(guard, [])
        if a.negative_control:
            old_guard = guard.replace('__riscv_xtheadbb', '__riscv_xtheadvector')
            assert not included(old_guard, target), 'old compiler-macro regression must be detected'
    print('PASS: C906 scalar-ISA integration guards, no-asm/FIPS exclusion')
    source=added_file(patch,'crypto/modes/gcm_c906.c')
    with tempfile.TemporaryDirectory(prefix='ghash-dispatch-') as td:
        temp=Path(td); (temp/'crypto').mkdir(); (temp/'crypto/riscv_arch.h').write_text('extern int ossl_riscv_has_xtheadvector;\n')
        (temp/'crypto/modes.h').write_text('#include <stdint.h>\ntypedef uint64_t u64; typedef uint32_t u32; typedef uint8_t u8; typedef struct { u64 hi, lo; } u128;\n')
        (temp/'gcm_c906.c').write_text(source)
        exe=temp/'test'
        cmd=['cc','-O2','-Wall','-Wextra','-Werror','-fsanitize=address,undefined','-fno-omit-frame-pointer','-I'+td,str(Path(__file__).with_suffix('.c')),'-lcrypto','-o',str(exe)]
        subprocess.run(cmd,check=True);subprocess.run([str(exe)],check=True)
        if a.negative_control:
            for name, old, new in [('thread', 'control < 0 || (control & 3) != 2', 'control < -100'),
                                   ('capability', '!ossl_riscv_has_xtheadvector', '0')]:
                assert old in source
                (temp/'gcm_c906.c').write_text(source.replace(old, new))
                subprocess.run(cmd,check=True)
                run=subprocess.run([str(exe)],capture_output=True,text=True)
                if run.returncode==0: raise SystemExit('negative control unexpectedly passed')
                if 'Assertion' not in run.stderr: raise SystemExit(run.stderr)
                print('PASS: removing the '+name+' check is detected')
if __name__=='__main__':main()
