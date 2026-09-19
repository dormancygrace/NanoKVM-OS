#!/usr/bin/env python3
from pathlib import Path
import subprocess,tempfile,json
import argparse
p=argparse.ArgumentParser(description='Root-only firstboot test on a disposable loop disk. The source image is read-only.')
p.add_argument('--image',type=Path,required=True)
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
  script=t/'init.sh';script.write_text(s)
  result=subprocess.run(['sh',str(script)],text=True,capture_output=True);print(result.stdout,result.stderr);print(run('sfdisk','--json',loop));print(run('lsblk',loop));result.check_returncode()
  assert not marker.exists()
  assert run('blkid','-s','TYPE','-o','value',loop+'p3').strip()=='exfat'
  (data/'preserve-test').write_text('user data')
  run('sh',str(script))
  assert (data/'preserve-test').read_text()=='user data'
  table=json.loads(run('sfdisk','--json',loop))['partitiontable']['partitions']
  assert table[2]['start']==1705984 and table[2]['size']==4194304-1705984
  print('PASS: fresh partition, exFAT mount, full card capacity, second boot preserves data')
 finally:
  subprocess.run(['umount',str(t/'data')],capture_output=True)
  run('losetup','-d',loop)
