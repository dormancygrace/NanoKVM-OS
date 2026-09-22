#!/usr/bin/env python3
from pathlib import Path
import shutil, subprocess, json, hashlib

import argparse
p=argparse.ArgumentParser(description="Assemble the complete Alpine package payload from accepted native inputs and current application builds")
p.add_argument('--port-payloads',type=Path,required=True)
p.add_argument('--accepted-root',type=Path,required=True)
p.add_argument('--server',type=Path,required=True)
p.add_argument('--web',type=Path,required=True)
p.add_argument('--output',type=Path,required=True)
p.add_argument('--boot-payloads',type=Path,help='Matched board FIT images, checksums and kernel.release')
args=p.parse_args()
r=Path(__file__).resolve().parents[1]
release_values=dict(line.split('=', 1) for line in (r/'firmware/alpine/release.env').read_text().splitlines() if line and not line.startswith('#'))
version=release_values['NANOKVM_VERSION']
old=args.accepted_root.resolve()
out=args.output.resolve()
if out.exists(): p.error('Use a new payload output directory')
shutil.copytree(args.port_payloads, out, symlinks=True)
def copy(src, dst):
    dst.parent.mkdir(parents=True,exist_ok=True)
    if dst.is_symlink(): dst.unlink()
    shutil.copy2(src,dst)
def link(target,dst):
    dst.parent.mkdir(parents=True,exist_ok=True)
    if dst.is_symlink() or dst.is_file(): dst.unlink()
    dst.symlink_to(target)
base=out/'base'; app=out/'app'; fw=out/'firmware-sg2002'
enhanced_s15=r/'firmware/buildroot/board/enhanced/init.d/S15kvmhwd'
copy(r/'firmware/alpine/compat/nanokvm-firstboot-storage',base/'usr/libexec/nanokvm/firstboot-storage')
copy(r/'firmware/alpine/compat/nanokvm-activate-kernel',base/'usr/libexec/nanokvm/activate-kernel')
copy(r/'firmware/alpine/compat/nkos-board-select',base/'usr/sbin/nkos-board-select')
copy(old/'usr/sbin/nkos-board-probe',base/'usr/sbin/nkos-board-probe')
copy(args.server/'nkos-update',base/'usr/sbin/nkos-update')
copy(args.server/'nkos-apply-updates',base/'usr/sbin/nkos-apply-updates')
copy(r/'firmware/alpine/compat/50-nanokvm-apply',base/'etc/apk/commit_hooks.d/50-nanokvm-apply')
for src in (r/'firmware/alpine/openrc').iterdir(): copy(src,base/'etc/init.d'/src.name)
copy(enhanced_s15,base/'usr/libexec/nanokvm/legacy/S15kvmhwd')
link('/usr/libexec/nanokvm/legacy/S15kvmhwd',base/'etc/init.d/S15kvmhwd')
copy(enhanced_s15,app/'kvmapp/system/init.d/S15kvmhwd')
copy(r/'kvmapp/system/init.d/S95nanokvm',app/'kvmapp/system/init.d/S95nanokvm')
names='S95nanokvm S29qdisc S34mssclamp S38memory S49persistent-cron S94sg2002aes S96picoclaw S98tailscaled S80dnsmasq S13nanokvm-watchdog'.split()
for name in names:
    copy(r/'kvmapp/system/init.d'/name,base/'usr/libexec/nanokvm/legacy'/name)
    link('/usr/libexec/nanokvm/legacy/'+name,base/'etc/init.d'/name)
for name in ('S50sshd','S49chronyd','S50avahi-daemon'):
    copy(r/'firmware/alpine/compat'/name,base/'usr/libexec/nanokvm/legacy'/name)
    link('/usr/libexec/nanokvm/legacy/'+name,base/'etc/init.d'/name)
copy(r/'server/service/network/scripts/S02ipv6',base/'usr/libexec/nanokvm/legacy/S02ipv6')
link('/usr/libexec/nanokvm/legacy/S02ipv6',base/'etc/init.d/S02ipv6')
copy(r/'firmware/alpine/compat/nanokvm-stage-update',base/'usr/sbin/nanokvm-stage-update')
for name in ('nanokvm_update_edid','nanokvm-wifi-tx-live','nanokvm-wifi-tx-policy'):
    copy(old/'usr/sbin'/name,base/'usr/sbin'/name)
for name in ('nanokvm-buildroot','chrony.conf','console_handler.sh'):
    copy(old/'etc'/name,base/'etc'/name)
for name in ('ttyGS0_handler.sh',):
    if (old/'etc'/name).exists(): copy(old/'etc'/name,base/'etc'/name)
copy(args.server/'NanoKVM-Server.stripped',app/'kvmapp/server/NanoKVM-Server')
# Native libraries and server must come from the same build (including the GOP ABI).
shutil.copytree(args.server/'dl_lib',app/'kvmapp/server/dl_lib',dirs_exist_ok=True)
if args.boot_payloads:
    boots=out/'kernel-sg2002/usr/lib/nanokvm/boot'
    shutil.rmtree(boots)
    boots.mkdir(parents=True)
    for src in args.boot_payloads.iterdir():
        if src.suffix in ('.sd','.sha256') or src.name == 'kernel.release':
            copy(src,boots/src.name)
web=app/'kvmapp/server/web'
assert web.is_relative_to(out)
shutil.rmtree(web)
shutil.copytree(args.web,web)
(app/'kvmapp/version').write_text(version.removeprefix('v')+'\n')
# Alpine marks keep the accepted native high-rate and file-backed runtime paths.
(base/'etc/nanokvm-buildroot').write_text('Alpine 3.24; SG2002/C906; flavour=enhanced\n')
release=out/'release/etc'; release.mkdir(parents=True,exist_ok=True)
(release/'nanokvm-release').write_text(f'NAME="NanoKVM OS"\nVERSION="{version}"\nALPINE_VERSION="3.24"\nBUILD_PROFILE="c906-scalar"\n')
(release/'nanokvm-build-profile').write_text('c906-scalar\n')
for name,target in [('usr/sbin/watchdog','/sbin/watchdog')]:
    link(target,base/name)
for src in (old/'mnt/data').glob('sensor_cfg.ini*'):
    copy(src,fw/'usr/share/nanokvm/board-defaults'/src.name)
required=['base/usr/sbin/nanokvm_update_edid','base/etc/init.d/S50sshd','base/etc/init.d/S38memory','base/etc/init.d/nanokvm-policy','base/usr/libexec/nanokvm/legacy/S15kvmhwd','app/kvmapp/system/init.d/S15kvmhwd','app/kvmapp/server/NanoKVM-Server']
for name in required:
    path=out/name
    if not (path.is_file() or path.is_symlink()): p.error('Missing payload: '+name)
for path in (base/'usr/libexec/nanokvm/legacy/S15kvmhwd',app/'kvmapp/system/init.d/S15kvmhwd'):
    if path.read_bytes() != enhanced_s15.read_bytes(): p.error('Enhanced S15kvmhwd was replaced: '+str(path))
protected=base/'etc/apk/protected_paths.d/nanokvm.list'
protected.parent.mkdir(parents=True,exist_ok=True)
protected.write_text('+kvmapp/kvm\n')
manifest=app/'kvmapp/enhanced-stage-manifest.json'
manifest.write_text(json.dumps({'version':version,'source_dirty':bool(subprocess.check_output(['git','-C',str(r),'status','--porcelain'],text=True).strip()),'source_base_commit':subprocess.check_output(['git','-C',str(r),'rev-parse','HEAD'],text=True).strip(),'files':{str(p.relative_to(app/'kvmapp')):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted((app/'kvmapp').rglob('*')) if p.is_file() and p!=manifest}},indent=2)+'\n')
print(out)
