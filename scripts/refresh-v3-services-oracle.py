#!/usr/bin/env python3
"""Immutable Go service contracts with in-memory command/password fixtures."""
from pathlib import Path
import base64,json,os,re,subprocess
repo=Path(__file__).resolve().parents[1];baseline='a53b25579ab87cc98f85323b4743deb0d4da907b'
def source(path):return subprocess.check_output(['git','show',baseline+':'+path],cwd=repo,text=True)
def function(text,name,method=False):
 marker=('func (s *Service) ' if method else 'func ')+name+'(';start=text.index(marker);end=text.index('{',start)+1;level=1
 while level:
  if text[end]=='{':level+=1
  if text[end]=='}':level-=1
  end+=1
 return text[start:end]
mdns=source('server/service/vm/mdns.go');ssh=source('server/service/vm/ssh.go');auth=source('server/authn/store.go')
go='package main\nimport("bytes";"crypto/aes";"crypto/cipher";"crypto/md5";"encoding/base64";"encoding/json";"errors";"fmt";"os";"path/filepath";"strings";"net/http/httptest";"net/url";"github.com/gin-gonic/gin";"NanoKVM-Server/proto";"NanoKVM-Server/utils";log "github.com/sirupsen/logrus")\ntype Service struct{}\nvar root string\nconst AvahiDaemonPid="/run/avahi-daemon/pid"\nconst AvahiDaemonScript="/etc/init.d/S50avahi-daemon"\nconst AvahiDaemonBackupScript="/kvmapp/system/init.d/S50avahi-daemon"\nconst SSHScript="/etc/init.d/S50sshd"\nconst SSHStopFlag="/etc/kvm/ssh_stop"\n'
for text,names,method in [(mdns,['GetMdnsState','EnableMdns','DisableMdns'],True),(mdns,['getAvahiDaemonPid','setAlpineMdns'],False),(ssh,['GetSSHState','EnableSSH','DisableSSH'],True),(ssh,['isSSHEnabled'],False),(auth,['ValidatePassword'],False)]:
 for name in names:
  fn=function(text,name,method).replace('exec.Command(','fixtureExec(').replace('authn.ValidatePassword','ValidatePassword').replace('changeRootPassword(password)','fixturePassword(password)')
  fn=fn.replace('os.Stat(AvahiDaemonPid)','os.Stat(filepath.Join(root,AvahiDaemonPid))').replace('os.ReadFile(AvahiDaemonPid)','os.ReadFile(filepath.Join(root,AvahiDaemonPid))').replace('os.Stat(SSHStopFlag)','os.Stat(filepath.Join(root,SSHStopFlag))').replace('os.Remove(AvahiDaemonPid)','os.Remove(filepath.Join(root,AvahiDaemonPid))').replace('os.Remove(AvahiDaemonScript)','os.Remove(filepath.Join(root,AvahiDaemonScript))').replace('os.Remove(marker)','os.Remove(filepath.Join(root,marker))').replace('os.WriteFile(marker,','os.WriteFile(filepath.Join(root,marker),').replace('os.Stat("/etc/alpine-release")','os.Stat(filepath.Join(root,"etc/alpine-release"))')
  go+=fn+'\n'
go+=r'''
type Case struct{Name,Method,Path,Body,ContentType,Password string;Form,RawBody,Fail,PasswordFail bool;Files map[string]string;Dirs []string}
var current Case
var calls [][]string
var changed bool
func fixturePassword(password string)error{changed=true;if current.PasswordFail{return errors.New("failed")};return nil}
type fixtureCommand struct{}
func fixtureExec(name string,args ...string)*fixtureCommand{calls=append(calls,append([]string{name},args...));return &fixtureCommand{}}
func(*fixtureCommand)Run()error{if len(calls)>0&&strings.HasPrefix(strings.Join(calls[len(calls)-1]," "),"sh -c cp -f "){source:=filepath.Join(root,AvahiDaemonBackupScript);data,e:=os.ReadFile(source);if e!=nil{return e};destination:=filepath.Join(root,AvahiDaemonScript);if e=os.WriteFile(destination,data,0644);e!=nil{return e}};if current.Fail{return errors.New("failed")};return nil}
func encrypt(password string)string{salt:=[]byte{1,2,3,4,5,6,7,8};derived:=[]byte{};previous:=[]byte{};for len(derived)<48{hash:=md5.New();hash.Write(previous);hash.Write([]byte(utils.SecretKey));hash.Write(salt);previous=hash.Sum(nil);derived=append(derived,previous...)};block,_:=aes.NewCipher(derived[:32]);plain:=[]byte(password);pad:=16-len(plain)%16;plain=append(plain,bytes.Repeat([]byte{byte(pad)},pad)...);encrypted:=make([]byte,len(plain));cipher.NewCBCEncrypter(block,derived[32:48]).CryptBlocks(encrypted,plain);raw:=append(append([]byte("Salted__"),salt...),encrypted...);return base64.StdEncoding.EncodeToString(raw)}
func main(){gin.SetMode(gin.ReleaseMode);var cases []Case;if e:=json.NewDecoder(os.Stdin).Decode(&cases);e!=nil{panic(e)};result:=[]any{};for _,c:=range cases{current=c;calls=[][]string{};changed=false;dir,e:=os.MkdirTemp("","service-go-oracle-");if e!=nil{panic(e)};root=dir;os.MkdirAll(filepath.Join(root,"etc/kvm"),0755);for _,d:=range c.Dirs{os.MkdirAll(filepath.Join(root,d),0755)};for name,value:=range c.Files{path:=filepath.Join(root,strings.TrimPrefix(name,"/"));os.MkdirAll(filepath.Dir(path),0755);data,_:=base64.StdEncoding.DecodeString(value);os.WriteFile(path,data,0644)};body:=c.Body;if c.Path=="/api/vm/ssh/enable"&&!c.RawBody{encrypted:=encrypt(c.Password);if c.Form{key:="password";if c.Name=="ssh-form-uppercase"{key="Password"};body=url.Values{key:[]string{encrypted}}.Encode()}else{data,_:=json.Marshal(map[string]string{"password":encrypted});body=string(data)}};recorder:=httptest.NewRecorder();ctx,_:=gin.CreateTestContext(recorder);ctx.Request=httptest.NewRequest(c.Method,"http://localhost"+c.Path,bytes.NewBufferString(body));ctx.Request.Header.Set("Content-Type",c.ContentType);service:=&Service{};switch c.Path{case "/api/vm/mdns":service.GetMdnsState(ctx);case "/api/vm/mdns/enable":service.EnableMdns(ctx);case "/api/vm/mdns/disable":service.DisableMdns(ctx);case "/api/vm/ssh":service.GetSSHState(ctx);case "/api/vm/ssh/enable":service.EnableSSH(ctx);case "/api/vm/ssh/disable":service.DisableSSH(ctx)};var response any;if e:=json.Unmarshal(recorder.Body.Bytes(),&response);e!=nil{panic(e)};state:=map[string]any{};for _,name:=range []string{"etc/kvm/mdns_disabled","run/avahi-daemon/pid","etc/init.d/S50avahi-daemon","etc/kvm/ssh_stop"}{path:=filepath.Join(root,name);data,e:=os.ReadFile(path);if e==nil{state[name]=base64.StdEncoding.EncodeToString(data)}else{state[name]=nil}};c.Body=body;result=append(result,map[string]any{"case":c,"response":response,"calls":calls,"changed":changed,"state":state});os.RemoveAll(dir)};json.NewEncoder(os.Stdout).Encode(result)}
'''
cases=[]
def case(name,path,**kw):cases.append(dict(Name=name,Method='POST',Path=path,Body='',ContentType='application/json',Password='operator-password',Files={},Dirs=[],**kw))
for name,pid in [('missing',None),('empty',b''),('newline',b'\n'),('active',b'1234\n'),('cr',b'\r\n')]:
 for suffix in ['','/enable','/disable']:
  case('mdns-'+name+suffix,'/api/vm/mdns'+suffix);c=cases[-1];c['Method']='GET'if suffix==''else'POST'
  if pid is not None:c['Files']['/run/avahi-daemon/pid']=base64.b64encode(pid).decode()
for enabled in [False,True]:
 for fail in [False,True]:
  case('alpine-'+str(enabled)+'-'+str(fail),'/api/vm/mdns/'+('enable'if enabled else'disable'),Fail=fail);cases[-1]['Files']={'/etc/alpine-release':'','/etc/kvm/mdns_disabled':base64.b64encode(b'old').decode()}
for suffix in ['enable','disable']:case('legacy-fail-'+suffix,'/api/vm/mdns/'+suffix,Fail=True);cases[-1]['Files']={'/run/avahi-daemon/pid':base64.b64encode(b'1234\n').decode()}if suffix=='disable'else{}
for name,kind in [('missing','missing'),('flag','file'),('directory','dir')]:
 case('ssh-get-'+name,'/api/vm/ssh');c=cases[-1];c['Method']='GET'
 if kind=='file':c['Files']={'/etc/kvm/ssh_stop':''}
 if kind=='dir':c['Dirs']=['etc/kvm/ssh_stop']
for name,password in [('good','operator-password'),('short','short'),('long','x'*73),('root',' ROOT '),('empty',''),('unicode','é'*4),('max','x'*72)]:
 case('ssh-enable-'+name,'/api/vm/ssh/enable');cases[-1]['Password']=password
case('ssh-form','/api/vm/ssh/enable',Form=True);cases[-1]['ContentType']='application/x-www-form-urlencoded'
case('ssh-form-uppercase','/api/vm/ssh/enable',Form=True);cases[-1]['ContentType']='application/x-www-form-urlencoded'
case('ssh-password-fail','/api/vm/ssh/enable',PasswordFail=True)
case('ssh-start-fail','/api/vm/ssh/enable',Fail=True)
for name,body in [('missing','{}'),('malformed','{'),('wrong-type','{"password":7}'),('invalid-cipher','{"password":"not-ciphertext"}')]:case('ssh-'+name,'/api/vm/ssh/enable',RawBody=True);cases[-1]['Body']=body
for fail in [False,True]:case('ssh-disable-'+str(fail),'/api/vm/ssh/disable',Fail=fail)
for c in cases:
 if c['Path'].startswith('/api/vm/mdns'):
  c['Files']['/kvmapp/system/init.d/S50avahi-daemon']=base64.b64encode(b'#!/bin/sh\nfixture-service\n').decode();c['Dirs']+=['etc/init.d']
out=repo/'work/v3/services-oracle';out.mkdir(parents=True,exist_ok=True);(out/'main.go').write_text(go);(out/'input.json').write_text(json.dumps(cases))
platform=Path(os.environ.get('NK_V3_PLATFORM',repo.parent/'work/v2.1-b1-20261004/platform'));goroot=platform/'server/goroot';env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(repo/'work/v3/gocache'))
with(out/'input.json').open('rb')as f:result=json.loads(subprocess.check_output([goroot/'bin/go','run',out/'main.go'],cwd=repo/'server',env=env,stdin=f))
(repo/'docs/experiments/v3.0/services-go-oracle.json').write_text(json.dumps(dict(baseline=baseline,goVersion='1.27.1',cases=result),indent=2)+'\n');print(len(result),'actual Go service cases; no host commands')
