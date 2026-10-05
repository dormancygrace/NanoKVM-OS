#!/usr/bin/env python3
"""Run immutable Go CPU status logic against isolated policy/thermal files."""
from pathlib import Path
import json, os, subprocess
repo=Path(__file__).resolve().parents[1]
baseline='a53b25579ab87cc98f85323b4743deb0d4da907b'
source=subprocess.check_output(['git','show',baseline+':server/service/vm/cpufreq.go'],cwd=repo,text=True)
def function(name):
    start=source.index('func '+name+'(');body=source.index('{',start);level=1;end=body+1
    while level:
        if source[end]=='{':level+=1
        if source[end]=='}':level-=1
        end+=1
    return source[start:end]
start=source.index('type cpuFrequencyStatus struct {');end=source.index('\n}',start)+2
policy='/sys/devices/system/cpu/cpufreq/policy0/'
base={policy+'scaling_driver':'sg2002-cpufreq\n',policy+'cpuinfo_cur_freq':'850000\n'}
cases=[]
def case(name,updates=None,qualified=True):
    files=dict(base) if qualified else {}
    files.update(updates or {})
    cases.append(dict(name=name,files=files))
case('missing',qualified=False)
case('qualified-default')
case('wrong-driver',{policy+'scaling_driver':'unqualified'})
for value in ['0','-1','invalid','9223372036854775808','+1000000','1','849999']:
    case('running-'+value,{policy+'cpuinfo_cur_freq':value})
for value in ['850','1000','1050','invalid','  +850\n','9223372036854775808']:
    case('boot-'+value,{'/etc/kvm/cpufreq':value})
for value in ['1125','1175','invalid','-0']:
    case('runtime-'+value,{'/etc/kvm/cpufreq':'850','/run/nanokvm-cpufreq':value})
for name,value in [('empty',''),('invalid','bad 600000 1175000 850001'),('ordered','1125000 850000 600000 850000 +1000000 1150000 850001')]:
    case('options-'+name,{policy+'scaling_available_frequencies':value})
thermal='/sys/class/thermal/thermal_zone0/'
for name,zone,cooling,state in [('known','sg2002-cpu','cpufreq-cpu0','1'),('high','sg2002-cpu','cpufreq-cpu0','6'),('wrong-zone','other','cpufreq-cpu0','1'),('wrong-cooling','sg2002-cpu','other','1'),('zero','sg2002-cpu','cpufreq-cpu0','0'),('negative','sg2002-cpu','cpufreq-cpu0','-1'),('invalid','sg2002-cpu','cpufreq-cpu0','bad')]:
    case('thermal-'+name,{thermal+'type':zone,thermal+'cdev0/type':cooling,thermal+'cdev0/cur_state':state})
out=repo/'work/v3/cpufreq-oracle';out.mkdir(parents=True,exist_ok=True)
(out/'input.json').write_text(json.dumps(cases))
go='package main\nimport("encoding/json";"os";"path/filepath";"strconv";"strings")\nvar cpuFreqPolicy,cpuFreqPreference,cpuFreqRuntimePreference,cpuThermalRoot string\n'+source[start:end]+'\n'+'\n'.join(function(name) for name in ['readCPUFreq','cpuThermalLimited','cpuFrequencyState','validCPUFrequency','cpuFrequencyOptions'])+r'''
func main(){var input []struct{Name string `json:"name"`;Files map[string]string `json:"files"`};if e:=json.NewDecoder(os.Stdin).Decode(&input);e!=nil{panic(e)};result:=[]any{};for _,c:=range input{root,e:=os.MkdirTemp("","cpu-go-oracle-");if e!=nil{panic(e)};for path,data:=range c.Files{target:=filepath.Join(root,strings.TrimPrefix(path,"/"));if e:=os.MkdirAll(filepath.Dir(target),0755);e!=nil{panic(e)};if e:=os.WriteFile(target,[]byte(data),0644);e!=nil{panic(e)}};cpuFreqPolicy=filepath.Join(root,"sys/devices/system/cpu/cpufreq/policy0");cpuFreqPreference=filepath.Join(root,"etc/kvm/cpufreq");cpuFreqRuntimePreference=filepath.Join(root,"run/nanokvm-cpufreq");cpuThermalRoot=filepath.Join(root,"sys/class/thermal");result=append(result,map[string]any{"name":c.Name,"files":c.Files,"status":cpuFrequencyState()});if e:=os.RemoveAll(root);e!=nil{panic(e)}};if e:=json.NewEncoder(os.Stdout).Encode(result);e!=nil{panic(e)}}
'''
(out/'main.go').write_text(go)
platform=Path(os.environ.get('NK_V3_PLATFORM',repo.parent/'work/v2.1-b1-20261004/platform'));goroot=platform/'server/goroot'
env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(repo/'work/v3/gocache'),GO111MODULE='off')
with (out/'input.json').open('rb') as f:result=json.loads(subprocess.check_output([goroot/'bin/go','run',out/'main.go'],cwd=out,env=env,stdin=f))
(repo/'docs/experiments/v3.0/cpufreq-go-oracle.json').write_text(json.dumps(result,indent=2)+'\n')
print(len(result),'immutable Go CPU status cases; baseline',baseline)
