#!/usr/bin/env python3
"""Immutable baseline identity/IP/pid functions, isolated files and interfaces."""
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
info=source('server/service/vm/info.go');ip=source('server/service/vm/ip.go');mdns=source('server/service/vm/mdns.go');proto=source('server/proto/vm.go')
go='package main\nimport("encoding/base64";"encoding/json";"fmt";"os";"path/filepath";"strings";"net";log "github.com/sirupsen/logrus")\nvar root,AvahiDaemonPid string\nvar fixtureIPs map[string]net.IP\n'
for name in ['IP','GetInfoRsp']:
    start=proto.index('type '+name+' struct {');end=proto.index('\n}',start)+2;go+=proto[start:end]+'\n'
start=ip.index('type InterfaceInfo struct {');end=ip.index('\n}',start)+2;go+=ip[start:end]+'\nconst(Wired="Wired";Wireless="Wireless";Other="Other")\n'
start=info.index('var imageVersionMap =');end=info.index('\n}',start)+2;go+=info[start:end]+'\n'
for name in ['getMdns','getImageVersion','getApplicationVersion','getDeviceKey']:
    text=function(info,name)
    for path in ['/etc/hostname','/boot/ver','/kvmapp/version','/device_key']:text=text.replace('os.ReadFile("'+path+'")','os.ReadFile(filepath.Join(root,"'+path.lstrip('/')+'"))')
    go+=text+'\n'
go+=function(mdns,'getAvahiDaemonPid')+'\n'+function(ip,'getInterfaceType')+'\n'+function(ip,'getInterfaceInfo').replace('getInterfaceIP(iface)','fixtureIPs[iface.Name]')+'\n'+function(info,'getIPs').replace('proto.IP','IP')+'\nvar fixtureInterfaces []*InterfaceInfo\nfunc GetInterfaceInfos()([]*InterfaceInfo,error){return fixtureInterfaces,nil}\n'
cases=[]
def case(name,files=None,interfaces=None):cases.append(dict(name=name,files={path:base64.b64encode(value if isinstance(value,bytes) else value.encode()).decode() for path,value in (files or {}).items()},interfaces=interfaces or []))
case('defaults')
for image in ['2024-06-23-20-59-2d2bfb.img','2024-07-23-20-18-587710.img','2024-08-08-19-44-bef2ca.img','2024-11-13-09-59-9c961a.img','2025-02-17-19-08-3649fe.img','2025-04-17-14-21-98d17d.img','2026-01-05-1_4_1.img','v3-experimental','v3\r\n']:
    case('image-'+image,{'/boot/ver':image+'\n','/kvmapp/version':'3.0\nexperimental\n','/device_key':' device\r\nkey\n'})
for name,pid,hostname in [('normal','123\n','nano\n'),('empty-pid','\n','nano'),('spaces-pid',' \r\n',' nano\r\n'),('empty-host','1','\n'),('invalid-bytes',b'1',b'na\xe1\x80\xff\n')]:case('mdns-'+name,{'/run/avahi-daemon/pid':pid,'/etc/hostname':hostname})
case('missing-host',{'/run/avahi-daemon/pid':'1'});case('raw',{'/boot/ver':b'\xff\n','/kvmapp/version':b'\xe1\x80\xff','/device_key':b'\xff\xff\n'})
for name,up,running,addresses in [('eth0',True,True,['192.0.2.1','2001:db8::1']),('enp0s1',True,True,['198.51.100.1']),('wlan0',True,True,['192.0.2.2']),('wlx0',True,True,['::ffff:192.0.2.3']),('wg0',True,True,['192.0.2.4']),('lo',True,True,['127.0.0.1']),('eth0',False,True,['192.0.2.1']),('eth0',True,False,['169.254.1.1']),('eth0',True,True,['2001:db8::1','192.0.2.1']),('eth0',True,True,[])]:case('interface-'+str(len(cases)),interfaces=[dict(name=name,up=up,running=running,addresses=addresses)])
case('ordered',interfaces=[dict(name=name,up=True,running=True,addresses=[address]) for name,address in [('wlan0','192.0.2.2'),('eth0','192.0.2.1')]])
out=repo/'work/v3/info-oracle';out.mkdir(parents=True,exist_ok=True);(out/'input.json').write_text(json.dumps(cases))
go+=r'''
func main(){var input []struct{Name string;Files map[string]string;Interfaces []struct{Name string `json:"name"`;Up bool `json:"up"`;Running bool `json:"running"`;Addresses []string `json:"addresses"`}};if e:=json.NewDecoder(os.Stdin).Decode(&input);e!=nil{panic(e)};result:=[]any{};for _,c:=range input{dir,e:=os.MkdirTemp("","info-go-oracle-");if e!=nil{panic(e)};root=dir;AvahiDaemonPid=filepath.Join(root,"run/avahi-daemon/pid");for name,value:=range c.Files{path:=filepath.Join(root,strings.TrimPrefix(name,"/"));os.MkdirAll(filepath.Dir(path),0755);data,_:=base64.StdEncoding.DecodeString(value);os.WriteFile(path,data,0644)};fixtureInterfaces=nil;fixtureIPs=map[string]net.IP{};for _,item:=range c.Interfaces{var flags net.Flags;if item.Up{flags|=net.FlagUp};if item.Running{flags|=net.FlagRunning};if len(item.Addresses)>0{fixtureIPs[item.Name]=net.ParseIP(item.Addresses[0])};if info:=getInterfaceInfo(net.Interface{Name:item.Name,Flags:flags});info!=nil{fixtureInterfaces=append(fixtureInterfaces,info)}};info:=GetInfoRsp{IPs:getIPs(),Mdns:getMdns(),Image:getImageVersion(),Application:getApplicationVersion(),DeviceKey:getDeviceKey()};result=append(result,map[string]any{"name":c.Name,"files":c.Files,"interfaces":c.Interfaces,"info":info,"mdnsEnabled":getAvahiDaemonPid()!=""});os.RemoveAll(root)};if e:=json.NewEncoder(os.Stdout).Encode(result);e!=nil{panic(e)}}
'''
(out/'main.go').write_text(go)
platform=Path(os.environ.get('NK_V3_PLATFORM',repo.parent/'work/v2.1-b1-20261004/platform'));goroot=platform/'server/goroot';env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(repo/'work/v3/gocache'))
with (out/'input.json').open('rb') as f:result=json.loads(subprocess.check_output([goroot/'bin/go','run',out/'main.go'],cwd=repo/'server',env=env,stdin=f))
(repo/'docs/experiments/v3.0/info-go-oracle.json').write_text(json.dumps(result,indent=2)+'\n');print(len(result),'immutable Go identity/IP/pid cases')
