#!/usr/bin/env python3
"""Assemble explicit, matched Enhanced inputs into a host file. Never flashes."""
import argparse
import hashlib
import json
import os
import re
from pathlib import Path
import shutil
import stat
import struct
import subprocess


def run(*args):
    result = subprocess.run([str(x) for x in args], capture_output=True, text=True, env=dict(os.environ, TZ="UTC"))
    if result.returncode:
        raise RuntimeError(result.stdout + result.stderr)
    return result.stdout + result.stderr


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def main():
    p = argparse.ArgumentParser(description=__doc__)
    for name in ['layout-image', 'fit', 'fip', 'rootfs', 'kernel-output', 'board-stage', 'dumpimage', 'output']:
        p.add_argument('--' + name, type=Path, required=True)
    p.add_argument('--fit-sha256', required=True, help='Explicitly selected qualified FIT identity')
    p.add_argument('--fip-sha256', required=True, help='Explicitly selected qualified FIP identity')
    p.add_argument('--epoch', type=int, default=0, help='Fixed source epoch for FAT file timestamps')
    p.add_argument('--version', help='Explicit beta version; enables beta content checks')
    p.add_argument('--enable-ncm', action='store_true', help='Include the optional USB NCM boot marker')
    a = p.parse_args()
    if a.version and not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?', a.version):
        p.error('Invalid release version')
    for name in ['layout_image', 'fit', 'fip', 'rootfs', 'dumpimage']:
        path = getattr(a, name).resolve()
        if not stat.S_ISREG(path.stat().st_mode):
            p.error('Input must be a regular host file: ' + str(path))
        setattr(a, name, path)
    assert digest(a.fit) == a.fit_sha256, 'Wrong selected FIT'
    assert digest(a.fip) == a.fip_sha256, 'Wrong selected FIP'
    with a.layout_image.open('rb') as stream:
        mbr = bytearray(stream.read(512))
    assert len(mbr) == 512 and mbr[510:] == b'\x55\xaa'
    boot_start, boot_sectors = struct.unpack_from('<II', mbr, 454)
    root_start, _ = struct.unpack_from('<II', mbr, 470)
    assert (boot_start, boot_sectors, root_start) == (1, 32768, 32769)
    assert mbr[450] == 0x0c and mbr[466] == 0x83
    root_bytes = a.rootfs.stat().st_size
    assert root_bytes % 512 == 0 and 0 < root_bytes // 512 < 2**32 - root_start
    struct.pack_into('<I', mbr, 474, root_bytes // 512)
    mbr[478:510] = bytes(32)  # No data partition beyond the portable image.
    out = a.output.resolve()
    out.mkdir(parents=True, exist_ok=False)
    # Extract actual FIT data, not an adjacent file with a similar name.
    kernel_zst = out / 'kernel.zst'
    run(a.dumpimage, '-T', 'flat_dt', '-p', '0', '-o', kernel_zst, a.fit)
    image = subprocess.check_output(['zstd', '-dc', str(kernel_zst)])
    assert image == (a.kernel_output / 'arch/riscv/boot/Image').read_bytes(), 'FIT/kernel build mismatch'
    assert b'Linux version 7.2.4-nanokvm-enhanced' in image
    # Verify actual ext4 modules against the selected board and kernel build.
    extracted = out / 'rootfs-modules'
    extracted.mkdir()
    # debugfs command language needs quoted paths; reject embedded quotes.
    assert '"' not in str(extracted) and '\n' not in str(extracted)
    run('debugfs', '-R', 'rdump /usr/lib/modules "' + str(extracted) + '"', a.rootfs)
    actual = {str(x.relative_to(extracted / 'modules')): x for x in (extracted / 'modules').rglob('*.ko')}
    expected_dir = a.board_stage / 'usr/lib/modules'
    expected = {str(x.relative_to(expected_dir)): x for x in expected_dir.rglob('*.ko')}
    assert actual and actual.keys() == expected.keys(), 'Rootfs module inventory mismatch'
    for name, source in expected.items():
        assert actual[name].read_bytes() == source.read_bytes(), name
        relative = Path(name).parts
        if relative[1] == 'kernel':
            assert source.read_bytes() == a.kernel_output.joinpath(*relative[2:]).read_bytes(), 'Kernel build mismatch: ' + name
    zram = next(x for name, x in actual.items() if name.endswith('/zram/zram.ko'))
    assert ' recompress_store' in run('readelf', '-sW', zram), 'Missing ZRAM recompression'
    if a.version:
        board_manifest = json.loads((a.board_stage/'manifest.json').read_text())
        assert board_manifest.get('beta_candidate'), 'Beta requires the complete matched board stage'
        # Read exact file bytes from the image; do not trust an adjacent rootfs tree.
        def extract_required(path):
            host = out / ('check-' + path.strip('/').replace('/', '_'))
            run('debugfs', '-R', 'dump '+path+' "'+str(host)+'"', a.rootfs)
            assert host.is_file(), 'Missing beta rootfs file: '+path
            return host.read_bytes()
        assert extract_required('/kvmapp/version').decode().strip() == a.version
        os_release = extract_required('/usr/lib/os-release').decode()
        assert 'NAME="NanoKVM OS"' in os_release
        assert 'VERSION_ID="'+a.version+'"' in os_release
        for path in ['/etc/nanokvm/image-updates-only', '/etc/kvm/update-nanokvm.py',
                     '/kvmapp/system/update-nanokvm.py', '/kvmapp/system/update-components.sh',
                     '/kvmapp/kvm_new_app', '/kvmapp/kvm_new_img']:
            probe = out / ('absent-' + path.strip('/').replace('/', '_'))
            run('debugfs', '-R', 'dump '+path+' "'+str(probe)+'"', a.rootfs)
            assert not probe.exists(), 'Obsolete updater in rootfs: '+path
        installed = json.loads(extract_required('/kvmapp/.os-update/installed.json'))
        assert installed.get('version') == a.version and isinstance(installed.get('sequence'), int) and installed['sequence'] > 0, 'Missing installed application release sequence'
        for mode in (600, 720, 1080, 1440):
            assert len(extract_required(f'/usr/share/nanokvm/edid/NanoKVM-monitor-{mode}.bin')) == 256
        for path in ['/usr/sbin/nkos-update', '/etc/init.d/S94nanokvm-update', '/usr/sbin/openvpn', '/etc/init.d/S13nanokvm-watchdog', '/etc/init.d/S94sg2002aes',
                     '/usr/share/nanokvm/edid/NanoKVM-QHD30.bin', '/usr/share/nanokvm/edid/NanoKVM-stock.bin']:
            assert extract_required(path), 'Empty beta rootfs file: '+path
        assert any(name.endswith('/extra/sg2002_aes_probe.ko') for name in actual)
        assert any(name.endswith('/ovpn/ovpn.ko') for name in actual)
    # New FAT contents avoid carrying personal boot overrides/recovery markers
    # or Windows metadata from a filesystem backup. Keep tested FAT geometry.
    boot = out / 'boot-partition.img'
    with boot.open('xb') as stream:
        stream.truncate(boot_sectors * 512)
    run('/usr/sbin/mkfs.fat', '--invariant', '-F', '16', '-S', '512', '-s', '4', '-R', '4', '-n', 'boot', boot)
    files = out / 'boot-files'
    files.mkdir()
    shutil.copyfile(a.fit, files / 'boot.sd')
    shutil.copyfile(a.fip, files / 'fip.bin')
    (files / 'uEnv.txt').write_text('showlogo=echo NanoKVM OS\n')
    (files / 'hostname.prefix').write_text('kvm')
    (files / 'ver').write_text('NanoKVM OS '+('v'+a.version.replace('-beta.', ' beta-') if a.version else 'development')+'\n')
    boot_markers = ['usb.dev', 'usb.disk0', 'wifi.sta', 'gt9xx']
    if a.enable_ncm:
        boot_markers.append('usb.ncm')
    for name in boot_markers:
        (files / name).touch()
    for source in sorted(files.iterdir()):
        os.utime(source, (max(a.epoch, 315532800), max(a.epoch, 315532800)))
        run('mcopy', '-m', '-i', boot, source, '::/' + source.name)
    (out / 'fat-check.txt').write_text(run('/usr/sbin/fsck.fat', '-n', boot))
    (out / 'rootfs-check.txt').write_text(run('/usr/sbin/e2fsck', '-fn', a.rootfs))
    roundtrip = out / 'boot-readback'
    roundtrip.mkdir()
    for source in files.iterdir():
        run('mcopy', '-i', boot, '::/' + source.name, roundtrip / source.name)
        assert source.read_bytes() == (roundtrip / source.name).read_bytes()
    image_path = out / ('NanoKVM-OS-'+(a.version or 'development')+'.img')
    with image_path.open('xb') as stream:
        stream.write(mbr)
        for source in [boot, a.rootfs]:
            with source.open('rb') as input_stream:
                shutil.copyfileobj(input_stream, stream, 4 * 1024 * 1024)
    assert image_path.stat().st_size == root_start * 512 + root_bytes
    # Verify embedded ranges by byte comparison, avoiding redundant full-image hashes.
    with image_path.open('rb') as stream:
        assert stream.read(512) == mbr
        for source in [boot, a.rootfs]:
            with source.open('rb') as original:
                while block := original.read(4 * 1024 * 1024):
                    assert stream.read(len(block)) == block
        assert not stream.read(1)
    report = {'status': 'assembled-not-boot-qualified', 'installed': False,
              'version': a.version, 'epoch': a.epoch, 'ncm_boot_enabled': a.enable_ncm,
              'image': image_path.name, 'bytes': image_path.stat().st_size,
              'inputs': {name: str(getattr(a, name)) for name in ['layout_image', 'fit', 'fip', 'rootfs', 'kernel_output', 'board_stage']},
              'fit_sha256': a.fit_sha256, 'fip_sha256': a.fip_sha256,
              'fit_kernel_matches_build': True, 'rootfs_modules_match_build_and_stage': len(actual),
              'fat_ext4_checks': 'PASS', 'boot_readback_and_embedded_partition_bytes': 'PASS',
              'boot_files': sorted(x.name for x in files.iterdir()),
              'partitions': [{'number': 1, 'start_sector': boot_start, 'sectors': boot_sectors},
                             {'number': 2, 'start_sector': root_start, 'sectors': root_bytes // 512}],
              'data_partition': 'Absent in assembled image; S01fs creates exFAT p3 on first boot when absent and usb.disk0 is enabled. Fresh-card behavior requires qualification.',
              'limitations': 'Fresh FAT layout and combined image require hardware boot/recovery qualification.'}
    (out / 'manifest.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
