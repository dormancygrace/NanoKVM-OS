#!/usr/bin/env python3
"""Build the server against an explicitly selected existing native set."""
from nanokvm_cpu_profile import flags as cpu_flags, record as record_cpu_profile
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--libraries', type=Path, required=True)
p.add_argument('--compression', type=Path, required=True,
               help='zlib-ng and liblzma headers (include/) and static libraries (lib/) from the tools step')
p.add_argument('--buildroot-output', type=Path, required=True)
p.add_argument('--go', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
a = p.parse_args()
repo = Path(__file__).resolve().parents[1]
out = a.output.resolve()
if out.exists():
    p.error('Use a new output directory')
names = ['kvm', 'kvm_mmf', 'sys', 'vi', 'vpss', 'vo', 'rgn', 'gdc', 'venc', 'vdec',
         'misc', 'isp', 'isp_algo', 'ae', 'awb', 'af', 'cvi_bin', 'cvi_bin_isp']
full_names = {f'lib{name}.so' for name in names}
light_names = {f'lib{name}.so' for name in ('kvm', 'kvm_mmf', 'ae', 'awb', 'af', 'isp_algo')}
actual_names = {path.name for path in a.libraries.resolve().glob('*.so')}
if actual_names not in (full_names, light_names):
    p.error('Native bundle must contain exactly the six HDMI-light or eighteen full libraries; mixed bundles are unsupported')
sources = {name: a.libraries.resolve()/name for name in sorted(actual_names)}
for source in sources.values():
    if not source.is_file():
        p.error('Missing native library: '+str(source))
cross = str(a.buildroot_output.resolve()/'host/bin/riscv64-buildroot-linux-musl-')
required_native_symbols = {
    'libkvm.so': {'kvmv_read_video_sink_v2', 'kvmv_read_mjpeg_sink', 'set_mjpeg_chroma', 'get_mjpeg_chroma_status'},
    'libkvm_mmf.so': {'mmf_add_venc_channel_v2', 'mmf_add_vi_channel_configured', 'mmf_vi_drop_pending'},
}
required_native_dependencies = {
    'libkvm.so': {'mmf_add_venc_channel_v2', 'mmf_add_vi_channel_configured', 'mmf_vi_drop_pending'},
}
native_exports = {}
for library, required in required_native_symbols.items():
    output = subprocess.check_output(
        [cross+'nm', '-D', '--defined-only', str(sources[library])], text=True)
    defined = {line.split()[-1] for line in output.splitlines() if line.split()}
    native_exports[library] = defined
    missing = sorted(required - defined)
    if missing:
        p.error(f'Native bundle mismatch: {library} is missing {", ".join(missing)}')
# The optional park-status query adds a capture -> MMF dependency.
# Older complete bundles remain supported through the Go dlsym fallback.
if ('kvmv_capture_reclaim_pending' in native_exports['libkvm.so']
        and 'mmf_vi_has_pending_pool' not in native_exports['libkvm_mmf.so']):
    p.error('Native bundle mismatch: libkvm_mmf.so is missing mmf_vi_has_pending_pool')
for library, required in required_native_dependencies.items():
    output = subprocess.check_output(
        [cross+'nm', '-D', '--undefined-only', str(sources[library])], text=True)
    dependencies = {line.split()[-1] for line in output.splitlines() if line.split()}
    missing = sorted(required - dependencies)
    if missing:
        p.error(f'Native bundle mismatch: {library} does not require {", ".join(missing)}')
# server/internal/decompress links zlib-ng and liblzma statically: only the
# .a files are in this lib directory, so -lz and -llzma cannot pick a .so.
compression = a.compression.resolve()
for name in ('include/zlib.h', 'include/lzma.h', 'lib/libz.a', 'lib/liblzma.a'):
    if not (compression/name).is_file():
        p.error(f'Missing {name} in {compression}; run the tools step first')
if any(compression.glob('lib/*.so*')):
    p.error(f'{compression}/lib must hold only static libraries')
lib = out/'dl_lib'
lib.mkdir(parents=True)
record_cpu_profile(out/'cpu-profile-cgo.json', cross+'gcc')
for name, source in sources.items():
    shutil.copyfile(source, lib/name)
env = dict(os.environ, GOOS='linux', GOARCH='riscv64', GORISCV64='rva20u64', CGO_ENABLED='1',
           GOEXPERIMENT='', CC=cross+'gcc',
           CGO_CFLAGS=' '.join(cpu_flags() + [f'-I{compression}/include']), CGO_CXXFLAGS=' '.join(cpu_flags()),
           CGO_LDFLAGS=f'-L{lib} -L{compression}/lib -Wl,-rpath-link,{lib} -Wl,--enable-new-dtags -Wl,-rpath,$ORIGIN/dl_lib')
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
# Go forbids replacing GOMODCACHE files with -overlay. Stage the server and
# patched Gorilla with relative replacements; source and cache stay intact.
websocket = out / 'websocket-c906'
subprocess.run([sys.executable, str(repo / 'firmware/cpu/go-websocket-c906/prepare.py'),
                '--go', go, '--project', str(repo / 'server'), '--output', str(websocket)],
               env=env, check=True)
websocket_modfile = str(websocket / 'server/go.mod')
# Go action IDs include the output-specific CGO library search path.
# Omit the Go build ID; manifest SHA-256 values identify the exact binaries.
subprocess.run([go, 'build', '-modfile=' + websocket_modfile, '-tags=nomsgpack', '-trimpath', '-buildvcs=false', '-ldflags=-linkmode=external -buildid=', '-o',
                str(out/'NanoKVM-Server'), '.'], cwd=websocket/'server', env=env, check=True)
info = subprocess.check_output([go, 'version', '-m', str(out/'NanoKVM-Server')], env=env, text=True)
symbols = subprocess.check_output([go, 'tool', 'nm', str(out/'NanoKVM-Server')], env=env, text=True)
custom_runtime_present = all(name in {line.split()[-1] for line in symbols.splitlines() if line.split()}
                             for name in ('runtime.nanokvmSysmonInit', 'runtime.nanokvmThreadInit',
                                          # Data word read by the inlined sysmon sleep hook.
                                          'runtime.nanokvmSysmonMinDelay'))
if custom_runtime_expected and not custom_runtime_present:
    raise SystemExit('Requested NanoKVM runtime hooks are missing from the built server')
websocket_mask_present = 'github.com/gorilla/websocket.maskBytesC906.abi0' in {
    line.split()[-1] for line in symbols.splitlines() if line.split()}
if not websocket_mask_present:
    raise SystemExit('Requested C906 WebSocket mask is missing from the built server')
(out/'build-info.txt').write_text(info)
shutil.copyfile(out/'NanoKVM-Server', out/'NanoKVM-Server.stripped')
# The symbol table too: 3 MB the server reads from the SD card as it starts.
# Go needs none of it; stack traces and profiles use its own tables.
subprocess.run([cross+'strip', '--strip-all', str(out/'NanoKVM-Server.stripped')], check=True)
(out/'NanoKVM-Server.stripped').chmod(0o755)
helper_env = dict(env, CGO_ENABLED='0')
subprocess.run([go, 'build', '-trimpath', '-buildvcs=false', '-ldflags=-s -w -buildid=',
                '-o', str(out/'nkos-update'), './cmd/nkos-update'],
               cwd=repo/'server', env=helper_env, check=True)
files = [out/'NanoKVM-Server', out/'NanoKVM-Server.stripped', *sorted(lib.iterdir())]
files.append(out/'nkos-update')
subprocess.run([go, 'build', '-trimpath', '-buildvcs=false', '-ldflags=-s -w -buildid=',
                '-o', str(out/'nkos-apply-updates'), './cmd/nkos-apply-updates'],
               cwd=repo/'server', env=helper_env, check=True)
files.append(out/'nkos-apply-updates')
subprocess.run([go, 'build', '-trimpath', '-buildvcs=false', '-ldflags=-s -w -buildid=',
                '-o', str(out/'nkos-usb-internet'), './cmd/nkos-usb-internet'],
               cwd=repo/'server', env=helper_env, check=True)
files.append(out/'nkos-usb-internet')
subprocess.run([go, 'build', '-trimpath', '-buildvcs=false', '-ldflags=-s -w -buildid=',
                '-o', str(out/'nkos-standby'), './cmd/nkos-standby'],
               cwd=repo/'server', env=helper_env, check=True)
files.append(out/'nkos-standby')
manifest = {'qualification': 'cross-build only',
            'custom_runtime_expected': custom_runtime_expected,
            'custom_runtime_present': custom_runtime_present,
            'websocket_c906_present': websocket_mask_present,
            'websocket_c906': json.loads((websocket/'manifest.json').read_text()),
            'native_bundle_contract': {
                'id': 'sink-only-no-gop-v5',
                'activation': 'stage server, libkvm.so, and libkvm_mmf.so together; activate only by tested device reboot',
                'required_symbols': {name: sorted(symbols)
                                     for name, symbols in required_native_symbols.items()},
                'required_dependencies': {name: sorted(symbols)
                                          for name, symbols in required_native_dependencies.items()},
            },
            'files': {str(f.relative_to(out)): hashlib.sha256(f.read_bytes()).hexdigest() for f in files}}
(out/'manifest.json').write_text(json.dumps(manifest, indent=2)+'\n')
print('Built server:', out)
