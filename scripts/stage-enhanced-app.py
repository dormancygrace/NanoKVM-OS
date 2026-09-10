#!/usr/bin/env python3
"""Stage explicitly selected Enhanced outputs, never legacy media binaries."""
import argparse, hashlib, json, re, shutil, subprocess
from pathlib import Path

p = argparse.ArgumentParser()
p.add_argument('--workspace', type=Path, required=True)
p.add_argument('--output', type=Path, required=True)
p.add_argument('--server', type=Path, required=True,
               help='Built server directory containing NanoKVM-Server and dl_lib')
p.add_argument('--system', type=Path, required=True, help='Built kvm_system executable')
p.add_argument('--web', type=Path, required=True, help='Current web dist directory')
p.add_argument('--version',help='Explicit application version for the image')
p.add_argument('--update-sequence', type=int, help='Installed signed-release sequence for a fresh OS image')
p.add_argument('--strip', type=Path, help='Matching cross-strip; strip debug data from the staged server copy')
a = p.parse_args()
if a.version and not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?', a.version):
    p.error('Invalid release version')
if a.update_sequence is not None and (a.update_sequence < 1 or not a.version):
    p.error('--update-sequence requires a positive sequence and --version')
base, out = a.workspace.resolve(), a.output.resolve()
app, server = base / 'app', a.server.resolve()
if out.exists():
    p.error('Use a new stage')
for source in [server / 'NanoKVM-Server', server / 'dl_lib/libkvm.so',
               server / 'dl_lib/libkvm_mmf.so', a.system, a.web / 'index.html']:
    if not source.is_file():
        p.error(f'Missing required input: {source}')
libraries = sorted((server / 'dl_lib').glob('*.so*'))
for library in libraries:
    if library.name in ['libc.so', 'libstdc++.so.6', 'libgcc_s.so.1']:
        p.error(f'System runtime library must come from Buildroot: {library}')
out.mkdir(parents=True)
# Baseline supplies static board assets only; all executable server, media and
# board-service inputs are replaced below from the explicitly selected builds.
subprocess.run(['rsync', '-a', '--exclude=/system/modern/', '--exclude=/system/bin/',
                '--exclude=/system/share/', '--exclude=/system/ko/',
                '--exclude=/server/', '--exclude=/kvm_system/',
                '--exclude=/jpg_stream/', '--exclude=/system/tool/nanokvm_update_edid',
                str(app / 'build/release/nanokvm_2.6.0') + '/', str(out) + '/'], check=True)
for name in ['kvm_new_app', 'kvm_new_img', 'system/update-components.sh', 'system/update-nanokvm.py']:
    path = out / name
    if path.exists():
        path.unlink()
(out / 'server/dl_lib').mkdir(parents=True)
(out / 'kvm_system').mkdir()
subprocess.run(['rsync', '-a', str(app / 'kvmapp/system/init.d') + '/',
                str(out / 'system/init.d') + '/'], check=True)
subprocess.run(['rsync', '-a', str(a.web) + '/', str(out / 'server/web') + '/'], check=True)
shutil.copy2(server / 'NanoKVM-Server', out / 'server/NanoKVM-Server')
if a.strip:
    subprocess.run([str(a.strip.resolve()), '--strip-debug',
                    str(out / 'server/NanoKVM-Server')], check=True)
shutil.copy2(a.system, out / 'kvm_system/kvm_system')
if (server / 'nkos-update').is_file():
    (out / 'system/bin').mkdir(parents=True, exist_ok=True)
    shutil.copy2(server / 'nkos-update', out / 'system/bin/nkos-update')
for library in libraries:
    shutil.copy2(library, out / 'server/dl_lib' / library.name)
if a.version:
    (out/'version').write_text(a.version+'\n')
if a.update_sequence is not None:
    state = out / '.os-update'
    state.mkdir(mode=0o700)
    installed = state / 'installed.json'
    installed.write_text(json.dumps({'version': a.version, 'sequence': a.update_sequence})+'\n')
    installed.chmod(0o600)
manifest = {'version': (out/'version').read_text().strip(), 'qualification': 'packaging only; full-init runtime test pending',
            # This manifest is shipped in the image: record roles, not private host paths.
            'inputs': {'server': 'selected-server-build', 'system': 'selected-board-service',
                       'web': 'selected-web-dist',
                       'strip': a.strip.name if a.strip else None},
            'files': {str(path.relative_to(out)): hashlib.sha256(path.read_bytes()).hexdigest()
                      for path in sorted(out.rglob('*')) if path.is_file()}}
(out / 'enhanced-stage-manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')
print('Application stage:', out)
