#!/usr/bin/env python3
"""The SD image builder composes boot.sd from the kernel package's FIT template.

Host fixture only: a miniature boot set in the compact layout, stub f2fs tools
and the host's mtools/dosfstools. Checks the FAT boot partition's boot.sd."""
import hashlib, shutil, subprocess, tarfile, tempfile, unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
BUILDER = ROOT/'scripts/build-alpine-sd-image.py'
COMPOSE = ROOT/'platform/boot/compose-fit'
DTB = b'detect-1'
TEMPLATE = b'HEAD' + bytes(8) + bytes(32) + b'TAIL'
COMPOSED = b'HEAD' + DTB + hashlib.sha256(DTB).digest() + b'TAIL'


@unittest.skipUnless(shutil.which('mkfs.fat') or Path('/usr/sbin/mkfs.fat').exists(), 'dosfstools missing')
@unittest.skipUnless(shutil.which('mcopy'), 'mtools missing')
class SdImageBootTests(unittest.TestCase):
    def test_boot_partition_gets_the_composed_detect_image(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp = Path(tmp)
            root = tmp/'root'
            boot = root/'usr/lib/nanokvm/boot'
            boot.mkdir(parents=True)
            (boot/'kernel.release').write_text('7.2.9-test\n')
            (boot/'fit-template.sd').write_bytes(TEMPLATE)
            (boot/'fit.layout').write_text('DTB_OFFSET=4\nDTB_SIZE=8\nHASH_OFFSET=12\n')
            (boot/'detect.dtb').write_bytes(DTB)
            (boot/'detect.fdt-sha256').write_bytes(hashlib.sha256(DTB).digest())
            (boot/'detect.sha256').write_text(hashlib.sha256(COMPOSED).hexdigest() + '  detect.sd\n')
            shutil.copyfile(COMPOSE, boot/'compose-fit')
            (root/'lib/modules/7.2.9-test').mkdir(parents=True)
            (root/'etc/kvm').mkdir(parents=True)
            (root/'etc/kvm/ssh_stop').write_text('')
            (root/'etc/fstab').write_text('/dev/mmcblk0p2 / f2fs rw 0 1\n')
            archive = tmp/'rootfs.tar.gz'
            with tarfile.open(archive, 'w:gz') as tar:
                tar.add(root, arcname='.')
            f2fs = tmp/'f2fs'
            f2fs.mkdir()
            for name in ('mkfs.f2fs', 'sload.f2fs', 'fsck.f2fs'):
                stub = f2fs/name
                stub.write_text('#!/bin/sh\nfor a; do last=$a; done\n[ -e "$last" ] || : > "$last"\n')
                stub.chmod(0o755)
            fip = tmp/'fip.bin'
            fip.write_bytes(b'fip')
            out = tmp/'out'
            result = subprocess.run(['python3', str(BUILDER), '--rootfs-archive', str(archive), '--fip', str(fip),
                            '--f2fs-tools', str(f2fs), '--output', str(out)], capture_output=True, text=True)
            # Later disk assembly steps may need more host tools; the boot set is assembled first.
            self.assertTrue((out/'boot-files/boot.sd').exists(), result.stderr[-1500:])
            self.assertEqual((out/'boot-files/boot.sd').read_bytes(), COMPOSED)
            self.assertIn('compose-fit', (out/'assembly.log').read_text())


if __name__ == '__main__':
    unittest.main()
