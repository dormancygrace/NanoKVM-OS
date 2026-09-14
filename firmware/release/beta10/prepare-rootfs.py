from pathlib import Path
import hashlib, json, os, shutil, subprocess

import argparse
p = argparse.ArgumentParser(description='Apply the beta.10 delta to a mounted pristine beta.9 rootfs')
for name in ('repo', 'output', 'accepted', 'server'):
    p.add_argument('--'+name, type=Path, required=True)
a=p.parse_args()
repo, out, accepted = a.repo.resolve(), a.output.resolve(), a.accepted.resolve()
root=out/'rootfs-mount'
assert os.path.ismount(root) and root.resolve() == root
assert (root/'kvmapp/version').read_text().strip() == '1.0.0-beta.9'
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
accept_manifest = json.loads((accepted/'manifest.json').read_text())
for relative in ['NanoKVM-Server', *['dl_lib/'+p.name for p in (accepted/'dl_lib').glob('*.so')], *['edid/'+p.name for p in (accepted/'edid').glob('*.bin')]]:
    assert sha(accepted/relative) == accept_manifest['files'][relative]['sha256'], relative
before = {str(p.relative_to(root)): sha(p) for p in (root/'kvmapp/server/dl_lib').glob('*.so')}
# Rebuild the final server for versioned GitHub discovery. Preserve the beta-9
# nkos-update helper at its existing capability/sequence identity.
assert sha(root/'usr/sbin/nkos-update') == '3e5179d3f2b97050ab28994545abee594f5b8e0f9583b7810dbf3bb62c0dbcaf'
shutil.copy2(a.server.resolve(), root/'kvmapp/server/NanoKVM-Server')
shutil.copytree(accepted/'dl_lib', root/'kvmapp/server/dl_lib', dirs_exist_ok=True)
web = root/'kvmapp/server/web'
assert web.resolve().is_relative_to(root)
shutil.rmtree(web)
shutil.copytree(out/'web', web)
shutil.copytree(accepted/'edid', root/'usr/share/nanokvm/edid', dirs_exist_ok=True)
modules = root/'usr/lib/modules'
assert modules.resolve().is_relative_to(root)
shutil.rmtree(modules)
shutil.copytree(out/'board-stage/usr/lib/modules', modules)
fw = 'usr/lib/firmware/aic8800_sdio/aic8800_and_aic8800D80/fmacfwbt_8800d80_h_u02.bin'
shutil.copy2(repo/'firmware/buildroot/package/aic8800-sdio-firmware/files/fmacfwbt_8800d80_h_u02.bin', root/fw)
assert sha(root/fw) == 'f7fa0a1a296589568fbea1de89ab8feeb13ef2e6db6c7fafa682a34fb583a1bd'
for relative in ['etc/init.d/S29qdisc','kvmapp/system/init.d/S29qdisc']:
    shutil.copy2(repo/'kvmapp/system/init.d/S29qdisc', root/relative)
    (root/relative).chmod(0o755)
(root/'kvmapp/version').write_text('1.0.0-beta.10\n')
(root/'kvmapp/.os-update/installed.json').write_text('{"version":"1.0.0-beta.10","sequence":23}\n')
p = root/'usr/lib/os-release'
p.write_text(p.read_text().replace('beta-9','beta-10').replace('beta.9','beta.10'))
subprocess.run(['python3', str(repo/'firmware/buildroot/board/system-update-base.py'), str(root), str(out/'board-stage')], check=True)
app_manifest = root/'kvmapp/enhanced-stage-manifest.json'
manifest = {'version':'1.0.0-beta.10','sequence':23,'qualification':'assembled; full image boot qualification pending',
            'inputs':{'base':'public beta.9 seq22 full image','server_native_edid_commit':accept_manifest['commit'],'web_commit':'72f8d2c56b5916c368b6d873e4a1b796971e53e3'},
            'files':{str(p.relative_to(root/'kvmapp')):sha(p) for p in sorted((root/'kvmapp').rglob('*')) if p.is_file() and not p.is_symlink() and p != app_manifest}}
app_manifest.write_text(json.dumps(manifest,indent=2)+'\n')
for p in [root/'etc/kvm/pwd', root/'etc/ssh/ssh_host_ed25519_key']:
    assert not p.exists(), 'Device identity must not enter image'
assert (root/'etc/kvm/ssh_stop').exists()
assert len(list(modules.glob('*'))) == 1
assert (modules/'7.2.5-nanokvm-os-r3').is_dir()
for p in sorted(root.rglob('*')):
    # Only newly installed content needs ownership normalization.
    if p.is_symlink(): continue
    if p.stat().st_uid != 0 or p.stat().st_gid != 0: os.chown(p,0,0)
(out/'rootfs-delta.json').write_text(json.dumps({'version':'1.0.0-beta.10','sequence':23,'kernel':'7.2.5-nanokvm-os-r3',
    'native_changed':[name for name,old in before.items() if sha(root/name)!=old],
    'application_manifest_sha256':sha(app_manifest),'board_manifest_sha256':sha(out/'board-stage/manifest.json'),
    'd80_firmware_sha256':sha(root/fw)},indent=2)+'\n')
print('BETA10_ROOTFS_STAGED')
