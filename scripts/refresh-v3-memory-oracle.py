#!/usr/bin/env python3
"""Extract pure memory telemetry and title GET from the immutable Go baseline."""
from pathlib import Path
import base64,json,os,re,subprocess
repo=Path(__file__).resolve().parents[1];baseline='a53b25579ab87cc98f85323b4743deb0d4da907b'
def source(path):return subprocess.check_output(['git','show',baseline+':'+path],cwd=repo,text=True)
def function(text,name,method=False):
    marker=('func (s *Service) ' if method else 'func ')+name+'(';start=text.index(marker);end=text.index('{',start)+1;level=1
    while level:
        if text[end]=='{':level+=1
        if text[end]=='}':level-=1
        end+=1
    return text[start:end]
memory=source('server/service/vm/memory-status.go');video=source('server/service/vm/video-memory.go');maintenance=source('server/service/vm/memory-maintenance.go');title=source('server/service/vm/web_title.go')
go='package main\nimport("bufio";"encoding/base64";"encoding/json";"fmt";"os";"path/filepath";"strconv";"strings";"net/http/httptest";"github.com/gin-gonic/gin";"NanoKVM-Server/proto";log "github.com/sirupsen/logrus")\nvar root,WebTitleFile string\nconst memoryService="/etc/init.d/S38memory"\ntype Service struct{}\n'
for text,name in [(memory,'memorySwap'),(memory,'memoryStatus'),(memory,'memorySwapRequest'),(video,'videoMemoryStatus')]:
    start=text.index('type '+name+' struct {');end=text.index('\n}',start)+2;go+=text[start:end]+'\n'
for text,names in [(memory,['parseMemoryCounters','parseActiveSwaps','validSwapRequest','readMemoryStatus']),(video,['validVideoMemoryMode','readVideoMemoryStatus']),(maintenance,['hasZstdRecompression','parseRecompressionSetting'])]:
    for name in names:
        fn=function(text,name)
        if name=='readMemoryStatus':
            fn=re.sub(r'os\.(ReadFile|Stat)\("(/[^"\n]*)"\)',lambda m:'os.'+m[1]+'(filepath.Join(root,"'+m[2].lstrip('/')+'"))',fn)
            fn=fn.replace('readVideoMemoryStatus("/")','readVideoMemoryStatus(root)').replace('os.Stat(memoryService)','os.Stat(filepath.Join(root,strings.TrimPrefix(memoryService,"/")))').replace('"/lib/modules/" +','filepath.Join(root,"lib/modules")+"/" +')
        go+=fn+'\n'
go+=function(title,'GetWebTitle',method=True)+'\n'
mem='MemTotal: 193712 kB\nMemAvailable: 142644 kB\nCached: 32000 kB\nBuffers: 1000 kB\nSReclaimable: 2000 kB\nShmem: 600 kB\nSwapTotal: 327672 kB\nSwapFree: 315384 kB\n'
header='Filename Type Size Used Priority\n';base={'/proc/meminfo':mem,'/proc/swaps':header}
cases=[]
def case(name,updates=None,dirs=None,defaults=True):
    files=dict(base) if defaults else {};files.update(updates or {});cases.append(dict(name=name,files={name:base64.b64encode(data if isinstance(data,bytes) else data.encode()).decode() for name,data in files.items()},dirs=dirs or []))
case('missing',defaults=False);case('no-swaps',{'/proc/meminfo':mem},defaults=False);case('defaults')
case('zero-total',{'/proc/meminfo':'MemTotal: 0 kB\n'});case('available-clamped',{'/proc/meminfo':'MemTotal: 100 kB\nMemAvailable: 200 kB\nCached: 1 kB\nShmem: 2 kB\nSwapTotal: 5 kB\nSwapFree: 9 kB\n'})
swaps=header+'/dev/zram0 partition 65532 8192 100\n/swapfile file 262140 4096 10\n/dev/sda2 partition 99999 0 -2\n'
case('active-both',{'/proc/swaps':swaps,'/etc/kvm/memory.conf':'ZRAM_SIZE=162\nSD_SIZE=512\nZRAM_RECOMPRESS=1\n'})
case('selected-inactive',{'/etc/kvm/memory.conf':'ZRAM_SIZE=128\nSD_SIZE=512\nZRAM_RECOMPRESS=1\nZRAM_RECOMPRESS=0\nZRAM_RECOMPRESS=invalid\n'})
case('unsupported-selected',{'/etc/kvm/memory.conf':'ZRAM_SIZE=999\nSD_SIZE=-1\nZRAM_RECOMPRESS=10\n'})
for size in [32,64,128,162]:case('zram-'+str(size),{'/etc/kvm/memory.conf':f'ZRAM_SIZE={size}\n'})
for size in [128,256,512]:case('sd-'+str(size),{'/etc/kvm/memory.conf':f'SD_SIZE={size}\n'})
for name,files,dirs in [('helper-only',{'/etc/init.d/S38memory':''},[]),('builtin',{'/etc/init.d/S38memory':''},['/sys/block/zram0']),('module',{'/etc/init.d/S38memory':''},['/sys/module/zram']),('module-file',{'/etc/init.d/S38memory':'','/proc/sys/kernel/osrelease':'fixture\n','/lib/modules/fixture/kernel/drivers/block/zram/zram.ko.xz':''},[])]:case(name,files,dirs)
case('recompression',{'/proc/swaps':swaps,'/etc/kvm/memory.conf':'ZRAM_RECOMPRESS=1\n','/etc/init.d/S38memory':'','/sys/block/zram0/recompress':'','/sys/block/zram0/idle':'','/sys/block/zram0/recomp_algorithm':'#1: lz4 [zstd]\n','/sys/block/zram0/mm_stat':'1048576 262144 524288 0 0','/sys/block/zram0/comp_algorithm':'lzo [lz4] zstd\n','/sys/kernel/debug/ion/carveout/num_of_alloc_bytes':'524288\n'})
case('wrong-secondary',{'/proc/swaps':swaps,'/sys/block/zram0/recomp_algorithm':'#2: lz4 [zstd]\n','/sys/block/zram0/mm_stat':'+1 invalid 3','/sys/block/zram0/comp_algorithm':'[zstd] [lzo]\n','/sys/kernel/debug/ion/carveout/num_of_alloc_bytes':'+10\n'})
case('unsigned-ranges',{'/proc/meminfo':'MemTotal: 1 kB\nCached: 18446744073709551615 kB\nBuffers: +2 kB\nSReclaimable: 2 kB\nBad: -1 kB\nTooBig: 18446744073709551616 kB\n','/proc/swaps':header+'/dev/zram0 partition 9223372036854775807 18446744073709551615 -2\n'})
case('scanner-too-long',{'/proc/meminfo':'x'*65536+'\nMemTotal: 1 kB\n'});case('scanner-keeps-prior',{'/proc/meminfo':'MemTotal: 1 kB\n'+'x'*65536+'\nMemTotal: 2 kB\n'})
fdt='/sys/firmware/devicetree/base/'
for board in ['alpha','beta','pcie','lite','unknown','PCIE']:
    case('video-'+board,{fdt+'sipeed,board-revision':board+'\x00',fdt+'cvitek-ion/heap-carveout/nanokvm,cma-backend':'','/usr/lib/nanokvm/boot/'+board+'.sd':'cma','/usr/lib/nanokvm/boot/'+board+'-fixed.sd':'fixed','/etc/kvm/video-memory-mode':'fixed\n'})
case('fixed-fallback',{fdt+'reserved-memory/ion/compatible':'ion-region\x00','/etc/kvm/video-memory-mode':'fixed\n'});case('mode-explicit',{fdt+'nanokvm,video-memory-mode':'fixed\x00','/etc/kvm/video-memory-mode':'cma\n'});case('missing-fixed-boot',{fdt+'sipeed,board-revision':'pcie\x00',fdt+'cvitek-ion/heap-carveout/nanokvm,cma-backend':'','/usr/lib/nanokvm/boot/pcie.sd':'cma'});case('trim-limited',{fdt+'sipeed,board-revision':'\tpcie\x00',fdt+'nanokvm,video-memory-mode':'\tcma\x00'})
title_cases=[dict(name=name,bytes=None if data is None else base64.b64encode(data).decode()) for name,data in [('missing',None),('normal',b'Nano\nKVM\r\n'),('empty',b''),('invalid',b'\xff\xff'),('truncated',b'x\xe1\x80\xff\ny')]]
out=repo/'work/v3/memory-oracle';out.mkdir(parents=True,exist_ok=True);(out/'input.json').write_text(json.dumps(dict(cases=cases,titles=title_cases)))
go+=r'''
func main(){gin.SetMode(gin.ReleaseMode);var input struct{Cases []struct{Name string;Files map[string]string;Dirs []string};Titles []struct{Name string;Bytes *string}};if e:=json.NewDecoder(os.Stdin).Decode(&input);e!=nil{panic(e)};results:=[]any{};for _,c:=range input.Cases{dir,e:=os.MkdirTemp("","memory-go-oracle-");if e!=nil{panic(e)};root=dir;for _,name:=range c.Dirs{os.MkdirAll(filepath.Join(root,strings.TrimPrefix(name,"/")),0755)};for name,value:=range c.Files{path:=filepath.Join(root,strings.TrimPrefix(name,"/"));os.MkdirAll(filepath.Dir(path),0755);data,_:=base64.StdEncoding.DecodeString(value);os.WriteFile(path,data,0644)};status,e:=readMemoryStatus();var value any;if e==nil{value=status};results=append(results,map[string]any{"name":c.Name,"files":c.Files,"dirs":c.Dirs,"status":value,"error":e!=nil,"video":readVideoMemoryStatus(root)});os.RemoveAll(root)};titles:=[]any{};for _,c:=range input.Titles{dir,_:=os.MkdirTemp("","title-go-oracle-");WebTitleFile=filepath.Join(dir,"title");if c.Bytes!=nil{data,_:=base64.StdEncoding.DecodeString(*c.Bytes);os.WriteFile(WebTitleFile,data,0644)};recorder:=httptest.NewRecorder();ctx,_:=gin.CreateTestContext(recorder);ctx.Request=httptest.NewRequest("GET","http://localhost",nil);(&Service{}).GetWebTitle(ctx);var response any;if e:=json.Unmarshal(recorder.Body.Bytes(),&response);e!=nil{panic(e)};titles=append(titles,map[string]any{"name":c.Name,"bytes":c.Bytes,"response":response});os.RemoveAll(dir)};json.NewEncoder(os.Stdout).Encode(map[string]any{"cases":results,"titles":titles})}
'''
(out/'main.go').write_text(go)
platform=Path(os.environ.get('NK_V3_PLATFORM',repo.parent/'work/v2.1-b1-20261004/platform'));goroot=platform/'server/goroot';env=dict(os.environ,GOROOT=str(goroot),GOTOOLCHAIN='local',CGO_ENABLED='0',GOCACHE=str(repo/'work/v3/gocache'))
with (out/'input.json').open('rb') as f:result=json.loads(subprocess.check_output([goroot/'bin/go','run',out/'main.go'],cwd=repo/'server',env=env,stdin=f))
result['baseline']=baseline;result['goVersion']='1.27.1';(repo/'docs/experiments/v3.0/memory-go-oracle.json').write_text(json.dumps(result,indent=2)+'\n');print(len(result['cases']),'Go memory/video cases;',len(result['titles']),'actual Gin title responses')
