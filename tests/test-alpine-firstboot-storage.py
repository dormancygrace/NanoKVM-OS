#!/usr/bin/env python3
from pathlib import Path
import subprocess,tempfile,json,shlex
import argparse
p=argparse.ArgumentParser(description='Root-only firstboot test on a disposable loop disk. The source image is read-only.')
p.add_argument('--card-bytes', type=int, default=2*1024**3, help='Disposable card capacity in bytes (1000000000 for decimal 1 GB)')
p.add_argument('--legacy', action='store_true', help='Exercise the older unaligned layout')
p.add_argument('--image',type=Path,help='SD image whose partition table is used; without it, the fresh image table is written with sfdisk')
p.add_argument('--mkfs-exfat', type=Path, help='Path to mkfs.exfat or a wrapper for the image version')
a=p.parse_args()
r=Path(__file__).resolve().parents[1]
def run(*args,**kw):return subprocess.check_output(args,text=True,**kw)
with tempfile.TemporaryDirectory(prefix='nkos-firstboot-') as temp:
 t=Path(temp);img=t/'sd.img'
 with img.open('wb') as f:
  if a.image: f.write(a.image.open('rb').read(512))
  f.truncate(a.card_bytes)
 if not a.image:
  boot_start, root_start = (1, 131073) if a.legacy else (8192, 139264)
  table=f'label: dos\nstart={boot_start}, size=131072, type=e\nstart={root_start}, size=1572864, type=83\n'
  subprocess.run(['sfdisk','--no-reread','--no-tell-kernel',str(img)],input=table,text=True,check=True,capture_output=True)
 loop=run('losetup','--find','--show','--partscan',str(img)).strip()
 try:
  marker=t/'firstboot';marker.touch();data=t/'data';data.mkdir()
  s=(r/'firmware/alpine/compat/nanokvm-firstboot-storage').read_text().replace('/etc/nanokvm-firstboot-storage',str(marker)).replace('/var/log/nanokvm-firstboot-storage.log',str(t/'reasons.log')).replace('/sys/class/block/mmcblk0','/sys/class/block/'+Path(loop).name).replace('/dev/mmcblk0',loop).replace('/data',str(data)).replace('mount '+str(data),'mount -t exfat '+loop+'p3 '+str(data))
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
  expected_start = 1705984 if table[1]['start'] == 131073 else 1712128
  assert table[2]['start']==expected_start and table[2]['size']==a.card_bytes//512-expected_start
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
