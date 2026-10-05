#!/usr/bin/env python3
"""Replay immutable Go time configuration/binding and local status parsers."""
from pathlib import Path
import base64,json,os,subprocess
repo=Path(__file__).resolve().parents[1];baseline='a53b25579ab87cc98f85323b4743deb0d4da907b'
def source(path):return subprocess.check_output(['git','show',baseline+':'+path],cwd=repo,text=True)
def function(text,name):
    start=text.index('func '+name+'(');level=1;end=text.index('{',start)+1
    while level:
        if text[end]=='{':level+=1
        if text[end]=='}':level-=1
        end+=1
    return text[start:end]
config=source('server/timeconfig/config.go')
assert '\n\t"NanoKVM-Server/utils"\n' in config
config=config.replace('package timeconfig','package main',1).replace('\n\t"NanoKVM-Server/utils"\n','\n\t"encoding/base64"\n\t"encoding/binary"\n\t"encoding/csv"\n\t"strconv"\n\t"bytes"\n\t"net/http/httptest"\n\t"github.com/gin-gonic/gin"\n',1)
config=config.replace('utils.JoinWithin("/usr/share/zoneinfo", name)','JoinWithin(zoneRoot, name)').replace('os.ReadFile("/usr/share/zoneinfo/zone.tab")','os.ReadFile(filepath.Join(zoneRoot,"zone.tab"))')
config+='\nvar zoneRoot string\nvar ErrOutsideDirectory=errors.New("path is outside its directory")\n'+function(source('server/utils/path.go'),'JoinWithin')+'\n'+function(source('server/timeconfig/chrony.go'),'parseChronyTracking')+'\n'+function(source('server/timeconfig/ntp.go'),'parseNTPStatus')
fixture={name:base64.b64encode(Path('/usr/share/zoneinfo',name).read_bytes()).decode() for name in ['UTC','Asia/Jerusalem']}
fixture['zone.tab']=base64.b64encode(b'# fixture\nIL\t+3146+03514\tAsia/Jerusalem\nXX 0 UTC\nIL 0 Asia/Jerusalem\n').decode();fixture['Invalid']=base64.b64encode(b'not TZif').decode()
good=dict(servers=['pool.ntp.org','192.0.2.1','2001:db8::1'],timezone='Asia/Jerusalem',format='24')
validation=[]
def valid_case(name,**changes):validation.append(dict(name=name,config=dict(good,**changes)))
valid_case('good')
for tz in ['', '.', '..','../UTC','Europe/../UTC','/UTC','UTC/','./UTC','Asia//Jerusalem','Missing/Zone','Invalid','UTC\\x','zone.tab']:valid_case('zone-'+tz,timezone=tz)
for fmt in ['12','24','auto','']:valid_case('format-'+fmt,format=fmt)
for server in ['-q','pool.ntp.org\nrestrict default','foo;reboot','https://pool.ntp.org','foo bar','','a..b','a.','0','a'*63,'a'*64,'a.'*126+'b','a.'*127+'b','192.168.01.1','::ffff:192.0.2.1','fe80::1%eth0','2001:db8:0:0:0:0:0:1']:
    valid_case('server-'+server,servers=[server])
for name,servers in [('none',None),('empty',[]),('seven',['s'+str(n) for n in range(7)]),('duplicate',['pool.ntp.org','POOL.NTP.ORG.']),('ips-distinct-text',['2001:db8::1','2001:db8:0:0:0:0:0:1'])]:valid_case(name,servers=servers)
binding=[]
for name,body in [('good',json.dumps(good)),('null','null'),('missing','{}'),('array','[]'),('servers-null','{"servers":null}'),('server-null-element','{"servers":[null,"pool.ntp.org"]}'),('server-number','{"servers":[1]}'),('servers-string','{"servers":"pool.ntp.org"}'),('primitive-null','{"timezone":"UTC","TIMEZONE":null,"format":"24","FORMAT":null}'),('case','{"SERVERS":["x"],"TIMEZONE":"UTC","FORMAT":"12"}'),('duplicate','{"servers":["a"],"SERVERS":["b"]}'),('duplicate-error','{"servers":[1],"SERVERS":["b"]}'),('clear','{"servers":["x"],"SERVERS":null}'),('unknown-large','{"unknown":1e10000,"format":"24"}'),('trailing','{"format":"24"} true'),('surrogate','{"timezone":"\\ud800"}'),('wrong-string','{"format":24}')]:binding.append(dict(name=name,body=body))
reads=[]
def read_case(name,files):reads.append(dict(name=name,files=files))
read_case('defaults',{})
read_case('legacy',{'timezone':' Asia/Jerusalem\n','ntp.conf':'server old.example iburst\npool pool.example\nrestrict default noquery\n'})
read_case('chrony',{'ntp.conf':'server ignored\n','chrony.conf':'pool chrony.example iburst\n'})
for name,body in [('saved',json.dumps(good)),('empty','{}'),('null','null'),('null-strings','{"timezone":null,"format":null}'),('servers-null','{"servers":null}'),('wrong-type','{"servers":[1]}'),('invalid-format','{"format":"auto"}'),('trailing','{} true'),('broken','{')]:read_case(name,{'kvm/date-time.json':body})
read_case('invalid-legacy-unvalidated',{'timezone':'Invalid','ntp.conf':'server -q\n'})
configs=[]
for name,old,chrony in [('ntp-empty',b'',False),('chrony-empty',b'',True),('policy',b'server old iburst\npool old2\n  restrict default noquery\ndriftfile /var/lib/ntp/drift\n',False),('raw',b'server old\n# raw\xff\r\nport 0\n\n',True),('white',b'\xc2\xa0server old\n# kept\n',False)]:configs.append(dict(name=name,input=base64.b64encode(old).decode(),servers=['new.example','192.0.2.1'],chrony=chrony))
tracking='C0A80101,192.168.1.1,3,1789128483.0,0.0001,-0.0002,0.0003,1.0,0.0,0.1,0.02,0.003,64.0,Normal'
chrony=[]
for leap in ['Normal','Insert second','Delete second','Not synchronised','unknown']:chrony.append(dict(name='leap-'+leap,text=tracking.replace('Normal',leap)))
for value in ['0','16','+3','-1','bad',' 3']:
    fields=tracking.split(',');fields[2]=value;chrony.append(dict(name='stratum-'+value,text=','.join(fields)))
for ref in ['00000000','7F7F0101']:chrony.append(dict(name='local-'+ref,text=tracking.replace('C0A80101',ref)))
for name,text in [('empty',''),('error','506 Cannot talk to daemon'),('twice',tracking+'\n'+tracking),('crlf',tracking+'\r\n'),('quoted-comma',tracking.replace('192.168.1.1','"name,host"')),('quoted-newline',tracking.replace('192.168.1.1','"name\nline"')),('quote-escape',tracking.replace('192.168.1.1','"name""host"')),('bare-quote',tracking.replace('192.168.1.1','name"host')),('quote-space',tracking.replace('Normal','"Normal" ')),('malformed-quoted',tracking.replace('Normal','"Normal')),('blank',tracking+'\n\n')]:chrony.append(dict(name=name,text=text))
ntp=[]
packet=bytearray(12);packet[0]=4<<3|6;packet[1]=0x80|2;packet[2:4]=(42).to_bytes(2,'big');packet[4:6]=(0x0615).to_bytes(2,'big')
def ntp_case(name,data,sequence=42):ntp.append(dict(name=name,packet=base64.b64encode(data).decode(),sequence=sequence))
for status in [0x0615,0xc016,0xc615,0x4615,0x8615,0x0515]:
    data=bytearray(packet);data[4:6]=status.to_bytes(2,'big');ntp_case('status-'+hex(status),data)
ntp_case('wrong-sequence',packet,43);ntp_case('short',packet[:8])
for name,offset,value in [('mode',0,4<<3|5),('version',0,1<<3|6),('no-response',1,2),('error-flag',1,0xc2),('opcode',1,0x83),('association',7,1),('offset',9,1),('truncated-payload',11,1),('more-fragments',1,0xa2)]:
    data=bytearray(packet);data[offset]=value;ntp_case(name,data)
input=dict(zones=fixture,validation=validation,binding=binding,reads=reads,configs=configs,chrony=chrony,ntp=ntp)
out=repo/'work/v3/time-oracle';out.mkdir(parents=True,exist_ok=True);(out/'input.json').write_text(json.dumps(input))
config+=r'''
func main(){gin.SetMode(gin.ReleaseMode);var input struct{Zones map[string]string;Validation []struct{Name string;Config Config};Binding []struct{Name,Body string};Reads []struct{Name string;Files map[string]string};Configs []struct{Name,Input string;Servers []string;Chrony bool};Chrony []struct{Name,Text string};Ntp []struct{Name,Packet string;Sequence uint16}};if e:=json.NewDecoder(os.Stdin).Decode(&input);e!=nil{panic(e)};root,e:=os.MkdirTemp("","time-go-oracle-");if e!=nil{panic(e)};defer os.RemoveAll(root);zoneRoot=filepath.Join(root,"zoneinfo");for name,value:=range input.Zones{path:=filepath.Join(zoneRoot,name);os.MkdirAll(filepath.Dir(path),0755);data,_:=base64.StdEncoding.DecodeString(value);os.WriteFile(path,data,0644)};result:=map[string]any{"zones":input.Zones,"zoneNames":Zones()};valid:=[]any{};for _,c:=range input.Validation{message:="";if e:=Validate(c.Config);e!=nil{message=e.Error()};valid=append(valid,map[string]any{"name":c.Name,"config":c.Config,"error":message})};result["validation"]=valid;bound:=[]any{};for _,c:=range input.Binding{ctx,_:=gin.CreateTestContext(httptest.NewRecorder());ctx.Request=httptest.NewRequest("POST","http://localhost",bytes.NewBufferString(c.Body));var config Config;e:=ctx.ShouldBindJSON(&config);bound=append(bound,map[string]any{"name":c.Name,"body":c.Body,"config":config,"error":e!=nil})};result["binding"]=bound;reads:=[]any{};for index,c:=range input.Reads{dir:=filepath.Join(root,fmt.Sprint(index));os.MkdirAll(dir,0755);for name,value:=range c.Files{path:=filepath.Join(dir,name);os.MkdirAll(filepath.Dir(path),0755);os.WriteFile(path,[]byte(value),0644)};store:=Store{Dir:dir};config,e:=store.Read();reads=append(reads,map[string]any{"name":c.Name,"files":c.Files,"config":config,"error":e!=nil})};result["reads"]=reads;configs:=[]any{};for _,c:=range input.Configs{old,_:=base64.StdEncoding.DecodeString(c.Input);configs=append(configs,map[string]any{"name":c.Name,"input":c.Input,"servers":c.Servers,"chrony":c.Chrony,"output":base64.StdEncoding.EncodeToString(serverConfig(old,c.Servers,c.Chrony))})};result["configs"]=configs;chrony:=[]any{};for _,c:=range input.Chrony{ok,e:=parseChronyTracking(c.Text);chrony=append(chrony,map[string]any{"name":c.Name,"text":c.Text,"synchronized":ok,"error":e!=nil})};result["chrony"]=chrony;ntp:=[]any{};for _,c:=range input.Ntp{packet,_:=base64.StdEncoding.DecodeString(c.Packet);ok,e:=parseNTPStatus(packet,c.Sequence);ntp=append(ntp,map[string]any{"name":c.Name,"packet":c.Packet,"sequence":c.Sequence,"synchronized":ok,"error":e!=nil})};result["ntp"]=ntp;millis:=[]any{};for _,n:=range []int64{-1500000,-1000000,-999999,-1,0,1,999999,1000000,1500000}{millis=append(millis,map[string]int64{"nanos":n,"millis":time.Unix(0,n).UnixMilli()})};result["millis"]=millis;if e:=json.NewEncoder(os.Stdout).Encode(result);e!=nil{panic(e)}}
'''
(out/'main.go').write_text(config)
platform=Path(os.environ.get('NK_V3_PLATFORM',repo.parent/'work/v2.1-b1-20261004/platform'));goroot=platform/'server/goroot'
env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(repo/'work/v3/gocache'))
with (out/'input.json').open('rb') as f:result=json.loads(subprocess.check_output([goroot/'bin/go','run',out/'main.go'],cwd=repo/'server',env=env,stdin=f))
result['baseline']=baseline;result['goVersion']='1.27.1'
(repo/'docs/experiments/v3.0/time-go-oracle.json').write_text(json.dumps(result,indent=2)+'\n');print({name:len(result[name]) for name in ['validation','binding','reads','configs','chrony','ntp']})
