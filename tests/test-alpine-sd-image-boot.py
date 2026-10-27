#!/usr/bin/env python3
"""The SD image builder composes boot.sd from the kernel package's FIT template.

Host fixture only: a miniature boot set in the compact layout, stub f2fs tools
and the host's mtools/dosfstools. Checks the FAT boot partition's boot.sd."""
import hashlib, json, os, shutil, struct, subprocess, tarfile, tempfile, unittest
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
            (root/'kvmapp').mkdir()
            (root/'kvmapp/version').write_text('2.5-a1\n')
            (root/'etc/kvm').mkdir(parents=True)
            (root/'etc/kvm/ssh_stop').write_text('')
            (root/'etc/kvm/usb').mkdir()
            (root/'etc/kvm/usb/.layout-v1').write_text('')
            (root/'etc/kvm/usb/usb.dev').write_text('')
            (root/'etc/kvm/usb/usb.disk0').write_text('')
            (root/'etc/kvm/mdns_disabled').write_text('1\n')
            (root/'etc/conf.d').mkdir()
            (root/'etc/conf.d/udev-trigger').write_text(': "${rc_coldplug:=${RC_COLDPLUG:-NO}}"\n')
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
            if os.environ.get('NANOKVM_TEST_F2FS_TOOLS'):
                f2fs = Path(os.environ['NANOKVM_TEST_F2FS_TOOLS'])
            fip = tmp/'fip.bin'
            fip.write_bytes(b'fip')
            out = tmp/'out'
            result = subprocess.run(['python3', str(BUILDER), '--rootfs-archive', str(archive), '--fip', str(fip),
                            '--f2fs-tools', str(f2fs), '--output', str(out), '--version', 'v2.5-a1', '--image-version', 'v1.0-a1'], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr[-1500:])
            self.assertEqual((out/'root/etc/kvm/mdns_disabled').read_text(), '1\n')
            self.assertTrue((out/'boot-files/boot.sd').exists(), result.stderr[-1500:])
            self.assertEqual((out/'boot-files/boot.sd').read_bytes(), COMPOSED)
            self.assertIn('compose-fit', (out/'assembly.log').read_text())
            extracted = tmp/'boot-from-fat.sd'
            subprocess.run(['mcopy', '-i', str(out/'boot-partition.img'), '::/boot.sd', str(extracted)], check=True)
            self.assertEqual(extracted.read_bytes(), COMPOSED)
            metadata = tmp/'image.json'
            subprocess.run(['mcopy', '-i', str(out/'boot-partition.img'), '::/image.json', str(metadata)], check=True)
            expected = {'image_version':'v1.0-a1', 'bundled_application_version':'v2.5-a1'}
            self.assertEqual(json.loads(metadata.read_text()), expected)
            manifest = json.loads((out/'build-manifest.json').read_text())
            for key, value in expected.items():
                self.assertEqual(manifest[key], value)
            self.assertTrue((out/'NanoKVM-OS-Image-v1.0-a1-apps-v2.5-a1.img.zip').is_file())
            image = out/'NanoKVM-OS-Image-v1.0-a1-apps-v2.5-a1.img'
            bootimage = out/'boot-partition.img'
            with image.open('rb') as disk:
                mbr = disk.read(512)
                self.assertEqual(mbr[510:], b'\x55\xaa')
                for i, start, size, payload in [(0, 8192, 131072, bootimage),
                                                 (1, 139264, 1572864, out/'rootfs.f2fs')]:
                    self.assertEqual(struct.unpack_from('<II', mbr, 446 + 16*i + 8), (start, size))
                    self.assertEqual(start % 8192, 0)
                    disk.seek(start * 512)
                    with payload.open('rb') as partition:
                        self.assertEqual(disk.read(4096), partition.read(4096))
                self.assertEqual(mbr[478:510], bytes(32))  # p3 is made on the card
            self.assertEqual(image.stat().st_size, 836 * 1024 * 1024)
            self.assertEqual(manifest['partitions'][2]['start'], 1712128)
            # Check the real FAT at its offset inside the final disk, not just
            # the separately generated partition file.
            final_boot = tmp/'final-boot.sd'
            subprocess.run(['mcopy', '-i', str(image) + '@@4194304', '::/boot.sd', str(final_boot)], check=True)
            self.assertEqual(final_boot.read_bytes(), COMPOSED)
            with bootimage.open('rb') as fat:
                self.assertEqual(struct.unpack_from('<I', fat.read(64), 28)[0], 8192)



if __name__ == '__main__':
    unittest.main()
