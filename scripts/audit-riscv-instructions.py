#!/usr/bin/env python3
"""Audit actual RISC-V ELF instructions, independently of compiler ISA flags.

Reads executable sections with objdump -d; data bytes/ELF attributes alone are
not instruction evidence. No program is executed. Archives must be audited
through their linked executable/library or extracted objects.
"""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path
import re
import struct
import subprocess


def audit(path, cross):
    attributes = subprocess.run([cross+'readelf', '-A', str(path)], capture_output=True, text=True, check=True)
    attrs = attributes.stdout
    arch = re.search(r'Tag_RISCV_arch: "([^"]+)"', attrs)
    counts, examples = Counter(), {}
    symbol = section = None
    pattern = re.compile(r'^\s*([0-9a-f]+):\s+([0-9a-f]{4,16})\s+([a-zA-Z][a-zA-Z0-9_.]*)\s*(.*)$')
    proc = subprocess.Popen([cross+'objdump', '-d', str(path)], stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, text=True)
    for line in proc.stdout:
        if line.startswith('Disassembly of section '):
            section = line.strip().removeprefix('Disassembly of section ').removesuffix(':')
        match = re.match(r'^[0-9a-f]+ <(.*)>:', line)
        if match:
            symbol = match[1]
        match = pattern.match(line)
        if not match:
            continue
        address, encoding, mnemonic, operands = match.groups()
        counts[mnemonic] += 1
        if mnemonic.startswith('th.') and len(examples.setdefault(mnemonic, [])) < 3:
            examples[mnemonic].append(dict(section=section, symbol=symbol, address=address,
                                            encoding=encoding, operands=operands))
    error = proc.stderr.read()
    if proc.wait():
        raise RuntimeError(str(path)+': '+error)
    thead = {k: v for k, v in sorted(counts.items()) if k.startswith('th.')}
    return dict(bytes=path.stat().st_size, sha256=hashlib.sha256(path.read_bytes()).hexdigest(),
                elf_arch=arch[1] if arch else None, decoded_instructions=sum(counts.values()),
                thead_instruction_sites=sum(thead.values()),
                thead_scalar_instruction_sites=sum(v for k, v in thead.items() if not k.startswith('th.v')),
                thead_vector_decode_sites=sum(v for k, v in thead.items() if k.startswith('th.v')),
                vector_decode_is_ambiguous=True,
                thead_mnemonics=thead,
                examples=examples, mnemonic_counts=dict(sorted(counts.items())),
                disassembler_warnings=error.strip(), attribute_warnings=attributes.stderr.strip())


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--cross-prefix', required=True)
    p.add_argument('--root', type=Path, action='append', required=True)
    p.add_argument('--output', type=Path, required=True)
    p.add_argument('--require-thead', action='store_true', help='Fail if no T-Head instructions were decoded anywhere')
    a = p.parse_args()
    records, seen = {}, set()
    for root in a.root:
        root = root.resolve()
        if not root.exists():
            p.error('Missing input: '+str(root))
        for path in sorted(root.rglob('*')) if root.is_dir() else [root]:
            if not path.is_file() or path.resolve() in seen:
                continue
            path = path.resolve()
            if root.is_dir() and not path.is_relative_to(root):
                continue
            seen.add(path)
            with path.open('rb') as f:
                header = f.read(20)
            if len(header) < 20 or header[:6] != b'\x7fELF\x02\x01' or struct.unpack_from('<H', header, 18)[0] != 243:
                continue
            label = root.name+'/'+str(path.relative_to(root)) if root.is_dir() else root.name
            if label in records:
                raise RuntimeError('Duplicate audit label: '+label)
            records[label] = audit(path, a.cross_prefix)
    if not records:
        p.error('No little-endian ELF64 RISC-V files found')
    sites = sum(x['thead_instruction_sites'] for x in records.values())
    report = dict(method='objdump -d executable sections; static instruction sites, not execution frequency',
                  runtime_execution_proven=False,
                  vector_warning='RVV 1.0 and XTheadVector encodings overlap. A th.v disassembly does not prove legacy-vector generation or execution; check source and runtime gates, especially in Go/OpenSSL mixed objects.', compiler_flags_do_not_prove_instruction_use=True,
                  objdump=subprocess.check_output([a.cross_prefix+'objdump', '--version'], text=True).splitlines()[0],
                  files=records, thead_instruction_sites=sites)
    a.output.parent.mkdir(parents=True, exist_ok=True)
    a.output.write_text(json.dumps(report, indent=2)+'\n')
    print(f'{len(records)} RISC-V ELF files; {sites} decoded T-Head instruction sites; report: {a.output}')
    if a.require_thead and not sites:
        raise SystemExit('No actual T-Head instructions found')


if __name__ == '__main__':
    main()
