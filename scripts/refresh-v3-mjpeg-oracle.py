from pathlib import Path
from datetime import datetime,timezone
import hashlib,json,os,re,subprocess,tempfile
repo=Path(__file__).resolve().parents[1];baseline='a53b25579ab87cc98f85323b4743deb0d4da907b'
goroot=Path('/home/dgrace/nanokvm-astra/work/v2.1-b1-20261004/platform/server/goroot')
paths=['server/service/stream/mjpeg/streamer.go','server/service/stream/mjpeg/mjpeg.go','server/service/stream/mjpeg/streamer_test.go','server/service/stream/mjpeg/delivery_test.go','server/service/stream/mjpeg/cache_ownership_test.go','server/service/stream/mjpeg/frame-detect.go','server/proto/stream.go','server/proto/request.go','server/proto/response.go','server/go.mod','server/go.sum']
sources={p:subprocess.check_output(['git','show',f'{baseline}:{p}'],cwd=repo) for p in paths}
output=repo/'docs/experiments/v3.0/mjpeg-go-oracle.json';assert not output.exists()
checkpoint=repo/'CHECKPOINT.md';cp=checkpoint.read_text();old='The goal remains active: 64 isolated routes, one partial and 139 pending do not constitute completion.';assert cp.count(old)==1;assert '## In-progress MJPEG' not in cp
with (repo/'docs/experiments/v3.0/actions.md').open('a') as f:f.write('\n'+datetime.now(timezone.utc).isoformat()+' Previous goal turn classified PROGRESS: qualified Direct21new/261host+target, four routes/68isolated/1partial/135pending, e75e206 clean source proof verified. Before remaining package3: generate complete immutable MJPEG streamer/client/delivery/cache/write-frame reference and frame-detect actual Gin/proto handlers. Only common native/status/FPS/viewer and Sleep clock boundaries supplied; no device or Go runtime bridge. Original10 MJPEG tests, image pairs, delivery/queue and actual binding/side-effect traces. Correct stale goal paragraph64count to current68; no unchanged Direct/source/ABI oracle repetition. New-file/anchor prevalidation before mutations.\n')
cp=cp.replace(old,'The goal remains active:68 isolated routes, one partial and135 pending do not constitute completion.')
checkpoint.write_text(cp+'\n## In-progress MJPEG/frame-detect\nDirect e75e206/261 tests/68 routes is frozen and committed. Begin missing complete MJPEG reference and frame-detect API binding/effects; then connect shared native capture/multipart/cache/cancellation and temporary-detect intent. No new qualified route yet; goal ACTIVE; device read-only.\n')
with tempfile.TemporaryDirectory(prefix='nk-v3-mjpeg-oracle-') as directory:
 root=Path(directory)
 for d in ['common','service/stream','service/vm','service/stream/mjpeg','proto']:(root/d).mkdir(parents=True,exist_ok=True)
 for p,data in sources.items():
  if p.startswith('server/service/stream/mjpeg/'):
   dest=root/p.removeprefix('server/');text=data.decode();
   if p.endswith('/frame-detect.go'):
    assert text.count('time.Sleep(duration)')==1;text=text.replace('time.Sleep(duration)','fixtureSleep(duration)')
   dest.write_text(text)
  elif p.startswith('server/proto/'):(root/p.removeprefix('server/')).write_bytes(data)
  elif p in ['server/go.mod','server/go.sum']:(root/Path(p).name).write_bytes(data)
 (root/'common/fixture.go').write_text('''package common
type Screen struct {Width,Height,Quality uint16;FPS uint8}
type Vision struct {Calls []uint8}
var FixtureVision= &Vision{}
func (*Vision)ReadMjpeg(uint16,uint16,uint16)([]byte,int){return nil,-5}
func (v *Vision)SetFrameDetect(value uint8){v.Calls=append(v.Calls,value)}
func GetKvmVision()*Vision{return FixtureVision}
func CheckScreen(){}
func GetCaptureScreen()Screen{return Screen{Quality:80,FPS:50}}
''')
 (root/'service/stream/fixture.go').write_text('''package stream
const CaptureModeMJPEG="mjpeg"
func UpdateCaptureStatus(string,int){}
type Counter struct{}
func(*Counter)Update(){}
func GetFrameRateCounter()*Counter{return &Counter{}}
''')
 (root/'service/vm/fixture.go').write_text('package vm\nfunc UpdateHdmiViewerSnapshot(string,int,uint64){}\n')
 cases=[]
 update_bodies=['{}','null','[]','{"enabled":true}','{"Enabled":true}','{"enabled":false}','{"enabled":true,"enabled":null}','{"enabled":"true"}','{"enabled":1}','{"enabled":false,"enabled":true}','{"enabled":true,"enabled":1}','{"enabled":true} false','{"enabled":true,"unknown":1e9999}']
 stop_bodies=['{}','null','[]','{"duration":1}','{"Duration":2}','{"duration":0}','{"duration":-1}','{"duration":10}','{"duration":1,"duration":null}','{"duration":"1"}','{"duration":1.0}','{"duration":9223372036854775807}','{"duration":9223372036854775808}','{"duration":9223372037}','{"duration":-9223372036854775808}','{"duration":2,"duration":1}','{"duration":1} true']
 for kind,bodies in [('update',update_bodies),('stop',stop_bodies)]:
  for body in bodies:cases.append(dict(kind=kind,body=body,contentType='application/json',query=''))
  for ct in ['', 'application/x-www-form-urlencoded','application/json; charset=utf-8','text/plain','garbage;=bad']:
   for body in (["Enabled=true","enabled=true","Enabled=1","Enabled=TRUE","Enabled=invalid","Enabled=true&Enabled=false","Enabled=","Enabled=%ZZ"] if kind=='update' else ['Duration=1','duration=1','Duration=+1','Duration=%2B1','Duration=-1','Duration=','Duration=9223372036854775807','Duration=9223372036854775808','Duration=1&Duration=2','Duration=%ZZ']):
    cases.append(dict(kind=kind,body=body,contentType=ct,query=''))
  for body,query in ([('Enabled=false','Enabled=true'),('','Enabled=true'),('enabled=true','Enabled=false'),('Enabled=','Enabled=true'),('Enabled=%ZZ','Enabled=true')] if kind=='update' else [('Duration=2','Duration=1'),('','Duration=1'),('duration=2','Duration=3'),('Duration=','Duration=1'),('Duration=%ZZ','Duration=1')]):
   cases.append(dict(kind=kind,body=body,contentType='application/x-www-form-urlencoded',query=query))
 (root/'service/stream/mjpeg/cases.json').write_text(json.dumps(cases))
 custom=r'''package mjpeg
import("NanoKVM-Server/common";"bytes";"context";"encoding/json";"net/http/httptest";"os";"testing";"time";"github.com/gin-gonic/gin")
var slept []int64
func fixtureSleep(duration time.Duration){slept=append(slept,int64(duration))}
type BindingCase struct{Kind,Body,ContentType,Query string}
func TestMjpegContractOracle(t *testing.T){
 gin.SetMode(gin.ReleaseMode)
 var cases []BindingCase;encoded,_:=os.ReadFile("cases.json");if err:=json.Unmarshal(encoded,&cases);err!=nil{t.Fatal(err)}
 var bindings []map[string]any
 for _,item:=range cases{
  common.FixtureVision.Calls=nil;slept=nil
  writer:=httptest.NewRecorder();c,_:=gin.CreateTestContext(writer);c.Request=httptest.NewRequest("POST","/?"+item.Query,bytes.NewBufferString(item.Body));if item.ContentType!=""{c.Request.Header.Set("Content-Type",item.ContentType)}
  if item.Kind=="update"{UpdateFrameDetect(c)}else{StopFrameDetect(c)}
  var response any;if err:=json.Unmarshal(writer.Body.Bytes(),&response);err!=nil{t.Fatal(err)}
  calls:=[]int{};for _,v:=range common.FixtureVision.Calls{calls=append(calls,int(v))};delays:=[]int64{};delays=append(delays,slept...)
  bindings=append(bindings,map[string]any{"case":item,"status":writer.Code,"response":response,"calls":calls,"sleeps":delays})
 }
 var pairs []map[string]any
 appendPair:=func(a,b []byte){pairs=append(pairs,map[string]any{"a":a,"b":b,"same":sameMjpegImage(a,b)})}
 prefix:=[]byte{255,216,255,233,0,4,1,255}
 for size:=0;size<=32;size++{
  a:=make([]byte,size);copy(a,prefix);for i:=8;i<size;i++{a[i]=byte(i*31)}
  appendPair(a,append([]byte{},a...))
  for index:=0;index<size;index++{b:=append([]byte{},a...);b[index]++;appendPair(a,b)}
  appendPair(a,append(append([]byte{},a...),0))
 }
 appendPair([]byte{1,2,3},[]byte{1,2,4});appendPair(nil,nil)
 clients:=[]*mjpegClient{};cancels:=[]context.CancelFunc{}
 for i:=0;i<3;i++{ctx,cancel:=context.WithCancel(context.Background());clients=append(clients,newMjpegClient(ctx));cancels=append(cancels,cancel)}
 delivery:=frameDelivery{};now:=time.Unix(100,0)
 type Op struct{Kind string;Data []byte;Offset int64;Members []int;Client int}
 a:=append(append([]byte{},prefix...),9,10,11);b:=append([]byte{},a...);b[6]++;changed:=append([]byte{},a...);changed[9]++
 ops:=[]Op{
  {Kind:"offer",Data:a,Members:[]int{0}},{Kind:"pop",Client:0},
  {Kind:"offer",Data:b,Offset:1e9,Members:[]int{0}},{Kind:"offer",Data:b,Offset:2e9,Members:[]int{0,1}},{Kind:"pop",Client:1},
  {Kind:"offer",Data:b,Offset:5e9,Members:[]int{0,1}},{Kind:"pop",Client:0},
  {Kind:"offer",Data:changed,Offset:6e9,Members:[]int{0,1}},
  {Kind:"offer",Data:a,Offset:7e9,Members:[]int{0,1}},{Kind:"pop",Client:0},{Kind:"pop",Client:1},
  {Kind:"reset"},{Kind:"offer",Data:a,Offset:8e9,Members:[]int{0,1}},
  {Kind:"cancel",Client:1},{Kind:"offer",Data:a,Offset:9e9,Members:[]int{0,1,2}},{Kind:"pop",Client:2},
  {Kind:"offer",Data:a,Offset:13e9,Members:[]int{0,1,2}},
 }
 var steps []map[string]any
 for _,op:=range ops{
  sent:=false;var popped []byte;ok:=false
  members:=[]*mjpegClient{};for _,id:=range op.Members{members=append(members,clients[id])}
  switch op.Kind{
  case "offer":sent=delivery.offer(members,op.Data,now.Add(time.Duration(op.Offset)))
  case "pop":if len(clients[op.Client].frames)>0{popped,ok=clients[op.Client].next()}
  case "reset":delivery.last=nil
  case "cancel":cancels[op.Client]()
  }
  states:=[]map[string]any{}
  for _,c:=range clients{
   closed:=c.ctx.Err()!=nil;pending:=len(c.frames)>0;var data []byte
   if pending&&!closed{data=<-c.frames;c.frames<-data}
   at:=int64(0);if !c.lastOffer.IsZero(){at=c.lastOffer.Sub(now).Nanoseconds()+1}
   states=append(states,map[string]any{"closed":closed,"pending":pending&&!closed,"data":data,"generation":c.generation,"offeredAt":at})
  }
  steps=append(steps,map[string]any{"operation":op,"sent":sent,"popped":popped,"ok":ok,"generation":delivery.generation,"last":delivery.last,"clients":states,"ready":viewersReady(members)})
 }
 recorder:=httptest.NewRecorder();c,_:=gin.CreateTestContext(recorder);c.Request=httptest.NewRequest("GET","/",nil);controller:=newResponseController(c.Writer)
 images:=[][]byte{{255,216,1,255,217},{255,216,2,255,217},{255,216,3,255,217}};var chunks [][]byte
 for index,image:=range images{before:=recorder.Body.Len();if err:=writeFrame(c,controller,image,index==0);err!=nil{t.Fatal(err)};chunks=append(chunks,append([]byte{},recorder.Body.Bytes()[before:]...))}
 result:=map[string]any{"bindings":bindings,"pairs":pairs,"delivery":steps,"multipartChunks":chunks,"images":images,"partHeader":mjpegPartHeader,"nextPart":mjpegNextPart,"duplicateRefreshNanos":int64(duplicateRefreshInterval),"writeTimeoutNanos":int64(clientWriteTimeout)}
 data,err:=json.Marshal(result);if err!=nil{t.Fatal(err)};if err=os.WriteFile(os.Getenv("NK_ORACLE_OUT"),data,0600);err!=nil{t.Fatal(err)}
}
'''
 (root/'service/stream/mjpeg/contract_oracle_test.go').write_text(custom)
 env=dict(os.environ,GOROOT=str(goroot),PATH=str(goroot/'bin')+':'+os.environ['PATH'],GOTOOLCHAIN='local',GOWORK='off',CGO_ENABLED='0',GIN_MODE='release',NK_ORACLE_OUT=str(root/'oracle.json'))
 run=subprocess.run([str(goroot/'bin/go'),'test','-v','./service/stream/mjpeg'],cwd=root,env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True)
 if run.returncode:print(run.stdout[-16000:]);raise SystemExit(run.returncode)
 result=json.loads((root/'oracle.json').read_text());result.update({'baseline':baseline,'source_sha256':{p:hashlib.sha256(data).hexdigest() for p,data in sources.items()},'original_tests':re.findall(r'^--- PASS: (Test(?!MjpegContractOracle)\w+)',run.stdout,re.M),'go_version':subprocess.check_output([str(goroot/'bin/go'),'version'],env=env,text=True).strip(),'boundaries':['common native capture/frame-detect, capture status/FPS and HDMI viewer notifications are fixtures','time.Sleep in frame-detect handler is the sole clock boundary; requested signed duration is recorded','Browser fixture test excluded; actual Chrome Main proof remains required']})
 assert len(result['original_tests'])==10,result['original_tests'];assert len(result['bindings'])==130,len(result['bindings'])
 output.write_text(json.dumps(result,indent=2)+'\n')
 print(json.dumps({'original_tests':len(result['original_tests']),'bindings':len(result['bindings']),'pairs':len(result['pairs']),'delivery_steps':len(result['delivery'])}))
with (repo/'docs/experiments/v3.0/actions.md').open('a') as f:f.write('\n'+datetime.now(timezone.utc).isoformat()+' MJPEG/frame-detect Go reference after:10 original tests pass;130 handler/binding/side-effect cases, byte-pair/delivery/multipart traces generated from complete immutable sources. Frame-detect Sleep replaced only by recorded requested duration clock boundary; actual timer/restore/C/API and MJPEG Rust/native/socket/browser proof next. No native/device or Go bridge execution.\n')
