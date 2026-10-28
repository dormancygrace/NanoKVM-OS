#!/usr/bin/env python3
"""Build the stock offline package (also used by the GitHub SSH installer).

Uses completed platform outputs; no signing keys, device access or root needed.
The installer is experimental until the stock boot chain is qualified.
"""
import argparse
import gzip
import hashlib
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile

REPO = Path(__file__).resolve().parents[1]
VERSION = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+(?:-beta\.[1-9][0-9]*)?")
BOOT_BYTES = 64 * 1024 * 1024


def digest(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def run(*args, **kwargs):
    return subprocess.run([str(a) for a in args], check=True, **kwargs)


def compress(source, target):
    with Path(source).open('rb') as src, Path(target).open('wb') as dst:
        with gzip.GzipFile(filename='', mode='wb', fileobj=dst, mtime=0, compresslevel=9) as gz:
            shutil.copyfileobj(src, gz)


def archive_root_size(path, version):
    with tarfile.open(path, 'r:gz') as tar:
        members = tar.getmembers()
        names = {m.name.removeprefix('./'): m for m in members}
        for name in ('etc/alpine-release', 'kvmapp/version', 'bin/busybox', 'etc/init.d/nanokvm-app'):
            if name not in names or not names[name].isreg():
                raise ValueError(f'root archive lacks {name}')
        with tar.extractfile(names['kvmapp/version']) as stream:
            if stream.read(128).decode().strip().removeprefix('v') != version:
                raise ValueError('root archive belongs to a different application version')
        for member in members:
            if member.name.startswith('/') or '..' in Path(member.name).parts:
                raise ValueError('unsafe root archive member')
        # Per-file block rounding plus filesystem metadata/headroom is checked
        # by install.sh against the planned root size before activation.
        return sum((m.size + 4095) // 4096 * 4 for m in members if m.isreg())


def package_tree(directory, archive):
    def normalize(info):
        info.uid = info.gid = 0
        info.uname = info.gname = 'root'
        info.mtime = 0
        info.mode = 0o755 if info.isdir() or info.mode & 0o111 else 0o644
        return info
    with archive.open('wb') as dst:
        with gzip.GzipFile(filename='', mode='wb', fileobj=dst, mtime=0, compresslevel=9) as gz:
            with tarfile.open(fileobj=gz, mode='w', format=tarfile.USTAR_FORMAT) as tar:
                tar.add(directory, arcname=directory.name, filter=normalize)


def build(build_root, output, version):
    version = version.removeprefix('v')
    if not VERSION.fullmatch(version):
        raise ValueError('expected X.Y.Z or X.Y.Z-beta.N')
    source = REPO / 'firmware/stock-install'
    images = build_root / 'images'
    host = build_root / 'buildroot-output/host/bin'
    target = build_root / 'buildroot-output/target'
    rootfs = build_root / 'release/alpine-rootfs.tar.gz'
    required = [rootfs, images / 'Image', images / 'dtb/detect.dtb',
                images / 'boot-fit/detect.sd', images / 'fip.bin',
                build_root / 'kernel/build/usr/gen_init_cpio',
                host / 'riscv64-buildroot-linux-musl-gcc', host / 'mkimage',
                target / 'bin/busybox', target / 'usr/sbin/mkfs.f2fs']
    for file in required:
        if not file.is_file():
            raise ValueError(f'missing platform output: {file}')
    config = (build_root / 'kernel/build/.config').read_text()
    for name in ('RD_LZ4', 'BLK_DEV_LOOP', 'F2FS_FS', 'EXT4_FS', 'EXFAT_FS', 'FAT_FS'):
        if f'CONFIG_{name}=y\n' not in config:
            raise ValueError(f'migration kernel requires CONFIG_{name}=y')
    lz4 = host / 'lz4'
    if not lz4.exists():
        lz4 = shutil.which('lz4')
    if not lz4:
        raise ValueError('lz4 host tool missing')
    root_kib = archive_root_size(rootfs, version)
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.stock-build-', dir=output) as temporary:
        work = Path(temporary)
        stock_version = version.split('-', 1)[0]
        package = work / f'nanokvm_{stock_version}'
        payload = package / 'migration'
        payload.mkdir(parents=True)
        (package / 'version').write_text(stock_version + '\n')
        (payload / 'target-version').write_text(version + '\n')
        for name in ('install.sh', 'offline-run.sh'):
            shutil.copy2(source / name, payload / name)
        entry = package / 'system/init.d/S95nanokvm'
        entry.parent.mkdir(parents=True)
        shutil.copy2(source / 'S95nanokvm', entry)
        for name in ('server/NanoKVM-Server', 'kvm_system/kvm_system'):
            stub = package / name
            stub.parent.mkdir(parents=True)
            stub.write_text('#!/bin/sh\necho "NanoKVM OS migration package; use S95nanokvm" >&2\nexit 1\n')
            stub.chmod(0o755)
        run(host / 'riscv64-buildroot-linux-musl-gcc', '-Os', '-static', '-s',
            '-Wall', '-Wextra', '-Werror', '-march=rv64imafdc', '-mabi=lp64d',
            source / 'layout.c', '-o', payload / 'stock-layout')
        # A fresh 64 MiB boot filesystem is written inside old p2. Its old FAT
        # filesystem stays bootable until the final partition-table commit.
        boot_img = work / 'boot.img'
        with boot_img.open('wb') as stream:
            stream.truncate(BOOT_BYTES)
        run('mkfs.fat', '--invariant', '-F', '16', '-n', 'NKOSBOOT', boot_img, stdout=subprocess.DEVNULL)
        boot_files = work / 'boot-files'
        boot_files.mkdir()
        for src, name in [(images / 'boot-fit/detect.sd', 'boot.sd'), (images / 'fip.bin', 'fip.bin')]:
            shutil.copyfile(src, boot_files / name)
        (boot_files / 'ver').write_text(f'NanoKVM OS v{version}\n')
        for file in sorted(boot_files.iterdir()):
            os.utime(file, (315532800, 315532800))
            run('mcopy', '-m', '-i', boot_img, file, '::' + file.name)
        boot_raw_hash = digest(boot_img)
        compress(boot_img, payload / 'boot.img.gz')
        shutil.copyfile(rootfs, payload / 'alpine-rootfs.tar.gz')
        (payload / 'rootfs-kib').write_text(str(root_kib) + '\n')
        init = (source / 'init').read_text()
        for key, value in {'ROOT_HASH': digest(rootfs), 'BOOT_HASH': digest(payload / 'boot.img.gz'),
                           'BOOT_RAW_HASH': boot_raw_hash}.items():
            init = init.replace('@' + key + '@', value)
        if re.search(r'@[A-Z_]+@', init):
            raise ValueError('unexpanded recovery placeholder')
        (work / 'init').write_text(init)
        ram = work / 'ram'
        ram.mkdir()
        lines = ['dir /bin 755 0 0', 'dir /sbin 755 0 0', 'dir /lib 755 0 0',
                 'dir /dev 755 0 0', 'nod /dev/console 600 0 0 c 5 1', 'nod /dev/null 666 0 0 c 1 3',
                 f'file /init {work / "init"} 755 0 0',
                 f'file /sbin/stock-layout {payload / "stock-layout"} 755 0 0']
        copied = set()
        def copy_elf(src, destination):
            if destination in copied:
                return
            copied.add(destination)
            file = ram / Path(destination).name
            shutil.copyfile(src, file)
            run(host / 'riscv64-buildroot-linux-musl-strip', '--strip-unneeded', file)
            lines.append(f'file {destination} {file} 755 0 0')
            deps = subprocess.check_output([str(host / 'riscv64-buildroot-linux-musl-readelf'), '-d', str(file)], text=True)
            for name in re.findall(r'\(NEEDED\).*?\[(.*?)\]', deps):
                library = next((d / name for d in (target / 'lib', target / 'usr/lib') if (d / name).is_file()), None)
                if library is None:
                    raise ValueError(f'missing library {name}')
                copy_elf(library, '/lib/' + name)
        copy_elf(target / 'bin/busybox', '/busybox')
        copy_elf(target / 'usr/sbin/mkfs.f2fs', '/sbin/mkfs.f2fs')
        lines.append('slink /lib/ld-musl-riscv64.so.1 libc.so 777 0 0')
        for applet in ('awk cat chmod cmp cp echo grep gzip losetup mkdir mount mv reboot rm sha256sum sh sleep sync tar touch umount wc') .split():
            lines.append(f'slink /bin/{applet} ../busybox 777 0 0')
        listing = work / 'initramfs.list'
        listing.write_text('\n'.join(lines) + '\n')
        cpio = work / 'initramfs.cpio'
        with cpio.open('wb') as stream:
            run(build_root / 'kernel/build/usr/gen_init_cpio', '-t', '0', listing, stdout=stream)
        run(lz4, '-q', '-f', '-l', '-12', cpio, work / 'initramfs.cpio.lz4')
        # Stock U-Boot must load the first FIT; gzip avoids relying on its LZ4
        # or Zstd configuration. Linux itself unpacks the LZ4 initramfs.
        compress(images / 'Image', work / 'Image.gz')
        shutil.copyfile(images / 'dtb/detect.dtb', work / 'board.dtb')
        its = (REPO / 'platform/boot/boot.its').read_text().replace('Image.lz4', 'Image.gz').replace('compression = "lz4"', 'compression = "gzip"')
        (work / 'boot.its').write_text(its)
        run(host / 'mkimage', '-f', 'boot.its', 'recovery.sd', cwd=work,
            env={**os.environ, 'SOURCE_DATE_EPOCH': '0'}, stdout=subprocess.DEVNULL)
        shutil.move(work / 'recovery.sd', payload / 'recovery.sd')
        if (payload / 'recovery.sd').stat().st_size >= 16 * 1024 * 1024:
            raise ValueError('recovery FIT does not fit a stock 16 MiB boot partition')
        (payload / 'SHA256SUMS').write_text(''.join(f'{digest(f)}  {f.name}\n' for f in sorted(payload.iterdir())))
        archive = output / (package.name + '.tar.gz')
        partial = archive.with_suffix(archive.suffix + '.partial')
        package_tree(package, partial)
        if partial.stat().st_size > 1024**3:
            raise ValueError('offline package exceeds Sipeed upload limit')
        partial.replace(archive)
        (output / 'SHA256SUMS').write_text(f'{digest(archive)}  {archive.name}\n')
        print(f'{archive}\nrecovery FIT: {(payload / "recovery.sd").stat().st_size} bytes; root: {root_kib} KiB')
    return archive


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--build-root', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--version', required=True)
    args = parser.parse_args()
    build(args.build_root.resolve(), args.output.resolve(), args.version)


if __name__ == '__main__':
    main()
