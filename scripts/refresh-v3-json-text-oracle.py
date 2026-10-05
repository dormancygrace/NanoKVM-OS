#!/usr/bin/env python3
"""Actual baseline Gin string request binding for UTF-8/UTF-16 compatibility."""
from pathlib import Path
import base64,json,os,subprocess
repo=Path(__file__).resolve().parents[1];out=repo/'work/v3/json-text-oracle';out.mkdir(parents=True,exist_ok=True)
for name in ['server/proto/request.go','server/proto/vm.go']:
 if subprocess.check_output(['git','show','a53b25579ab87cc98f85323b4743deb0d4da907b:'+name],cwd=repo)!=(repo/name).read_bytes():raise RuntimeError('Baseline changed')
cases=[('invalid-byte',b'{"Title":"\xff"}'),('truncated-UTF8',b'{"Title":"\xe1\x80\xff"}'),('high',br'{"Title":"x\ud800y"}'),('low',br'{"Title":"\udc00"}'),('high-before-normal',br'{"Title":"\ud800\u0041"}'),('paired',br'{"Title":"\ud800\udc00"}'),('escaped-backslash',br'{"Title":"\\ud800"}'),('unicode-backslash',br'{"Title":"\u005Cud800"}'),('unknown-surrogate-key',br'{"\ud800":"ignored","Title":"ok"}'),('unknown-value',br'{"Title":"ok","ignored":{"value":"\ud800"}}'),('pair-high-low-max',br'{"Title":"\ud800\udfff"}'),('high-high-low',br'{"Title":"\ud800\ud800\udc00"}'),('invalid-key',b'{"\xff":"ignored","Title":"ok"}')]
(out/'input.json').write_text(json.dumps([dict(name=name,input=base64.b64encode(data).decode()) for name,data in cases]))
(out/'main.go').write_text(r'package main;import("encoding/base64";"encoding/json";"net/http/httptest";"os";"bytes";"github.com/gin-gonic/gin";"NanoKVM-Server/proto");func main(){gin.SetMode(gin.ReleaseMode);var input []struct{Name,Input string};json.NewDecoder(os.Stdin).Decode(&input);out:=[]any{};for _,c:=range input{data,_:=base64.StdEncoding.DecodeString(c.Input);ctx,_:=gin.CreateTestContext(httptest.NewRecorder());ctx.Request=httptest.NewRequest("POST","http://localhost",bytes.NewReader(data));ctx.Request.Header.Set("Content-Type","application/json");var req proto.SetWebTitleReq;e:=proto.ParseFormRequest(ctx,&req);out=append(out,map[string]any{"name":c.Name,"input":c.Input,"title":req.Title,"error":e!=nil})};json.NewEncoder(os.Stdout).Encode(out)}')
platform=Path(os.environ.get('NK_V3_PLATFORM',repo.parent/'work/v2.1-b1-20261004/platform'));goroot=platform/'server/goroot';env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(repo/'work/v3/gocache'))
with (out/'input.json').open('rb') as f:result=json.loads(subprocess.check_output([goroot/'bin/go','run',out/'main.go'],cwd=repo/'server',env=env,stdin=f))
(repo/'docs/experiments/v3.0/json-text-go-oracle.json').write_text(json.dumps(result,indent=2)+'\n');print(len(result),'actual Go string cases')
