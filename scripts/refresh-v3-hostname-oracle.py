#!/usr/bin/env python3
"""Extract and run pure hostname functions from the immutable Go baseline."""
from pathlib import Path
import json,os,subprocess
repo=Path(__file__).resolve().parents[1]
baseline='a53b25579ab87cc98f85323b4743deb0d4da907b'
source=subprocess.check_output(['git','show',baseline+':server/service/vm/hostname.go'],cwd=repo,text=True)
def function(name):
 start=source.index('func '+name+'(');body=source.index('{',start);level=1;end=body+1
 while level:
  if source[end]=='{':level+=1
  if source[end]=='}':level-=1
  end+=1
 return source[start:end]
label=next(line for line in source.splitlines() if line.startswith('var hostnameLabel ='))
out=repo/'work/v3/hostname-oracle';out.mkdir(parents=True,exist_ok=True)
names=['','local','nanokvm-1','Example.CO.UK','0','-a','a-','a_b','a b','a..b','a.','плата','a'*63,'a'*64,'.'.join(['a'*63]*3+['b'*61]),'.'.join(['a'*63]*4)]
hosts=[dict(input='127.0.0.1 local localhost # local in comment\n::1\tlocal\tother\n# local\n',old='local',new='nano.example'),dict(input='  10.0.0.1\told old\t# untouched  \ninvalid\n',old='old',new='new'),dict(input='127.0.0.1 old-name old\n',old='old',new='X'),dict(input='127.0.0.1 old\n',old='',new='new'),dict(input='127.0.0.1\u00a0old\u0085alias #c\n',old='old',new='new')]
(out/'input.json').write_text(json.dumps(dict(names=names,hosts=hosts)))
go='package main\nimport("encoding/base64";"encoding/json";"errors";"os";"regexp";"strings")\n'+label+'\n'+function('validHostname')+'\n'+function('replaceHostEntry')+r'''
func main(){var input struct{Names []string `json:"names"`;Hosts []struct{Input,Old,New string} `json:"hosts"`};json.NewDecoder(os.Stdin).Decode(&input);names:=[]any{};hosts:=[]any{};for _,name:=range input.Names{msg:="";if e:=validHostname(name);e!=nil{msg=e.Error()};names=append(names,map[string]string{"name":name,"error":msg})};for _,c:=range input.Hosts{hosts=append(hosts,map[string]string{"input":c.Input,"old":c.Old,"new":c.New,"output":replaceHostEntry(c.Input,c.Old,c.New)})};raw:=[]any{};for _,old:=range []string{"old",string([]byte{255})}{input:="127.0.0.1 old #"+string([]byte{255})+"\n10.0.0.1 "+string([]byte{255})+" alias\n";enc:=base64.StdEncoding.EncodeToString;raw=append(raw,map[string]string{"input":enc([]byte(input)),"old":enc([]byte(old)),"new":enc([]byte("new")),"output":enc([]byte(replaceHostEntry(input,old,"new")))})};json.NewEncoder(os.Stdout).Encode(map[string]any{"names":names,"hosts":hosts,"rawHosts":raw})}
'''
(out/'main.go').write_text(go)
platform=Path(os.environ.get('NK_V3_PLATFORM',repo.parent/'work/v2.1-b1-20261004/platform'));goroot=platform/'server/goroot';env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(repo/'work/v3/gocache'),GO111MODULE='off')
with (out/'input.json').open('rb') as f:result=json.loads(subprocess.check_output([goroot/'bin/go','run',out/'main.go'],cwd=out,env=env,stdin=f))
result['baseline']=baseline;result['goVersion']='1.27.1'
(repo/'docs/experiments/v3.0/hostname-go-oracle.json').write_text(json.dumps(result,indent=2)+'\n');print(len(names),'Go hostname validation cases;',len(hosts),'hosts replacements')
