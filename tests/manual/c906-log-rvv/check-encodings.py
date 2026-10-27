#!/usr/bin/env python3
"""Check every embedded RVV 0.7.1 WORD against GNU as, without executing it."""
from pathlib import Path
import argparse, re, struct, subprocess, tempfile

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--prefix',required=True,help='Binutils path/prefix ending in riscv64-...-')
p.add_argument('--source',type=Path,default=Path(__file__).resolve().parents[3]/'server/logs/sanitize_text_linux_riscv64.s')
a=p.parse_args()
instructions=[]
for line in a.source.read_text().splitlines():
 m=re.match(r'\s*WORD\s+\$0x([0-9a-fA-F]{8})\s*//\s*(th\..+)$',line)
 if m:instructions.append((int(m[1],16),m[2]))
if not instructions:raise SystemExit('No annotated RVV instruction words found')
with tempfile.TemporaryDirectory(prefix='c906-log-rvv-') as temporary:
 root=Path(temporary);asm=root/'ops.S';obj=root/'ops.o';binary=root/'ops.bin'
 asm.write_text('.text\n.option norvc\n'+'\n'.join(op for _,op in instructions)+'\n')
 subprocess.run([a.prefix+'as','-march=rv64gc_xtheadvector','-o',str(obj),str(asm)],check=True)
 subprocess.run([a.prefix+'objcopy','-O','binary','--only-section=.text',str(obj),str(binary)],check=True)
 data=binary.read_bytes()
 expected=b''.join(struct.pack('<I',word) for word,_ in instructions)
 if data!=expected:
  for offset in range(0,min(len(data),len(expected)),4):
   if data[offset:offset+4]!=expected[offset:offset+4]:
    raise SystemExit(f'Encoding mismatch for {instructions[offset//4][1]}')
  raise SystemExit(f'Instruction byte count differs: {len(data)} != {len(expected)}')
print(f'PASS: {len(instructions)} embedded words match GNU as XTheadVector encoding')
