#!/usr/bin/env python3
"""Run pure USB/EDID functions extracted verbatim from the immutable Go baseline.

The extraction/standalone Go file lives only in ignored work/, never runtime.
"""
import importlib.util, json, os, subprocess
from pathlib import Path
spec = importlib.util.spec_from_file_location('checks', Path(__file__).with_name('check-v3.py'))
checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checks)
repo = checks.REPO
baseline = 'a53b25579ab87cc98f85323b4743deb0d4da907b'
out = repo / 'work/v3/usb-oracle'
out.mkdir(parents=True, exist_ok=True)
def source(path):
    return subprocess.check_output(['git','show',f'{baseline}:{path}'],cwd=repo,text=True)
def function(text, signature):
    start = text.index(signature)
    # All selected functions terminate at a top-level unindented closing brace.
    end = text.index('\n}',start)+2
    return text[start:end]+'\n'
vm = source('server/service/vm/virtual-device.go')
usb = source('server/service/vm/usb-composition.go')
pointer = source('server/common/windows_pointer.go')
typedef = vm[vm.index('type usbComposition struct {'):]
typedef = typedef[:typedef.index('\n}')+2]
costs = vm[vm.index('var usbEndpointCosts = '):]
costs = costs[:costs.index('\n}')+2].replace('proto.USBEndpointCost','cost')
functions = ''.join(function(vm, s) for s in ['func (s usbComposition) revision()', 'func (s usbComposition) endpointUsage()', 'func (s usbComposition) fitsEndpointBudget()'])
functions += function(usb, 'func (s usbComposition) validate()')
functions = functions.replace('hid.ModeNormal','"normal"').replace('hid.ModeHidOnly','"hid-only"')
functions += function(pointer, 'func windowsPointerEDID(')
header = '''package main
import ("bytes"; "crypto/sha256"; "encoding/json"; "errors"; "fmt"; "os")
const usbInEndpointLimit=6
const usbOutEndpointLimit=7
type cost struct {In,Out int}
'''
main = r'''
func checksum(a []byte) { for o:=0;o<256;o+=128 { var s byte;for _,v:=range a[o:o+127] {s+=v};a[o+127]=-s } }
func edid(dense bool) []byte {
 a:=make([]byte,256);copy(a,[]byte{0,255,255,255,255,255,255,0});a[126]=1;a[54]=3;a[55]=1;a[75]=0xff;a[93]=0xfc;a[111]=0xfd;a[128]=2;a[129]=3;a[130]=4
 if dense {a[130]=18;copy(a[132:],[]byte{0x65,3,12,0,0,0,0x27,9,7,7,0,0,0,0});for i:=0;i<6;i++ {a[146+18*i]=byte(i+1);a[147+18*i]=1}}
 checksum(a);return a
}
func main() {
 compositions:=[]any{}
 for _,mode:=range []string{"normal","hid-only"} {for mask:=0;mask<256;mask++ {
 s:=usbComposition{mode:mode,keyboard:mask&1!=0,relative:mask&2!=0,absolute:mask&4!=0,network:mask&8!=0,disk:mask&16!=0,serial:mask&32!=0,audio:mask&64!=0,windowsPointer:mask&128!=0}
 in,out:=s.endpointUsage();err:="";if e:=s.validate();e!=nil {err=e.Error()}
 compositions=append(compositions,map[string]any{"mode":mode,"mask":mask,"in":in,"out":out,"revision":s.revision(),"error":err})
 }}
 id:=[]byte{0x2c,0xa7,0xb4,0x0c,0x7b,0xd1,0x4f,0x25,0xb5,0x73,0xa1,0x3a,0x97,0x5d,0xdc,7}
 edids:=[]any{}
 for _,name:=range []string{"simple","dense","redecorate","checksum","boundary","block","full"} {
 a:=edid(name!="simple");switch name {case "redecorate":a,_=windowsPointerEDID(a,id);case "checksum":a[20]++;case "boundary":a[130]=0;checksum(a);case "block":a[132]=0x7f;checksum(a);case "full":a[75]=0xfc;checksum(a)}
 b,e:=windowsPointerEDID(a,id);err:="";if e!=nil {err=e.Error()}
 edids=append(edids,map[string]any{"name":name,"input":a,"output":b,"error":err})
 }
 json.NewEncoder(os.Stdout).Encode(map[string]any{"compositions":compositions,"edids":edids})
}
'''
path = out/'main.go'
path.write_text(header+typedef+'\n'+costs+'\n'+functions+main)
goroot = checks.PLATFORM/'server/goroot'
env = dict(os.environ, GOROOT=str(goroot), CGO_ENABLED='0', GOCACHE=str(out/'cache'), GO111MODULE='off')
subprocess.run([goroot/'bin/gofmt','-w',path],check=True,env=env)
data = subprocess.check_output([goroot/'bin/go','run',path],env=env,cwd=out)
result = json.loads(data)
result['baseline'] = baseline
result['sourceFunctions'] = ['usbComposition.revision','usbComposition.endpointUsage','usbComposition.fitsEndpointBudget','usbComposition.validate','windowsPointerEDID']
(repo/'docs/experiments/v3.0/usb-go-oracle.json').write_text(json.dumps(result,indent=2)+'\n')
print(len(result['compositions']),'actual Go composition cases;',len(result['edids']),'actual Go EDID cases')
