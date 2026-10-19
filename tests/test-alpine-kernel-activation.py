#!/usr/bin/env python3
"""Exercise the APK activation script on isolated host fixtures, never a device."""
import hashlib, os, re, shutil, subprocess, tempfile, unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT/'firmware/alpine/compat/nanokvm-activate-kernel'
COMPOSE = ROOT/'platform/boot/compose-fit'
# A miniature FIT: 4 bytes, the 8-byte device tree, its 32-byte SHA-256, 4 bytes.
TEMPLATE = b'HEAD' + bytes(8) + bytes(32) + b'TAIL'
# The board's 8-byte device tree, as the layout below expects.
DTB = b'pcie-dtb'
def composed(dtb):
    return b'HEAD' + dtb + hashlib.sha256(dtb).digest() + b'TAIL'
class ActivationTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.dt = self.root/'dt'
        self.payload = self.root/'payload'
        self.boot = self.root/'fat'
        self.modules = self.root/'modules'/'7.2.5-nanokvm-os-r4'
        for path in (self.payload,self.boot,self.modules,self.root/'bin',self.root/'run',self.root/'state',self.root/'etc/kvm',self.dt):
            path.mkdir(parents=True,exist_ok=True)
        (self.payload/'kernel.release').write_text('7.2.5-nanokvm-os-r4\n')
        (self.payload/'fit-template.sd').write_bytes(TEMPLATE)
        (self.payload/'fit.layout').write_text('DTB_OFFSET=4\nDTB_SIZE=8\nHASH_OFFSET=12\n')
        shutil.copyfile(COMPOSE,self.payload/'compose-fit')
        self.put_image('pcie',DTB)
        (self.modules/'test.ko').write_bytes(b'module')
        (self.boot/'boot.sd').write_bytes(b'old FIT')
        (self.dt/'sipeed,board-revision').write_text('pcie')
        (self.root/'mounts').write_text('/dev/mmcblk0p1 '+str(self.boot)+' vfat rw 0 0\n')
        for name,body in {'mountpoint':'exit 0','depmod':'exit 0','modinfo':"echo '7.2.5-nanokvm-os-r4 SMP'"}.items():
            f=self.root/'bin'/name;f.write_text('#!/bin/sh\n'+body+'\n');f.chmod(0o755)
        source=SOURCE.read_text().replace('/usr/lib/nanokvm/boot',str(self.payload))
        source=source.replace('/lib/modules',str(self.root/'modules'))
        source=re.sub(r'/boot(?=/|\s|;|"|$)',str(self.boot),source)
        source=source.replace('/sys/firmware/devicetree/base',str(self.dt))
        source=source.replace('/etc/kvm',str(self.root/'etc/kvm'))
        source=source.replace('/proc/mounts',str(self.root/'mounts')).replace('/var/lib/nanokvm',str(self.root/'state')).replace('/run',str(self.root/'run'))
        source=source.replace('export PATH=/usr/sbin:/usr/bin:/sbin:/bin','export PATH='+str(self.root/'bin')+':/usr/bin:/bin')
        self.script=self.root/'activate';self.script.write_text(source)
    def put_image(self,name,dtb):
        (self.payload/f'{name}.dtb').write_bytes(dtb)
        (self.payload/f'{name}.fdt-sha256').write_bytes(hashlib.sha256(dtb).digest())
        (self.payload/f'{name}.sha256').write_text(hashlib.sha256(composed(dtb)).hexdigest()+f'  {name}.sd\n')
    def run_activation(self):
        return subprocess.run(['sh',str(self.script)],capture_output=True,text=True)
    def test_installs_matched_fit_and_requests_reboot(self):
        result=self.run_activation()
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual((self.boot/'boot.sd').read_bytes(),composed(DTB))
        self.assertTrue((self.root/'run/reboot-required').exists())
        self.assertEqual(sorted(p.name for p in self.boot.iterdir()),['boot.sd','kernel.release'])
    def test_bad_source_hash_leaves_boot_untouched(self):
        (self.payload/'pcie.dtb').write_bytes(b'corrupt!')
        self.assertNotEqual(self.run_activation().returncode,0)
        self.assertEqual(sorted(p.name for p in self.boot.iterdir()),['boot.sd'])
        self.assertEqual((self.boot/'boot.sd').read_bytes(),b'old FIT')
    def test_wrong_module_abi_leaves_boot_untouched(self):
        (self.root/'bin/modinfo').write_text('#!/bin/sh\necho incompatible\n')
        self.assertNotEqual(self.run_activation().returncode,0)
        self.assertEqual((self.boot/'boot.sd').read_bytes(),b'old FIT')
    def test_every_earlier_mode_boots_the_board_image(self):
        # Earlier releases selected an image per video memory mode; the
        # selection files go, and every device boots the board's image.
        for mode in (None,'fhd','qhd','uhd','cma','fixed','fhd-fixed','../bad'):
            with self.subTest(mode=mode):
                (self.boot/'boot.sd').write_bytes(b'old FIT')
                (self.dt/'nanokvm,video-memory-mode').write_bytes((mode or 'uhd').encode()+b'\0')
                if mode:
                    (self.root/'etc/kvm/video-memory-mode').write_text(mode+'\n')
                    (self.boot/'video-memory-mode').write_text(mode+'\n')
                result=self.run_activation()
                self.assertEqual(result.returncode,0,result.stderr)
                self.assertEqual((self.boot/'boot.sd').read_bytes(),composed(DTB))
                self.assertFalse((self.root/'etc/kvm/video-memory-mode').exists())
                self.assertFalse((self.boot/'video-memory-mode').exists())
    def test_a_mode_argument_of_an_earlier_server_is_ignored(self):
        result=subprocess.run(['sh',str(self.script),'pcie','qhd'],capture_output=True,text=True)
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual((self.boot/'boot.sd').read_bytes(),composed(DTB))
    def test_missing_board_image_leaves_boot_untouched(self):
        (self.payload/'pcie.dtb').unlink()
        self.assertNotEqual(self.run_activation().returncode,0)
        self.assertEqual((self.boot/'boot.sd').read_bytes(),b'old FIT')
    def test_unknown_board_is_rejected(self):
        (self.dt/'sipeed,board-revision').write_text('unknown')
        self.assertNotEqual(self.run_activation().returncode,0)
        self.assertEqual((self.boot/'boot.sd').read_bytes(),b'old FIT')
if __name__=='__main__': unittest.main()
