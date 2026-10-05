#!/usr/bin/env python3
"""Immutable dashboard functions with fixture filesystem/network/statfs/clock."""
from pathlib import Path
import base64,json,os,subprocess
repo=Path(__file__).resolve().parents[1];baseline='a53b25579ab87cc98f85323b4743deb0d4da907b'
def source(name):return subprocess.check_output(['git','show',baseline+':server/dashboard/'+name],cwd=repo,text=True)
def function(text,name):
 start=text.index('func '+name+'(');level=1;end=text.index('{',start)+1
 while level:
  if text[end]=='{':level+=1
  if text[end]=='}':level-=1
  end+=1
 return text[start:end]
status=source('status.go');temp=source('temperature.go');freq=source('cpufreq.go');sampler=source('cpu_usage.go');links=source('links.go')
go='package main\nimport("encoding/base64";"encoding/binary";"encoding/json";"fmt";"net";"os";"path/filepath";"strconv";"strings";"sync";"time";"golang.org/x/sys/unix")\nvar root string\nvar now=time.Unix(123456,0)\nvar fixtureInterfaces []net.Interface\nvar fixtureKinds map[int]string\nvar fixtureAddresses map[int][]string\nvar fixtureStats map[string]unix.Statfs_t\n'
a=status.index('type CPU struct');b=status.index('func read(');go+=status[a:b]
go+='func fixturePath(path string)string{if strings.HasPrefix(path,root+"/"){return path};return filepath.Join(root,path)}\n'
go+=function(status,'read').replace('os.ReadFile(path)','os.ReadFile(fixturePath(path))')+'\n'+function(status,'counter')+'\n'+function(status,'parseCPU')+'\n'+function(status,'mountedPaths')+'\n'+function(status,'storage').replace('unix.Statfs(path, &stat)','fixtureStatfs(path, &stat)')+'\n'+function(status,'interfaceEnabled')+'\n'
go+=function(temp,'readTemperature').replace('filepath.Join(sys,','filepath.Join(root,sys,')+'\n'+function(freq,'readCPUFrequency')+'\n'
a=sampler.index('type cpuSampler struct');b=sampler.index('func init()');go+=sampler[a:b]
go+=function(links,'linkAttribute')+'\n'
read=function(status,'Read').replace('time.Now()','now').replace('runtime.GOARCH','"riscv64"').replace('runtime.NumCPU()','2').replace('linkKinds()','fixtureKinds').replace('net.Interfaces()','fixtureIfaces()').replace('iface.Addrs()','fixtureAddrs(iface.Index)').replace('os.Stat(filepath.Join(base, "wireless"))','os.Stat(filepath.Join(root,base,"wireless"))').replace('interfaceEnabled(iface.Name, "/boot", "/etc/kvm")','interfaceEnabled(iface.Name,filepath.Join(root,"boot"),filepath.Join(root,"etc/kvm"))')
go+=read+'\n'+r'''
func fixtureStatfs(path string,stat *unix.Statfs_t)error{value,ok:=fixtureStats[path];if !ok{return fmt.Errorf("missing fixture statfs")};*stat=value;return nil}
func fixtureIfaces()([]net.Interface,error){return fixtureInterfaces,nil}
func fixtureAddrs(index int)([]net.Addr,error){out:=[]net.Addr{};for _,s:=range fixtureAddresses[index]{ip,n,e:=net.ParseCIDR(s);if e!=nil{continue};n.IP=ip;out=append(out,n)};return out,nil}
'''
cases=[]
def case(name,files=None,interfaces=None,stats=None):
 cases.append(dict(name=name,files={p:base64.b64encode(v if isinstance(v,bytes) else v.encode()).decode() for p,v in (files or {}).items()},interfaces=interfaces or [],stats=stats or {}))
base={'/proc/sys/kernel/hostname':' nano\n','/proc/sys/kernel/osrelease':'6.6.0\n','/proc/stat':'cpu 100 20 30 400 50 6 7 8 90 10\ncpu0 1 2 3 4','/proc/uptime':'100.25 19.0\n','/proc/loadavg':'0.00 1.00 2.00 1/99 123\n'}
case('missing');case('normal',base);case('invalid-utf8',{**base,'/proc/sys/kernel/hostname':b'\xff\xe1\x80\n'})
for cpu in ['', 'cpu0 1 2 3 4','cpu 1 2 bad 4','cpu 1 2 3','cpu +1 2 3 4','cpu 18446744073709551615 1 2 3','cpu 1 2 3 4 5 6 7 8 bad']:
 case('cpu-'+str(len(cases)),{**base,'/proc/stat':cpu})
for uptime in ['','bad','-1','0','1.5','+2.5','1e3','NaN']:
 case('uptime-'+uptime,{**base,'/proc/uptime':uptime})
for load in ['','1 2','1 2 3 4','\t1\u20022\u30003\n']:
 case('load-'+str(len(cases)),{**base,'/proc/loadavg':load})
therm={'/sys/class/hwmon/hwmon0/name':'nvme\n','/sys/class/hwmon/hwmon0/temp1_input':'99000','/sys/class/thermal/thermal_zone0/type':'SoC-thermal','/sys/class/thermal/thermal_zone0/temp':'45000'}
case('thermal-fallback',{**base,**therm})
for value in ['52875','','NaN','-273000','151000','not available','0','-40000','150000','+42000']:
 case('board-temp-'+value,{**base,**therm,'/sys/class/hwmon/hwmon1/name':'sg2002','/sys/class/hwmon/hwmon1/temp1_input':value,'/sys/class/hwmon/hwmon1/temp2_input':'invalid'})
case('max-temp',{**base,**therm,'/sys/class/hwmon/hwmon1/name':'sg2002','/sys/class/hwmon/hwmon1/temp1_input':'55000','/sys/class/hwmon/hwmon1/temp2_input':'57000'})
case('wrong-name',{**base,'/sys/class/hwmon/hwmon0/name':'sg2002-extra','/sys/class/hwmon/hwmon0/temp1_input':'55000','/sys/class/thermal/thermal_zone0/type':'ambient','/sys/class/thermal/thermal_zone0/temp':'45000'})
for frequency in [None,'850000','1000000','1125000','1','0','-1','+1234000','invalid','9223372036854775808']:
 files={**base,'/sys/devices/system/cpu/cpufreq/policy0/scaling_setspeed':'1150000'}
 if frequency is not None:files['/sys/devices/system/cpu/cpufreq/policy0/cpuinfo_cur_freq']=frequency
 case('frequency-'+str(frequency),files)
stat={'blocks':100,'available':30,'free':40,'blockSize':4096,'flags':1}
case('unmounted',base,stats={'/data':stat});case('mounted',{**base,'/proc/mounts':'/dev/root / ext4 rw 0 0\n/dev/sd /data ext4 ro 0 0\n'},stats={'/':{**stat,'flags':0},'/data':stat});case('stat-error',{**base,'/proc/mounts':'/dev/sd /data ext4 rw 0 0\n'});case('zero-stat',{**base,'/proc/mounts':'/dev/sd /data ext4 rw 0 0\n'},stats={'/data':{**stat,'blocks':0}});case('over-free',{**base,'/proc/mounts':'/dev/sd /data ext4 rw 0 0\n'},stats={'/data':{**stat,'free':101}})
interfaces=[dict(index=i+1,name=name,up=up,running=running,loopback=loopback,mtu=1500,mac='02:00:00:00:00:01',kind=kind,addresses=addresses) for i,(name,up,running,loopback,kind,addresses) in enumerate([('lo',True,True,True,'',['127.0.0.1/8']),('eth0',True,True,False,'',['192.0.2.1/24','2001:db8::1/64']),('eth0.100',True,False,False,'vlan',['198.51.100.1/24']),('wlan0',False,False,False,'',[]),('wg0',True,True,False,'wireguard',['10.0.0.1/32']),('wlx0',True,True,False,'',[])])]
case('interfaces',base,interfaces=interfaces)
case('preferences-carrier',{**base,'/boot/eth.disabled':'1','/etc/kvm/wifi.disabled':'1','/sys/class/net/eth0/carrier':'0','/sys/class/net/eth0/statistics/rx_bytes':'+1','/sys/class/net/eth0/statistics/tx_bytes':'18446744073709551615','/sys/class/net/wg0/statistics/rx_bytes':'42','/sys/class/net/wg0/statistics/tx_bytes':'bad','/sys/class/net/wg0/wireless/marker':''},interfaces=interfaces)
case('counter-overflow',{**base,'/sys/class/net/eth0/statistics/rx_bytes':'18446744073709551616','/sys/class/net/eth0/statistics/tx_bytes':'0'},interfaces=interfaces)
go+=r'''
func main(){var input []struct{Name string;Files map[string]string;Interfaces []struct{Index int;Name string;Up,Running,Loopback bool;MTU int;MAC,Kind string;Addresses []string};Stats map[string]struct{Blocks,Available,Free uint64;BlockSize,Flags int64}};if e:=json.NewDecoder(os.Stdin).Decode(&input);e!=nil{panic(e)};result:=[]any{};for _,c:=range input{dir,e:=os.MkdirTemp("","dashboard-go-oracle-");if e!=nil{panic(e)};root=dir;for name,value:=range c.Files{path:=filepath.Join(root,name);os.MkdirAll(filepath.Dir(path),0755);data,_:=base64.StdEncoding.DecodeString(value);os.WriteFile(path,data,0644)};fixtureInterfaces=nil;fixtureAddresses=map[int][]string{};fixtureKinds=map[int]string{};fixtureStats=map[string]unix.Statfs_t{};for _,item:=range c.Interfaces{var flags net.Flags;if item.Up{flags|=net.FlagUp};if item.Running{flags|=net.FlagRunning};if item.Loopback{flags|=net.FlagLoopback};mac,_:=net.ParseMAC(item.MAC);fixtureInterfaces=append(fixtureInterfaces,net.Interface{Index:item.Index,Name:item.Name,Flags:flags,MTU:item.MTU,HardwareAddr:mac});fixtureAddresses[item.Index]=item.Addresses;fixtureKinds[item.Index]=item.Kind};for path,item:=range c.Stats{fixtureStats[path]=unix.Statfs_t{Blocks:item.Blocks,Bavail:item.Available,Bfree:item.Free,Bsize:item.BlockSize,Flags:item.Flags}};result=append(result,map[string]any{"name":c.Name,"files":c.Files,"interfaces":c.Interfaces,"stats":c.Stats,"status":Read()});os.RemoveAll(root)};samplerResult:=[]any{};var sample cpuSampler;for i,item:=range []*CPU{{Total:100,Idle:60},{Total:200,Idle:130},{Total:1,Idle:1},nil,{Total:100,Idle:10},{Total:101,Idle:20},{Total:201,Idle:70}}{at:=now.Add(time.Duration(i)*time.Second);sample.update(item,at);samplerResult=append(samplerResult,map[string]any{"cpu":item,"usage":sample.value(at),"at3":sample.value(at.Add(3*time.Second)),"at4":sample.value(at.Add(4*time.Second))})};cidrs:=[]any{};for _,pair:=range [][2]string{{"192.0.2.1","255.255.255.0"},{"2001:db8::1","ffff:ffff:ffff:ffff::"},{"::ffff:192.0.2.1","ffff:ffff:ffff:ffff:ffff:ffff:ffff:ff00"},{"192.0.2.1","255.0.255.0"},{"2001:db8::1","ff00:ffff::"}}{ip,mask:=net.ParseIP(pair[0]),net.ParseIP(pair[1]);cidrs=append(cidrs,map[string]any{"address":pair[0],"mask":pair[1],"text":(&net.IPNet{IP:ip,Mask:net.IPMask(mask)}).String()})};attrs:=[]any{};for _,data:=range [][]byte{{1,0,1,0},{200,0,1,0},{0,0},{},{8,0,1,128,1,2,3,4},{5,0,2,0,9,0,0,0,5,0,1,0,7,0,0,0}}{attrs=append(attrs,map[string]any{"data":base64.StdEncoding.EncodeToString(data),"value":base64.StdEncoding.EncodeToString(linkAttribute(data,1))})};var native unix.Statfs_t;if e:=unix.Statfs("/",&native);e!=nil{panic(e)};output:=map[string]any{"cases":result,"sampler":samplerResult,"cidrs":cidrs,"attributes":attrs,"native":map[string]any{"total":native.Blocks*uint64(native.Bsize),"readOnly":native.Flags&unix.ST_RDONLY!=0}};if e:=json.NewEncoder(os.Stdout).Encode(output);e!=nil{panic(e)}}
'''
out=repo/'work/v3/dashboard-oracle';out.mkdir(parents=True,exist_ok=True);(out/'main.go').write_text(go);(out/'input.json').write_text(json.dumps(cases))
platform=Path(os.environ.get('NK_V3_PLATFORM',repo.parent/'work/v2.1-b1-20261004/platform'));goroot=platform/'server/goroot';env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(repo/'work/v3/gocache'))
with (out/'input.json').open('rb') as f:result=json.loads(subprocess.check_output([goroot/'bin/go','run',out/'main.go'],cwd=repo/'server',env=env,stdin=f))
(repo/'docs/experiments/v3.0/dashboard-go-oracle.json').write_text(json.dumps(result,indent=2)+'\n');print(len(result['cases']),'actual immutable Go dashboard cases;',len(result['sampler']),'CPU intervals;',len(result['cidrs']),'IPNet strings;',len(result['attributes']),'netlink attributes')
