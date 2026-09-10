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
if out.exists() or out.is_relative_to(base) or base.is_relative_to(out):
    parser.error('Output must be a new directory outside the base toolchain')
pin = json.loads((source / 'go-source-pin.json').read_text())
original = (base / 'src/runtime/proc.go').read_bytes()
if hashlib.sha256(original).hexdigest() != pin['proc_go_sha256']:
    parser.error('Runtime source checksum differs from pin')
if (base / 'VERSION').read_text().splitlines()[0] != pin['go_version']:
    parser.error('Go version differs from pin')
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
text = original.decode()
anchor = 'func sysmon() {\n'
assert text.count(anchor) == 1
text = text.replace(anchor, anchor + '\tnanokvmSysmonInit()\n')
pos = text.index('\tminit()\n', text.index('func mstart1() {')) + len('\tminit()\n')
text = text[:pos] + '\tnanokvmThreadInit()\n' + text[pos:]
(out / 'src/runtime/proc.go').write_text(text)
for p in source.glob('nanokvm_*'):
    shutil.copyfile(p, out / 'src/runtime' / p.name)
manifest = {'source_go': str(base), 'goroot': str(out), 'source_pin': pin,
            'runtime_sources': {p.name: hashlib.sha256(p.read_bytes()).hexdigest()
                                for p in [out / 'src/runtime/proc.go', *sorted((out / 'src/runtime').glob('nanokvm_*'))]}}
(out / 'nanokvm-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
assert hashlib.sha256((base / 'src/runtime/proc.go').read_bytes()).hexdigest() == pin['proc_go_sha256']
print(json.dumps(manifest, indent=2))
