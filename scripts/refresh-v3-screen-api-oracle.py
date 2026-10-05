#!/usr/bin/env python3
"""Immutable full screen handlers/profile logic, fixture-only native boundary."""
from pathlib import Path
import base64,hashlib,json,os,re,subprocess,tempfile
REPO=Path(__file__).resolve().parents[1]
BASELINE='a53b25579ab87cc98f85323b4743deb0d4da907b'
paths=['server/service/vm/screen.go','server/common/screen.go','server/common/video_status.go','server/common/monitor.go','server/common/windows_pointer.go','server/proto/request.go','server/proto/response.go','server/proto/vm.go','server/go.mod','server/go.sum']
sources={p:subprocess.check_output(['git','show',BASELINE+':'+p],cwd=REPO)for p in paths}
assert all((REPO/p).read_bytes()==s for p,s in sources.items())
cases=[]
def case(name,body='',method='POST',ct='application/json',query='',**options):
    c=dict(Name=name,Body=body,Method=method,ContentType=ct,Query=query,Board='pcie',Chip='ux',IonMiB=64,Stride='Y',Portrait=False,PortraitResolution='1920',Monitor='0',Pointer=False,Pending=False,BlockedSetting='',MissingProfile='',FailChroma=False,FailProfile=False,ActiveMode=1,Fallback='',Role='admin')
    c.update(options);cases.append(c)
def setting(name,key,value,**options):case(name,json.dumps(dict(Type=key,Value=value)),**options)
for key,values in [('fps',[-1,0,9,10,50,120,121]),('resolution',[-1,0,600,720,1080,1440,1441,65536]),('quality',[0,1,50,100,101,3000,20000,20001]),('gop',[-1,0,1,30,100,101]),('gop_mode',[-1,0,1,2]),('mjpeg_chroma',[420,422,421]),('type',[-1,0,1,2,3]),('unknown',[1])]:
    for value in values:setting(key+'-'+str(value),key,value)
for name,body in [('missing','{}'),('missing-value','{"Type":"fps"}'),('empty-type','{"Type":"","Value":50}'),('null-root','null'),('array','[]'),('wrong-number','{"Type":"fps","Value":"50"}'),('float','{"Type":"fps","Value":50.0}'),('overflow','{"Type":"fps","Value":9223372036854775808}'),('duplicates','{"Type":"fps","Value":40,"VALUE":50}'),('null-no-overwrite','{"Type":"fps","Value":50,"VALUE":null}'),('early-error','{"Type":"fps","Value":"bad","Value":50}'),('trailing','{"Type":"fps","Value":50} true'),('bool-confirm','{"Type":"monitor","Value":0,"confirmPowerCycle":"yes"}')]:case(name,body)
for name,body in [('form','Type=fps&Value=50'),('form-lower','type=fps&value=50'),('form-space','Type=fps&Value=+50+'),('form-first','Type=fps&Value=50&Value=bad'),('form-empty','Type=fps&Value='),('form-confirm','Type=monitor&Value=0&ConfirmPowerCycle=true')]:case(name,body,ct='application/x-www-form-urlencoded')
case('query','Type=fps','POST','application/x-www-form-urlencoded','Value=50')
for name,options in [('get',{}),('get-cube',dict(Board='alpha',Chip='c',Pending=True)),('get-low-ion',dict(IonMiB=61)),('get-no-stride',dict(Stride='N')),('get-portrait',dict(Portrait=True,PortraitResolution='2304')),('get-max-portrait-no-stride',dict(Portrait=True,PortraitResolution='2560',Stride='N')),('get-unsupported',dict(Board='unknown',Chip='unknown')),('get-fallback',dict(Fallback='fixture-chroma-fallback')),('get-missing-profile',dict(MissingProfile='NanoKVM-portrait-1080x1920.bin'))]:case(name,method='GET',**options)
for value in [0,600,720,1080,1440,1441]:setting('monitor-'+str(value),'monitor',value)
case('cube-confirm-required','{"Type":"monitor","Value":1080}',Board='alpha',Chip='c')
case('cube-confirmed','{"Type":"monitor","Value":1080,"confirmPowerCycle":true}',Board='alpha',Chip='c')
case('cube-qhd','{"Type":"monitor","Value":1440,"confirmPowerCycle":true}',Board='alpha',Chip='c')
setting('monitor-qhd-low-ion','monitor',1440,IonMiB=61)
setting('monitor-unsupported','monitor',1080,Board='unknown',Chip='ux')
setting('monitor-keep-portrait','monitor',720,Portrait=True,PortraitResolution='2304')
for value in [-1,0,1,2]:setting('portrait-'+str(value),'portrait',value)
for value in [0,1280,1920,2304,2560]:setting('portrait-resolution-'+str(value),'portrait_resolution',value)
setting('portrait-resolution-active','portrait_resolution',2304,Portrait=True)
setting('portrait-resolution-low-ion','portrait_resolution',2560,IonMiB=63)
setting('portrait-resolution-max-no-stride','portrait_resolution',2560,Stride='N')
case('ack-unconfirmed','{"Type":"monitor_power_cycle_ack"}',Board='alpha',Chip='c',Pending=True)
case('ack-confirmed','{"Type":"monitor_power_cycle_ack","confirmPowerCycle":true}',Board='alpha',Chip='c',Pending=True)
case('ack-absent','{"Type":"monitor_power_cycle_ack","confirmPowerCycle":true}')
setting('pointer-monitor','monitor',720,Pointer=True)
setting('pointer-portrait','portrait',1,Pointer=True)
setting('chroma-native-fail','mjpeg_chroma',420,FailChroma=True)
setting('chroma-save-fail-rollback','mjpeg_chroma',420,BlockedSetting='mjpeg_chroma')
setting('fps-save-fail','fps',60,BlockedSetting='fps')
setting('profile-native-fail','monitor',720,FailProfile=True)
setting('gop-mode-restart','gop_mode',0,ActiveMode=1)
with(REPO/'docs/experiments/v3.0/actions.md').open('a')as log:log.write('\nBefore full screen API/profile oracle: exact baseline handler, screen/video_status/monitor/windows_pointer, proto and pinned modules. '+str(len(cases))+' cases; fixed path literals redirected to temporary fixture, native vision methods/status and current admin principal fixture-only. Full profile selection/portrait/ION/stride/pointer decoration logic unchanged. Native process/audio/EDID programming/hardware never invoked; monitor native transaction itself still pending.\n')
platform=Path(os.environ.get('NK_V3_PLATFORM',REPO.parent/'work/v2.1-b1-20261004/platform'));goroot=platform/'server/goroot'
env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(REPO/'work/v3/gocache'))
with tempfile.TemporaryDirectory(prefix='nk-v3-screen-api-oracle-')as tmp:
    root=Path(tmp);fixture=root/'fixture';fixture.mkdir();module=root/'module';module.mkdir()
    for p in paths:
        if p.endswith('proto/vm.go'):continue
        target=module/p.removeprefix('server/');target.parent.mkdir(parents=True,exist_ok=True)
        source=sources[p].decode()
        if p.endswith('.go'):source=re.sub(r'"(/[^"\n]*)"',lambda m:json.dumps(str(fixture)+m.group(1)),source)
        target.write_text(source)
    proto=sources['server/proto/vm.go'].decode();a=proto.index('type SetScreenReq struct {');b=proto.index('\n}',a)+2
    (module/'proto/screen.go').write_text('package proto\n'+proto[a:b])
    (module/'authn').mkdir();(module/'authn/stub.go').write_text('package authn\ntype Role string\nconst RoleAdmin Role="admin"\n')
    (module/'middleware').mkdir();(module/'middleware/stub.go').write_text('package middleware\nimport("github.com/gin-gonic/gin";"NanoKVM-Server/authn")\ntype Principal struct{Role authn.Role}\nfunc CurrentPrincipal(c *gin.Context)(Principal,bool){return Principal{Role:authn.Role(c.Request.Header.Get("X-Fixture-Role"))},true}\n')
    (module/'service/vm/stub.go').write_text('package vm\ntype Service struct{}\n')
    stub=r'''package common
import("os";"errors";"strings";"sync";"time")
var Root string
var Calls []any
var FailChroma,FailProfile bool
var ActiveChroma uint16=422
var ActiveMode uint8=1
var Fallback string
type KvmVision struct{}
func GetKvmVision()*KvmVision{return &KvmVision{}}
func(*KvmVision)SetGop(value uint8){Calls=append(Calls,map[string]any{"kind":"gop","value":value})}
func(*KvmVision)SetMjpegChroma(value uint16)int{Calls=append(Calls,map[string]any{"kind":"chroma","value":value});if FailChroma{return -5};ActiveChroma=value;if Fallback!=""{ActiveChroma=422};return 0}
func(*KvmVision)ApplyMonitorProfile(path string)error{data,e:=os.ReadFile(path);if e!=nil{return e};name:=strings.TrimPrefix(path,Root);if strings.Contains(name,"/run/nanokvm-pointer-"){name="decorated-pointer"};Calls=append(Calls,map[string]any{"kind":"profile","path":name,"data":data});if FailProfile{return errors.New("fixture monitor failure")};return nil}
func GetActiveGOPMode()uint8{return ActiveMode}
func GetMjpegChromaStatus()(uint16,string){return ActiveChroma,Fallback}
func ResetFixture(){screenOnce=sync.Once{};screen.Store(nil);sourceTiming.expires=time.Time{};Calls=[]any{};ActiveChroma=422}
'''
    (module/'common/stub.go').write_text(stub)
    # Valid EDID from the already verified immutable Windows-pointer oracle.
    edid=json.loads((REPO/'docs/experiments/v3.0/usb-go-oracle.json').read_text())['edids'][0]['input']
    main=r'''package main
import("encoding/base64";"encoding/binary";"encoding/json";"os";"path/filepath";"strings";"net/http/httptest";"github.com/gin-gonic/gin";"NanoKVM-Server/common";"NanoKVM-Server/service/vm")
type Case struct{Name,Body,Method,ContentType,Query,Board,Chip,Stride,PortraitResolution,Monitor,BlockedSetting,MissingProfile,Fallback,Role string;IonMiB int;Portrait,Pointer,Pending,FailChroma,FailProfile bool;ActiveMode uint8}
func write(root,path,value string){if e:=os.WriteFile(filepath.Join(root,path),[]byte(value),0644);e!=nil{panic(e)}}
func main(){gin.SetMode(gin.ReleaseMode);var input struct{Cases []Case;EDID string};if e:=json.NewDecoder(os.Stdin).Decode(&input);e!=nil{panic(e)};root:=os.Args[1];data,e:=base64.StdEncoding.DecodeString(input.EDID);if e!=nil{panic(e)};common.Root=root;s:=vm.Service{};out:=[]any{};for _,c:=range input.Cases{if e=os.RemoveAll(root);e!=nil{panic(e)};for _,dir:=range []string{"etc/kvm","kvmapp/kvm","proc/device-tree/reserved-memory/ion","sys/module/cv181x_vi/parameters","sys/kernel/config/usb_gadget/g0/os_desc","usr/share/nanokvm/edid","run/nanokvm","boot"}{if e=os.MkdirAll(filepath.Join(root,dir),0755);e!=nil{panic(e)}};for _,name:=range []string{"NanoKVM-stock.bin","NanoKVM-final-video-profiles.bin","NanoKVM-monitor-600.bin","NanoKVM-monitor-720.bin","NanoKVM-monitor-1080.bin","NanoKVM-monitor-1440.bin","NanoKVM-cube-monitor-720.bin","NanoKVM-cube-monitor-1080.bin","NanoKVM-portrait-720x1280.bin","NanoKVM-portrait-1080x1920.bin","NanoKVM-portrait-1296x2304.bin","NanoKVM-portrait-1440x2560.bin"}{if name!=c.MissingProfile{if e=os.WriteFile(filepath.Join(root,"usr/share/nanokvm/edid",name),data,0644);e!=nil{panic(e)}}};write(root,"etc/kvm/hw",c.Board);write(root,"etc/kvm/hdmi_version",c.Chip);write(root,"etc/kvm/monitor_resolution",c.Monitor);write(root,"etc/kvm/monitor_portrait_resolution",c.PortraitResolution);if c.Portrait{write(root,"etc/kvm/monitor_portrait","1\n")};if c.Pending{write(root,"etc/kvm/monitor_power_cycle_pending","")};if c.Pointer{write(root,"boot/usb.pointer_windows","")};write(root,"etc/kvm/usb_container_id","2ca7b40c-7bd1-4f25-b573-a13a975ddc07\n");write(root,"sys/kernel/config/usb_gadget/g0/os_desc/container_id","");write(root,"sys/module/cv181x_vi/parameters/yuv_bypass_aligned_stride",c.Stride);ion:=make([]byte,4);binary.BigEndian.PutUint32(ion,uint32(c.IonMiB)*1024*1024);if e=os.WriteFile(filepath.Join(root,"proc/device-tree/reserved-memory/ion/size"),ion,0644);e!=nil{panic(e)};if c.BlockedSetting!=""{if e=os.Mkdir(filepath.Join(root,"kvmapp/kvm",c.BlockedSetting),0755);e!=nil{panic(e)}};common.ResetFixture();common.FailChroma=c.FailChroma;common.FailProfile=c.FailProfile;common.ActiveMode=c.ActiveMode;common.Fallback=c.Fallback;rec:=httptest.NewRecorder();ctx,_:=gin.CreateTestContext(rec);ctx.Request=httptest.NewRequest(c.Method,"http://localhost/api?"+c.Query,strings.NewReader(c.Body));ctx.Request.Header.Set("Content-Type",c.ContentType);ctx.Request.Header.Set("X-Fixture-Role",c.Role);if c.Method=="GET"{s.GetScreen(ctx)}else{s.SetScreen(ctx)};var response any;if rec.Body.Len()>0{if e=json.Unmarshal(rec.Body.Bytes(),&response);e!=nil{panic(e)}};saved:=map[string]any{};for _,path:=range []string{"kvmapp/kvm/fps","kvmapp/kvm/res","kvmapp/kvm/qlty","kvmapp/kvm/type","kvmapp/kvm/gop_mode","kvmapp/kvm/mjpeg_chroma","etc/kvm/monitor_resolution","etc/kvm/monitor_portrait","etc/kvm/monitor_portrait_resolution","etc/kvm/monitor_power_cycle_pending"}{b,e:=os.ReadFile(filepath.Join(root,path));if e==nil{saved["/"+path]=string(b)}else{saved["/"+path]=nil}};out=append(out,map[string]any{"case":c,"status":rec.Code,"response":response,"calls":common.Calls,"screen":*common.GetScreen(),"saved":saved})};json.NewEncoder(os.Stdout).Encode(map[string]any{"cases":out})}
'''
    (module/'main.go').write_text(main)
    result=json.loads(subprocess.check_output([str(goroot/'bin/go'),'run','-mod=readonly','.',str(fixture)],cwd=module,env=env,input=json.dumps(dict(Cases=cases,EDID=edid)).encode()))
result.update(baseline=BASELINE,goVersion='1.27.1',source_sha256={p:hashlib.sha256(s).hexdigest()for p,s in sources.items()},transforms=['fixed firmware paths redirected to temporary fixture root','native vision calls/status stubbed; native monitor transaction not exercised','admin principal supplied by fixture middleware','native temporary pointer path label canonicalized; EDID bytes preserved'])
(REPO/'docs/experiments/v3.0/screen-api-go-oracle.json').write_text(json.dumps(result,indent=2)+'\n')
print(len(result['cases']),'actual full screen API/profile cases; no native/device effects')
