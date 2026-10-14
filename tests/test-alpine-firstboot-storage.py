#!/usr/bin/env python3
from pathlib import Path
import subprocess,tempfile,json,shlex
import argparse
p=argparse.ArgumentParser(description='Root-only firstboot test on a disposable loop disk. The source image is read-only.')
p.add_argument('--image',type=Path,required=True)
p.add_argument('--mkfs-exfat', type=Path, help='Path to mkfs.exfat or a wrapper for the image version')
a=p.parse_args()
r=Path(__file__).resolve().parents[1]
def run(*args,**kw):return subprocess.check_output(args,text=True,**kw)
with tempfile.TemporaryDirectory(prefix='nkos-firstboot-') as temp:
 t=Path(temp);img=t/'sd.img'
 with img.open('wb') as f:
  f.write(a.image.open('rb').read(512));f.truncate(2*1024**3)
 loop=run('losetup','--find','--show','--partscan',str(img)).strip()
 try:
  marker=t/'firstboot';marker.touch();data=t/'data';data.mkdir()
  s=(r/'firmware/alpine/compat/nanokvm-firstboot-storage').read_text().replace('/etc/nanokvm-firstboot-storage',str(marker)).replace('/sys/class/block/mmcblk0','/sys/class/block/'+Path(loop).name).replace('/dev/mmcblk0',loop).replace('/data',str(data)).replace('mount '+str(data),'mount -t exfat '+loop+'p3 '+str(data))
  if a.mkfs_exfat:
   s=s.replace('mkfs.exfat ',shlex.quote(str(a.mkfs_exfat.resolve()))+' ')
  script=t/'init.sh';script.write_text(s)
  result=subprocess.run(['sh',str(script)],text=True,capture_output=True);print(result.stdout,result.stderr);print(run('sfdisk','--json',loop));print(run('lsblk',loop));result.check_returncode()
  assert not marker.exists()
  assert run('blkid','-s','TYPE','-o','value',loop+'p3').strip()=='exfat'
  (data/'preserve-test').write_text('user data')
  run('sh',str(script))
  assert (data/'preserve-test').read_text()=='user data'
  table=json.loads(run('sfdisk','--json',loop))['partitiontable']['partitions']
  assert table[2]['start']==1705984 and table[2]['size']==4194304-1705984
  # Reflashing p1+p2 removes p3 from the table but leaves its old exFAT
  # signature in the remaining card space. Recreate that real failure case.
  run('umount',str(data))
  run('partx','-d','-n','3',loop)
  run('sfdisk','--no-reread','--no-tell-kernel','--delete',loop,'3')
  marker.touch()
  result=subprocess.run(['sh',str(script)],text=True,capture_output=True)
  print(result.stdout,result.stderr);result.check_returncode()
  assert not marker.exists()
  assert not (data/'preserve-test').exists()
  assert run('blkid','-s','TYPE','-o','value',loop+'p3').strip()=='exfat'
  print('PASS: fresh partition, full capacity, second boot preserves data, reused card reformatted')
 finally:
  subprocess.run(['umount',str(t/'data')],capture_output=True)
  run('losetup','-d',loop)
