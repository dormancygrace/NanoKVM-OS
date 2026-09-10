from pathlib import Path
import json, sys, statistics

p=Path(sys.argv[1]); samples=[]; current=None
for line in p.read_text(encoding='utf-8-sig').splitlines():
    if line.startswith('NKCORE_SAMPLE '):
        current={'uptime':float(line.split()[2])}; samples.append(current)
    elif current is not None:
        if line.startswith('{'): current['core']=json.loads(line)
        elif line.startswith('cpu '): current['cpu']=[int(v) for v in line.split()[1:9]]
        elif ') ' in line:
            v=line.split(') ',1)[1].split(); current['server_ticks']=int(v[11])+int(v[12])
        elif line.startswith('VIDEO_FPS '): current['fps']=float(line.split()[1])
        elif line.startswith('MemAvailable:'): current['available_kib']=int(line.split()[1])
        elif line.startswith('CmaFree:'): current['cma_free_kib']=int(line.split()[1])
valid=[s for s in samples if all(k in s for k in ('core','cpu','server_ticks','fps','available_kib','cma_free_kib'))]
assert len(valid)==len(samples) and len(valid)>1
a,b=valid[0],valid[-1]
assert all(s['core']['mode']==0 for s in valid), 'Compare idle FreeRTOS windows only'
total=sum(b['cpu'])-sum(a['cpu']); idle=b['cpu'][3]+b['cpu'][4]-a['cpu'][3]-a['cpu'][4]
result={'file':p.name,'samples':len(valid),'complete':p.read_text().rstrip().endswith('NKCORE_SAMPLE_END'),'elapsed_s':b['uptime']-a['uptime'],'main_cpu_pct':100*(total-idle)/total,'server_cpu_pct':100*(b['server_ticks']-a['server_ticks'])/total,'fps_mean':statistics.mean(s['fps'] for s in valid),'fps_min':min(s['fps'] for s in valid),'fps_max':max(s['fps'] for s in valid),'available_kib_min':min(s['available_kib'] for s in valid),'cma_free_kib_min':min(s['cma_free_kib'] for s in valid),'core_max_errors':max(s['core']['errors'] for s in valid),'linux_loss_events':max(s['core']['linux_loss_events'] for s in valid)}
p.with_suffix('.summary.json').write_text(json.dumps(result,indent=2)+'\n');print(json.dumps(result,indent=2))
