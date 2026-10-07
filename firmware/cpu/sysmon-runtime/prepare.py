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
out.mkdir(exist_ok=False)
for p in base.iterdir():
    if p.name != 'src':
        (out / p.name).symlink_to(p, target_is_directory=p.is_dir())
(out / 'src').mkdir()
for p in (base / 'src').iterdir():
    if p.name == 'runtime':
        shutil.copytree(p, out / 'src/runtime', symlinks=False)
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
            shutil.copytree(b, o, symlinks=False)
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
# Poly1305: the riscv64 assembly of x/crypto v0.57.0 with its build tags.
poly = 'vendor/golang.org/x/crypto/internal/poly1305'
materialize(poly)
if sha256(crypto / 'sum_riscv64.s') != pin['x_crypto_v0.57.0_sum_riscv64_s_sha256']:
    parser.error('sum_riscv64.s differs from x/crypto v0.57.0')
edit(f'{poly}/mac_noasm.go', '(!amd64 && !loong64 && !ppc64le && !ppc64 && !s390x)',
     '(!amd64 && !loong64 && !ppc64le && !ppc64 && !riscv64 && !s390x)')
edit(f'{poly}/sum_asm.go', '(amd64 || loong64 || ppc64 || ppc64le)', '(amd64 || loong64 || ppc64 || ppc64le || riscv64)')
for name in ['sum_riscv64.s', 'sum_misaligned_riscv64.go']:
    shutil.copyfile(crypto / name, out / 'src' / poly / name)
# Unaligned messages (TLS ciphertext) take 64-bit loads where they are fast.
edit(f'{poly}/sum_riscv64.s', '\tAND\t$7, X6, X28\n',
     '\tAND\t$7, X6, X28\n'
     '\tMOVBU\t·useMisalignedLoads(SB), X29\n'
     '\tBEQZ\tX29, alignment_checked\n'
     '\tMOV\t$0, X28\t\t// NanoKVM: no byte loads\n'
     '\n'
     'alignment_checked:\n')
vendor_files += [f'{poly}/mac_noasm.go', f'{poly}/sum_asm.go', f'{poly}/sum_riscv64.s', f'{poly}/sum_misaligned_riscv64.go']
text = original.decode()
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
for p in source.glob('nanokvm_*'):
    shutil.copyfile(p, out / 'src/runtime' / p.name)
manifest = {'source_go': str(base), 'goroot': str(out), 'source_pin': pin,
            'runtime_sources': {p.name: hashlib.sha256(p.read_bytes()).hexdigest()
                                for p in [out / 'src/runtime/proc.go', *sorted((out / 'src/runtime').glob('nanokvm_*'))]},
            'vendor_sources': {rel: sha256(out / 'src' / rel) for rel in vendor_files}}
(out / 'nanokvm-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
assert hashlib.sha256((base / 'src/runtime/proc.go').read_bytes()).hexdigest() == pin['proc_go_sha256']
assert all(sha256(base / 'src' / rel) == digest for rel, digest in pin['vendor_sha256'].items())
print(json.dumps(manifest, indent=2))
