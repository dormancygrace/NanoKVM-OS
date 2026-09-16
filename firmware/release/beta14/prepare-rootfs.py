#!/usr/bin/env python3
"""Apply the beta-14 release delta to a mounted pristine beta-10 image."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess

p = argparse.ArgumentParser(description=__doc__)
for name in ('repo', 'output', 'server', 'web', 'capture', 'system', 'libraries', 'edid', 'utility', 'updater', 'board-stage', 'boot'):
    p.add_argument('--' + name, type=Path, required=True)
a = p.parse_args()
repo, out = a.repo.resolve(), a.output.resolve()
root = out / 'rootfs-mount'
assert root.is_mount() and root.resolve() == root
assert (root / 'kvmapp/version').read_text().strip() == '1.0.0-beta.10'

def sha(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()

assert sha(root / 'usr/sbin/nkos-update') == '3e5179d3f2b97050ab28994545abee594f5b8e0f9583b7810dbf3bb62c0dbcaf'
before_native = {p.name: sha(p) for p in (root / 'kvmapp/server/dl_lib').glob('*.so')}
base_before = (root / 'etc/nkos-system-base').read_text()
changed = set()

def copy(source, relative, mode=None):
    dest = root / relative
    assert dest.parent.resolve().is_relative_to(root)
    dest.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source, dest)
    if mode is not None:
        dest.chmod(mode)
    changed.add(relative)

copy(a.server, 'kvmapp/server/NanoKVM-Server', 0o755)
copy(a.capture, 'kvmapp/server/dl_lib/libkvm.so', 0o755)
for library in a.libraries.glob('*.so'):
    copy(library, 'kvmapp/server/dl_lib/' + library.name, 0o755)
copy(a.system, 'kvmapp/kvm_system/kvm_system', 0o755)
copy(a.utility, 'usr/sbin/nanokvm_update_edid', 0o755)
copy(a.updater, 'usr/sbin/nkos-update', 0o755)
for asset in a.edid.glob('*.bin'):
    copy(asset, 'usr/share/nanokvm/edid/' + asset.name, 0o644)
modules = root / 'usr/lib/modules'
assert modules.resolve().is_relative_to(root)
shutil.rmtree(modules)
shutil.copytree(a.board_stage / 'usr/lib/modules', modules)

web = root / 'kvmapp/server/web'
assert web.resolve().is_relative_to(root)
shutil.rmtree(web)
shutil.copytree(a.web, web)
for path in web.rglob('*'):
    if path.is_file():
        changed.add(path.relative_to(root).as_posix())

# The release delta must replace both live and restore-template services.
for prefix in ('etc/init.d', 'kvmapp/system/init.d'):
    copy(repo / 'firmware/buildroot/board/enhanced/init.d/S10uuid', prefix + '/S10uuid', 0o755)

hardware_service = repo / 'firmware/buildroot/board/enhanced/init.d/S15kvmhwd'
for prefix in ('etc/init.d', 'kvmapp/system/init.d'):
    copy(hardware_service, prefix + '/S15kvmhwd', 0o755)
    subprocess.run(['sh', '-n', str(root / prefix / 'S15kvmhwd')], check=True)

for name in ('S95nanokvm', 'S30eth', 'S30wifi', 'S34mssclamp', 'S38memory', 'S49persistent-cron'):
    for prefix in ('etc/init.d', 'kvmapp/system/init.d'):
        copy(repo / 'kvmapp/system/init.d' / name, prefix + '/' + name, 0o755)
for prefix in ('etc/init.d', 'kvmapp/system/init.d'):
    copy(repo / 'firmware/buildroot/board/enhanced/init.d/S30usbnet', prefix + '/S30usbnet', 0o755)
    (root / prefix / 'S30rndis').unlink(missing_ok=True)
# Preserve the image's existing, qualified gadget composition and serial fixes.
# Only retarget the already injected network lifecycle calls to the renamed service.
usb_templates = []
for prefix in ('etc/init.d', 'kvmapp/system/init.d'):
    for name in ('S03usbdev', 'S03usbhid'):
        path = root / prefix / name
        if path.is_file():
            body = path.read_text()
            if '/etc/init.d/S30rndis' in body:
                path.write_text(body.replace('/etc/init.d/S30rndis', '/etc/init.d/S30usbnet'))
                changed.add(path.relative_to(root).as_posix())
            assert '/etc/init.d/S30rndis' not in path.read_text()
            subprocess.run(['sh', '-n', str(path)], check=True)
            usb_templates.append(path.relative_to(root).as_posix())
assert 'etc/init.d/S03usbdev' in usb_templates
assert (root / 'etc/init.d/S50crond').is_file()
for prefix in ('etc/init.d', 'kvmapp/system/init.d'):
    for name in ('S30eth', 'S30wifi', 'S34mssclamp', 'S49persistent-cron', 'S30usbnet'):
        subprocess.run(['sh', '-n', str(root / prefix / name)], check=True)

copy(a.boot / 'nkos-board-probe', 'usr/sbin/nkos-board-probe', 0o755)
copy(repo / 'firmware/boards/nkos-board-select', 'usr/sbin/nkos-board-select', 0o755)
for profile in ('alpha', 'beta', 'pcie', 'lite'):
    copy(a.boot / profile / 'boot.sd', 'usr/lib/nkos/boot/' + profile + '.sd', 0o644)
    (root / 'usr/lib/nkos/boot' / (profile + '.sha256')).write_text(
        sha(a.boot / profile / 'boot.sd') + '  ' + profile + '.sd\n')
copy(a.boot / 'manifest.json', 'usr/lib/nkos/boot/manifest.json', 0o644)
for marker in ('hw', 'hdmi_version', 'board-profile', 'oled_exist'):
    (root / 'etc/kvm' / marker).unlink(missing_ok=True)

version, sequence = '1.0.0-beta.14', 30
(root / 'kvmapp/version').write_text(version + '\n')
(root / 'kvmapp/.os-update/installed.json').write_text(json.dumps({'version': version, 'sequence': sequence}) + '\n')
(root / 'kvmapp/.os-update/installed.json').chmod(0o600)
(root / 'usr/lib/os-release').write_text(
    'NAME="NanoKVM OS"\nID=nanokvm-os\nVERSION="1.0.0 beta-14"\n'
    'VERSION_ID="1.0.0-beta.14"\nPRETTY_NAME="NanoKVM OS v1.0.0 beta-14"\n')
(root / 'etc/issue').write_text('NanoKVM OS v1.0.0 beta-14\n')
subprocess.run(['python3', str(repo / 'firmware/buildroot/board/system-update-base.py'), str(root), str(a.board_stage)], check=True)
changed_native = [name for name, digest in before_native.items()
                  if sha(root / 'kvmapp/server/dl_lib' / name) != digest]
assert (root / 'etc/kvm/ssh_stop').is_file()
for relative in ('etc/kvm/pwd', 'etc/ssh/ssh_host_ed25519_key', 'usr/bin/python',
                 'usr/bin/python3', 'opt/nkos/addons/python', 'etc/init.d/S30rndis'):
    assert not (root / relative).exists(), relative
assert not list((root / 'opt/nkos/addons').iterdir())
assert (root / 'usr/lib/modules/7.2.5-nanokvm-os-r4').is_dir()

manifest_path = root / 'kvmapp/enhanced-stage-manifest.json'
manifest = {
    'version': version, 'sequence': sequence,
    'qualification': 'assembled from accepted components; complete image not device-flashed',
    'inputs': {'base': 'pristine beta-10 seq23 image',
               'hardware_detection': 'firmware/boards; automatic first-boot profile selection',
               'source_commit': subprocess.check_output(['git', '-C', str(repo), 'rev-parse', 'HEAD'], text=True).strip(),
               'web_input': str(a.web),
               'capture_input': str(a.capture)},
    'files': {p.relative_to(root / 'kvmapp').as_posix(): sha(p)
              for p in sorted((root / 'kvmapp').rglob('*'))
              if p.is_file() and not p.is_symlink() and p != manifest_path},
}
manifest_path.write_text(json.dumps(manifest, indent=2) + '\n')
for path in root.rglob('*'):
    if not path.is_symlink() and (path.stat().st_uid != 0 or path.stat().st_gid != 0):
        os.chown(path, 0, 0)
(out / 'rootfs-delta.json').write_text(json.dumps({
    'version': version, 'sequence': sequence, 'kernel': '7.2.5-nanokvm-os-r4',
    'hardware_service_sha256': sha(hardware_service),
    'server_sha256': sha(a.server), 'web_index_sha256': sha(a.web / 'index.html'),
    'capture_sha256': sha(a.capture), 'native_changed': changed_native,
    'usb_templates': usb_templates, 'changed_files': sorted(changed),
    'system_base': (root / 'etc/nkos-system-base').read_text().strip(), 'application_manifest_sha256': sha(manifest_path),
    'boot_profiles': json.loads((a.boot / 'manifest.json').read_text()),
    'ssh_default': 'disabled', 'python_in_base': False,
}, indent=2) + '\n')
print('BETA14_ROOTFS_STAGED')
