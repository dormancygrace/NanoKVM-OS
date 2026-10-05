#!/usr/bin/env python3
"""Actual baseline proto/Gin ATX binding and C/Go GPIO-v2 layouts. No GPIO access."""
from pathlib import Path
import json,os,subprocess
repo=Path(__file__).resolve().parents[1]
for name in ['server/proto/request.go','server/proto/vm.go']:
    baseline=subprocess.check_output(['git','show','a53b25579ab87cc98f85323b4743deb0d4da907b:'+name],cwd=repo)
    if baseline!=(repo/name).read_bytes():raise RuntimeError('Baseline proto source changed: '+name)
out=repo/'work/v3/gpio-oracle';out.mkdir(parents=True,exist_ok=True)
cases=[]
def case(name,body,ct='application/json',query=''):cases.append(dict(name=name,body=body,contentType=ct,query=query))
for name,body in [('default','{"Type":"power"}'),('fold+duplicates','{"TYPE":"power","type":"reset","Duration":3,"DURATION":null}'),('uint-max','{"Type":"power","Duration":18446744073709551615}'),('uint-overflow','{"Type":"power","Duration":18446744073709551616}'),('negative','{"Type":"power","Duration":-1}'),('float','{"Type":"power","Duration":1.0}'),('exponent','{"Type":"power","Duration":1e2}'),('string','{"Type":"power","Duration":"3"}'),('boolean','{"Type":"power","Duration":true}'),('object','{"Type":"power","Duration":{}}'),('array','{"Type":"power","Duration":[]}'),('early-error','{"Type":"power","Duration":-1,"Duration":3}'),('missing','{}'),('empty','{"Type":""}'),('null','null'),('array-root','[]'),('string-root','"power"'),('wrong-type','{"Type":3}'),('type-null','{"Type":"power","Type":null}'),('unknown-huge','{"Type":"power","ignored":1e10000}'),('trailing','{"Type":"power"} true'),('whitespace','{"Type":" "}'),('empty-body','')]:case(name,body)
for name,body in [('incomplete','{'),('key','{type:1}'),('colon','{"Type" 1}'),('value','{"Type":x}'),('object-comma','{"Type":"power" "Duration":1}'),('trailing-comma','{"Type":"power",}'),('array-comma','[1 x]'),('fraction','{"Duration":1.x}'),('exponent-error','{"Duration":1ex}'),('literal','{"Duration":trux}'),('escape',r'{"Type":"\x"}'),('unicode-escape',r'{"Type":"\u0x00"}')]:case(name,body)
for name,body in [('form-default','Type=power'),('form-tags','type=power&duration=3'),('form-first','Type=power&Type=reset&Duration=3&Duration=4'),('form-plus','Type=power&Duration=%2B3'),('form-whitespace','Type=power&Duration=+3+'),('form-negative','Type=power&Duration=-1'),('form-empty','Type=power&Duration='),('form-overflow','Type=power&Duration=18446744073709551616'),('form-hex','Type=power&Duration=0x3')]:case(name,body,'application/x-www-form-urlencoded')
case('query-type','Duration=3','application/x-www-form-urlencoded','Type=reset')
case('deep-unknown', '{"Type":"power","ignored":'+('['*9999)+'0'+(']'*9999)+'}')
case('depth-limit', '{"Type":"power","ignored":'+('['*10000)+'0'+(']'*10000)+'}')
(out/'input.json').write_text(json.dumps(cases))
source=r'''package main
import("encoding/json";"net/http/httptest";"os";"strings";"unsafe";"github.com/gin-gonic/gin";"NanoKVM-Server/proto";"golang.org/x/sys/unix")
type Case struct {Name string `json:"name"`;Body string `json:"body"`;ContentType string `json:"contentType"`;Query string `json:"query"`}
func main(){gin.SetMode(gin.ReleaseMode);var cases []Case;json.NewDecoder(os.Stdin).Decode(&cases);out:=[]any{};for _,c:=range cases {ctx,_:=gin.CreateTestContext(httptest.NewRecorder());ctx.Request=httptest.NewRequest("POST","http://localhost/api?"+c.Query,strings.NewReader(c.Body));ctx.Request.Header.Set("Content-Type",c.ContentType);var req proto.SetGpioReq;e:=proto.ParseFormRequest(ctx,&req);msg:="";if e!=nil{msg=e.Error()};out=append(out,map[string]any{"name":c.Name,"body":c.Body,"contentType":c.ContentType,"query":c.Query,"type":req.Type,"duration":req.Duration,"error":msg})};r:=unix.GPIOV2LineRequest{};abi:=map[string]any{"chipSize":unsafe.Sizeof(unix.GPIOChipInfo{}),"valuesSize":unsafe.Sizeof(unix.GPIOV2LineValues{}),"configSize":unsafe.Sizeof(r.Config),"requestSize":unsafe.Sizeof(r),"configOffset":unsafe.Offsetof(r.Config),"fdOffset":unsafe.Offsetof(r.Fd),"chipInfo":unix.GPIO_GET_CHIPINFO_IOCTL,"getLine":unix.GPIO_V2_GET_LINE_IOCTL,"getValues":unix.GPIO_V2_LINE_GET_VALUES_IOCTL,"setValues":unix.GPIO_V2_LINE_SET_VALUES_IOCTL};json.NewEncoder(os.Stdout).Encode(map[string]any{"cases":out,"abi":abi})}
'''
(out/'main.go').write_text(source)
platform=repo.parent/'work/v2.1-b1-20261004/platform';goroot=platform/'server/goroot'
env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(repo/'work/v3/gocache'))
with (out/'input.json').open('rb') as f: result=json.loads(subprocess.check_output([goroot/'bin/go','run',out/'main.go'],cwd=repo/'server',env=env,stdin=f))
c=r'''#include <linux/gpio.h>
#include <stddef.h>
#include <stdio.h>
int main(void){printf("{\"chipSize\":%zu,\"valuesSize\":%zu,\"configSize\":%zu,\"requestSize\":%zu,\"configOffset\":%zu,\"fdOffset\":%zu,\"chipInfo\":%lu,\"getLine\":%lu,\"getValues\":%lu,\"setValues\":%lu}\n",sizeof(struct gpiochip_info),sizeof(struct gpio_v2_line_values),sizeof(struct gpio_v2_line_config),sizeof(struct gpio_v2_line_request),offsetof(struct gpio_v2_line_request,config),offsetof(struct gpio_v2_line_request,fd),(unsigned long)GPIO_GET_CHIPINFO_IOCTL,(unsigned long)GPIO_V2_GET_LINE_IOCTL,(unsigned long)GPIO_V2_LINE_GET_VALUES_IOCTL,(unsigned long)GPIO_V2_LINE_SET_VALUES_IOCTL);}
'''
(out/'abi.c').write_text(c)
subprocess.run(['cc',out/'abi.c','-o',out/'abi-host'],check=True)
native=json.loads(subprocess.check_output([out/'abi-host']))
assert native==result['abi'],(native,result['abi'])
assertions={'chipSize':'sizeof(struct gpiochip_info)','valuesSize':'sizeof(struct gpio_v2_line_values)','configSize':'sizeof(struct gpio_v2_line_config)','requestSize':'sizeof(struct gpio_v2_line_request)','configOffset':'offsetof(struct gpio_v2_line_request,config)','fdOffset':'offsetof(struct gpio_v2_line_request,fd)','chipInfo':'GPIO_GET_CHIPINFO_IOCTL','getLine':'GPIO_V2_GET_LINE_IOCTL','getValues':'GPIO_V2_LINE_GET_VALUES_IOCTL','setValues':'GPIO_V2_LINE_SET_VALUES_IOCTL'}
c += '\n'+'\n'.join(f'_Static_assert({expression} == {native[key]}ULL, "{key}");' for key,expression in assertions.items())
(out/'abi.c').write_text(c)
subprocess.run([platform/'buildroot-output/host/bin/riscv64-buildroot-linux-musl-gcc','-c','-march=rv64gc','-mabi=lp64d',out/'abi.c','-o',out/'abi.o'],check=True)

result['nativeTarget']='matched riscv64 musl C UAPI static assertions + host C execution';result['goVersion']='1.27.1';result['baseline']='a53b25579ab87cc98f85323b4743deb0d4da907b'
(repo/'docs/experiments/v3.0/gpio-go-oracle.json').write_text(json.dumps(result,indent=2)+'\n')
print(len(result['cases']), 'actual baseline GPIO binding cases; C/Go ABI equal')
