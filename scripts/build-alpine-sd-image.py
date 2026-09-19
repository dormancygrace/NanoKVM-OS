#!/usr/bin/env python3
"""Assemble a fresh NanoKVM SD image from a signed-package Alpine root bundle."""
import argparse, hashlib, json, os, shutil, struct, subprocess, zipfile
from pathlib import Path

p=argparse.ArgumentParser(description=__doc__)
p.add_argument('--rootfs-archive',type=Path,required=True)
p.add_argument('--fip',type=Path,required=True)
p.add_argument('--f2fs-tools',type=Path,required=True)
p.add_argument('--output',type=Path,required=True)
release_file=Path(__file__).resolve().parents[1]/'firmware/alpine/release.env'
release_values=dict(line.split('=',1) for line in release_file.read_text().splitlines() if line and not line.startswith('#'))
p.add_argument('--version',default=release_values['NANOKVM_VERSION'])
a=p.parse_args()
if not a.version or any(c not in 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-' for c in a.version):
    p.error('Invalid version')
out=a.output.resolve();out.mkdir(parents=True,exist_ok=False)
root=out/'root';root.mkdir()
def run(*args):
    result=subprocess.run([str(x) for x in args],capture_output=True,text=True)
    with (out/'assembly.log').open('a') as log:log.write(' '.join(map(str,args))+'\n'+result.stdout+result.stderr)
    if result.returncode:raise RuntimeError(result.stdout+result.stderr)
def sha(path):
    with path.open('rb') as stream:return hashlib.file_digest(stream,'sha256').hexdigest()
run('tar','--numeric-owner','-xzf',a.rootfs_archive,'-C',root)
bootset=root/'usr/lib/nanokvm/boot'
assert (bootset/'detect.sd').is_file()
release=(bootset/'kernel.release').read_text().strip()
assert (root/'lib/modules'/release).is_dir()
assert (root/'etc/kvm/ssh_stop').is_file()
assert not list((root/'root').glob('.ssh/*'))
assert not list((root/'etc/ssh').glob('ssh_host_*'))
for name in ['server.key','server.crt','pwd','wifi.pass','wifi.ssid','.jwt_secret']:
    assert not (root/'etc/kvm'/name).exists(), 'Personal configuration in clean image: '+name
for name in ['boot','data','dev','proc','sys','run','tmp']:
    (root/name).mkdir(exist_ok=True)
(root/'etc/nanokvm-firstboot-storage').touch()
fstab=root/'etc/fstab';lines=[]
for line in fstab.read_text().splitlines():
    fields=line.split()
    if len(fields)>3 and fields[1]=='/data':
        fields[3]='noauto,'+fields[3];line=' '.join(fields)
    lines.append(line)
fstab.write_text('\n'.join(lines)+'\n')
# The builder VM's resolver is not a runtime network setting.
(root/'etc/resolv.conf').unlink(missing_ok=True)
(root/'etc/resolv.conf').write_text('')
rootimage=out/'rootfs.f2fs'
with rootimage.open('xb') as stream:stream.truncate(768*1024*1024)
run(a.f2fs_tools/'mkfs.f2fs','-f','-t','0','-l','rootfs',rootimage)
run(a.f2fs_tools/'sload.f2fs','-f',root,'-P','-t','/',rootimage)
run(a.f2fs_tools/'fsck.f2fs','-f',rootimage)
bootimage=out/'boot-partition.img'
with bootimage.open('xb') as stream:stream.truncate(64*1024*1024)
run('/usr/sbin/mkfs.fat','--invariant','-F','16','-S','512','-s','4','-R','4','-n','boot',bootimage)
files=out/'boot-files';files.mkdir()
shutil.copy2(bootset/'detect.sd',files/'boot.sd');shutil.copy2(a.fip,files/'fip.bin')
(files/'uEnv.txt').write_text('showlogo=echo NanoKVM OS\n')
(files/'hostname.prefix').write_text('kvm')
(files/'ver').write_text('NanoKVM OS '+a.version+'\n')
for name in ['usb.dev','usb.disk0','wifi.sta','gt9xx']:(files/name).touch()
for file in sorted(files.iterdir()):run('mcopy','-i',bootimage,file,'::/'+file.name)
run('/usr/sbin/fsck.fat','-n',bootimage)
mbr=bytearray(512);mbr[510:]=b'\x55\xaa'
for index,kind,start,sectors in [(0,0x0c,1,131072),(1,0x83,131073,1572864)]:
    offset=446+16*index
    struct.pack_into('<B3sB3sII',mbr,offset,0,b'\xfe\xff\xff',kind,b'\xfe\xff\xff',start,sectors)
image=out/('NanoKVM-OS-'+a.version+'.img')
with image.open('xb') as dst:
    dst.write(mbr)
    for source in [bootimage,rootimage]:
        with source.open('rb') as src:shutil.copyfileobj(src,dst,4*1024*1024)
assert image.stat().st_size==832*1024*1024+512
archive=out/(image.name+'.zip')
with zipfile.ZipFile(archive,'w',compression=zipfile.ZIP_DEFLATED,compresslevel=9) as z:z.write(image,image.name)
manifest={'version':a.version,'kernel':release,'rootfs_archive_sha256':sha(a.rootfs_archive),'fip_sha256':sha(a.fip),'image_bytes':image.stat().st_size,'zip_bytes':archive.stat().st_size,'partitions':[{'number':1,'start':1,'sectors':131072,'filesystem':'FAT16'},{'number':2,'start':131073,'sectors':1572864,'filesystem':'F2FS'},{'number':3,'start':1705984,'sectors':'remaining SD capacity; created on first boot','filesystem':'exFAT'}]}
(out/'build-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
(out/'SHA256SUMS').write_text(''.join(sha(x)+'  '+x.name+'\n' for x in [image,archive,out/'build-manifest.json']))
print(json.dumps(manifest,indent=2))
