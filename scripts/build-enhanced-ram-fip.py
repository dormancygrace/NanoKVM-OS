#!/usr/bin/env python3
from pathlib import Path
import importlib.util, subprocess, struct, hashlib, json, lzma, binascii
import argparse, sys
parser=argparse.ArgumentParser(description='Build and verify a Sophgo FIP candidate; never writes to a device')
for name in ['base-fip','uboot','sdk-fiptool','output-dir']:
 parser.add_argument('--'+name,type=Path,required=True)
parser.add_argument('--opensbi',type=Path,help='Replace OpenSBI; omit to preserve the base FIP monitor')
parser.add_argument('--base-sha256',required=True)
parser.add_argument('--text-base',type=lambda x:int(x,0),default=0x80200000)
args=parser.parse_args()
old=args.base_fip.resolve();fw=args.opensbi.resolve() if args.opensbi else None;ub=args.uboot.resolve();tool=args.sdk_fiptool.resolve();o=args.output_dir.resolve()
for path in [old,ub,tool]+([fw] if fw else []):
 if not path.is_file(): parser.error('Input does not exist: '+str(path))
if o.exists() and any(o.iterdir()): parser.error('Output directory must be new or empty')
if hashlib.sha256(old.read_bytes()).hexdigest()!=args.base_sha256: parser.error('Base FIP checksum mismatch')
o.mkdir(parents=True,exist_ok=True)
# FSBL adds the 32-byte header size to RUNADDR when entering U-Boot.
header=struct.pack('<I4sIIQII',0,b'BL33',0,32+ub.stat().st_size,args.text_base-32,0,0)
(o/'u-boot-raw.bin').write_bytes(header+ub.read_bytes())
with (o/'pack.log').open('w') as log:
 subprocess.run([sys.executable,str(tool),'genfip','--OLD_FIP',str(old)]+(['--MONITOR',str(fw)] if fw else [])+['--LOADER_2ND',str(o/'u-boot-raw.bin'),'--compress','lzma',str(o/'fip.bin')],stdout=log,stderr=subprocess.STDOUT,check=True)
spec=importlib.util.spec_from_file_location('fiptool_check',tool);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
def snapshot(path):
 f=m.FIP();f.read_fip(str(path)); blob=path.read_bytes()
 for params in [f.param1,f.param2]:
  base=0 if params is f.param1 else f.param1['PARAM2_LOADADDR'].toint()
  field='PARAM_CKSUM' if params is f.param1 else 'PARAM2_CKSUM'
  end=2048 if params is f.param1 else 4096
  assert bytes(params[field].content)==f.image_crc(blob[base+params[field].end:base+end])
 parts={k:bytes(v.content) for table in [f.body1,f.body2] for k,v in table.items()}
 return parts,{k:v.toint() for k,v in f.param2.items() if k.endswith(('LOADADDR','RUNADDR','SIZE'))}
a,_=snapshot(old);b,layout=snapshot(o/'fip.bin')
for part in ['BL2','BLCP','DDR_PARAM','BLCP_2ND']:
 assert a[part]==b[part],part
if fw:
 assert b['MONITOR'][:fw.stat().st_size]==fw.read_bytes()
else:
 assert a['MONITOR']==b['MONITOR'], 'OpenSBI must be preserved byte-for-byte'
loader=b['LOADER_2ND'];fields=struct.unpack('<I4sIIQII',loader[:32]);assert fields[4]+32==args.text_base
assert fields[2]==(0xcafe0000 | binascii.crc_hqx(loader[12:fields[3]],0))
dec=lzma.LZMADecompressor(format=lzma.FORMAT_ALONE)
assert dec.decompress(loader[32:])==ub.read_bytes() and dec.eof
assert not dec.unused_data.strip(bytes([0]))
info={'purpose':'Early-boot candidate; builder does not flash or qualify runtime','base_fip_sha256':hashlib.sha256(old.read_bytes()).hexdigest(),'fip_bytes':(o/'fip.bin').stat().st_size,'fip_sha256':hashlib.sha256((o/'fip.bin').read_bytes()).hexdigest(),'opensbi_sha256':hashlib.sha256(fw.read_bytes() if fw else b['MONITOR']).hexdigest(),'uboot_sha256':hashlib.sha256(ub.read_bytes()).hexdigest(),'preserved':['BL2','BLCP','DDR_PARAM','BLCP_2ND']+([] if fw else ['MONITOR']),'loader_header_base':hex(fields[4]),'loader_entry':hex(fields[4]+32),'layout':layout}
(o/'manifest.json').write_text(json.dumps(info,indent=2)+'\n');print(json.dumps(info,indent=2))


