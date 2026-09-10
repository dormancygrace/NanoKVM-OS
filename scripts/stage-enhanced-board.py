#!/usr/bin/env python3
"""Assemble candidate board assets; never install anything on a device."""
import argparse,hashlib,json,os,shutil,subprocess
from pathlib import Path
p=argparse.ArgumentParser()
p.add_argument('--workspace',type=Path,required=True)
p.add_argument('--output',type=Path,required=True)
p.add_argument('--media-modules',type=Path,required=True,help='Selected current media module directory')
p.add_argument('--buildroot-output',type=Path,required=True)
p.add_argument('--kernel-output',type=Path,help='Selected kernel output; defaults to the original board build')
p.add_argument('--kernel-source',type=Path,help='Source tree used for the selected kernel build')
p.add_argument('--wifi-modules',type=Path,help='Explicit matched Wi-Fi module directory')
p.add_argument('--rtl8733bs-module',type=Path,help='Matched RTL8733BS module; required for beta')
p.add_argument('--beta',action='store_true',help='Require CryptoDMA in the beta candidate bundle')
p.add_argument('--aes-module',type=Path,help='Optional SG2002 AES module built against this exact kernel output')
p.add_argument('--cryptodev-module',type=Path,help='Standard /dev/crypto module matched to this kernel')
a=p.parse_args();base=a.workspace.resolve();out=a.output.resolve()
assert not out.exists(), 'Use a fresh output directory'
if a.beta and (not a.aes_module or not a.cryptodev_module or not a.wifi_modules or not a.kernel_output or not a.kernel_source or not a.rtl8733bs_module):
    p.error('Beta requires explicit kernel source/output, AIC and RTL8733BS modules, AES and cryptodev modules')
kernel=a.kernel_source.resolve() if a.kernel_source else base/'enhanced/sources/linux-7.2.4';ko=a.kernel_output.resolve() if a.kernel_output else base/'enhanced/kernel-board-build'
aic=base/'enhanced/sources/aic8800-radxa-sdio'
release=(ko/'include/config/kernel.release').read_text().strip()
assert release=='7.2.4-nanokvm-enhanced'
out.mkdir(parents=True)
env=dict(os.environ,PATH='/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin')
subprocess.run(['make','-C',str(kernel),'O='+str(ko),'ARCH=riscv',
    'CROSS_COMPILE='+str(a.buildroot_output.resolve()/'host/bin/riscv64-buildroot-linux-musl-'),
    'INSTALL_MOD_PATH='+str(out),'DEPMOD=true','modules_install'],check=True,env=env)
modules=out/'lib/modules'/release
if not (modules/'kernel/drivers/net/ovpn/ovpn.ko').is_file():
    raise SystemExit('Missing required OpenVPN DCO module ovpn.ko')
for name in ['build','source']:
    path=modules/name
    if path.is_symlink(): path.unlink()
extra=modules/'extra';extra.mkdir()
selected=['cv181x_sys','cv181x_base','cvi_mipi_rx','cv181x_vi',
          'cv181x_vpss','cv181x_vcodec','cv181x_jpeg','cvi_vc_driver',
          'cv181x_ive','cv181x_dwa','cv181x_rgn','snsr_i2c']
for name in selected:
    source=a.media_modules.resolve()/(name+'.ko')
    if not source.is_file(): raise SystemExit('Missing selected media module: '+str(source))
    shutil.copy2(source,extra/source.name)
for name in ['aic8800_bsp','aic8800_fdrv']:
    source=(a.wifi_modules.resolve()/(name+'.ko') if a.wifi_modules else aic/'src/SDIO/driver_fw/driver/aic8800'/name/(name+'.ko'))
    shutil.copy2(source,extra/source.name)
if a.beta:
    bsp_aliases = subprocess.check_output(['modinfo','-F','alias',str(extra/'aic8800_bsp.ko')],text=True).splitlines()
    if 'sdio:c07v*d*' in bsp_aliases or 'sdio:c*v*d*' in bsp_aliases:
        raise SystemExit('AIC BSP still claims the whole SDIO Wi-Fi class; apply the ownership patch')
    if not {'sdio:c07v5449d0145*', 'sdio:c07v544Ad0146*'}.issubset(bsp_aliases):
        raise SystemExit('AIC BSP lacks a NanoKVM AIC8801 primary/secondary function alias')
if a.rtl8733bs_module:
    shutil.copy2(a.rtl8733bs_module.resolve(),extra/'8733bs.ko')
if a.aes_module:
    shutil.copy2(a.aes_module.resolve(),extra/'sg2002_aes_probe.ko')
if a.cryptodev_module:
    shutil.copy2(a.cryptodev_module.resolve(),extra/'cryptodev.ko')
manifest=[]
for mod in sorted(modules.rglob('*.ko')):
    magic=subprocess.check_output(['modinfo','-F','vermagic',str(mod)],text=True).strip()
    assert magic.startswith(release+' ') and 'SMP' not in magic, (mod,magic)
    manifest.append({'path':str(mod.relative_to(out)),'sha256':hashlib.sha256(mod.read_bytes()).hexdigest(),'bytes':mod.stat().st_size,'vermagic':magic})
result=subprocess.run(['depmod','-b',str(out),'-e','-F',str(ko/'System.map'),release],capture_output=True,text=True,env=env)
(out/'depmod.log').write_text(result.stdout+result.stderr)
assert result.returncode==0 and 'unknown symbol' not in result.stderr, result.stderr
firmware=out/'usr/lib/firmware';firmware.mkdir(parents=True)
for source in sorted((aic/'src/SDIO/driver_fw/fw/aic8800').iterdir()):
    if source.is_file():
        shutil.copy2(source,firmware/source.name)
        manifest.append({'path':str((firmware/source.name).relative_to(out)),'sha256':hashlib.sha256(source.read_bytes()).hexdigest(),'bytes':source.stat().st_size})
# Merge module layout to match Buildroot's merged /usr rootfs.
shutil.move(str(out/'lib/modules'),str(out/'usr/lib/modules'))
(out/'lib').rmdir()
for item in manifest:
    if item['path'].startswith('lib/'): item['path']='usr/'+item['path']
(out/'mnt/system').mkdir(parents=True)
(out/'kernel.release').write_text(release+'\n')
(out/'manifest.json').write_text(json.dumps({'kernel':release,'beta_candidate':a.beta,'qualification':'packaging and dependency validation only','files':manifest},indent=2)+'\n')
print('Staged',len(manifest),'module/firmware files at',out)
