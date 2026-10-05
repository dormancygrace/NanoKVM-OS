#!/usr/bin/env python3
from pathlib import Path
import importlib.util,json,os,subprocess
repo=Path(__file__).resolve().parents[1];out=repo/'work/v3/jiff-probe';out.mkdir(parents=True,exist_ok=True);(out/'src').mkdir(exist_ok=True)
with (repo/'docs/experiments/v3.0/actions.md').open('a') as f:f.write('\nJiff probe before implementation: official0.2.37 active Sep12, build isolated temporary crate with default-features=false/std and explicit TZif bytes. Compare actual Go LoadLocationFromTZData offsets across six system zones/historical/future epochs plus corrupt/truncated data. Run host and generic-rv64 static musl/QEMU; no server dependency or time/zone/daemon changes.\n')
names=['UTC','Europe/London','America/New_York','Asia/Jerusalem','Australia/Lord_Howe','Pacific/Apia']
epochs=[-2208988800,-1,0,946684800,1710052200,1761442200,1791144000,4102444800,253402041600]
cases=[]
for index,name in enumerate(names):
 path=out/f'zone-{index}.tzif';path.write_bytes((Path('/usr/share/zoneinfo')/name).read_bytes())
 for second in epochs:cases.append(dict(name=name,path=str(path),second=second))
for name,data in [('bad-magic',b'not-a-timezone'),('truncated',(out/'zone-0.tzif').read_bytes()[:10]),('empty',b''),('zero-header',b'TZif'+bytes(40))]:
 path=out/f'{name}.tzif';path.write_bytes(data);cases.append(dict(name=name,path=str(path),second=0))
(out/'cases.json').write_text(json.dumps(cases))
(out/'main.go').write_text(r'package main;import("encoding/json";"os";"time");func main(){var cases []struct{Name,Path string;Second int64};json.NewDecoder(os.Stdin).Decode(&cases);out:=[]any{};for _,c:=range cases{data,e:=os.ReadFile(c.Path);var loc *time.Location;if e==nil{loc,e=time.LoadLocationFromTZData(c.Name,data)};offset:=0;if e==nil{_,offset=time.Unix(c.Second,0).In(loc).Zone()};out=append(out,map[string]any{"name":c.Name,"second":c.Second,"offset":offset,"error":e!=nil})};json.NewEncoder(os.Stdout).Encode(out)}')
platform=repo.parent/'work/v2.1-b1-20261004/platform';goroot=platform/'server/goroot';env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(repo/'work/v3/gocache'),GO111MODULE='off')
with (out/'cases.json').open('rb') as f:expected=json.loads(subprocess.check_output([goroot/'bin/go','run',out/'main.go'],cwd=out,env=env,stdin=f))
(out/'Cargo.toml').write_text('''[package]
name="v3-jiff-probe"
version="0.0.0"
edition="2021"
publish=false
[dependencies]
jiff={version="=0.2.37",default-features=false,features=["std"]}
serde={version="1",features=["derive"]}
serde_json="1"
''')
(out/'src/main.rs').write_text('''use serde::{Deserialize,Serialize};
#[derive(Deserialize)]struct Case{name:String,path:String,second:i64}
#[derive(Serialize)]struct ResultCase{name:String,second:i64,offset:i32,error:bool}
fn main(){let cases:Vec<Case>=serde_json::from_reader(std::io::stdin()).unwrap();let out=cases.into_iter().map(|case|{let value=std::fs::read(&case.path).ok().and_then(|data|jiff::tz::TimeZone::tzif(&case.name,&data).ok()).and_then(|tz|jiff::Timestamp::from_second(case.second).ok().map(|time|tz.to_offset(time).seconds()));ResultCase{name:case.name,second:case.second,offset:value.unwrap_or(0),error:value.is_none()}}).collect::<Vec<_>>();serde_json::to_writer(std::io::stdout(),&out).unwrap();}
''')
shutil_source=repo/'docs/experiments/v3.0/probes/jiff/Cargo.lock'
if shutil_source.is_file(): (out/'Cargo.lock').write_bytes(shutil_source.read_bytes())
cargo=Path.home()/'.cargo/bin/cargo';spec=importlib.util.spec_from_file_location('checks',repo/'scripts/check-v3.py');checks=importlib.util.module_from_spec(spec);spec.loader.exec_module(checks)
host=dict(os.environ,RUSTUP_TOOLCHAIN='1.99.0')
with (out/'build-host.log').open('w') as f:subprocess.run([cargo,'build','--release','--locked'],cwd=out,env=host,stdout=f,stderr=subprocess.STDOUT,check=True)
with (out/'cases.json').open('rb') as f:actual=json.loads(subprocess.check_output([out/'target/release/v3-jiff-probe'],cwd=out,env=host,stdin=f));assert actual==expected
target=dict(checks.target_env(),RUSTUP_TOOLCHAIN='1.99.0')
with (out/'build-target.log').open('w') as f:subprocess.run([cargo,'build','--release','--locked','--target',checks.TARGET],cwd=out,env=target,stdout=f,stderr=subprocess.STDOUT,check=True)
binary=out/f'target/{checks.TARGET}/release/v3-jiff-probe'
with (out/'cases.json').open('rb') as f:actual=json.loads(subprocess.check_output([target['CARGO_TARGET_RISCV64GC_UNKNOWN_LINUX_MUSL_RUNNER'],binary],cwd=out,env=target,stdin=f));assert actual==expected
import hashlib
result={'version':'0.2.37','features':['std'],'defaultFeatures':False,'host':'pass','target':'riscv64 generic musl static/QEMU pass','goCases':len(expected),'zones':names,'caseResults':expected,'binarySha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'binaryBytes':binary.stat().st_size,'elf':subprocess.check_output(['file',binary],text=True).strip(),'use':'qualified isolated parser probe; not yet linked into server/time API'}
(repo/'work/v3/jiff-probe-result.json').write_text(json.dumps(result,indent=2)+'\n')
with (repo/'docs/experiments/v3.0/actions.md').open('a') as f:f.write('\nJiff probe result:58 actual Go cases match host and target, six system TZif zones through1900..9999 plus four invalid files; std-only/no auto system database features. Static target SHA256 '+result['binarySha256']+'. Server/time API integration remains pending.\n')
print(json.dumps({key:value for key,value in result.items() if key!='caseResults'}))
