#!/usr/bin/env python3
"""Exercise the APK activation script on isolated host fixtures, never a device."""
import hashlib, os, re, shutil, subprocess, tempfile, unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT/'firmware/alpine/compat/nanokvm-activate-kernel'
COMPOSE = ROOT/'platform/boot/compose-fit'
# A miniature FIT: 4 bytes, the 8-byte device tree, its 32-byte SHA-256, 4 bytes.
TEMPLATE = b'HEAD' + bytes(8) + bytes(32) + b'TAIL'
# One 8-byte device tree per video memory mode, as the layout below expects.
DTBS = {'fhd': b'pcie-fhd', 'fhd-fixed': b'pcie-ffx', 'qhd': b'pcie-qhd', 'qhd-fixed': b'pcie-qfx', 'uhd': b'pcie-uhd'}
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
        self.image('fhd')
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
    def image(self,mode):
        self.put_image('pcie' if mode=='fhd' else 'pcie-'+mode,DTBS[mode])
    def put_image(self,name,dtb):
        (self.payload/f'{name}.dtb').write_bytes(dtb)
        (self.payload/f'{name}.fdt-sha256').write_bytes(hashlib.sha256(dtb).digest())
        (self.payload/f'{name}.sha256').write_text(hashlib.sha256(composed(dtb)).hexdigest()+f'  {name}.sd\n')
    def run_activation(self):
        return subprocess.run(['sh',str(self.script)],capture_output=True,text=True)
    def test_installs_matched_fit_and_requests_reboot(self):
        result=self.run_activation()
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual((self.boot/'boot.sd').read_bytes(),composed(DTBS['fhd']))
        self.assertTrue((self.root/'run/reboot-required').exists())
        self.assertEqual(sorted(p.name for p in self.boot.iterdir()),['boot.sd','kernel.release','video-memory-mode'])
    def test_bad_source_hash_leaves_boot_untouched(self):
        (self.payload/'pcie.dtb').write_bytes(b'corrupt!')
        self.assertNotEqual(self.run_activation().returncode,0)
        self.assertEqual(sorted(p.name for p in self.boot.iterdir()),['boot.sd'])
        self.assertEqual((self.boot/'boot.sd').read_bytes(),b'old FIT')
    def test_wrong_module_abi_leaves_boot_untouched(self):
        (self.root/'bin/modinfo').write_text('#!/bin/sh\necho incompatible\n')
        self.assertNotEqual(self.run_activation().returncode,0)
        self.assertEqual((self.boot/'boot.sd').read_bytes(),b'old FIT')
    def select(self,mode):
        (self.root/'etc/kvm/video-memory-mode').write_text(mode+'\n')
    def selected(self):
        return (self.root/'etc/kvm/video-memory-mode').read_text()
    def booted(self,fit):
        self.assertEqual((self.boot/'boot.sd').read_bytes(),composed(fit))
        self.assertEqual((self.boot/'video-memory-mode').read_text(),self.selected())
    def test_resolution_selections_survive_kernel_update(self):
        for mode in ('fhd-fixed','qhd','qhd-fixed','uhd'):
            with self.subTest(mode=mode):
                self.image(mode)
                self.select(mode)
                result=self.run_activation()
                self.assertEqual(result.returncode,0,result.stderr)
                self.booted(DTBS[mode])
                self.assertEqual(self.selected(),mode+'\n')
    def test_explicit_mode_argument_selects_and_saves(self):
        self.image('qhd')
        result=subprocess.run(['sh',str(self.script),'pcie','qhd'],capture_output=True,text=True)
        self.assertEqual(result.returncode,0,result.stderr)
        self.booted(DTBS['qhd'])
    def test_default_is_fhd_on_a_new_device(self):
        result=self.run_activation()
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(self.selected(),'fhd\n')
    def test_legacy_selection_keeps_its_capability(self):
        for legacy,mode in {'cma':'qhd','fixed':'qhd-fixed','uhd':'uhd'}.items():
            with self.subTest(legacy=legacy):
                self.image(mode)
                self.select(legacy)
                result=self.run_activation()
                self.assertEqual(result.returncode,0,result.stderr)
                self.booted(DTBS[mode])
                self.assertEqual(self.selected(),mode+'\n')
    def test_no_selection_follows_the_running_image(self):
        for raw,mode in {'cma':'qhd','fixed':'qhd-fixed','uhd':'uhd','qhd':'qhd','fhd-fixed':'fhd-fixed','fhd':'fhd'}.items():
            with self.subTest(running=raw):
                (self.root/'etc/kvm/video-memory-mode').unlink(missing_ok=True)
                self.image(mode)
                (self.dt/'nanokvm,video-memory-mode').write_bytes(raw.encode()+b'\0')
                self.assertEqual(self.run_activation().returncode,0)
                self.booted(DTBS[mode])
    def test_no_selection_follows_a_running_image_without_the_property(self):
        ion=self.dt/'reserved-memory/ion'
        backend=self.dt/'cvitek-ion/heap-carveout/nanokvm,cma-backend'
        for layout,mode in (('cma','qhd'),('fixed','qhd-fixed'),('uhd','uhd')):
            with self.subTest(layout=layout):
                (self.root/'etc/kvm/video-memory-mode').unlink(missing_ok=True)
                self.image(mode)
                shutil.rmtree(self.dt/'reserved-memory',ignore_errors=True)
                shutil.rmtree(self.dt/'cvitek-ion',ignore_errors=True)
                if layout=='cma':
                    backend.parent.mkdir(parents=True);backend.write_bytes(b'')
                else:
                    ion.mkdir(parents=True);(ion/'compatible').write_bytes(b'ion-region\0')
                    (ion/'size').write_bytes(bytes([8 if layout=='uhd' else 4,0,0,0]))
                self.assertEqual(self.run_activation().returncode,0)
                self.booted(DTBS[mode])
    def test_missing_fixed_payload_does_not_silently_switch_to_cma(self):
        self.select('qhd-fixed')
        self.assertNotEqual(self.run_activation().returncode,0)
        self.assertEqual((self.boot/'boot.sd').read_bytes(),b'old FIT')
    def test_missing_legacy_mapped_payload_does_not_silently_switch(self):
        self.select('cma')
        self.assertNotEqual(self.run_activation().returncode,0)
        self.assertEqual((self.boot/'boot.sd').read_bytes(),b'old FIT')
    def test_invalid_selection_leaves_boot_untouched(self):
        (self.root/'etc/kvm/video-memory-mode').write_text('../bad\n')
        self.assertNotEqual(self.run_activation().returncode,0)
        self.assertEqual((self.boot/'boot.sd').read_bytes(),b'old FIT')
    def test_unknown_board_is_rejected(self):
        (self.dt/'sipeed,board-revision').write_text('unknown')
        self.assertNotEqual(self.run_activation().returncode,0)
        self.assertEqual((self.boot/'boot.sd').read_bytes(),b'old FIT')
if __name__=='__main__': unittest.main()
