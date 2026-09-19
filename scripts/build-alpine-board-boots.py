#!/usr/bin/env python3
"""Build all NanoKVM board FITs from one matched kernel and F2FS initramfs."""
import argparse, hashlib, json, os, shutil, subprocess
from pathlib import Path

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--base-fit', type=Path, required=True)
p.add_argument('--board-inputs', type=Path, required=True)
p.add_argument('--kernel-release', required=True)
p.add_argument('--tools', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
a = p.parse_args()
a.output.mkdir(parents=True, exist_ok=False)
def run(*args, **kw):
    subprocess.run([str(x) for x in args], check=True, env=dict(os.environ, SOURCE_DATE_EPOCH='0'), **kw)
def sha(path):
    return hashlib.file_digest(path.open('rb'), 'sha256').hexdigest()
run(a.tools/'dumpimage', '-T', 'flat_dt', '-p', '0', '-o', a.output/'Image.zst', a.base_fit)
run(a.tools/'dumpimage', '-T', 'flat_dt', '-p', '1', '-o', a.output/'initramfs.cpio.gz', a.base_fit)
image = subprocess.check_output(['zstd', '-dc', str(a.output/'Image.zst')])
if b'Linux version ' + a.kernel_release.encode() + b' ' not in image:
    raise SystemExit('Kernel release does not match the selected FIT')
(a.output/'kernel.release').write_text(a.kernel_release+'\n')
manifest = {'kernel': a.kernel_release, 'source_fit_sha256': sha(a.base_fit), 'profiles': {}}
for profile in ('detect', 'alpha', 'beta', 'pcie', 'lite'):
    work = a.output/('build-'+profile)
    work.mkdir()
    shutil.copy2(a.output/'Image.zst', work/'Image.zst')
    shutil.copy2(a.output/'initramfs.cpio.gz', work/'initramfs.cpio.gz')
    shutil.copy2(a.board_inputs/profile/'board.dtb', work/'board.dtb')
    shutil.copy2(a.board_inputs/profile/'boot.its', work/'boot.its')
    run(a.tools/'mkimage', '-f', 'boot.its', 'boot.sd', cwd=work)
    target = a.output/(profile+'.sd')
    shutil.copy2(work/'boot.sd', target)
    (a.output/(profile+'.sha256')).write_text(sha(target)+'  '+target.name+'\n')
    manifest['profiles'][profile] = {'sha256': sha(target), 'bytes': target.stat().st_size}
    shutil.rmtree(work)
(a.output/'manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
