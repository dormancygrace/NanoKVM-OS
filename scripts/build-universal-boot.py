#!/usr/bin/env python3
"""Build first-boot selector and matched profile FITs, using an explicitly selected kernel."""
from pathlib import Path
import argparse, gzip, hashlib, json, os, re, shutil, subprocess

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--output',type=Path,required=True)
p.add_argument('--build-base',type=Path,required=True)
p.add_argument('--kernel-source',type=Path,required=True)
p.add_argument('--kernel-output',type=Path,required=True)
p.add_argument('--epoch',type=int,required=True)
a=p.parse_args()
r=Path(__file__).resolve().parents[1]
out=a.output.resolve();out.mkdir(parents=True,exist_ok=False)
base=a.build_base.resolve();old=base/'releases/beta10-seq23'
tools=base/'buildroot-output/host/bin'
cross=tools/'riscv64-buildroot-linux-musl-gcc'
env=dict(os.environ,SOURCE_DATE_EPOCH=str(a.epoch))
def run(*args,**kw):
    return subprocess.run([str(x) for x in args],check=True,env=env,**kw)
def sha(f):
    return hashlib.sha256(f.read_bytes()).hexdigest()
run(cross,'-O2','-march=rv64gc','-mabi=lp64d','-mno-fence-tso','-static','-s','-Wall','-Wextra','-Werror',r/'firmware/boards/nkos-board-probe.c','-o',out/'nkos-board-probe')
# Keep the exact accepted BusyBox and libraries; only init gains the selector hook.
listing=(old/'initramfs/initramfs.list').read_text()
listing=re.sub(r'^file /init .*$',f'file /init {r}/firmware/boot/initramfs/init 755 0 0',listing,flags=re.M)
(out/'initramfs.list').write_text(listing)
cpio=subprocess.check_output([str(old/'kernel-output/usr/gen_init_cpio'),'-t',str(a.epoch),str(out/'initramfs.list')])
(out/'initramfs.cpio.gz').write_bytes(gzip.compress(cpio,mtime=a.epoch))
kernel_release=(a.kernel_output/'include/config/kernel.release').read_text().strip()
assert re.fullmatch(r'7\.2\.5-nanokvm-os-r[0-9]+',kernel_release), kernel_release
run('zstd','-19','-T0',a.kernel_output/'arch/riscv/boot/Image','-o',out/'Image.zst')
template=(r/'scripts/build-enhanced-fit.py').read_text().split("its = '''",1)[1].split("'''",1)[0]
template=template.replace('Image.gz','Image.zst').replace('compression = "gzip"','compression = "zstd"')
manifest={'kernel':kernel_release,'kernel_payload_sha256':sha(out/'Image.zst'),'epoch':a.epoch,'profiles':{}}
for profile in ('detect','alpha','beta','pcie','lite'):
    dst=out/profile;dst.mkdir()
    for name in ('Image.zst','initramfs.cpio.gz'):os.link(out/name,dst/name)
    cpp=subprocess.check_output(['gcc','-E','-P','-nostdinc','-undef','-D__DTS__','-x','assembler-with-cpp',
        '-I'+str(a.kernel_source/'arch/riscv/boot/dts/sophgo'),'-I'+str(a.kernel_source/'include'),
        '-I'+str(a.kernel_source/'scripts/dtc/include-prefixes'),str(r/f'firmware/boards/sg2002-nanokvm-{profile}.dts')])
    (dst/'board.dts').write_bytes(cpp)
    with (dst/'dtc.log').open('w') as log:run('dtc','-I','dts','-O','dtb','-o',dst/'board.dtb',dst/'board.dts',stderr=log)
    (dst/'boot.its').write_text(template.replace('NanoKVM OS candidate - hardware qualification pending',f'NanoKVM OS automatic profile: {profile}'))
    with (dst/'mkimage.log').open('w') as log:run(tools/'mkimage','-f','boot.its','boot.sd',cwd=dst,stdout=log)
    assert (dst/'boot.sd').stat().st_size < 16*1024*1024
    manifest['profiles'][profile]={'fit_sha256':sha(dst/'boot.sd'),'dtb_sha256':sha(dst/'board.dtb'),'bytes':(dst/'boot.sd').stat().st_size}
    # Guard against reintroducing early ownership of Alpha's ATX reset.
    if profile=='detect':
        assert subprocess.check_output(['fdtget',str(dst/'board.dtb'),'/i2c-oled','status'],text=True).strip()=='disabled'
manifest['probe_sha256']=sha(out/'nkos-board-probe')
(out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
print(json.dumps(manifest,indent=2))
