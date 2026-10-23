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
def sha(data):
    return hashlib.sha256(data).hexdigest()
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
        # Reads "from the card"; CORRUPT names a file to read back wrong.
        # Reads "from the card"; CORRUPT names a file to read back wrong, when
        # its content has the hash CORRUPT_IF (if set).
        f=self.root/'bin'/'readcard';f.write_text('#!/bin/sh\nif [ "$(basename "$1")" = "${CORRUPT:-}" ] && { [ -z "${CORRUPT_IF:-}" ] || [ "$(sha256sum < "$1" | cut -d " " -f 1)" = "$CORRUPT_IF" ]; }; then echo garbage; exit 0; fi\ncat "$1"\n');f.chmod(0o755)
        # FAIL_MV names the destination of a rename that fails.
        f=self.root/'bin'/'mv';f.write_text('#!/bin/sh\nfor last; do :; done\n[ "$(basename "$last")" != "${FAIL_MV:-}" ] || exit 1\nexec /bin/mv "$@"\n');f.chmod(0o755)
        source=SOURCE.read_text()
        self.assertIn('dd if="$1" bs=64k iflag=direct 2>/dev/null',source)
        source=source.replace('dd if="$1" bs=64k iflag=direct 2>/dev/null','readcard "$1"')
        source=source.replace('/usr/lib/nanokvm/boot',str(self.payload))
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
    def run_activation(self,**env):
        return subprocess.run(['sh',str(self.script)],capture_output=True,text=True,env={**os.environ,**env})
    def files(self):
        return {p.name:p.read_bytes() for p in self.boot.iterdir() if p.name!='kernel.release' and not p.name.endswith('.sha256')}
    def records(self):
        return {p.name[:-7]:p.read_text().strip() for p in self.boot.glob('*.sha256')}
    def record(self,name,data):
        (self.boot/f'{name}.sha256').write_text(sha(data)+'\n')
    def installed_read_wrong(self):
        # The installed image reads back wrong; earlier reads of boot.sd are right.
        return {'CORRUPT':'boot.sd','CORRUPT_IF':sha(composed(DTB))}
    def boots(self,uboot):
        """What a U-Boot tries, in order: the new one boot.sd then boot.prev,
        the one before this change only boot.sd. The first readable image."""
        names=['boot.sd','boot.prev'] if uboot=='new' else ['boot.sd']
        for name in names:
            if (self.boot/name).exists():
                return (self.boot/name).read_bytes()
    def test_installs_matched_fit_and_requests_reboot(self):
        result=self.run_activation()
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual((self.boot/'boot.sd').read_bytes(),composed(DTB))
        self.assertTrue((self.root/'run/reboot-required').exists())
        # The image this device booted from (no records yet) is the fallback.
        self.assertEqual(self.files(),{'boot.sd':composed(DTB),'boot.prev':b'old FIT'})
        self.assertEqual(self.records(),{'boot.sd':sha(composed(DTB)),'boot.prev':sha(b'old FIT')})
    def test_an_image_already_on_the_card_is_not_rewritten(self):
        (self.boot/'boot.sd').write_bytes(composed(DTB))
        (self.boot/'boot.prev').write_bytes(b'older FIT')
        self.record('boot.prev',b'older FIT')
        (self.boot/'boot.new').write_bytes(b'leftover')
        before=(self.boot/'boot.sd').stat().st_mtime_ns
        result=self.run_activation()
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(self.files(),{'boot.sd':composed(DTB),'boot.prev':b'older FIT'})
        self.assertEqual((self.boot/'boot.sd').stat().st_mtime_ns,before)
        self.assertEqual(self.records()['boot.sd'],sha(composed(DTB)))
    def test_a_new_image_read_back_wrong_is_not_installed(self):
        (self.boot/'boot.prev').write_bytes(b'older FIT')
        self.record('boot.prev',b'older FIT')
        self.record('boot.sd',b'old FIT')
        result=self.run_activation(CORRUPT='boot.new')
        self.assertNotEqual(result.returncode,0)
        self.assertIn('boot.sd is unchanged',result.stderr)
        self.assertEqual(self.files(),{'boot.sd':b'old FIT','boot.prev':b'older FIT'})
    def test_an_installed_image_read_back_wrong_restores_the_previous_one(self):
        result=self.run_activation(**self.installed_read_wrong())
        self.assertNotEqual(result.returncode,0)
        self.assertIn('restored from boot.prev',result.stderr)
        self.assertEqual(self.files(),{'boot.sd':b'old FIT','boot.prev':b'old FIT'})
    def test_first_image_without_a_boot_sd(self):
        (self.boot/'boot.sd').unlink()
        result=self.run_activation()
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(self.files(),{'boot.sd':composed(DTB)})
    def test_a_damaged_image_left_by_a_failed_install_is_never_backed_up(self):
        # A first install whose image reads back wrong, with no fallback to
        # restore: the next transaction must not take it for a good image.
        (self.boot/'boot.sd').unlink()
        self.assertNotEqual(self.run_activation(**self.installed_read_wrong()).returncode,0)
        (self.boot/'boot.sd').write_bytes(b'damaged FIT')
        result=self.run_activation()
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(self.files(),{'boot.sd':composed(DTB)})
    def corrupt_primary_good_fallback(self):
        # A device that booted through the fallback: boot.sd no longer matches
        # its record, boot.prev does.
        (self.boot/'boot.sd').write_bytes(b'damaged FIT')
        self.record('boot.sd',b'old FIT')
        (self.boot/'boot.prev').write_bytes(b'older FIT')
        self.record('boot.prev',b'older FIT')
    def test_a_damaged_boot_sd_never_replaces_a_good_fallback(self):
        self.corrupt_primary_good_fallback()
        result=self.run_activation()
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(self.files(),{'boot.sd':composed(DTB),'boot.prev':b'older FIT'})
    def test_a_failed_promotion_keeps_the_good_fallback(self):
        self.corrupt_primary_good_fallback()
        result=self.run_activation(FAIL_MV='boot.sd')
        self.assertNotEqual(result.returncode,0)
        self.assertEqual((self.boot/'boot.prev').read_bytes(),b'older FIT')
        self.assertEqual(self.boots('new'),b'damaged FIT')
        self.assertEqual(self.records()['boot.prev'],sha(b'older FIT'))
    def test_a_wrong_installed_image_with_a_damaged_primary_restores_the_fallback(self):
        self.corrupt_primary_good_fallback()
        result=self.run_activation(**self.installed_read_wrong())
        self.assertNotEqual(result.returncode,0)
        self.assertIn('restored from boot.prev',result.stderr)
        self.assertEqual(self.files(),{'boot.sd':b'older FIT','boot.prev':b'older FIT'})
    def test_boot_sd_is_never_missing_for_the_previous_bootloader(self):
        # That U-Boot tries boot.sd only: every failure leaves it in place.
        for env in ({'FAIL_MV':'boot.sd'},{'FAIL_MV':'boot.prev'},{'CORRUPT':'boot.new'},self.installed_read_wrong()):
            with self.subTest(env=env):
                for f in self.boot.iterdir():
                    f.unlink()
                (self.boot/'boot.sd').write_bytes(b'old FIT')
                result=self.run_activation(**env)
                self.assertNotEqual(result.returncode,0)
                self.assertEqual(self.boots('old'),b'old FIT')
                self.assertEqual(self.boots('new'),b'old FIT')
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
class UBootFallbackTests(unittest.TestCase):
    def test_uboot_boots_the_replaced_image_and_never_stops_at_its_prompt(self):
        config=(ROOT/'platform/uboot/defconfig').read_text()
        command=re.search(r'^CONFIG_BOOTCOMMAND="(.*)"$',config,re.M)[1]
        self.assertIn('for image in boot.sd boot.prev; do if fatload mmc 0:1 0x85000000 ${image}; then bootm',command)
        self.assertTrue(command.endswith('; done; reset'))
if __name__=='__main__': unittest.main()
