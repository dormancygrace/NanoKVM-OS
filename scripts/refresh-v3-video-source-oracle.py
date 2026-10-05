from pathlib import Path
import subprocess, hashlib, json, os, tempfile
from datetime import datetime, timezone

root=Path(__file__).resolve().parents[1]
baseline='a53b25579ab87cc98f85323b4743deb0d4da907b'
go=Path('/home/dgrace/nanokvm-astra/work/v2.1-b1-20261004/platform/server/goroot/bin/go')
paths=['server/service/stream/video_source.go','server/service/stream/video_source_test.go','server/service/stream/encoder_config.go']
sources={name:subprocess.check_output(['git','show',f'{baseline}:{name}'],cwd=root) for name in paths}
for name,data in sources.items(): assert data.startswith(b'package stream\n'),name
source=sources[paths[0]].decode()
assert source.count('"NanoKVM-Server/common"')==1
fixture_path=root/'docs/experiments/v3.0/video-source-go-oracle.json'
assert not fixture_path.exists(), 'oracle collision'
with (root/'docs/experiments/v3.0/actions.md').open('a') as f:
 f.write(f'\n{datetime.now(timezone.utc).isoformat()} Before shared-source Go reference: execute complete immutable baseline video_source.go/encoder_config.go and all original source tests in a temporary module; only package/common import relocated, local common screen/native/FPS fixture values supplied. Produce native-free queue/portrait/cadence/keyframe/conflict values for Rust differential checks. No Go runtime packaging, native/device/host action or unchanged oracle regeneration.\n')
with tempfile.TemporaryDirectory(prefix='v3-video-go-') as directory:
 fixture=Path(directory)
 (fixture/'common').mkdir()
 (fixture/'go.mod').write_text('module fixture\n\ngo 1.27\n')
 (fixture/'common/common.go').write_text('''package common
type Screen struct{Width,Height,BitRate uint16;GOP uint8;FPS int}
func GetScreen() Screen{return Screen{FPS:50,BitRate:3000,GOP:30}}
func GetCaptureScreen() Screen{return GetScreen()}
func CheckScreen(){}
func ReadVideoValue(string)int{return 0}
type Vision struct{}
func GetKvmVision()*Vision{return &Vision{}}
func(*Vision)RequestKeyframe(){}
func(*Vision)ReadVideoWithHeadroom(uint16,uint16,uint8,uint16,uint8,uint8,int)([]byte,[]byte,int){return nil,nil,0}
''')
 for name,data in sources.items():
  data=data.decode().replace('package stream\n','package main\n',1).replace('"NanoKVM-Server/common"','"fixture/common"')
  (fixture/Path(name).name).write_text(data)
 (fixture/'oracle.go').write_text('''package main
import("encoding/json";"fmt";"time")
type FrameRateCounter struct{}
func GetFrameRateCounter()*FrameRateCounter{return &FrameRateCounter{}}
func(*FrameRateCounter)Update(){}
func main(){
 sequences:=[][]int{{4,-1,0,3,4,-3,3,4},{-5,-1,-2},{3,4,3,4}}
 full:=[]int{3};for i:=0;i<23;i++{full=append(full,4)}
 for _,tail:=range [][]int{{4,4,3,4},{3,4},{-3,-5,3,4}}{seq:=append([]int{},full...);sequences=append(sequences,append(seq,tail...))}
 errors:=[]int{};for i:=0;i<26;i++{errors=append(errors,-1)};sequences=append(sequences,append(errors,4,3,4))
 queues:=[]any{};for _,seq:=range sequences{s:=&VideoSource{};sub:=&VideoSubscription{source:s,frames:make(chan VideoFrame,24),done:make(chan struct{}),waitingForKeyframe:true};steps:=[]any{}
 for i,result:=range seq{accepted:=sub.send(VideoFrame{Result:result,Timestamp:int64(i)});steps=append(steps,map[string]any{"result":result,"accepted":accepted,"waiting":sub.waitingForKeyframe,"requested":s.keyframeRequested.Load(),"queued":len(sub.frames)})}
 frames:=[]any{};for len(sub.frames)>0{f:=<-sub.frames;frames=append(frames,map[string]any{"result":f.Result,"timestamp":f.Timestamp})};queues=append(queues,map[string]any{"steps":steps,"frames":frames})}
 portraits:=[]any{};for _,codec:=range []VideoCodec{VideoCodecH264,VideoCodecH265}{for _,height:=range []uint16{0,600,720,1080,1439,1440,2560}{for _,input:=range [][2]int{{1440,2560},{2560,1440},{1088,1920},{1296,2304},{0,0}}{portraits=append(portraits,map[string]any{"codec":codec,"streamHeight":height,"width":input[0],"height":input[1],"blocked":portraitCodecBlocked(codec,height,input[0],input[1])})}}}
 cadence:=[]any{};start:=time.Unix(1000,0);for _,fps:=range []int{30,50,75,120}{period:=time.Second/time.Duration(fps);for _,offset:=range []time.Duration{0,2*time.Millisecond,period+time.Millisecond,10*period}{cadence=append(cadence,map[string]any{"period":int64(period),"offset":int64(offset),"next":int64(advanceCaptureDeadline(start,start.Add(offset),period).Sub(start))})}}
 keys:=[]any{};s:=&VideoSource{};var last time.Time;for _,step:=range []struct{offset time.Duration;request bool}{{0,false},{0,true},{time.Second,false},{time.Second,true},{time.Second+499*time.Millisecond,true},{time.Second+500*time.Millisecond,false},{2*time.Second,false}}{if step.request{s.keyframeRequested.Store(true)};taken:=s.takeKeyframeRequest(start.Add(step.offset),&last);keys=append(keys,map[string]any{"offset":int64(step.offset),"request":step.request,"taken":taken,"pending":s.keyframeRequested.Load()})}
 conflict:=(&EncoderConfigConflictError{Active:LegacyEncoderConfig(),Requested:DefaultEncoderConfig()}).Error()
 result:=map[string]any{"queues":queues,"portraits":portraits,"cadence":cadence,"keyframes":keys,"conflict":conflict};data,_:=json.Marshal(result);fmt.Println(string(data))
}
''')
 env={**os.environ,'GOTOOLCHAIN':'local','GOWORK':'off'}
 tested=subprocess.run([str(go),'test','-count=1','-v','.'],cwd=fixture,env=env,text=True,capture_output=True,check=True)
 result=json.loads(subprocess.check_output([str(go),'run','.'],cwd=fixture,env=env,text=True))
 result['baseline']=baseline
 result['sources']={name:hashlib.sha256(data).hexdigest() for name,data in sources.items()}
 result['fixture_changes']=['package renamed to main','common import relocated; screen/native methods and FPS counter are fixture values, no native execution']
 result['original_tests']=[line.split()[2] for line in tested.stdout.splitlines() if line.startswith('--- PASS:')]
 result['go_version']=subprocess.check_output([str(go),'version'],text=True).strip()
 fixture_path.write_text(json.dumps(result,indent=2)+'\n')
 print(json.dumps({'original_tests':len(result['original_tests']),'queues':len(result['queues']),'queue_steps':sum(len(case['steps']) for case in result['queues']),'portraits':len(result['portraits']),'cadence':len(result['cadence']),'keyframes':len(result['keyframes'])}))
