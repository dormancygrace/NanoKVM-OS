#!/usr/bin/env python3
"""Link the server against explicitly selected Enhanced capture/MPI artifacts."""
from pathlib import Path
import hashlib, json, os, shutil, subprocess

repo = Path(__file__).resolve().parents[1]
br = Path(os.environ['NANOKVM_BUILDROOT_OUTPUT']).resolve()
capture = Path(os.environ['NANOKVM_CAPTURE_OUTPUT']).resolve()
mmf = Path(os.environ['NANOKVM_MMF_OUTPUT']).resolve()
mpi = Path(os.environ['NANOKVM_MPI_SOURCE']).resolve()
out = Path(os.environ['NANOKVM_SERVER_OUTPUT']).resolve()
lib = out / 'dl_lib'
lib.mkdir(parents=True, exist_ok=True)
inputs = {'libkvm.so': capture/'libkvm.so', 'libkvm_mmf.so': mmf/'libkvm_mmf.so'}
for name in ['sys','vi','vpss','vo','rgn','gdc','venc','vdec','misc','cvi_ive','isp','isp_algo','ae','awb','af','cvi_bin','cvi_bin_isp']:
    inputs['lib'+name+'.so'] = mpi/'lib'/('lib'+name+'.so')
for name, source in inputs.items():
    shutil.copyfile(source, lib/name)
cross = str(br/'host/bin/riscv64-buildroot-linux-musl-')
env = dict(os.environ, GOOS='linux', GOARCH='riscv64', GORISCV64='rva20u64', CGO_ENABLED='1', GOEXPERIMENT='boringcrypto',
           CC=cross+'gcc', CGO_CFLAGS='-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector -mtune=thead-c906 -mno-fence-tso -mabi=lp64d',
           CGO_LDFLAGS=f'-L{lib} -Wl,-rpath-link,{lib} -Wl,--enable-new-dtags -Wl,-rpath,$ORIGIN/dl_lib')
go = os.environ.get('NANOKVM_GO', 'go')
# Keep the selected patched runtime even when bin/go is a symlink.
go_root = Path(go).absolute().parent.parent.resolve() if '/' in go else Path(subprocess.check_output([go, 'env', 'GOROOT'], text=True).strip())
env.update(GOROOT=str(go_root), GOTOOLCHAIN='local')
version = subprocess.check_output([go,'version'],env=env,text=True).strip()
compiler = subprocess.check_output([cross+'gcc','-dumpfullversion'],text=True).strip()
subprocess.run([go,'build','-trimpath','-o',str(out/'NanoKVM-Server'),'.'],cwd=repo/'server',env=env,check=True)
# Set RUNPATH at link time: post-link program-header reordering can break
# Go build-info readers even when the binary still loads correctly.
info = subprocess.check_output([go,'version','-m',str(out/'NanoKVM-Server')],env=env,text=True,stderr=subprocess.STDOUT)
# Require a positive metadata report as well as a successful command exit.
if not info.startswith(str(out/'NanoKVM-Server')+': go'):
    raise SystemExit(info)
artifacts = {'NanoKVM-Server': out/'NanoKVM-Server'}
artifacts.update({'dl_lib/'+name: lib/name for name in inputs})
manifest = dict(go=version,gcc=compiler,artifacts={name:hashlib.sha256(p.read_bytes()).hexdigest() for name,p in artifacts.items()})
(out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
print(json.dumps(manifest,indent=2))
