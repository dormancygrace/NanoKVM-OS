#!/usr/bin/env python3
"""Build the Enhanced server against an explicitly selected existing native set."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--libraries', type=Path, required=True)
p.add_argument('--buildroot-output', type=Path, required=True)
p.add_argument('--go', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
a = p.parse_args()
repo = Path(__file__).resolve().parents[1]
out = a.output.resolve()
if out.exists():
    p.error('Use a new output directory')
names = ['kvm', 'kvm_mmf', 'sys', 'vi', 'vpss', 'vo', 'rgn', 'gdc', 'venc', 'vdec',
         'misc', 'cvi_ive', 'isp', 'isp_algo', 'ae', 'awb', 'af', 'cvi_bin', 'cvi_bin_isp']
sources = {f'lib{name}.so': a.libraries.resolve()/f'lib{name}.so' for name in names}
for source in sources.values():
    if not source.is_file():
        p.error('Missing native library: '+str(source))
lib = out/'dl_lib'
lib.mkdir(parents=True)
for name, source in sources.items():
    shutil.copyfile(source, lib/name)
cross = str(a.buildroot_output.resolve()/'host/bin/riscv64-buildroot-linux-musl-')
env = dict(os.environ, GOOS='linux', GOARCH='riscv64', GORISCV64='rva20u64', CGO_ENABLED='1',
           GOEXPERIMENT='boringcrypto', CC=cross+'gcc',
           CGO_CFLAGS='-march=rv64gc_xtheadba_xtheadbb_xtheadbs_xtheadcmo_xtheadcondmov_xtheadfmemidx_xtheadfmv_xtheadint_xtheadmac_xtheadmemidx_xtheadmempair_xtheadsync_xtheadvector -mtune=thead-c906 -mno-fence-tso -mabi=lp64d',
           CGO_LDFLAGS=f'-L{lib} -Wl,-rpath-link,{lib} -Wl,--enable-new-dtags -Wl,-rpath,$ORIGIN/dl_lib')
# Resolve the GOROOT directory, not bin/go: prepared runtimes symlink bin/ to
# the official toolchain. Resolving the executable alone silently loses the
# selected runtime source tree when GOROOT is not explicitly inherited.
go_root = a.go.absolute().parent.parent.resolve()
go = str(go_root / 'bin/go')
env['GOROOT'] = str(go_root)
env['GOTOOLCHAIN'] = 'local'
selected_root = subprocess.check_output([go, 'env', 'GOROOT'], env=env, text=True).strip()
if Path(selected_root).resolve() != go_root:
    p.error('Go did not select the requested GOROOT')
custom_runtime_expected = (go_root/'src/runtime/nanokvm_sysmon_linux_riscv64.go').is_file()
# Go action IDs include the output-specific CGO library search path.
# Omit the Go build ID; manifest SHA-256 values identify the exact binaries.
subprocess.run([go, 'build', '-trimpath', '-buildvcs=false', '-ldflags=-linkmode=external -buildid=', '-o',
                str(out/'NanoKVM-Server'), '.'], cwd=repo/'server', env=env, check=True)
info = subprocess.check_output([go, 'version', '-m', str(out/'NanoKVM-Server')], env=env, text=True)
symbols = subprocess.check_output([go, 'tool', 'nm', str(out/'NanoKVM-Server')], env=env, text=True)
custom_runtime_present = all(name in {line.split()[-1] for line in symbols.splitlines() if line.split()}
                             for name in ('runtime.nanokvmSysmonInit', 'runtime.nanokvmThreadInit'))
if custom_runtime_expected and not custom_runtime_present:
    raise SystemExit('Requested NanoKVM runtime hooks are missing from the built server')
(out/'build-info.txt').write_text(info)
shutil.copyfile(out/'NanoKVM-Server', out/'NanoKVM-Server.stripped')
subprocess.run([cross+'strip', '--strip-debug', str(out/'NanoKVM-Server.stripped')], check=True)
(out/'NanoKVM-Server.stripped').chmod(0o755)
helper_env = dict(env, CGO_ENABLED='0', GOEXPERIMENT='')
subprocess.run([go, 'build', '-trimpath', '-buildvcs=false', '-ldflags=-s -w -buildid=',
                '-o', str(out/'nkos-update'), './cmd/nkos-update'],
               cwd=repo/'server', env=helper_env, check=True)
files = [out/'NanoKVM-Server', out/'NanoKVM-Server.stripped', *sorted(lib.iterdir())]
files.append(out/'nkos-update')
manifest = {'qualification': 'cross-build only',
            'custom_runtime_expected': custom_runtime_expected,
            'custom_runtime_present': custom_runtime_present,
            'files': {str(f.relative_to(out)): hashlib.sha256(f.read_bytes()).hexdigest() for f in files}}
(out/'manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
print('Built server:', out)
