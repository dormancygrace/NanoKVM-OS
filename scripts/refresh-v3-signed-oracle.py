#!/usr/bin/env python3
"""Run actual immutable baseline OLED signed-int request binding."""
from pathlib import Path
import json,os,subprocess
repo=Path(__file__).resolve().parents[1];baseline='a53b25579ab87cc98f85323b4743deb0d4da907b'
for name in ['server/proto/request.go','server/proto/vm.go']:
 if subprocess.check_output(['git','show',baseline+':'+name],cwd=repo)!=(repo/name).read_bytes():raise RuntimeError('Baseline request source changed')
out=repo/'work/v3/signed-oracle';out.mkdir(parents=True,exist_ok=True)
bodies=[('empty','{}'),('null','null'),('null-retains','{"Sleep":15,"sleep":null}'),('case-order','{"sleep":15,"SLEEP":30}'),('negative','{"Sleep":-1}'),('negative-zero','{"Sleep":-0}'),('min','{"Sleep":-9223372036854775808}'),('max','{"Sleep":9223372036854775807}'),('overflow','{"Sleep":9223372036854775808}'),('underflow','{"Sleep":-9223372036854775809}'),('float','{"Sleep":15.0}'),('exponent','{"Sleep":15e0}'),('bool','{"Sleep":false}'),('string','{"Sleep":"15"}'),('array','{"Sleep":[]}'),('object','{"Sleep":{}}'),('early-type','{"Sleep":false,"Sleep":15}'),('unknown','{"Sleep":15,"ignored":1e10000}'),('trailing','{"Sleep":15} true')]
(out/'input.json').write_text(json.dumps([dict(name=name,body=body) for name,body in bodies]))
(out/'main.go').write_text(r'package main;import("encoding/json";"net/http/httptest";"os";"strings";"github.com/gin-gonic/gin";"NanoKVM-Server/proto");func main(){gin.SetMode(gin.ReleaseMode);var input []struct{Name,Body string};json.NewDecoder(os.Stdin).Decode(&input);out:=[]any{};for _,c:=range input{ctx,_:=gin.CreateTestContext(httptest.NewRecorder());ctx.Request=httptest.NewRequest("POST","http://localhost",strings.NewReader(c.Body));ctx.Request.Header.Set("Content-Type","application/json");var req proto.SetOledReq;e:=proto.ParseFormRequest(ctx,&req);out=append(out,map[string]any{"name":c.Name,"body":c.Body,"sleep":req.Sleep,"error":e!=nil})};json.NewEncoder(os.Stdout).Encode(out)}')
platform=Path(os.environ.get('NK_V3_PLATFORM',repo.parent/'work/v2.1-b1-20261004/platform'));goroot=platform/'server/goroot';env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(repo/'work/v3/gocache'))
with (out/'input.json').open('rb') as f:result=json.loads(subprocess.check_output([goroot/'bin/go','run',out/'main.go'],cwd=repo/'server',env=env,stdin=f))
(repo/'docs/experiments/v3.0/signed-go-oracle.json').write_text(json.dumps(result,indent=2)+'\n');print(len(result),'actual Go signed-int binding cases')
