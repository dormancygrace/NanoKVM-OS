from pathlib import Path
import subprocess,tempfile,json,hashlib,os
repo=Path(__file__).resolve().parents[1];baseline='a53b25579ab87cc98f85323b4743deb0d4da907b'
goroot=Path('/home/dgrace/nanokvm-astra/work/v2.1-b1-20261004/platform/server/goroot')
paths=['server/service/stream/direct/client.go','server/service/stream/direct/client_test.go','server/service/stream/encoder_config.go','server/go.mod','server/go.sum']
sources={name:subprocess.check_output(['git','show',f'{baseline}:{name}'],cwd=repo) for name in paths}
output=repo/'docs/experiments/v3.0/direct-queue-go-oracle.json';assert not output.exists()
with (repo/'docs/experiments/v3.0/actions.md').open('a') as f:
 f.write('\nBefore Direct queue oracle: run complete immutable client.go and all seven original queue/headroom/ACK tests against pinned Go modules; only package/import positioning and RequestKeyframe fixture supplied. Generate default8frame/2MiB queue traces for flow1/8/uncontrolled, signed ACK, discontinuity retaining credits, resync clearing credits, key replacement, frame/byte overflow and close. No native/device/network execution.\n')
with tempfile.TemporaryDirectory(prefix='nk-v3-direct-queue-') as directory:
 module=Path(directory);(module/'service/stream').mkdir(parents=True)
 (module/'go.mod').write_bytes(sources['server/go.mod']);(module/'go.sum').write_bytes(sources['server/go.sum'])
 for name in ['server/service/stream/direct/client.go','server/service/stream/direct/client_test.go']:
  (module/Path(name).name).write_text(sources[name].decode().replace('package direct\n','package main\n',1))
 (module/'service/stream/encoder_config.go').write_bytes(sources['server/service/stream/encoder_config.go'])
 (module/'service/stream/stub.go').write_text('package stream\nfunc RequestKeyframe(){}\n')
 (module/'main.go').write_text(r'''package main
import("bytes";"encoding/json";"fmt")
type op struct{Kind string;Key bool;Timestamp int64;Size int}
func main(){
cases:=[][]op{
{{Kind:"offer",Key:false,Timestamp:1,Size:1},{Kind:"offer",Key:true,Timestamp:10,Size:1},{Kind:"pop"},{Kind:"offer",Timestamp:20,Size:1},{Kind:"pop"},{Kind:"ack",Timestamp:-1},{Kind:"pop"},{Kind:"ack",Timestamp:10},{Kind:"pop"},{Kind:"discontinuity"},{Kind:"offer",Timestamp:30,Size:1},{Kind:"offer",Key:true,Timestamp:40,Size:1},{Kind:"pop"},{Kind:"resync"},{Kind:"offer",Key:true,Timestamp:50,Size:1},{Kind:"pop"},{Kind:"close"},{Kind:"offer",Key:true,Timestamp:60,Size:1}},
{{Kind:"offer",Key:true,Timestamp:10,Size:1},{Kind:"pop"},{Kind:"offer",Key:true,Timestamp:20,Size:1},{Kind:"pop"},{Kind:"ack",Timestamp:9},{Kind:"ack",Timestamp:10},{Kind:"pop"},{Kind:"ack",Timestamp:9223372036854775807},{Kind:"pop"}},
{{Kind:"offer",Key:true,Timestamp:1,Size:2097152-9},{Kind:"offer",Timestamp:2,Size:1},{Kind:"offer",Key:true,Timestamp:3,Size:2097152-8},{Kind:"pop"},{Kind:"offer",Key:true,Timestamp:4,Size:1},{Kind:"pop"}},
};full:=[]op{{Kind:"offer",Key:true,Timestamp:1,Size:1}};for i:=2;i<=10;i++{full=append(full,op{Kind:"offer",Timestamp:int64(i),Size:1})};full=append(full,op{Kind:"offer",Key:true,Timestamp:20,Size:1},op{Kind:"pop"});cases=append(cases,full)
results:=[]any{};for _,window:=range []int{0,1,8}{for _,operations:=range cases{q:=newFrameQueue(defaultQueueFrames,defaultQueueBytes);if window>0{q.enableFlowControl(window)};steps:=[]any{};for _,op:=range operations{accepted:=false;var popped any;switch op.Kind{case "offer":accepted=q.offer(newOutboundFrame(op.Key,op.Timestamp,nil,bytes.Repeat([]byte{'x'},op.Size)));case "pop":if f:=q.popForWrite();f!=nil{popped=map[string]any{"key":f.key,"timestamp":f.timestamp,"size":len(f.payload)}};case "ack":q.acknowledge(op.Timestamp);case "resync":q.requestResync();case "discontinuity":q.markDiscontinuity();case "close":q.close()};frames:=[]any{};for _,f:=range q.frames{frames=append(frames,map[string]any{"key":f.key,"timestamp":f.timestamp,"size":len(f.payload)})};pending:=append([]int64{},q.inFlight...);steps=append(steps,map[string]any{"operation":op,"accepted":accepted,"popped":popped,"frames":frames,"bytes":q.queuedBytes,"pending":pending,"waiting":q.waitingForKeyframe,"closed":q.closed,"canAdvance":q.canAdvanceStream()})};results=append(results,map[string]any{"window":window,"steps":steps})}}
data,_:=json.Marshal(map[string]any{"cases":results});fmt.Println(string(data))
}
''')
 env={**os.environ,'GOROOT':str(goroot),'GOTOOLCHAIN':'local','CGO_ENABLED':'0','GOCACHE':str(repo/'work/v3/gocache')}
 tested=subprocess.run([str(goroot/'bin/go'),'test','-mod=readonly','-count=1','-v','.'],cwd=module,env=env,text=True,capture_output=True)
 if tested.returncode:print(tested.stderr+tested.stdout);raise SystemExit(tested.returncode)
 result=json.loads(subprocess.check_output([str(goroot/'bin/go'),'run','-mod=readonly','.'],cwd=module,env=env,text=True))
 result.update(baseline=baseline,source_sha256={name:hashlib.sha256(data).hexdigest() for name,data in sources.items()},original_tests=[line.split()[2] for line in tested.stdout.splitlines() if line.startswith('--- PASS:')],go_version='1.27.1')
 output.write_text(json.dumps(result,indent=2)+'\n');print(json.dumps({'cases':len(result['cases']),'steps':sum(len(case['steps']) for case in result['cases']),'original_tests':len(result['original_tests'])}))
