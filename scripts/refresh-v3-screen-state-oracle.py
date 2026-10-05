#!/usr/bin/env python3
"""Actual immutable Go screen loading/publication/rate policy with fixture paths."""
from pathlib import Path
import hashlib,json,os,subprocess,tempfile

REPO=Path(__file__).resolve().parents[1]
BASELINE='a53b25579ab87cc98f85323b4743deb0d4da907b'
paths=['server/common/screen.go','server/common/video_status.go','server/go.mod','server/go.sum']
sources={path:subprocess.check_output(['git','show',BASELINE+':'+path],cwd=REPO)for path in paths}
assert all((REPO/path).read_bytes()==source for path,source in sources.items())
cases=[]
def case(name,files=None,ion='',env='',width='',height='',steps=None):
    cases.append(dict(Name=name,Files=files or {},Ion=ion,Env=env,InputWidth=width,InputHeight=height,Steps=steps or []))
case('defaults');case('env-420',env='0');case('env-other',env='false')
for key,values in [('fps',['','abc','0','-1','9','10','50','120','121','9223372036854775808',' 75\n']),('res',['0','600','720','1080','1440','-1','65536','1081']),('qlty',['0','1','50','60','80','100','101','999','1000','2000','3000','5000','10000','15000','20000','20001']),('gop_mode',['0','1','2','-1']),('mjpeg_chroma',['420','422','421','0'])]:
    for value in values:case(key+'-'+repr(value),{key:value})
for mib in [60,61,62,63,64]:case('QHD-'+str(mib),{'res':'1440'},ion=(mib*1024*1024).to_bytes(4,'big').hex())
case('QHD-short',{'res':'1440'},ion='03');case('QHD-extra',{'res':'1440'},ion='0400000000')
case('save-over-env',{'mjpeg_chroma':'422'},env='0')
steps=[]
for key,values in [('resolution',[-1,65536,1081,600,720,1080,1440,0]),('fps',[-1,10,50,120,121]),('quality',[1,50,100,101,3000,20000,20001,0]),('gop',[0,1,100,101]),('gop_mode',[-1,0,1,2]),('mjpeg_chroma',[420,421,422]),('unknown',[1])]:
    steps.extend(dict(Key=key,Value=value)for value in values)
steps.extend([dict(Key='quality',Value=101),dict(Key='quality',Value=1),dict(Key='check',Value=0)])
case('published-sequence',steps=steps)
for width,height in [(0,0),(1280,720),(720,1280),(1920,1080),(1088,1920),(1920,1088),(1920,1089),(2560,1440),(1440,2560),(-1,1080)]:
    for res in ['0','720','1080','1440']:
        case('capture-'+str(width)+'x'+str(height)+'-'+res,{'fps':'120','res':res},ion='04000000',width=str(width),height=str(height))
case('overflow-status',{'fps':'120'},width='9223372036854775808',height='1080')
for value in ['-9223372036854775809','9223372036854775808x','999999999999999999999999x','+9223372036854775808','999x','9_000',' ',' 1920\n']:
    case('status-'+repr(value),{'fps':'120'},width=value,height='1080')
dimensions=[-1,0,720,1080,1088,1280,1920,1921,2560]
rates=[dict(Width=w,Height=h)for w in dimensions for h in dimensions]
checks=[dict(Width=999,Height=h,FPS=-1,Quality=q,BitRate=b,GOP=0,GOPMode=3,MjpegChroma=421)for h,q,b in [(99,1,101),(720,80,3000),(0,100,20000),(1440,60,1000)]]
with(REPO/'docs/experiments/v3.0/actions.md').open('a')as log:log.write('\nHDMI own local commit25ca66e complete and clean, no push. Before next screen-state oracle: exact immutable common screen/video_status source and pinned modules; rewrite only fixed firmware path literals into a temporary root. '+str(len(cases))+' boot/publication/effective-FPS cases and '+str(len(rates))+' orientation/rate classifications. No native/vendor/hardware or host settings action. Screen API/monitor/native wiring remains pending.\n')
platform=Path(os.environ.get('NK_V3_PLATFORM',REPO.parent/'work/v2.1-b1-20261004/platform'));goroot=platform/'server/goroot'
env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(REPO/'work/v3/gocache'))
with tempfile.TemporaryDirectory(prefix='nk-v3-screen-oracle-')as tmp:
    root=Path(tmp);module=root/'module';module.mkdir();fixture=root/'fixture'
    for name in ['go.mod','go.sum']:(module/name).write_bytes(sources['server/'+name])
    for source_path in paths[:2]:
        source=sources[source_path].decode().replace('package common','package main',1)
        import re
        source=re.sub(r'"(/(?:kvmapp/kvm|etc/kvm|proc/device-tree/reserved-memory/ion|run/nanokvm)[^"\n]*)"',lambda m:json.dumps(str(fixture)+m.group(1)),source)
        (module/Path(source_path).name).write_text(source)
    main=r'''package main
import("encoding/hex";"encoding/json";"os";"path/filepath";"sync";"time")
type Step struct{Key string;Value int}
type Case struct{Name string;Files map[string]string;Ion,Env,InputWidth,InputHeight string;Steps []Step}
type Rate struct{Width,Height int}
func main(){var input struct{Cases []Case;Rates []Rate;Checks []Screen};if e:=json.NewDecoder(os.Stdin).Decode(&input);e!=nil{panic(e)};root:=os.Args[1];for _,dir:=range []string{"kvmapp/kvm","proc/device-tree/reserved-memory/ion","run/nanokvm"}{if e:=os.MkdirAll(filepath.Join(root,dir),0755);e!=nil{panic(e)}};out:=[]any{};for _,c:=range input.Cases{for _,key:=range []string{"fps","res","qlty","gop_mode","mjpeg_chroma"}{path:=filepath.Join(root,"kvmapp/kvm",key);os.Remove(path);if value,ok:=c.Files[key];ok{if e:=os.WriteFile(path,[]byte(value),0644);e!=nil{panic(e)}}};ion:=filepath.Join(root,"proc/device-tree/reserved-memory/ion/size");os.Remove(ion);if c.Ion!=""{data,e:=hex.DecodeString(c.Ion);if e!=nil{panic(e)};if e=os.WriteFile(ion,data,0644);e!=nil{panic(e)}};for key,value:=range map[string]string{"width":c.InputWidth,"height":c.InputHeight}{path:=filepath.Join(root,"run/nanokvm",key);os.Remove(path);if value!=""{if e:=os.WriteFile(path,[]byte(value),0644);e!=nil{panic(e)}}};os.Setenv("NANOKVM_MJPEG_422",c.Env);screenOnce=sync.Once{};screen.Store(nil);sourceTiming.expires=time.Time{};initial:=*GetScreen();capture:=*GetCaptureScreen();saved:=*GetScreen();published:=[]any{};for _,step:=range c.Steps{if step.Key=="check"{CheckScreen()}else{SetScreen(step.Key,step.Value)};published=append(published,map[string]any{"step":step,"screen":*GetScreen()})};out=append(out,map[string]any{"case":c,"initial":initial,"capture":capture,"savedAfterCapture":saved,"published":published,"inputWidth":ReadVideoValue(filepath.Join(root,"run/nanokvm/width")),"inputHeight":ReadVideoValue(filepath.Join(root,"run/nanokvm/height")),"qhd":SupportsQHD()})};rateOut:=[]any{};for _,r:=range input.Rates{rateOut=append(rateOut,map[string]any{"width":r.Width,"height":r.Height,"limit":CaptureRateLimit(r.Width,r.Height),"fhd":IsFHDClassDimensions(r.Width,r.Height)})};checkOut:=[]any{};for _,original:=range input.Checks{next:=original;checkScreen(&next);checkOut=append(checkOut,map[string]any{"input":original,"output":next})};json.NewEncoder(os.Stdout).Encode(map[string]any{"cases":out,"rates":rateOut,"checks":checkOut})}
'''
    (module/'main.go').write_text(main)
    result=json.loads(subprocess.check_output([str(goroot/'bin/go'),'run','-mod=readonly','.',str(fixture)],cwd=module,env=env,input=json.dumps(dict(Cases=cases,Rates=rates,Checks=checks)).encode()))
result.update(baseline=BASELINE,goVersion='1.27.1',source_sha256={p:hashlib.sha256(s).hexdigest()for p,s in sources.items()},transforms=['package common renamed main for private helpers','only fixed firmware path literals redirected to temporary fixture; full screen/state/rate logic unchanged'])
(REPO/'docs/experiments/v3.0/screen-state-go-oracle.json').write_text(json.dumps(result,indent=2)+'\n')
print(len(result['cases']),'actual screen cases;',len(result['rates']),'actual rate classifications; no device effects')
