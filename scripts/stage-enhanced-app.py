#!/usr/bin/env python3
"""Stage explicitly selected Enhanced outputs, never legacy media binaries."""
import argparse, hashlib, json, re, shutil, subprocess
from pathlib import Path

p = argparse.ArgumentParser()
p.add_argument('--workspace', type=Path, required=True)
p.add_argument('--base-assets', type=Path,
               help='Pinned static application assets; executable inputs are replaced by selected builds')
p.add_argument('--output', type=Path, required=True)
p.add_argument('--server', type=Path, required=True,
               help='Built server directory containing NanoKVM-Server and dl_lib')
p.add_argument('--system', type=Path, required=True, help='Built kvm_system executable')
p.add_argument('--web', type=Path, required=True, help='Current web dist directory')
p.add_argument('--board-scripts', type=Path,
               help='Selected enhanced board init.d directory (defaults to WORKSPACE/app source)')
p.add_argument('--usb-audio-capture', type=Path,
               help='Selected target usb-audio-capture binary')
p.add_argument('--usb-audio-share', type=Path,
               help='Selected target usb-audio data directory')
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
board_scripts = a.board_scripts.resolve() if a.board_scripts else app / 'firmware/buildroot/board/enhanced/init.d'
authoritative_s01fs = app / 'kvmapp/system/init.d/S01fs'
if not authoritative_s01fs.is_file():
    p.error(f'Missing authoritative S01fs: {authoritative_s01fs}')
board_s01fs = board_scripts / 'S01fs'
if board_s01fs.exists() and board_s01fs.read_bytes() != authoritative_s01fs.read_bytes():
    p.error('Board S01fs conflicts with the application-owned authoritative S01fs')
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
                str(a.base_assets.resolve() if a.base_assets else app / 'build/release/nanokvm_2.6.0') + '/', str(out) + '/'], check=True)
for name in ['kvm_new_app', 'kvm_new_img', 'system/update-components.sh', 'system/update-nanokvm.py']:
    path = out / name
    if path.exists():
        path.unlink()
(out / 'server/dl_lib').mkdir(parents=True)
(out / 'kvm_system').mkdir()
subprocess.run(['rsync', '-a', str(app / 'kvmapp/system/init.d') + '/',
                str(out / 'system/init.d') + '/'], check=True)
# Board-specific boot services must accompany the selected mainline kernel.
# The generic application release does not contain e.g. S12temperature.
subprocess.run(['rsync', '-a', '--exclude=S01fs', str(board_scripts) + '/',
                str(out / 'system/init.d') + '/'], check=True)
# S01fs is application-owned.  Never let a retained board stage silently
# replace its first-boot partition policy.
shutil.copy2(authoritative_s01fs, out / 'system/init.d/S01fs')
subprocess.run(['rsync', '-a', str(a.web) + '/', str(out / 'server/web') + '/'], check=True)
shutil.copy2(server / 'NanoKVM-Server', out / 'server/NanoKVM-Server')
if a.strip:
    subprocess.run([str(a.strip.resolve()), '--strip-debug',
                    str(out / 'server/NanoKVM-Server')], check=True)
shutil.copy2(a.system, out / 'kvm_system/kvm_system')
if (server / 'nkos-update').is_file():
    (out / 'system/bin').mkdir(parents=True, exist_ok=True)
    shutil.copy2(server / 'nkos-update', out / 'system/bin/nkos-update')
if bool(a.usb_audio_capture) != bool(a.usb_audio_share):
    p.error('--usb-audio-capture and --usb-audio-share must be supplied together')
if a.usb_audio_capture:
    if not a.usb_audio_capture.is_file() or not a.usb_audio_share.is_dir():
        p.error('Selected USB audio inputs are missing')
    (out / 'system/bin').mkdir(parents=True, exist_ok=True)
    (out / 'system/share/usb-audio').mkdir(parents=True, exist_ok=True)
    shutil.copy2(a.usb_audio_capture, out / 'system/bin/usb-audio-capture')
    subprocess.run(['rsync', '-a', str(a.usb_audio_share.resolve()) + '/',
                    str(out / 'system/share/usb-audio') + '/'], check=True)
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
