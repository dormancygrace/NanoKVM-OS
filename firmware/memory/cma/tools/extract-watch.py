from pathlib import Path
import json, sys, statistics, re, collections

root=Path(__file__).parent; name=sys.argv[1]; path=root/name
events=[json.loads(line) for line in (path/'events.jsonl').read_text().splitlines()]
allowed={'ready','closed','heartbeat','heartbeat_end','ping','sysrq_sent','sysrq_error'}
safe=[e for e in events if e['kind'] in allowed]
(root/(name+'-numeric.jsonl')).write_text(''.join(json.dumps(e)+'\n' for e in safe))
groups=[]
for e in safe:
    if e['kind']=='heartbeat':
        if not groups or e['seq']<=groups[-1][-1]['seq']: groups.append([])
        groups[-1].append(e)
summaries=[]
for group in groups:
    pings=[e for e in safe if e['kind']=='ping' and group[0]['utc']<=e['utc']<=group[-1]['utc']]
    rtts=sorted(e['rtt_ms'] for e in pings if e['status']=='Success')
    summaries.append({'first_utc':group[0]['utc'],'last_utc':group[-1]['utc'],'heartbeats':len(group),'ping_count':len(pings),'ping_failures':sum(e['status']!='Success' for e in pings),'rtt_median_ms':statistics.median(rtts) if rtts else None,'rtt_p95_ms':rtts[min(len(rtts)-1,int(len(rtts)*.95))] if rtts else None,'rtt_max_ms':max(rtts) if rtts else None})
text=''.join(json.loads(line)['text'] for line in (path/'uart-private.jsonl').read_text().splitlines())
markers=collections.Counter()
for line in text.splitlines():
    # Exact typed markers only: no raw console text or data from the HDMI input.
    if "Can't acquire VB BLK for VPSS" in line: markers['vpss_no_vb']+=1
    if 'fill buffer NG' in line: markers['vpss_fill_failed']+=1
    if 'NKL_LINUX_HEARTBEAT_LOST C906L_ALIVE' in line: markers['c906l_linux_heartbeat_lost']+=1
    if 'incorrect alignment of CMA region' in line: markers['cma_bad_alignment']+=1
    if 'Kernel panic' in line: markers['kernel_panic_text']+=1
    if 'WARNING:' in line: markers['kernel_warning_text']+=1
result={'watch':name,'closed':events[-1]['kind']=='closed','window_definition':'Each restarted matching heartbeat sequence separately; planned boot outages outside these intervals are excluded. Windows include any in-window upload or test activity.','groups':summaries,'markers':dict(markers)}
(root/(name+'-summary.json')).write_text(json.dumps(result,indent=2)+'\n'); print(json.dumps(result,indent=2))
