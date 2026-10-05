#!/usr/bin/env python3
"""Actual immutable Go chroma fallback priority for every native status byte."""
from pathlib import Path
import hashlib,json,os,re,subprocess,tempfile
R=Path(__file__).resolve().parents[1];B='a53b25579ab87cc98f85323b4743deb0d4da907b'
paths=['server/common/kvm_vision.go','server/include/kvm_vision.h','server/go.mod','server/go.sum']
sources={p:subprocess.check_output(['git','show',B+':'+p],cwd=R) for p in paths}
assert all((R/p).read_bytes()==v for p,v in sources.items())
source=sources[paths[0]].decode();a=source.index('func GetMjpegChromaStatus()');b=source.index('\nfunc (k *KvmVision) SetMjpegChroma',a)
body=source[a:b]
assert body.count('C.get_mjpeg_chroma_status()')==1
body=body.replace('C.get_mjpeg_chroma_status()','fixtureFlags')
constants={name:int(value) for name,value in re.findall(r'^#define (MJPEG_CHROMA_\w+) (\d+)$',sources[paths[1]].decode(),re.M)}
assert len(constants)==6
for name,value in constants.items():
    assert body.count('C.'+name)==1,name
    body=body.replace('C.'+name,str(value))
assert 'C.' not in body
go=R.parent/'work/v2.1-b1-20261004/platform/server/goroot'
env=dict(os.environ,GOROOT=str(go),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(R/'work/v3/gocache'))
with tempfile.TemporaryDirectory(prefix='nk-native-status-oracle-') as t:
    p=Path(t)
    (p/'main.go').write_text('package main\nimport("encoding/json";"os")\nvar fixtureFlags uint8\n'+body+'\nfunc main(){out:=[]any{};for n:=0;n<256;n++{fixtureFlags=uint8(n);active,reason:=GetMjpegChromaStatus();out=append(out,map[string]any{"flags":n,"active":active,"reason":reason})};json.NewEncoder(os.Stdout).Encode(out)}\n')
    output=json.loads(subprocess.check_output([str(go/'bin/go'),'run',str(p/'main.go')],env=env,cwd=p))
proof={'baseline':B,'goVersion':'1.27.1','source_sha256':{p:hashlib.sha256(v).hexdigest() for p,v in sources.items()},'transforms':['complete original GetMjpegChromaStatus function','only C status read supplied as fixture byte and six C constants supplied from immutable header'],'cases':output}
(R/'docs/experiments/v3.0/native-status-go-oracle.json').write_text(json.dumps(proof,indent=2)+'\n')
print('256 actual Go native-status byte/fallback cases; no native/device effects')
