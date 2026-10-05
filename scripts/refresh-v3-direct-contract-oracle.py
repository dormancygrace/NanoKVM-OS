from pathlib import Path
import subprocess,tempfile,hashlib,json,os
from datetime import datetime,timezone
repo=Path(__file__).resolve().parents[1]
baseline='a53b25579ab87cc98f85323b4743deb0d4da907b'
goroot=Path('/home/dgrace/nanokvm-astra/work/v2.1-b1-20261004/platform/server/goroot')
paths=['server/service/stream/encoder_config.go','server/service/stream/encoder_config_test.go','server/service/stream/capture_status.go']
sources={name:subprocess.check_output(['git','show',f'{baseline}:{name}'],cwd=repo) for name in paths}
oracle=repo/'docs/experiments/v3.0/direct-contract-go-oracle.json'
printable=repo/'server-rust/src/go_printable.rs'
license=repo/'server-rust/native/licenses/go-stdlib.txt'
assert not oracle.exists() and not printable.exists() and not license.exists()
assert all(data.startswith(b'package stream\n') for data in sources.values())
with (repo/'docs/experiments/v3.0/actions.md').open('a') as f:
 f.write(f'\n{datetime.now(timezone.utc).isoformat()} Previous goal turn classified progress: source/Runtime/Actor/HDMI implementation and corrected240 host/target proof committed1086fa8; clean authoritative tree. Before Direct connection: generate new complete immutable codec/capture-status reference and exact Go quoted-byte/Unicode printable data from qualified Go1.27.1; no unchanged oracle regeneration. Static generated facts plus BSD license are runtime data, no Go server/bridge/runtime invocation. Next native Direct/auth/state/FPS/WS implementation; frame-detect/MJPEG/audio/WebRTC still required. No native vendor/device/host settings action. New-file collisions prevalidated.\n')
with tempfile.TemporaryDirectory(prefix='nk-v3-direct-contract-') as directory:
 module=Path(directory)
 (module/'go.mod').write_text('module fixture\n\ngo 1.27\n')
 for name,data in sources.items(): (module/Path(name).name).write_text(data.decode().replace('package stream\n','package main\n',1))
 (module/'oracle.go').write_text(r'''package main
import("crypto/sha256";"encoding/hex";"encoding/json";"fmt";"net/url";"strconv";"time";"unicode/utf8")
func main(){
queries:=[]string{"","codec=h264","codec=h265&rc=cbr","codec=h264&codec=h265","codec=&codec=h264","codec=H264","codec=vp9","rc=vbr","rc=cbr&rc=vbr","codec=h264&bitrate=1&gop=0","x=%zz&codec=h264","codec=h264;x=x&rc=cbr","codec=%zz&codec=h264","codec=%FF","codec=%EF%BF%BD","codec=%25s","codec=%00","codec=%F0%9F%98%80","codec=%C2%A0","codec=%CC%81","codec=%E2%80%8B","flow=%2B8","flow=9999999999999999999999999999","flow=-1","flow=+8","flow=9&flow=1"}
for b:=0;b<256;b++{queries=append(queries,"codec="+url.QueryEscape(string([]byte{byte(b)})))}
configs:=[]any{};for _,query:=range queries{for _,fallback:=range []EncoderConfig{DefaultEncoderConfig(),LegacyEncoderConfig()}{values:=(&url.URL{RawQuery:query}).Query();config,err:=ParseEncoderConfig(values,fallback);message:="";if err!=nil{message=err.Error()};flow:=0;if value,err:=strconv.Atoi(values.Get("flow"));err==nil&&value>0{flow=value;if flow>8{flow=8}};configs=append(configs,map[string]any{"query":query,"fallback":fallback.Codec,"codec":config.Codec,"error":message,"flow":flow})}}
digest:=sha256.New();ranges:=[][2]int{};start:=-1;last:=-1
for r:=0;r<=utf8.MaxRune;r++{if r>=0xd800&&r<=0xdfff{continue};fmt.Fprint(digest,strconv.Quote(string(rune(r))));if strconv.IsPrint(rune(r)){if start<0{start=r};last=r}else if start>=0{ranges=append(ranges,[2]int{start,last});start=-1}}
if start>=0{ranges=append(ranges,[2]int{start,last})}
clock:=time.Unix(1000,0).UTC();s:=newStore(func()time.Time{clock=clock.Add(time.Nanosecond);return clock});sequence:=[]struct{mode string;result int}{{"mjpeg",-5},{"direct",3},{"h264",-7},{"direct",4},{"direct",-1},{"direct",-1},{"mjpeg",0},{"mjpeg",5},{"h264",-6},{"h264",-4},{"h264",-3},{"h264",-2},{"h264",-8},{"h264",0}};statuses:=[]any{};notifications:=0;s.Subscribe(func(CaptureStatus){notifications++});for _,step:=range sequence{s.UpdateCaptureStatus(step.mode,step.result);statuses=append(statuses,map[string]any{"mode":step.mode,"result":step.result,"latest":s.LatestCaptureStatuses(),"notifications":notifications,"timestamp":clock.Format(time.RFC3339Nano)})}
result:=map[string]any{"config":configs,"printableRanges":ranges,"allScalarQuotedSHA256":hex.EncodeToString(digest.Sum(nil)),"captureStatuses":statuses};data,_:=json.Marshal(result);fmt.Println(string(data))
}
''')
 env={**os.environ,'GOROOT':str(goroot),'GOTOOLCHAIN':'local','GOWORK':'off','CGO_ENABLED':'0'}
 tested=subprocess.run([str(goroot/'bin/go'),'test','-count=1','-v','.'],cwd=module,env=env,text=True,capture_output=True,check=True)
 result=json.loads(subprocess.check_output([str(goroot/'bin/go'),'run','.'],cwd=module,env=env,text=True))
 result['baseline']=baseline;result['sources']={name:hashlib.sha256(data).hexdigest() for name,data in sources.items()}
 result['original_tests']=[line.split()[2] for line in tested.stdout.splitlines() if line.startswith('--- PASS:')]
 result['go_version']=subprocess.check_output([str(goroot/'bin/go'),'version'],text=True).strip()
 result['quote_source_sha256']={name:hashlib.sha256((goroot/name).read_bytes()).hexdigest() for name in ['src/strconv/quote.go','src/strconv/isprint.go']}
 ranges=result.pop('printableRanges')
 generated='//! Generated from qualified Go1.27.1 strconv.IsPrint; BSD-3-Clause license in native/licenses/go-stdlib.txt.\n//! Development generator: scripts/refresh-v3-direct-contract-oracle.py. No Go runtime is used.\npub(crate) const PRINTABLE: &[(u32,u32)] = &[\n'+''.join(f'    (0x{a:x},0x{b:x}),\n' for a,b in ranges)+'];\n'
 license.parent.mkdir(parents=True,exist_ok=True);license.write_bytes((goroot/'LICENSE').read_bytes())
 printable.write_text(generated);oracle.write_text(json.dumps(result,indent=2)+'\n')
 print(json.dumps({'config_cases':len(result['config']),'original_tests':len(result['original_tests']),'status_steps':len(result['captureStatuses']),'unicode_printable_ranges':len(ranges),'all_scalars_quote_digest':result['allScalarQuotedSHA256']}))
