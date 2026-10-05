#!/usr/bin/env python3
"""Real Gin defaults from baseline API registrations; marker handlers only."""
from pathlib import Path
import json,os,subprocess
repo=Path(__file__).resolve().parents[1];baseline='a53b25579ab87cc98f85323b4743deb0d4da907b'
routes=json.loads((repo/'docs/experiments/v3.0/routes-baseline.json').read_text())
cases=[]
def case(name,method,url):cases.append(dict(name=name,method=method,url=url))
for method in ['GET','POST','HEAD','OPTIONS','CONNECT']:
    for path in ['/api/vm/mouse-jiggler/','/api/vm/mouse-jiggler','/api/vm/info/','/api/vm/info','/api/ws/','/api/ws']:case(method+path,method,path+'?a=%2F&b=1')
for name,url in [('encoded-static','/api/%76m/%69nfo'),('encoded-slash','/api%2fvm/info'),('encoded-alias','/api/vm/%69nfo/'),('user','/api/auth/users/alpha'),('user-encoded','/api/auth/users/a%6Cpha'),('user-double','/api/auth/users/alpha%252Fbeta'),('user-slash','/api/auth/users/alpha%2Fbeta'),('user-space','/api/auth/users/alpha%20beta'),('user-invalid-utf8','/api/auth/users/%FF'),('users-slash','/api/auth/users/'),('double-slash','/api/vm/info//'),('double-jiggler','/api/vm/mouse-jiggler//'),('case','/API/vm/info'),('dots','/api/vm/../vm/info'),('empty-segment','/api//vm/info')]:case(name,'GET',url)
for item in list(cases):
    if item['name'].startswith('user'):cases.append(dict(item,name='put-'+item['name'],method='PUT'))
case('encoded-api-root','GET','/%61pi/vm/info')
case('encoded-api-slashes','GET','/api%2Fvm%2Finfo')
out=repo/'work/v3/routing-oracle';out.mkdir(parents=True,exist_ok=True);(out/'input.json').write_text(json.dumps(dict(routes=routes,cases=cases)))
go=r'''package main
import("encoding/json";"net/http/httptest";"os";"github.com/gin-gonic/gin")
func main(){gin.SetMode(gin.ReleaseMode);var input struct{Routes []struct{Method,Path string};Cases []struct{Name,Method,Url string}};if e:=json.NewDecoder(os.Stdin).Decode(&input);e!=nil{panic(e)};router:=gin.New();for _,route:=range input.Routes{handler:=func(c *gin.Context){c.JSON(200,gin.H{"pattern":c.FullPath(),"path":c.Request.URL.Path,"params":c.Params})};if route.Method=="ANY"{router.Any(route.Path,handler)}else{router.Handle(route.Method,route.Path,handler)}};result:=[]any{};for _,item:=range input.Cases{recorder:=httptest.NewRecorder();request:=httptest.NewRequest(item.Method,item.Url,nil);router.ServeHTTP(recorder,request);var marker any;json.Unmarshal(recorder.Body.Bytes(),&marker);result=append(result,map[string]any{"name":item.Name,"method":item.Method,"url":item.Url,"status":recorder.Code,"location":recorder.Header().Get("Location"),"contentType":recorder.Header().Get("Content-Type"),"body":recorder.Body.String(),"marker":marker})};json.NewEncoder(os.Stdout).Encode(result)}'''
(out/'main.go').write_text(go);platform=Path(os.environ.get('NK_V3_PLATFORM',repo.parent/'work/v2.1-b1-20261004/platform'));goroot=platform/'server/goroot';env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(repo/'work/v3/gocache'))
with (out/'input.json').open('rb') as f:result=json.loads(subprocess.check_output([goroot/'bin/go','run',out/'main.go'],cwd=repo/'server',env=env,stdin=f))
(repo/'docs/experiments/v3.0/routing-go-oracle.json').write_text(json.dumps(result,indent=2)+'\n');print(len(result),'actual Gin route/default redirect cases')
