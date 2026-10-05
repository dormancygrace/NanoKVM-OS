from pathlib import Path
import hashlib,json,os,resource,subprocess
resource.setrlimit(resource.RLIMIT_CORE,(0,0))
p=Path(__file__).resolve().parents[1];platform=Path(os.environ.get('NK_V3_PLATFORM',p.parent/'work/v2.1-b1-20261004/platform'));out=p/'work/v3/native-link-probe';out.mkdir(parents=True,exist_ok=True)
with(p/'docs/experiments/v3.0/actions.md').open('a')as f:f.write('\nNative link boundary before probe: retained kvm/kvm_mmf components are DYNAMIC; musl MPI CMake links vendor .so libraries. Check static riscv64 musl dlopen through the Rust distributed libc using a synthetic exported function only, inspect existing native/server ELF dependencies. No libkvm initialization, capture, HDMI, device access or stand action. Runtime source stays frozen for services qualification.\n')
(out/'fixture.c').write_text('int nanokvm_fixture(void){return 7;}\n')
(out/'probe.c').write_text('#include <dlfcn.h>\n#include <stdio.h>\nint main(int argc,char**argv){if(argc!=2)return 2;void*p=dlopen(argv[1],RTLD_NOW|RTLD_LOCAL);if(!p){printf("%s\\n",dlerror());return 3;}int(*f)(void)=(int(*)(void))dlsym(p,"nanokvm_fixture");if(!f)return 4;int r=f();dlclose(p);printf("fixture:%d\\n",r);return r!=7;}\n')
cc=platform/'buildroot-output/host/bin/riscv64-buildroot-linux-musl-gcc';qemu=platform/'qemu/qemu-riscv64-static';flags=['-march=rv64gc','-mabi=lp64d','-Wall','-Wextra','-Werror']
subprocess.run([cc,*flags,'-shared','-fPIC',out/'fixture.c','-o',out/'fixture.so'],check=True)
subprocess.run([cc,*flags,'-static',out/'probe.c','-ldl','-o',out/'probe'],check=True)
first=subprocess.run([qemu,out/'probe',out/'fixture.so'],capture_output=True,text=True,timeout=5)
(out/'probe.rs').write_text('use std::ffi::{CString,CStr,c_char,c_int,c_void}; unsafe extern "C" {fn dlopen(p:*const c_char,flags:c_int)->*mut c_void;fn dlerror()->*const c_char;fn dlclose(p:*mut c_void)->c_int;}fn main(){let p=CString::new(std::env::args().nth(1).unwrap()).unwrap();unsafe{let h=dlopen(p.as_ptr(),2);if h.is_null(){println!("{}",CStr::from_ptr(dlerror()).to_string_lossy());std::process::exit(3)}dlclose(h);}}')
subprocess.run(['/home/dgrace/.cargo/bin/rustc',out/'probe.rs','--target','riscv64gc-unknown-linux-musl','-C','target-cpu=generic-rv64','-C','target-feature=+crt-static','-C','linker='+str(cc),'-o',out/'rust-probe'],cwd=p/'server-rust',check=True)
r=subprocess.run([qemu,out/'rust-probe',out/'fixture.so'],capture_output=True,text=True,timeout=5)
def elf(path):return dict(path=str(path),sha256=hashlib.sha256(path.read_bytes()).hexdigest(),description=subprocess.check_output(['file',path],text=True).strip(),needed=[line.strip() for line in subprocess.check_output(['readelf','-d',path],text=True,stderr=subprocess.STDOUT).splitlines()if'NEEDED'in line])
libs=platform/'server/out/dl_lib';native=[elf(f)for f in sorted(libs.glob('*.so*'))if f.is_file()]
result=dict(sdkCProbe=dict(exit=first.returncode,stdout=first.stdout,stderr=first.stderr,elf=elf(out/'probe')),staticLoader=dict(exit=r.returncode,output=r.stdout.strip(),elf=elf(out/'rust-probe')),baselineServer=elf(platform/'server/out/NanoKVM-Server'),libraries=native)
assert r.returncode==3 and 'Dynamic loading not supported' in r.stdout
(p/'docs/experiments/v3.0/native-link-probe.json').write_text(json.dumps(result,indent=2)+'\n')
with(p/'docs/experiments/v3.0/actions.md').open('a')as f:f.write('\nNative link probe result: static generic riscv64 musl/QEMU refuses synthetic dlopen: Dynamic loading not supported. Existing server/native ELF dependencies recorded in native-link-probe.json; native .so not loaded or initialized. Need a reviewed static-native archive build or a retained C capture process boundary; do not assume dynamic loading works in static Rust. No runtime/source/device action.\n')
print('static musl loader:',r.returncode,r.stdout.strip());print('native libraries inspected:',len(native));print('baseline:',result['baselineServer']['description']);print('kvm:',[v['needed']for v in native if Path(v['path']).name=='libkvm.so'])
