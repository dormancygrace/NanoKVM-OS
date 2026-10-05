#!/usr/bin/env python3
"""Actual immutable Go/Gin scalar form binding; no runtime/device operations."""
from pathlib import Path
import hashlib, json, os, subprocess

REPO=Path(__file__).resolve().parents[1]
BASELINE='a53b25579ab87cc98f85323b4743deb0d4da907b'
sources={name:subprocess.check_output(['git','show',BASELINE+':'+name],cwd=REPO) for name in ['server/proto/vm.go','server/go.mod','server/go.sum']}
assert all((REPO/name).read_bytes()==data for name,data in sources.items())
source=sources['server/proto/vm.go'].decode()
definitions=[]
for name in ['SetSwapReq','SetOledReq','SetMouseJigglerReq']:
    a=source.index('type '+name+' struct {');b=source.index('\n}',a)+2
    definitions.append(source[a:b])
go='package main\nimport("bytes";"encoding/json";"os";"net/http/httptest";"github.com/gin-gonic/gin")\n'+'\n'.join(definitions)+r'''
type Case struct{Name,Field,Value,Body,Query string}
func main(){gin.SetMode(gin.ReleaseMode);var cases []Case;if e:=json.NewDecoder(os.Stdin).Decode(&cases);e!=nil{panic(e)};result:=[]any{};for _,c:=range cases{recorder:=httptest.NewRecorder();ctx,_:=gin.CreateTestContext(recorder);ctx.Request=httptest.NewRequest("POST","http://localhost/?"+c.Query,bytes.NewBufferString(c.Body));ctx.Request.Header.Set("Content-Type","application/x-www-form-urlencoded");var value any;var err error;switch c.Field{case "Size":var req SetSwapReq;err=ctx.ShouldBind(&req);value=req.Size;case "Sleep":var req SetOledReq;err=ctx.ShouldBind(&req);value=req.Sleep;case "Enabled":var req SetMouseJigglerReq;err=ctx.ShouldBind(&req);value=req.Enabled};result=append(result,map[string]any{"case":c,"error":err!=nil,"value":value})};json.NewEncoder(os.Stdout).Encode(result)}
'''
cases=[]
from urllib.parse import urlencode
for field, values in [
    ('Size',['','0','+0128','-1','9223372036854775807','9223372036854775808',' 128','128 ','\t128','128\n','1.0','0x10',' ','\t\n','\u0085\u00a0128\u3000','-9223372036854775808','-9223372036854775809','1_000']),
    ('Sleep',['','+10','-1',' 10','10 ','\n10','10\t','10.0',' ','\u200210\u202f','\u200b10']),
    ('Enabled',['','true','True','TRUE','t','T','1','false','False','FALSE','f','F','0',' true','true ','\tfalse','false\n','yes',' ','\t\n','\u0085true\u3000','\u200btrue']),
]:
    for index, value in enumerate(values):
        cases.append(dict(Name=field+'-'+str(index),Field=field,Value=value,Body=urlencode({field:value}),Query=''))
for field in ['Size','Sleep','Enabled']:
    valid='true' if field=='Enabled' else '10'
    for name,body,query in [('lower',urlencode({field.lower():valid}),''),('body-first',urlencode({field:valid}),urlencode({field:' invalid'})),('query-space','',urlencode({field:' '+valid})),('repeat-first',urlencode([(field,valid),(field,' invalid')]),'')]:
        cases.append(dict(Name=field+'-'+name,Field=field,Value=valid,Body=body,Query=query))
out=REPO/'work/v3/form-scalar-oracle'
with(REPO/'docs/experiments/v3.0/actions.md').open('a')as log:
    log.write('\nScalar form binding before immutable Go oracle: pinned Gin1.12 trims Unicode whitespace for non-string fields; string contents remain unmodified. Execute '+str(len(cases))+' cases through real Gin ShouldBind using three exact baseline structs. No handler, native library, helper, host or stand operation.\n')
out.mkdir(parents=True,exist_ok=True);(out/'main.go').write_text(go);(out/'input.json').write_text(json.dumps(cases))
platform=Path(os.environ.get('NK_V3_PLATFORM',REPO.parent/'work/v2.1-b1-20261004/platform'));goroot=platform/'server/goroot'
env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(REPO/'work/v3/gocache'))
with(out/'input.json').open('rb')as data:
    result=json.loads(subprocess.check_output([str(goroot/'bin/go'),'run','-mod=readonly',str(out/'main.go')],cwd=REPO/'server',env=env,stdin=data))
(REPO/'docs/experiments/v3.0/form-scalar-go-oracle.json').write_text(json.dumps(dict(baseline=BASELINE,goVersion="1.27.1",source_sha256={name:hashlib.sha256(data).hexdigest() for name,data in sources.items()},cases=result),indent=2)+'\n')
print(len(result),'actual immutable Go/Gin scalar cases, no effects')
