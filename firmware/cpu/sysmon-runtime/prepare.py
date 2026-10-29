#!/usr/bin/env python3
from pathlib import Path
import argparse, hashlib, json, shutil

parser = argparse.ArgumentParser(description="Prepare an isolated, pinned NanoKVM Go runtime; never edit the base toolchain")
parser.add_argument('--base', type=Path, required=True)
parser.add_argument('--output', type=Path, required=True)
args = parser.parse_args()
base = args.base.resolve()
out = args.output.resolve()
source = Path(__file__).resolve().parent
crypto = source.parent / 'go-crypto-c906'
sha256 = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
if out.exists() or out.is_relative_to(base) or base.is_relative_to(out):
    parser.error('Output must be a new directory outside the base toolchain')
pin = json.loads((source / 'go-source-pin.json').read_text())
original = (base / 'src/runtime/proc.go').read_bytes()
if hashlib.sha256(original).hexdigest() != pin['proc_go_sha256']:
    parser.error('Runtime source checksum differs from pin')
if (base / 'VERSION').read_text().splitlines()[0] != pin['go_version']:
    parser.error('Go version differs from pin')
for rel, digest in pin['vendor_sha256'].items():
    if sha256(base / 'src' / rel) != digest:
        parser.error(f'{rel} checksum differs from pin')
def copy_sources(src, dst):
    # Downloaded Go module toolchains are read-only. Only the isolated copies
    # become writable; linked base-toolchain files retain their original mode.
    shutil.copytree(src, dst, symlinks=False)
    for p in [dst, *dst.rglob('*')]:
        if not p.is_symlink():
            p.chmod(p.stat().st_mode | 0o200)


out.mkdir(exist_ok=False)
for p in base.iterdir():
    if p.name != 'src':
        (out / p.name).symlink_to(p, target_is_directory=p.is_dir())
(out / 'src').mkdir()
for p in (base / 'src').iterdir():
    if p.name == 'runtime':
        copy_sources(p, out / 'src/runtime')
    else:
        (out / 'src' / p.name).symlink_to(p, target_is_directory=p.is_dir())


def materialize(rel):
    """Copy the package src/<rel>, splitting its symlinked parents into directories of symlinks."""
    o, b = out / 'src', base / 'src'
    parts = Path(rel).parts
    for i, part in enumerate(parts):
        o, b = o / part, b / part
        if not o.is_symlink():
            continue
        o.unlink()
        if i == len(parts) - 1:
            copy_sources(b, o)
        else:
            o.mkdir()
            for p in b.iterdir():
                (o / p.name).symlink_to(p, target_is_directory=p.is_dir())


def edit(rel, old, new):
    p = out / 'src' / rel
    t = p.read_text()
    assert t.count(old) == 1, rel
    p.write_text(t.replace(old, new))


# ChaCha20 for the std-vendored x/crypto used by crypto/tls.
chacha = 'vendor/golang.org/x/crypto/chacha20'
materialize(chacha)
edit(f'{chacha}/chacha_noasm.go', '//go:build (!arm64 && !s390x && !ppc64 && !ppc64le) || !gc || purego\n',
     '//go:build (!arm64 && !riscv64 && !s390x && !ppc64 && !ppc64le) || !gc || purego\n')
# Compute the last buffer before the end of the counter space one block at a
# time, as the generic build does.
edit(f'{chacha}/chacha_generic.go', '\tif uint64(s.counter)+blocksPerBuf > 1<<32 {\n',
     '\tif uint64(s.counter)+blocksPerBuf >= 1<<32 {\n')
vendor_files = [f'{chacha}/chacha_noasm.go', f'{chacha}/chacha_generic.go']
for name, pkg in [('chacha_riscv64.go', chacha), ('chacha_riscv64.s', chacha)]:
    shutil.copyfile(crypto / name, out / 'src' / pkg / name)
    vendor_files.append(f'{pkg}/{name}')
# Poly1305: keep the pinned upstream reference, install the C906 reduction.
poly = 'vendor/golang.org/x/crypto/internal/poly1305'
materialize(poly)
if sha256(crypto / 'sum_riscv64.s') != pin['x_crypto_v0.57.0_sum_riscv64_s_sha256']:
    parser.error('sum_riscv64.s differs from x/crypto v0.57.0')
edit(f'{poly}/mac_noasm.go', '(!amd64 && !loong64 && !ppc64le && !ppc64 && !s390x)',
     '(!amd64 && !loong64 && !ppc64le && !ppc64 && !riscv64 && !s390x)')
edit(f'{poly}/sum_asm.go', '(amd64 || loong64 || ppc64 || ppc64le)', '(amd64 || loong64 || ppc64 || ppc64le || riscv64)')
shutil.copyfile(crypto / 'sum_c906_riscv64.s', out / 'src' / poly / 'sum_riscv64.s')
shutil.copyfile(crypto / 'sum_misaligned_riscv64.go', out / 'src' / poly / 'sum_misaligned_riscv64.go')
vendor_files += [f'{poly}/mac_noasm.go', f'{poly}/sum_asm.go', f'{poly}/sum_riscv64.s', f'{poly}/sum_misaligned_riscv64.go']
# Short substring search without Rabin-Karp (go-bytes-index): where Index
# falls back to Rabin-Karp, a sep of at most 255 bytes uses Horspool.
index = source.parent / 'go-bytes-index'
for pkg, name in [('bytes', 'index_bytes.go'), ('internal/stringslite', 'index_stringslite.go')]:
    materialize(pkg)
    shutil.copyfile(index / name, out / 'src' / pkg / 'index_nanokvm.go')
    vendor_files.append(f'{pkg}/index_nanokvm.go')
edit('bytes/bytes.go', '''			// This code does not take that effect into account.
			j := bytealg.IndexRabinKarp(s[i:], sep)
''', '''			// This code does not take that effect into account.
			if n <= horspoolMaxLen { // NanoKVM: index_nanokvm.go
				if j := indexHorspool(s[i:], sep); j >= 0 {
					return i + j
				}
				return -1
			}
			j := bytealg.IndexRabinKarp(s[i:], sep)
''')
edit('internal/stringslite/strings.go', '''			// See comment in ../bytes/bytes.go.
			j := bytealg.IndexRabinKarp(s[i:], substr)
''', '''			// See comment in ../bytes/bytes.go.
			if n <= horspoolMaxLen { // NanoKVM: index_nanokvm.go
				if j := indexHorspool(s[i:], substr); j >= 0 {
					return i + j
				}
				return -1
			}
			j := bytealg.IndexRabinKarp(s[i:], substr)
''')
vendor_files += ['bytes/bytes.go', 'internal/stringslite/strings.go']
# SHA-1 and SHA-256 blocks for T-Head cores (go-crypto-c906/gen_sha_c906.py),
# chosen by cpu.RISCV64.HasXTheadBb, which riscv_hwprobe's mvendorid sets.
for pkg in ['internal/cpu', 'crypto/internal/fips140deps/cpu', 'crypto/internal/fips140/sha256', 'crypto/sha1']:
    materialize(pkg)
edit('internal/cpu/cpu.go', '\tHasZvkt           bool // Vector Data-Independent Execution Latency\n',
     '\tHasZvkt           bool // Vector Data-Independent Execution Latency\n'
     '\tHasXTheadBb       bool // T-Head XTheadBb (NanoKVM: mvendorid of a T-Head core)\n'
     '\tHasXTheadVector   bool // T-Head XTheadVector (NanoKVM: riscv_hwprobe vendor key)\n')
edit('internal/cpu/cpu_riscv64.go', '\t\t{Name: "zvkt", Feature: &RISCV64.HasZvkt},\n',
     '\t\t{Name: "zvkt", Feature: &RISCV64.HasZvkt},\n'
     '\t\t{Name: "xtheadbb", Feature: &RISCV64.HasXTheadBb},\n'
     '\t\t{Name: "xtheadvector", Feature: &RISCV64.HasXTheadVector},\n')
edit('internal/cpu/cpu_riscv64_linux.go', '\t\t{riscv_HWPROBE_KEY_CPUPERF_0, 0},\n\t}\n',
     '\t\t{riscv_HWPROBE_KEY_CPUPERF_0, 0},\n'
     '\t\t{0, 0}, // NanoKVM: RISCV_HWPROBE_KEY_MVENDORID\n'
     '\t\t{11, 0}, // NanoKVM: RISCV_HWPROBE_KEY_VENDOR_EXT_THEAD_0\n\t}\n')
edit('internal/cpu/cpu_riscv64_linux.go', '\tif pairs[1].key != -1 {\n',
     '\t// Every T-Head RISC-V core (vendor ID 0x5b7) implements XTheadBb, which\n'
     '\t// Linux reports neither in hwprobe nor in /proc/cpuinfo.\n'
     '\tif pairs[2].key != -1 {\n'
     '\t\tRISCV64.HasXTheadBb = pairs[2].value == 0x5b7\n'
     '\t}\n'
     '\tif pairs[3].key != -1 { // RISCV_HWPROBE_VENDOR_EXT_XTHEADVECTOR\n'
     '\t\tRISCV64.HasXTheadVector = pairs[3].value&1 != 0\n'
     '\t}\n'
     '\tif pairs[1].key != -1 {\n')
edit('crypto/internal/fips140deps/cpu/cpu.go', '\tRISCV64HasV = cpu.RISCV64.HasV\n',
     '\tRISCV64HasV        = cpu.RISCV64.HasV\n'
     '\tRISCV64HasXTheadBb = cpu.RISCV64.HasXTheadBb\n')
sha = 'crypto/internal/fips140/sha256'
edit(f'{sha}/sha256block_asm.go', '//go:build (386 || loong64 || riscv64) && !purego\n',
     '//go:build (386 || loong64) && !purego\n')
edit(f'{sha}/sha256block_riscv64.s', 'TEXT ·block(SB),0,$64-32\n', 'TEXT ·blockRV64(SB),0,$64-32\n')
for name in ['sha256block_riscv64.go', 'sha256block_c906_riscv64.s']:
    shutil.copyfile(crypto / name, out / 'src' / sha / name)
edit('crypto/sha1/sha1block_decl.go', '//go:build (386 || arm || loong64 || riscv64) && !purego\n',
     '//go:build (386 || arm || loong64) && !purego\n')
edit('crypto/sha1/sha1block_riscv64.s', 'TEXT ·block(SB),NOSPLIT,$64-32\n', 'TEXT ·blockRV64(SB),NOSPLIT,$64-32\n')
for name in ['sha1block_riscv64.go', 'sha1block_c906_riscv64.s']:
    shutil.copyfile(crypto / name, out / 'src/crypto/sha1' / name)
# Keccak-f[1600] for T-Head cores (go-crypto-c906/gen_keccak_c906.py), chosen
# like the SHA blocks by cpu.RISCV64.HasXTheadBb.
sha3 = 'crypto/internal/fips140/sha3'
materialize(sha3)
edit(f'{sha3}/sha3_noasm.go', '//go:build (!amd64 && !arm64 && !s390x) || purego\n',
     '//go:build (!amd64 && !arm64 && !riscv64 && !s390x) || purego\n')
for name in ['sha3_riscv64.go', 'keccak_c906_riscv64.s']:
    shutil.copyfile(crypto / name, out / 'src' / sha3 / name)
vendor_files += [f'{sha3}/sha3_noasm.go', f'{sha3}/sha3_riscv64.go', f'{sha3}/keccak_c906_riscv64.s']
vendor_files += ['internal/cpu/cpu.go', 'internal/cpu/cpu_riscv64.go', 'internal/cpu/cpu_riscv64_linux.go',
                 'crypto/internal/fips140deps/cpu/cpu.go', f'{sha}/sha256block_asm.go', f'{sha}/sha256block_riscv64.s',
                 f'{sha}/sha256block_riscv64.go', f'{sha}/sha256block_c906_riscv64.s',
                 'crypto/sha1/sha1block_decl.go', 'crypto/sha1/sha1block_riscv64.s',
                 'crypto/sha1/sha1block_riscv64.go', 'crypto/sha1/sha1block_c906_riscv64.s']
text = original.decode()
# memmove: misaligned 8-byte loads where riscv_hwprobe reports them fast.
zbb = '\t\triscv64HasZbb = cpu.RISCV64.HasZbb\n'
assert text.count(zbb) == 1
text = text.replace(zbb, zbb + '\t\tnanokvmFastMisaligned = cpu.RISCV64.HasFastMisaligned\n'
                    '\t\tnanokvmVectorMemmove = cpu.RISCV64.HasXTheadVector && nanokvmVectorEnabled()\n')
anchor = 'func sysmon() {\n'
assert text.count(anchor) == 1
text = text.replace(anchor, anchor + '\tnanokvmSysmonInit()\n')
# The sleep hook leaves sysmon's delay/idle backoff state untouched.
sleep = ('\t\tif delay > 10*1000 { // up to 10ms\n'
         '\t\t\tdelay = 10 * 1000\n'
         '\t\t}\n'
         '\t\tusleep(delay)\n')
assert text.count(sleep) == 1 and text.count('usleep(delay)') == 1
text = text.replace(sleep, sleep.replace('usleep(delay)', 'usleep(nanokvmSysmonDelay(delay))'))
pos = text.index('\tminit()\n', text.index('func mstart1() {')) + len('\tminit()\n')
text = text[:pos] + '\tnanokvmThreadInit()\n' + text[pos:]
(out / 'src/runtime/proc.go').write_text(text)
# A source and destination of different alignment continue in the 8-byte loops
# (misaligned loads) instead of the byte loops when the loads are fast. The
# test sits out of line, so the co-aligned path is unchanged. Below 32 bytes
# aligning the destination byte by byte costs more than it saves.
# Copies of nanokvmVectorMemmoveMin bytes or more go to the XTheadVector loop
# (gen_memmove_c906.py) when nanokvmVectorMemmove is set; the test for it
# costs smaller copies a compare and a branch.
edit('runtime/memmove_riscv64.s', '#include "textflag.h"\n', '#include "go_asm.h"\n#include "textflag.h"\n')
edit('runtime/memmove_riscv64.s', '\tBEQ\tX10, X11, done\n\tBEQZ\tX12, done\n',
     '\tBEQ\tX10, X11, done\n\tBEQZ\tX12, done\n\n'
     '\t// NanoKVM: large copies with XTheadVector (nanokvm_memmove.go).\n'
     '\tMOV\t$const_nanokvmVectorMemmoveMin, X9\n'
     '\tBLTU\tX12, X9, nanokvm_scalar\n'
     '\tMOVBU\truntime·nanokvmVectorMemmove(SB), X9\n'
     '\tBEQZ\tX9, nanokvm_scalar\n'
     '\tJMP\truntime·nanokvmMemmoveVector(SB)\n'
     'nanokvm_scalar:\n')
for d in 'fb':
    edit('runtime/memmove_riscv64.s',
         '\t// Check alignment - if alignment differs we have to do one byte at a time.\n'
         f'\tAND\t$7, X10, X5\n\tAND\t$7, X11, X6\n\tBNE\tX5, X6, {d}_loop8_unaligned_check\n',
         '\t// Check alignment - if alignment differs we have to do one byte at a time,\n'
         '\t// unless misaligned loads are fast (NanoKVM: nanokvm_memmove.go).\n'
         f'\tAND\t$7, X10, X5\n\tAND\t$7, X11, X6\n\tBNE\tX5, X6, {d}_misaligned\n'
         f'{d}_same_alignment:\n')
    edit('runtime/memmove_riscv64.s',
         f'\tJMP\t{d}_loop4_check\n\n{d}_loop8_unaligned_check:\n',
         f'\tJMP\t{d}_loop4_check\n\n'
         f'{d}_misaligned:\n'
         '\tMOV\t$32, X7\n'
         f'\tBLT\tX12, X7, {d}_loop8_unaligned_check\n'
         '\tMOVBU\truntime·nanokvmFastMisaligned(SB), X7\n'
         f'\tBNEZ\tX7, {d}_same_alignment\n'
         f'{d}_loop8_unaligned_check:\n')
for p in source.glob('nanokvm_*'):
    shutil.copyfile(p, out / 'src/runtime' / p.name)
manifest = {'source_go': str(base), 'goroot': str(out), 'source_pin': pin,
            'runtime_sources': {p.name: hashlib.sha256(p.read_bytes()).hexdigest()
                                for p in [out / 'src/runtime/proc.go', out / 'src/runtime/memmove_riscv64.s', *sorted((out / 'src/runtime').glob('nanokvm_*'))]},
            'vendor_sources': {rel: sha256(out / 'src' / rel) for rel in vendor_files}}
(out / 'nanokvm-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
assert hashlib.sha256((base / 'src/runtime/proc.go').read_bytes()).hexdigest() == pin['proc_go_sha256']
assert all(sha256(base / 'src' / rel) == digest for rel, digest in pin['vendor_sha256'].items())
print(json.dumps(manifest, indent=2))
