#!/usr/bin/env python3
"""Prepare a manual Enhanced app delta; never deploy or invoke the app updater."""
import argparse
import gzip
import hashlib
import json
from pathlib import Path
import re
import shutil
import struct
import subprocess
import tarfile

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--repo', type=Path, default=Path(__file__).resolve().parents[1])
p.add_argument('--server', type=Path, required=True)
p.add_argument('--web', type=Path, required=True)
p.add_argument('--go', type=Path, required=True, help='Go tool for post-strip build-info validation')
p.add_argument('--strip', type=Path, required=True, help='Matching RISC-V strip tool; remove debug sections from the package copy only')
p.add_argument('--output', type=Path, required=True, help='New host output directory')
a = p.parse_args()
repo = a.repo.resolve()
out = a.output.resolve()
if out.exists():
    p.error('Use a new output directory')
with a.server.open('rb') as f:
    header = f.read(20)
if len(header) < 20 or header[:6] != b'\x7fELF\x02\x01' or struct.unpack_from('<H', header, 18)[0] != 243:
    p.error('Expected a little-endian ELF64 RISC-V server')
scripts = ['S13nanokvm-watchdog', 'S38memory', 'S94sg2002aes', 'S95nanokvm', 'S98tailscaled']
for name in scripts:
    subprocess.run(['sh', '-n', str(repo/'kvmapp/system/init.d'/name)], check=True)
html = (a.web/'index.html').read_text()
for ref in re.findall(r'(?:src|href)="(/[^"?#]+)"', html):
    if not (a.web/ref.lstrip('/')).is_file():
        p.error('Missing index asset: '+ref)
for path in a.web.rglob('*'):
    if path.is_symlink() or not (path.is_dir() or path.is_file()):
        p.error('Unexpected web file type: '+str(path))

out.mkdir(parents=True)
stage = out/'NanoKVM-Enhanced-app'
stage.mkdir()
shutil.copy2(a.server, stage/'NanoKVM-Server')
subprocess.run([str(a.strip), '--strip-debug', str(stage/'NanoKVM-Server')], check=True)
(stage/'NanoKVM-Server').chmod(0o755)
build_info = subprocess.check_output([str(a.go), 'version', '-m', str(stage/'NanoKVM-Server')], text=True, stderr=subprocess.STDOUT)
if not build_info.startswith(str(stage/'NanoKVM-Server')+': go'):
    p.error('Packaged server Go build info is unreadable: '+build_info)
shutil.copytree(a.web, stage/'web')
(stage/'init.d').mkdir()
for name in scripts:
    shutil.copy2(repo/'kvmapp/system/init.d'/name, stage/'init.d'/name)
    (stage/'init.d'/name).chmod(0o755)
shutil.copy2(repo/'scripts/check-enhanced-app-update.sh', stage/'check.sh')
(stage/'check.sh').chmod(0o755)
shutil.copy2(repo/'firmware/application/APP-DELTA.md', stage/'README.md')
commit = subprocess.check_output(['git', '-C', str(repo), 'rev-parse', 'HEAD'], text=True).strip()
report = {
    'status': 'prepared-not-installed', 'source_commit_at_build': commit,
    'source_dirty_at_packaging': bool(subprocess.check_output(['git', '-C', str(repo), 'status', '--porcelain'])),
    'format': 'manual app delta; NOT compatible with the normal offline updater',
    'server_original_bytes': a.server.stat().st_size,
    'server_packaged_bytes': (stage/'NanoKVM-Server').stat().st_size,
    'go_build_info_after_strip': build_info.splitlines()[0].split(': ', 1)[1],
    'server_transform': 'RISC-V strip --strip-debug on a copy; original retained on host',
    'included': ['NanoKVM-Server', 'web/', *['init.d/'+n for n in scripts], 'check.sh', 'README.md', 'manifest.json'],
    'native_libraries_or_system_binary_included': False,
    'kernel_modules_boot_busybox_tests_or_settings_included': False,
    'automatic_install_or_restart': False,
}
(stage/'manifest.json').write_text(json.dumps(report, indent=2)+'\n')
archive = out/'NanoKVM-Enhanced-app.tar.gz'
def normalized(info):
    info.uid = info.gid = info.mtime = 0
    info.uname = info.gname = ''
    if not (info.isfile() or info.isdir()):
        raise ValueError('Only regular files/directories permitted: '+info.name)
    return info
with archive.open('xb') as raw:
    with gzip.GzipFile(filename='', mode='wb', fileobj=raw, mtime=0, compresslevel=6) as zipped:
        with tarfile.open(fileobj=zipped, mode='w', format=tarfile.PAX_FORMAT) as tar:
            tar.add(stage, arcname=stage.name, filter=normalized)
with tarfile.open(archive) as tar:
    members = tar.getmembers()
    assert len({m.name for m in members}) == len(members)
    report['unpacked_file_bytes'] = sum(m.size for m in members if m.isfile())
report['archive_bytes'] = archive.stat().st_size
with archive.open('rb') as f:
    report['archive_sha256'] = hashlib.file_digest(f, 'sha256').hexdigest()
(out/'report.json').write_text(json.dumps(report, indent=2)+'\n')
print(json.dumps(report, indent=2))
