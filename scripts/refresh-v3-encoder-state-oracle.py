from pathlib import Path
import subprocess,json,tempfile,hashlib,os
repo=Path(__file__).resolve().parents[1];baseline='a53b25579ab87cc98f85323b4743deb0d4da907b'
goroot=Path('/home/dgrace/nanokvm-astra/work/v2.1-b1-20261004/platform/server/goroot')
paths=['server/service/stream/state.go','server/service/stream/state_test.go','server/service/stream/video_source.go','server/service/stream/encoder_config.go','server/proto/response.go','server/go.mod','server/go.sum']
sources={name:subprocess.check_output(['git','show',f'{baseline}:{name}'],cwd=repo) for name in paths}
output=repo/'docs/experiments/v3.0/encoder-state-go-oracle.json';assert not output.exists()
cases=[]
for body in ['{}','null','[]','{"codec":"h264"}','{"CODEC":"h265"}','{"codec":"h264","codec":null}','{"codec":null}','{"codec":"H264"}','{"codec":1}','{"codec":1,"codec":"h264"}','{"codec":"h264","codec":"h265"}','{"codec":"h265"} true','{"codec":"h265","unknown":1e9999}']:
 cases.append(dict(body=body,method='POST',contentType='application/json',role='admin',active='',selected='',blocked=False))
for contentType in ['','application/x-www-form-urlencoded','garbage;=bad','text/plain']:
 cases.append(dict(body='{"codec":"h264"}',method='POST',contentType=contentType,role='admin',active='',selected='',blocked=False))
cases.append(dict(body='codec=h264',method='POST',contentType='application/x-www-form-urlencoded',role='admin',active='',selected='',blocked=False))
cases.append(dict(body='{"codec":"h265"}',method='POST',contentType='application/json',role='user',active='',selected='',blocked=False))
cases.append(dict(body='{"codec":"h265"}',method='POST',contentType='application/json',role='admin',active='h264',selected='',blocked=True))
for active,selected in [('', ''),('h264',''),('h265',''),('','h264'),('','h265'),('h265','h264')]:
 cases.append(dict(body='',method='GET',contentType='',role='user',active=active,selected=selected,blocked=False))
with (repo/'docs/experiments/v3.0/actions.md').open('a') as f:
 f.write('\nBefore encoder-state Go handler reference: complete immutable state.go plus actual video_source/encoder_config and original state tests; only fixed selection path, native/common/FPS boundary and principal fixture. '+str(len(cases))+' handler/JSON/media/role/active-selected/write-failure cases; temporary isolated root, no device/host settings effect.\n')
with tempfile.TemporaryDirectory(prefix='nk-v3-encoder-state-') as directory:
 module=Path(directory)/'module';module.mkdir();fixture=Path(directory)/'fixture';fixture.mkdir()
 for name,data in sources.items():
  destination=module/name.removeprefix('server/');destination.parent.mkdir(parents=True,exist_ok=True)
  text=data.decode().replace('"/etc/kvm/encoder_codec"',json.dumps(str(fixture/'encoder_codec')))
  destination.write_text(text)
 for name,text in {
 'authn/stub.go':'package authn\ntype Role string\nconst RoleAdmin Role="admin"\nconst RoleUser Role="user"\n',
 'middleware/stub.go':'package middleware\nimport("github.com/gin-gonic/gin";"NanoKVM-Server/authn")\ntype Principal struct{Username string;Role authn.Role}\nfunc CurrentPrincipal(c *gin.Context)(Principal,bool){value,ok:=c.Get("principal");if !ok{return Principal{},false};p,ok:=value.(Principal);return p,ok}\n',
 'common/stub.go':'package common\ntype Screen struct{Width,Height,BitRate uint16;GOP uint8;FPS int}\nfunc GetScreen()Screen{return Screen{FPS:50,BitRate:3000,GOP:30}}\nfunc GetCaptureScreen()Screen{return GetScreen()}\nfunc CheckScreen(){}\nfunc ReadVideoValue(string)int{return 0}\ntype Vision struct{}\nfunc GetKvmVision()*Vision{return &Vision{}}\nfunc(*Vision)RequestKeyframe(){}\nfunc(*Vision)ReadVideoWithHeadroom(uint16,uint16,uint8,uint16,uint8,uint8,int)([]byte,[]byte,int){return nil,nil,0}\n',
 'service/stream/stub.go':'package stream\ntype FrameRateCounter struct{}\nfunc GetFrameRateCounter()*FrameRateCounter{return &FrameRateCounter{}}\nfunc(*FrameRateCounter)Update(){}\n',
 }.items():
  destination=module/name;destination.parent.mkdir(parents=True,exist_ok=True);destination.write_text(text)
 (module/'service/stream/oracle_test.go').write_text(r'''package stream
import("encoding/json";"os";"net/http/httptest";"strings";"testing";"github.com/gin-gonic/gin";"NanoKVM-Server/middleware";"NanoKVM-Server/authn")
func TestOracle(t *testing.T){gin.SetMode(gin.ReleaseMode);var cases []struct{Body,Method,ContentType,Role,Active,Selected string;Blocked bool};data,_:=os.ReadFile(os.Getenv("NK_CASES"));if e:=json.Unmarshal(data,&cases);e!=nil{t.Fatal(e)};out:=[]any{};for _,c:=range cases{os.Remove(encoderSelectionFile);defaultVideoSource=newVideoSource(func(EncoderConfig)([]byte,[]byte,int){return nil,nil,0});if c.Active!=""{sub,e:=defaultVideoSource.subscribe(EncoderConfig{Codec:VideoCodec(c.Active)});if e!=nil{t.Fatal(e)};defer sub.Close()};if c.Selected!=""{defaultVideoSource.selectConfig(EncoderConfig{Codec:VideoCodec(c.Selected)})};if c.Blocked{os.Mkdir(encoderSelectionFile,0755)};rec:=httptest.NewRecorder();ctx,_:=gin.CreateTestContext(rec);ctx.Request=httptest.NewRequest(c.Method,"http://localhost/api/stream/state",strings.NewReader(c.Body));ctx.Request.Header.Set("Content-Type",c.ContentType);ctx.Set("principal",middleware.Principal{Role:authn.Role(c.Role)});if c.Method=="GET"{GetEncoderState(ctx)}else{SetEncoderState(ctx)};var response any;if rec.Body.Len()>0{if e:=json.Unmarshal(rec.Body.Bytes(),&response);e!=nil{t.Fatal(e)}};var saved any;var mode any;if b,e:=os.ReadFile(encoderSelectionFile);e==nil{saved=string(b);info,_:=os.Stat(encoderSelectionFile);mode=uint32(info.Mode().Perm())};selected,ok:=defaultVideoSource.selectedConfig();out=append(out,map[string]any{"case":c,"status":rec.Code,"response":response,"cacheControl":rec.Header().Get("Cache-Control"),"saved":saved,"mode":mode,"selected":ok,"codec":selected.Codec});os.Remove(encoderSelectionFile)};data,_=json.Marshal(out);if e:=os.WriteFile(os.Getenv("NK_OUTPUT"),data,0600);e!=nil{t.Fatal(e)}}
''')
 input=Path(directory)/'cases.json';input.write_text(json.dumps(cases));result_path=Path(directory)/'result.json'
 env={**os.environ,'GOROOT':str(goroot),'GOTOOLCHAIN':'local','CGO_ENABLED':'0','GOCACHE':str(repo/'work/v3/gocache'),'NK_CASES':str(input),'NK_OUTPUT':str(result_path)}
 tested=subprocess.run([str(goroot/'bin/go'),'test','-mod=readonly','-count=1','-v','./service/stream'],cwd=module,env=env,text=True,capture_output=True)
 if tested.returncode:print(tested.stderr+tested.stdout);raise SystemExit(tested.returncode)
 result={'baseline':baseline,'source_sha256':{name:hashlib.sha256(data).hexdigest() for name,data in sources.items()},'cases':json.loads(result_path.read_text()),'go_version':'1.27.1','original_tests':[line.split()[2] for line in tested.stdout.splitlines() if line.startswith('--- PASS:') and 'TestOracle' not in line]}
 output.write_text(json.dumps(result,indent=2)+'\n');print(json.dumps({'handler_cases':len(cases),'original_tests':len(result['original_tests'])}))
