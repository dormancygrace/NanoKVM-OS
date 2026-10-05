#!/usr/bin/env python3
"""Immutable HDMI handlers/state with native-only stubs and fixture path redirection."""
from pathlib import Path
import hashlib, json, os, subprocess, tempfile

REPO=Path(__file__).resolve().parents[1]
BASELINE='a53b25579ab87cc98f85323b4743deb0d4da907b'
def original(path):
    return subprocess.check_output(['git','show',BASELINE+':'+path],cwd=REPO)
names=['server/service/vm/hdmi.go','server/service/vm/hdmi_state/state.go','server/utils/hdmi.go','server/proto/request.go','server/proto/response.go','server/proto/vm.go','server/go.mod','server/go.sum']
sources={name:original(name) for name in names}
assert all((REPO/name).read_bytes()==data for name,data in sources.items())
cases=[]
def case(name,path,body='',content_type='application/json',query='',disabled=False,timeout='0',viewers=0):
    cases.append(dict(name=name,path=path,body=body,contentType=content_type,query=query,disabled=disabled,timeout=timeout,viewers=viewers))
for name,body in [('default','{}'),('maximum','{"Minutes":10080}'),('negative','{"Minutes":-1}'),('too-large','{"Minutes":10081}'),('fold','{"minutes":10}'),('null','null'),('null-no-overwrite','{"Minutes":5,"MINUTES":null}'),('duplicates','{"Minutes":5,"MINUTES":10}'),('fraction','{"Minutes":1.0}'),('exponent','{"Minutes":1e2}'),('string','{"Minutes":"1"}'),('overflow','{"Minutes":9223372036854775808}'),('early-error','{"Minutes":"1","Minutes":2}'),('trailing','{"Minutes":2} true'),('unknown','{"ignored":1e10000}'),('empty',''),('array','[]')]:
    case(name,'timeout',body)
for name,body in [('form','Minutes=10'),('form-space','Minutes=+10+'),('form-Unicode','Minutes=%C2%8510%E3%80%80'),('form-first','Minutes=10&Minutes=invalid'),('form-lower','minutes=10'),('form-empty','Minutes='),('form-negative','Minutes=-1'),('form-overflow','Minutes=9223372036854775808'),('form-semicolon','Minutes=1;foo=x'),('form-bad-escape','Minutes=%GG')]:
    case(name,'timeout',body,'application/x-www-form-urlencoded')
case('form-body-over-query','timeout','Minutes=10','application/x-www-form-urlencoded','Minutes=20')
case('query-only','timeout','','application/x-www-form-urlencoded','Minutes=10')
case('text-query','timeout','Minutes=20','text/plain','Minutes=10')
for name,path,disabled in [('enable','enable',True),('enable-absent','enable',False),('disable','disable',False),('disable-present','disable',True),('reset','reset',False),('reset-disabled','reset',True)]:case(name,path,disabled=disabled)
for name,disabled,timeout,viewers in [('state',False,'0',0),('state-disabled',True,'10',3),('state-viewers',False,' 10\n',3),('state-invalid-timeout',False,'10081',0),('state-invalid-number',False,'abc',0)]:case(name,'get',disabled=disabled,timeout=timeout,viewers=viewers)
steps=[dict(op='viewer',source='a',count=3,version=0),dict(op='viewer',source='a',count=8,version=0),dict(op='viewer',source='a',count=2,version=2),dict(op='viewer',source='a',count=0,version=1),dict(op='viewer',source='b',count=4,version=9),dict(op='viewer',source='c',count=-8,version=2),dict(op='lease'),dict(op='lease'),dict(op='release'),dict(op='warm'),dict(op='claim'),dict(op='claim'),dict(op='warm'),dict(op='clear'),dict(op='claim'),dict(op='viewer',source='a',count=0,version=3),dict(op='viewer',source='b',count=0,version=10),dict(op='release'),dict(op='release')]
with(REPO/'docs/experiments/v3.0/actions.md').open('a')as log:log.write('\nHDMI before immutable oracle execution: exact baseline handlers, demand state, utils, proto request/response and pinned go.mod/go.sum. Redirect only two persistent paths into local fixture; replace native vision with counters/signal stub. '+str(len(cases))+' actual API cases and '+str(len(steps))+' demand transitions. No vendor/native/hardware or host settings operation.\n')
platform=Path(os.environ.get('NK_V3_PLATFORM',REPO.parent/'work/v2.1-b1-20261004/platform'))
goroot=platform/'server/goroot'
env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(REPO/'work/v3/gocache'))
with tempfile.TemporaryDirectory(prefix='nk-v3-hdmi-oracle-')as tmp:
    root=Path(tmp);module=root/'module';module.mkdir();fixture=root/'fixture';fixture.mkdir()
    for name in ['go.mod','go.sum']:(module/name).write_bytes(sources['server/'+name])
    mapping={'service/vm/hdmi.go':'server/service/vm/hdmi.go','service/vm/hdmi_state/state.go':'server/service/vm/hdmi_state/state.go','proto/request.go':'server/proto/request.go','proto/response.go':'server/proto/response.go'}
    for dest,src in mapping.items():
        path=module/dest;path.parent.mkdir(parents=True,exist_ok=True);path.write_bytes(sources[src])
    proto=sources['server/proto/vm.go'].decode();definitions=[]
    for name in ['GetGetHdmiStateRsp','SetHdmiIdleTimeoutReq']:
        a=proto.index('type '+name+' struct {');b=proto.index('\n}',a)+2;definitions.append(proto[a:b])
    (module/'proto/hdmi.go').write_text('package proto\n'+'\n'.join(definitions))
    utils=sources['server/utils/hdmi.go'].decode()
    for filename in ['hdmi_disable','hdmi_idle_timeout']:
        utils=utils.replace(json.dumps('/etc/kvm/'+filename),json.dumps(str(fixture/filename)))
    (module/'utils').mkdir();(module/'utils/hdmi.go').write_text(utils)
    (module/'common').mkdir();(module/'common/stub.go').write_text('''package common
var Calls []bool
type KvmVision struct{}
func GetKvmVision()*KvmVision{return &KvmVision{}}
func(*KvmVision)SetHDMI(enabled bool){Calls=append(Calls,enabled)}
func(*KvmVision)HasHDMISignal()bool{return true}
''')
    (module/'service/vm/stub.go').write_text('package vm\ntype Service struct{}\n')
    main=r'''package main
import("encoding/json";"os";"strings";"net/http/httptest";"time";"github.com/gin-gonic/gin";"NanoKVM-Server/common";"NanoKVM-Server/service/vm";state "NanoKVM-Server/service/vm/hdmi_state";"NanoKVM-Server/utils")
type Case struct{Name,Path,Body,ContentType,Query,Timeout string;Disabled bool;Viewers int}
type Step struct{Op,Source string;Count int;Version uint64}
func main(){gin.SetMode(gin.ReleaseMode);var input struct{Cases []Case;Steps []Step};if e:=json.NewDecoder(os.Stdin).Decode(&input);e!=nil{panic(e)};out:=[]any{};s:=vm.Service{};version:=uint64(0);for _,c:=range input.Cases{os.Remove(utils.HDMIDisableFile);if c.Disabled{os.WriteFile(utils.HDMIDisableFile,nil,0644)};os.WriteFile(utils.HDMIIdleTimeoutFile,[]byte(c.Timeout),0644);common.Calls=nil;version++;vm.UpdateHdmiViewerSnapshot("oracle",c.Viewers,version);common.Calls=nil;rec:=httptest.NewRecorder();ctx,_:=gin.CreateTestContext(rec);ctx.Request=httptest.NewRequest("POST","http://localhost/api?"+c.Query,strings.NewReader(c.Body));ctx.Request.Header.Set("Content-Type",c.ContentType);switch c.Path{case "timeout":s.SetHdmiIdleTimeout(ctx);case "enable":s.EnableHdmi(ctx);case "disable":s.DisableHdmi(ctx);case "reset":s.ResetHdmi(ctx);case "get":s.GetHdmiState(ctx)};var response any;if e:=json.Unmarshal(rec.Body.Bytes(),&response);e!=nil{panic(e)};data,e:=os.ReadFile(utils.HDMIIdleTimeoutFile);if e!=nil{panic(e)};calls:=common.Calls;if calls==nil{calls=[]bool{}};out=append(out,map[string]any{"case":c,"response":response,"calls":calls,"disabled":utils.IsHdmiDisabled(),"savedTimeout":string(data)})};d:=state.New();transitions:=[]any{};for _,step:=range input.Steps{accepted:=false;switch step.Op{case "viewer":accepted=d.UpdateViewer(step.Source,step.Count,step.Version);case "lease":d.AcquireLease();case "release":accepted=d.ReleaseLease();case "warm":d.MarkWarming(time.Unix(100,0));case "clear":d.ClearReadyAt();case "claim":accepted=d.ClaimFreshFrame()};transitions=append(transitions,map[string]any{"step":step,"accepted":accepted,"viewers":d.ViewerCount(),"leases":d.LeaseCount(),"demand":d.HasDemand(),"fresh":d.NeedsFreshFrame(),"warming":!d.ReadyAt().IsZero(),"nextA":d.NextVersion("a")})};json.NewEncoder(os.Stdout).Encode(map[string]any{"cases":out,"state":transitions})}
'''
    (module/'main.go').write_text(main)
    result=json.loads(subprocess.check_output([str(goroot/'bin/go'),'run','-mod=readonly','.'],cwd=module,env=env,input=json.dumps(dict(Cases=cases,Steps=steps)).encode()))
result['baseline']=BASELINE;result['goVersion']='1.27.1';result['source_sha256']={n:hashlib.sha256(s).hexdigest()for n,s in sources.items()}
result['transforms']=['two exact utils constant paths redirected into temporary fixture root','native vision stub only; complete HDMI handler/demand logic unchanged']
(REPO/'docs/experiments/v3.0/hdmi-go-oracle.json').write_text(json.dumps(result,indent=2)+'\n')
print(len(result['cases']),'actual API cases;',len(result['state']),'actual demand transitions; no device effects')
