#!/usr/bin/env python3
"""Package the adapted stock init with selected current Buildroot executables."""
from pathlib import Path
import gzip
import hashlib
import json
import os
import re
import shutil
import subprocess
import tarfile

app = Path(__file__).resolve().parents[1]
br = Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve()
kernel_out = Path(os.environ['NANOKVM_KERNEL_OUTPUT']).resolve()
out = Path(os.environ['NANOKVM_INITRAMFS_OUTPUT']).resolve()
target = br / 'target'
busybox_configs = list((br / 'build').glob('busybox-*/.config'))
if len(busybox_configs) != 1:
    raise SystemExit('Expected one configured Buildroot BusyBox output')
busybox_config = busybox_configs[0]
busybox_required_features = ['CONFIG_TAR=y', 'CONFIG_FEATURE_SEAMLESS_GZ=y']
config_lines = set(busybox_config.read_text().splitlines())
if missing := [feature for feature in busybox_required_features if feature not in config_lines]:
    raise SystemExit('Recovery BusyBox requires tar -z support: ' + ', '.join(missing))
stage = out / 'root'
stage.mkdir(parents=True, exist_ok=True)
readelf = br / 'host/bin/riscv64-buildroot-linux-musl-readelf'
objdump = br / 'host/bin/riscv64-buildroot-linux-musl-objdump'
strip = br / 'host/bin/riscv64-buildroot-linux-musl-strip'
epoch = int(os.environ.get('SOURCE_DATE_EPOCH', '0'))
def run(*args):
    return subprocess.check_output(args, text=True)
def sha(p):
    return hashlib.sha256(p.read_bytes()).hexdigest()

files = {}
sources = {}
queue = [('busybox', target / 'bin/busybox'), ('e2fsck', target / 'sbin/e2fsck')]
interpreters = set()
while queue:
    name, source = queue.pop(0)
    if name in files:
        continue
    source = source.resolve()
    if not source.is_relative_to(target):
        raise SystemExit('Dependency escapes Buildroot target: ' + str(source))
    data = source.read_bytes()
    if data[:4] != b'\x7fELF':
        raise SystemExit('Not an ELF: ' + str(source))
    header = run(str(readelf), '-h', str(source))
    if 'RISC-V' not in header:
        raise SystemExit('Wrong ELF architecture: ' + str(source))
    dest = stage / name
    dest.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, dest)
    subprocess.run([str(strip), '--strip-unneeded', str(dest)], check=True)
    dest.chmod(0o755)
    if 'fence.tso' in run(str(objdump), '-d', str(dest)):
        raise SystemExit('Unsupported fence.tso: ' + name)
    files[name] = dest
    sources[name] = {'source': str(source.relative_to(target)), 'source_sha256': hashlib.sha256(data).hexdigest()}
    for interp in re.findall(r'Requesting program interpreter: ([^\]]+)', run(str(readelf), '-l', str(source))):
        if interp != '/lib/ld-musl-riscv64.so.1':
            raise SystemExit('Unexpected loader: ' + interp)
        interpreters.add(interp.lstrip('/'))
    for needed in re.findall(r'\(NEEDED\).*?\[([^\]]+)\]', run(str(readelf), '-d', str(source))):
        if '/' in needed:
            raise SystemExit('Unexpected dependency path: ' + needed)
        candidates = [target / 'lib' / needed, target / 'usr/lib' / needed]
        dependency = next((p for p in candidates if p.is_file()), None)
        if dependency is None:
            raise SystemExit('Missing dependency: ' + needed)
        queue.append(('lib/' + needed, dependency))

init = stage / 'init'
shutil.copyfile(app / 'firmware/boot/initramfs/init', init)
init.chmod(0o755)
files['init'] = init
applets = ['sh', 'cat', 'mount', 'umount', 'mkdir', 'ln', 'ls', 'sleep', 'sync', 'grep', 'wc', 'switch_root', 'true']
symlinks = {name: 'busybox' for name in applets}
symlinks.update({name: 'libc.so' for name in interpreters})
directories = ['dev', 'proc', 'sys', 'lib', 'boot']
lines = [f'dir /{name} 755 0 0' for name in directories]
lines += ['nod /dev/console 600 0 0 c 5 1', 'nod /dev/null 666 0 0 c 1 3']
for name, file in sorted(files.items()):
    if re.search(r'\s', str(file)):
        raise SystemExit('Use an output path without whitespace for gen_init_cpio')
    lines.append(f'file /{name} {file} 755 0 0')
for name, link in sorted(symlinks.items()):
    lines.append(f'slink /{name} {link} 777 0 0')
listing = out / 'initramfs.list'
listing.write_text('\n'.join(lines) + '\n')
archive = out / 'initramfs.cpio'
subprocess.run([str(kernel_out / 'usr/gen_init_cpio'), '-t', str(epoch), '-o', str(archive), str(listing)], check=True)
compressed = gzip.compress(archive.read_bytes(), mtime=epoch)
(out / 'initramfs.cpio.gz').write_bytes(compressed)

# Passive chroot smoke-test bundle; no special nodes or mounts, never runs /init.
with tarfile.open(out / 'runtime.tar', 'w') as tar:
    for name in directories:
        info = tarfile.TarInfo(name)
        info.type = tarfile.DIRTYPE
        info.mode = 0o755
        info.mtime = epoch
        tar.addfile(info)
    for name, file in sorted(files.items()):
        info = tarfile.TarInfo(name)
        info.size = file.stat().st_size
        info.mode = 0o755
        info.mtime = epoch
        with file.open('rb') as stream:
            tar.addfile(info, stream)
    for name, link in sorted(symlinks.items()):
        info = tarfile.TarInfo(name)
        info.type = tarfile.SYMTYPE
        info.linkname = link
        info.mode = 0o777
        info.mtime = epoch
        tar.addfile(info)
manifest = {
    'status': 'packaged-not-boot-qualified', 'epoch': epoch,
    'busybox_required_features': busybox_required_features,
    'busybox_config_sha256': sha(busybox_config),
    'init_sha256': sha(init), 'cpio_sha256': sha(archive),
    'gzip_sha256': sha(out / 'initramfs.cpio.gz'), 'gzip_bytes': len(compressed),
    'runtime_tar_sha256': sha(out / 'runtime.tar'),
    'files': {name: dict(bytes=file.stat().st_size, sha256=sha(file), **sources.get(name, {})) for name, file in sorted(files.items())},
    'symlinks': symlinks,
}
(out / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
print(json.dumps(manifest, indent=2))
