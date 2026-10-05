#!/usr/bin/env python3
"""Immutable Go memory mutation contracts with fixture command outputs only."""
from pathlib import Path
import base64,json,os,subprocess
repo=Path(__file__).resolve().parents[1];baseline='a53b25579ab87cc98f85323b4743deb0d4da907b'
def source(path):return subprocess.check_output(['git','show',baseline+':'+path],cwd=repo,text=True)
def function(text,name,method=False):
 marker=('func (s *Service) ' if method else 'func ')+name+'(';start=text.index(marker);end=text.index('{',start)+1;level=1
 while level:
  if text[end]=='{':level+=1
  if text[end]=='}':level-=1
  end+=1
 return text[start:end]
mem=source('server/service/vm/memory-status.go');video=source('server/service/vm/video-memory.go');legacy=source('server/service/vm/swap.go')
go='package main\nimport("bytes";"context";"encoding/base64";"encoding/json";"fmt";"os";"path/filepath";"strconv";"strings";"sync";"time";"net/http/httptest";"github.com/gin-gonic/gin";"NanoKVM-Server/proto")\ntype Service struct{}\nconst memoryService="/etc/init.d/S38memory"\nvar memoryMutation sync.Mutex\n'
for text,name in [(mem,'memorySwap'),(mem,'memoryStatus'),(mem,'memorySwapRequest'),(video,'videoMemoryStatus')]:
 start=text.index('type '+name+' struct {');end=text.index('\n}',start)+2;go+=text[start:end]+'\n'
for text,name,method in [(mem,'validSwapRequest',False),(mem,'applyMemorySwap',False),(mem,'GetMemoryStatus',True),(mem,'SetMemorySwap',True),(video,'validVideoMemoryMode',False),(video,'readVideoMemoryStatus',False),(video,'SetVideoMemory',True),(legacy,'GetSwap',True),(legacy,'SetSwap',True)]:
 fn=function(text,name,method)
 fn=fn.replace('exec.Command("sh", args...)','command("sh", args...)').replace('exec.CommandContext(ctx,','commandContext(ctx,').replace('osupdate.Lock()','fixtureLock()').replace('readVideoMemoryStatus("/")','readVideoMemoryStatus(root)').replace('os.ReadFile("/sys/firmware/devicetree/base/sipeed,board-revision")','os.ReadFile(filepath.Join(root,"sys/firmware/devicetree/base/sipeed,board-revision"))')
 go+=fn+'\n'
go+=r'''
var root string
var current Case
var calls [][]string
func readMemoryStatus()(*memoryStatus,error){if current.ReadError{return nil,fmt.Errorf("read failed")};return &memoryStatus{SD:memorySwap{Enabled:current.SDEnabled,SizeMiB:current.SDSize}},nil}
type fixtureCommand struct{}
func command(name string,args ...string)*fixtureCommand{calls=append(calls,append([]string{name},args...));return &fixtureCommand{}}
func commandContext(ctx context.Context,name string,args ...string)*fixtureCommand{return command(name,args...)}
func (*fixtureCommand)CombinedOutput()([]byte,error){data,_:=base64.StdEncoding.DecodeString(current.Output);if current.Fail{return data,fmt.Errorf("exit status 1")};return data,nil}
func fixtureLock()(*os.File,error){if current.LockError!=""{return nil,fmt.Errorf("%s",current.LockError)};return os.CreateTemp(root,"lock-")}
type Case struct{Name,Method,Path,Body,ContentType,Output,LockError string;Fail,ReadError,SDEnabled bool;SDSize int64;Files map[string]string}
func main(){gin.SetMode(gin.ReleaseMode);var cases []Case;if e:=json.NewDecoder(os.Stdin).Decode(&cases);e!=nil{panic(e)};result:=[]any{};for _,c:=range cases{current=c;calls=[][]string{};dir,e:=os.MkdirTemp("","memory-command-go-");if e!=nil{panic(e)};root=dir;for name,value:=range c.Files{path:=filepath.Join(root,strings.TrimPrefix(name,"/"));os.MkdirAll(filepath.Dir(path),0755);data,_:=base64.StdEncoding.DecodeString(value);os.WriteFile(path,data,0644)};recorder:=httptest.NewRecorder();ctx,_:=gin.CreateTestContext(recorder);ctx.Request=httptest.NewRequest(c.Method,"http://localhost"+c.Path,bytes.NewBufferString(c.Body));ctx.Request.Header.Set("Content-Type",c.ContentType);service:=&Service{};switch c.Path{case "/api/vm/memory/swap":service.SetMemorySwap(ctx);case "/api/vm/memory/video":service.SetVideoMemory(ctx);case "/api/vm/swap":if c.Method=="GET"{service.GetSwap(ctx)}else{service.SetSwap(ctx)}};var response any;if e:=json.Unmarshal(recorder.Body.Bytes(),&response);e!=nil{panic(e)};result=append(result,map[string]any{"case":c,"response":response,"calls":calls});os.RemoveAll(dir)};json.NewEncoder(os.Stdout).Encode(result)}
'''
cases=[]
def case(name,path='/api/vm/memory/swap',body='',contentType='application/json',**kw):
 cases.append(dict(Name=name,Method='POST',Path=path,Body=body,ContentType=contentType,Output='',Files={},**kw))
for kind,sizes in [('zram',[0,31,32,64,128,162,163,256]),('sd',[0,64,128,256,512,513])]:
 for size in sizes:case(kind+'-'+str(size),body=json.dumps(dict(kind=kind,enabled=True,sizeMiB=size)))
for name,body in [('missing','{}'),('null','null'),('array','[]'),('wrong-size','{"kind":"sd","sizeMiB":"256"}'),('fraction','{"kind":"sd","sizeMiB":256.0}'),('overflow','{"kind":"sd","sizeMiB":9223372036854775808}'),('kind-case','{"kind":"SD","sizeMiB":256}'),('duplicates','{"kind":"first","KIND":"zram","SIZE MIB":1,"sizeMiB":64,"sizeMiB":null,"enabled":true,"ENABLED":null,"recompress":true,"RECOMPRESS":null}'),('type-error-first','{"sizeMiB":"bad","sizeMiB":64,"kind":"zram"}'),('pointer-bool','{"kind":"zram","sizeMiB":64,"recompress":true}'),('pointer-false','{"kind":"zram","sizeMiB":64,"recompress":false}'),('pointer-sd','{"kind":"sd","sizeMiB":256,"recompress":false}'),('pointer-wrong','{"kind":"zram","sizeMiB":64,"recompress":"true"}'),('enabled-wrong','{"kind":"zram","sizeMiB":64,"enabled":1}'),('first-value','{"kind":"sd","sizeMiB":128} true')]:case(name,body=body)
case('forced-json',body='{"kind":"sd","sizeMiB":256}',contentType='text/plain')
case('forced-form-invalid',body='kind=sd&sizeMiB=256',contentType='application/x-www-form-urlencoded')
for name,data in [('empty',b''),('spaces',b' \n\t'),('stderr',b' swap failed \n'),('long',b'prefix'+b'x'*1000+b' tail \n'),('long-trailing-space',b' useful error '+b' '*20000),('unicode-trim',('\u2003'+('é'*410)+'\u0085').encode()),('invalid',b' \xff\xe1\x80\xff\n')]:
 c=dict(Name='swap-fail-'+name,Method='POST',Path='/api/vm/memory/swap',Body='{"kind":"sd","sizeMiB":256}',ContentType='application/json',Output=base64.b64encode(data).decode(),Files={},Fail=True);cases.append(c)
case('success-read-error',body='{"kind":"sd","sizeMiB":256}',ReadError=True)
fdt='/sys/firmware/devicetree/base/sipeed,board-revision'
files={fdt:base64.b64encode(b'pcie\0').decode(),'/usr/lib/nanokvm/boot/pcie.sd':'','/usr/lib/nanokvm/boot/pcie-fixed.sd':''}
for name,body in [('good','{"mode":"fixed"}'),('cma','{"mode":"cma"}'),('wrong','{"mode":"FIXED"}'),('null','null'),('duplicate','{"mode":"fixed","MODE":null}'),('type','{"mode":1}'),('first','{"mode":"fixed"} []')]:
 case('video-'+name,path='/api/vm/memory/video',body=body);cases[-1]['Files']=files
case('video-missing',path='/api/vm/memory/video',body='{"mode":"fixed"}')
case('video-lock',path='/api/vm/memory/video',body='{"mode":"fixed"}',LockError='another update operation is in progress')
case('video-forced',path='/api/vm/memory/video',body='{"mode":"cma"}',contentType='text/plain');cases[-1]['Files']=files
for name,data in [('empty',b''),('long',b'prefix '+b'x'*1000+b' tail \n'),('unicode',('é'*410).encode()),('invalid',b'\xff\xe1\x80\xff'),('spaces',b' useful '+b' '*20000)]:
 case('video-fail-'+name,path='/api/vm/memory/video',body='{"mode":"fixed"}',Fail=True);cases[-1].update(Files=files,Output=base64.b64encode(data).decode())
for name,body,contentType in [('disable','{}','application/json'),('good','{"size":128}','application/json'),('invalid','{"size":1}','application/json'),('wrong','{"size":"256"}','application/json'),('form','Size=512','application/x-www-form-urlencoded'),('form-lower','size=512','application/x-www-form-urlencoded'),('form-empty','Size=','application/x-www-form-urlencoded'),('form-bad','Size=x','application/x-www-form-urlencoded'),('query','', 'text/plain')]:case('legacy-'+name,path='/api/vm/swap',body=body,contentType=contentType)
for name,enabled,size,readerror in [('inactive',False,512,False),('active',True,256,False),('error',False,0,True)]:
 case('legacy-get-'+name,path='/api/vm/swap',SDEnabled=enabled,SDSize=size,ReadError=readerror);cases[-1]['Method']='GET'
out=repo/'work/v3/memory-command-oracle';out.mkdir(parents=True,exist_ok=True);(out/'main.go').write_text(go);(out/'input.json').write_text(json.dumps(cases))
platform=Path(os.environ.get('NK_V3_PLATFORM',repo.parent/'work/v2.1-b1-20261004/platform'));goroot=platform/'server/goroot';env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(repo/'work/v3/gocache'))
with(out/'input.json').open('rb')as f:result=json.loads(subprocess.check_output([goroot/'bin/go','run',out/'main.go'],cwd=repo/'server',env=env,stdin=f))
(repo/'docs/experiments/v3.0/memory-command-go-oracle.json').write_text(json.dumps(dict(baseline=baseline,goVersion='1.27.1',cases=result),indent=2)+'\n');print(len(result),'actual Go mutation cases; fixture commands only')
