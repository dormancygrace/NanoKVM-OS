#!/usr/bin/env python3
"""Run the same net/http ParseForm primitive used by the baseline Gin binder."""
import importlib.util,json,os,subprocess
from pathlib import Path
spec=importlib.util.spec_from_file_location('checks',Path(__file__).with_name('check-v3.py'))
checks=importlib.util.module_from_spec(spec);spec.loader.exec_module(checks)
repo=checks.REPO;out=repo/'work/v3/form-oracle';out.mkdir(parents=True,exist_ok=True)
cases=[]
def case(name,method='POST',ctype='application/x-www-form-urlencoded',body='content=body&langue=de',query='content=query&langue=fr'):
    cases.append(dict(name=name,method=method,contentType=ctype,body=body,query=query))
case('body precedes query')
case('query only',body='')
case('absent type ignores body',ctype='')
case('plain type ignores body',ctype='text/plain')
case('DELETE ignores body',method='DELETE')
case('GET ignores body',method='GET')
case('PUT reads body',method='PUT')
case('PATCH reads body',method='PATCH')
case('case-insensitive form media',ctype='Application/X-WWW-Form-Urlencoded; CHARSET=UTF-8')
case('parameter whitespace',ctype=' application/x-www-form-urlencoded ; charset="UTF-8" ')
case('trailing semicolon',ctype='application/x-www-form-urlencoded;')
case('equal duplicate params',ctype='application/x-www-form-urlencoded; charset=UTF-8; CHARSET="UTF-8"')
case('unequal duplicate params',ctype='application/x-www-form-urlencoded; charset=UTF-8; charset=ASCII')
case('invalid media parameter',ctype='application/x-www-form-urlencoded; broken')
case('invalid media type',ctype='broken')
case('invalid body percent',body='content=%zz')
case('invalid query percent',query='content=%zz')
case('short percent',body='content=%')
case('raw semicolon',body='content=a;b')
case('encoded semicolon',body='content=a%3Bb')
case('semicolon in unknown',query='unknown=a;b')
case('ignored plain bad body',ctype='text/plain',body='content=%zz')
case('first scalar plus Unicode',body='content=a+b&content=last&langue=%D1%80%D1%83')
case('repeated body and query',body='Keys=first&Keys=second',query='Keys=third&Keys=fourth')
case('escaped equals ampersand',body='content=%26%3D%2B')
case('empty parameters',body='&&content=&flag&',query='')
case('UTF-8 replacement in response',body='content=%ff')
(out/'input.json').write_text(json.dumps(cases))
go=r'''package main
import("encoding/json";"net/http/httptest";"os";"strings")
type Case struct {Name string `json:"name"`;Method string `json:"method"`;ContentType string `json:"contentType"`;Body string `json:"body"`;Query string `json:"query"`}
func main(){var input []Case;json.NewDecoder(os.Stdin).Decode(&input);out:=[]any{};for _,c:=range input {r:=httptest.NewRequest(c.Method,"http://localhost/api?"+c.Query,strings.NewReader(c.Body));if c.ContentType!=""{r.Header.Set("Content-Type",c.ContentType)};e:=r.ParseForm();out=append(out,map[string]any{"name":c.Name,"method":c.Method,"contentType":c.ContentType,"body":c.Body,"query":c.Query,"error":e!=nil,"values":r.Form})};json.NewEncoder(os.Stdout).Encode(out)}'''
path=out/'main.go';path.write_text(go);goroot=checks.PLATFORM/'server/goroot'
env=dict(os.environ,GOROOT=str(goroot),CGO_ENABLED='0',GOCACHE=str(out/'cache'),GO111MODULE='off')
subprocess.run([goroot/'bin/gofmt','-w',path],env=env,check=True)
with (out/'input.json').open('rb') as source: data=json.loads(subprocess.check_output([goroot/'bin/go','run',path],stdin=source,env=env,cwd=out))
(repo/'docs/experiments/v3.0/form-go-oracle.json').write_text(json.dumps({'goVersion':'1.27.1','primitive':'net/http.Request.ParseForm (Gin v1.12 Form binder)','cases':data},indent=2)+'\n')
print(len(data),'actual Go form binding cases')
