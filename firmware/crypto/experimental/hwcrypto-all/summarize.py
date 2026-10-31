from pathlib import Path
import csv, json, statistics, argparse
p=argparse.ArgumentParser();p.add_argument('directory',type=Path);a=p.parse_args()
d=a.directory
summary=[]
for name,layout in [('all-v4-final.csv','all'),('gcm-v4-final.csv','gcm'),('ghash-go.csv','go'),('ghash-go-patched.csv','go-patched')]:
 groups={}
 for row in csv.reader((d/name).read_text(encoding='utf-8-sig').splitlines()):
  if not row or row[0]!='RESULT':continue
  if layout=='all': _,alg,op,size,backend,round_id,n,cpu,wall,calls=row
  else:
   _,alg,size,backend,round_id,n,cpu,wall,calls=row;op='enc'
   if layout=='go-patched' and alg.startswith('AES-'):backend='go-sw-unrolled-ghash'
  key=(alg,op,int(size),backend)
  n=int(n);cpu=int(cpu);wall=int(wall);size=int(size)
  groups.setdefault(key,[]).append(dict(cpu_us=cpu/n/1000,wall_mib_s=size*n*1e9/wall/1048576,cpu_ms_per_mib=cpu/n/1e6*1048576/size,calls=int(calls)))
 for (alg,op,size,backend),v in groups.items():
  summary.append(dict(source=name,algorithm=alg,operation=op,input_bytes=size,backend=backend,rounds=len(v),cpu_us=statistics.median(x['cpu_us'] for x in v),wall_mib_s=statistics.median(x['wall_mib_s'] for x in v),cpu_ms_per_mib=statistics.median(x['cpu_ms_per_mib'] for x in v),hw_calls=sum(x['calls'] for x in v)))
(d/'summary.json').write_text(json.dumps(summary,indent=2)+'\n')
def val(alg,size,backend,op='enc',source='all-v4-final.csv'):
 return next(x for x in summary if x['algorithm']==alg and x['input_bytes']==size and x['backend']==backend and x['operation']==op and x['source']==source)
lines=['# CryptoDMA measurements — 2026-09-09','',
'Physical SG2002/C906, NanoKVM OS, capture disabled, NCM disabled. Each cell is the median of three rounds. Paired OpenSSL hardware/software rounds alternate; Go and ChaCha-only runs are separate. Hardware includes ioctl, CPU copies, DMA synchronization and completion polling; software uses the installed OpenSSL 3.6.4 default provider (DES/TDES use its low-level software implementation). Kernel driver counters verify hardware submissions and no software fallback. Large inputs are split into at most 4096-byte DMA requests. CPU includes process user + system time, not whole-system CPU percentage. Scheduling can lower wall throughput independently of CPU cost.','',
'## All native algorithms, 64 KiB input','',
'Key setup is repeated for each whole operation in this table; this is not pure engine bandwidth. Hash padding/chaining and Base64 expansion are included. Base64 decode uses 87,384 encoded input bytes representing 65,536 original bytes.','',
'| Algorithm | Operation | HW MiB/s | SW MiB/s | HW CPU ms/MiB | SW CPU ms/MiB | CPU work saved |',
'|---|---|---:|---:|---:|---:|---:|']
for x in summary:
 if x['source']!='all-v4-final.csv' or x['backend']!='hw' or x['input_bytes'] not in (65536,87384):continue
 y=val(x['algorithm'],x['input_bytes'],'sw',x['operation'])
 lines.append(f"| {x['algorithm']} | {x['operation']} | {x['wall_mib_s']:.2f} | {y['wall_mib_s']:.2f} | {x['cpu_ms_per_mib']:.2f} | {y['cpu_ms_per_mib']:.2f} | {100*(1-x['cpu_us']/y['cpu_us']):.1f}% |")
lines += ['', '## AES-GCM: persistent key, nonce + 17-byte AAD + 16-byte tag included','',
'The hybrid uses CryptoDMA AES ECB for GCM setup/tag and CTR for the payload, with software GHASH through the OpenSSL GCM core. Encrypt, decrypt, partial-block boundaries and invalid-tag rejection are checked against software OpenSSL. These are differential tests, not a certification. Neither TLS nor SRTP is switched to this hybrid by the benchmark.','',
'| Algorithm | Bytes | Backend | MiB/s | CPU us/record |','|---|---:|---|---:|---:|']
for x in summary:
 if x['input_bytes'] not in (1200,16384,65536) or x['source']=='all-v4-final.csv':continue
 if x['source']=='ghash-go-patched.csv' and x['algorithm']=='GHASH':continue
 lines.append(f"| {x['algorithm']} | {x['input_bytes']} | {x['backend']} | {x['wall_mib_s']:.2f} | {x['cpu_us']:.2f} |")
(d/'TABLES.md').write_text('\n'.join(lines)+'\n')
for alg in ['AES-256-GCM','GHASH']:
 for x in summary:
  if x['algorithm']==alg and x['input_bytes']==16384: print(alg,x['backend'],round(x['wall_mib_s'],2),round(x['cpu_us'],2))