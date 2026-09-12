#!/usr/bin/env python3
"""Build isolated, matched beta modules. Never install on a device or submit DMA."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess

p = argparse.ArgumentParser(description=__doc__)
for name in ('kernel-source', 'kernel-output', 'osdrv-source', 'wifi-source', 'rtl8733bs-sdk', 'buildroot-output', 'output'):
    p.add_argument('--'+name, type=Path, required=True)
p.add_argument('--kernel-release', default='7.2.5-nanokvm-os')
p.add_argument('--jobs', type=int, default=8)
a = p.parse_args()
repo = Path(__file__).resolve().parents[1]
out = a.output.resolve()
if out.exists() or not 1 <= a.jobs <= 32:
    p.error('Use a fresh output directory and 1..32 jobs')
kernel, ko = a.kernel_source.resolve(), a.kernel_output.resolve()
release = (ko/'include/config/kernel.release').read_text().strip()
if release != a.kernel_release or not release.startswith('7.2.5-nanokvm-os'):
    p.error('Expected the ordinary Enhanced kernel')
cross = str(a.buildroot_output.resolve()/'host/bin/riscv64-buildroot-linux-musl-')
env = dict(os.environ, PATH='/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin')
subprocess.run(['python3', str(repo/'scripts/validate-enhanced-kernel-config.py'), str(ko/'.config'), '--localversion='+release[len('7.2.5'):]], check=True)
out.mkdir(parents=True)
sources = out/'sources'
sources.mkdir()
ignore = shutil.ignore_patterns('.git', '*.o', '*.ko', '*.mod', '*.mod.c', '.*.cmd', 'Module.symvers', 'modules.order', '.tmp_versions')
for name, source in [('osdrv', a.osdrv_source), ('wifi', a.wifi_source)]:
    shutil.copytree(source.resolve(), sources/name, ignore=ignore)
subprocess.run(['python3', str(repo/'scripts/apply-aic-sdio-ownership.py'), '--source', str(sources/'wifi')], check=True)
shutil.copytree(repo/'firmware/crypto/cryptodev-linux', sources/'cryptodev', ignore=ignore)
subprocess.run(['make', '-C', str(sources/'cryptodev'), 'version.h'], check=True)
for patch_name in ('0021-vpss-backpressure-log-ratelimit.patch', '0022-vi-monotonic-sleeping-fps.patch'):
    patch = repo/'firmware/osdrv/patches'/patch_name
    patch_args = ['patch', '-d', str(sources/'osdrv'), '-p1']
    with patch.open('rb') as f:
        applied = subprocess.run(patch_args+['--reverse', '--dry-run'], stdin=f, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0
    if not applied:
        with patch.open('rb') as f:
            subprocess.run(patch_args+['--forward'], stdin=f, check=True)
# Pin the same cached-descriptor implementation used in the current experiment.
# Its presence in a candidate bundle is not a stability qualification.
subprocess.run(['python3', str(repo/'firmware/crypto/experimental/sg2002-crypto-all/prepare.py'), '--repo', str(repo), '--output', str(sources/'aes')], check=True)
sha = lambda path: hashlib.sha256(path.read_bytes()).hexdigest()
source_hashes = {str(x.relative_to(sources)): sha(x) for x in sorted(sources.rglob('*'))
                 if x.is_file() and (x.suffix in ('.c', '.h', '.S', '.inc') or x.name in ('Makefile', 'Kbuild', 'Kconfig'))}
# __FILE__ is also embedded in assertions of stripped modules.
path_flags = ' '.join(f'-ffile-prefix-map={src}={name}' for src, name in [
    (kernel, './linux'), (ko, './linux-build'), (sources, './modules'),
    (repo, './nanokvm-os'), (a.buildroot_output.resolve(), './toolchain')])
isa_flags = json.loads((repo/'firmware/toolchain/thead/profile.json').read_text())['kernel']['kcflags']
args = ['make', '-C', str(kernel), 'O='+str(ko), 'ARCH=riscv',
        'CROSS_COMPILE='+cross, 'KCFLAGS='+isa_flags+' '+path_flags]
log = (out/'build.log').open('w', buffering=1)
def run(extra):
    cwd = next((x[2:] for x in extra if x.startswith('M=')), str(ko))
    subprocess.run(args+extra, cwd=cwd, env=dict(env, PWD=cwd), stdout=log, stderr=subprocess.STDOUT, check=True)

symbols, extra = [], []
for name in ['sys', 'base', 'cif', 'vi', 'vpss', 'vcodec', 'jpeg', 'cvi_vc_drv', 'ive', 'dwa', 'rgn', 'snsr_i2c']:
    module = sources/'osdrv/interdrv'/name
    print('Building', name, flush=True)
    run(['M='+str(module), 'CVIARCH=CV181X', 'CVIARCH_L=cv181x', 'KBUILD_EXTRA_SYMBOLS='+' '.join(symbols), '-j'+str(a.jobs), 'modules'])
    symbols.append(str(module/'Module.symvers'))
    extra.extend(module.rglob('*.ko'))
run(['M='+str(sources/'wifi'), 'CONFIG_PLATFORM_UBUNTU=n', 'CONFIG_SDIO_BT=y', 'CONFIG_AIC8800_BTLPM_SUPPORT=n', 'CONFIG_USE_FW_REQUEST=y', '-j'+str(a.jobs), 'modules'])
extra.extend((sources/'wifi').rglob('*.ko'))
rtl_out = out/'rtl8733bs'
subprocess.run(['python3', str(repo/'scripts/build-rtl8733bs.py'),
                '--sdk', str(a.rtl8733bs_sdk), '--kernel-source', str(kernel),
                '--kernel-output', str(ko), '--buildroot-output', str(a.buildroot_output),
                '--output', str(rtl_out), '--kernel-release', release, '--jobs', str(a.jobs)], check=True)
extra.append(rtl_out/'osdrv/extdrv/wireless/rtl8733bs/8733bs.ko')
run(['M='+str(sources/'aes'), '-j'+str(a.jobs), 'modules'])
extra.extend((sources/'aes').glob('*.ko'))
run(['M='+str(sources/'cryptodev'), '-j'+str(a.jobs), 'modules'])
extra.extend((sources/'cryptodev').glob('*.ko'))
stage = out/'stage'
stage.mkdir()
run(['INSTALL_MOD_PATH='+str(stage), 'DEPMOD=true', 'modules_install'])
modules = stage/'lib/modules'/release
dest = modules/'extra'
dest.mkdir()
for source in extra:
    target = dest/source.name
    if target.exists():
        raise RuntimeError('Duplicate module name: '+source.name)
    shutil.copyfile(source, target)
    subprocess.run([cross+'strip', '--strip-debug', str(target)], check=True)
for name in ('build', 'source'):
    link = modules/name
    if link.is_symlink():
        link.unlink()
result = subprocess.run(['depmod', '-b', str(stage), '-e', '-F', str(ko/'System.map'), release], capture_output=True, text=True)
(out/'depmod.log').write_text(result.stdout+result.stderr)
if result.returncode or result.stderr.strip():
    raise RuntimeError('Module dependency validation failed: '+result.stderr)
manifest = {}
for path in sorted(modules.rglob('*.ko')):
    magic = subprocess.check_output(['modinfo', '-F', 'vermagic', str(path)], text=True).strip()
    if magic.split()[0] != release:
        raise RuntimeError('Wrong kernel for '+str(path))
    manifest[str(path.relative_to(stage))] = dict(sha256=sha(path), bytes=path.stat().st_size, vermagic=magic)
for name in ('ovpn', 'sg2002_aes_probe', 'cryptodev', 'aic8800_fdrv', '8733bs', 'cvi_vc_driver', 'cv181x_vpss'):
    deps = subprocess.check_output(['modprobe', '--show-depends', '-d', str(stage), '-S', release, name], text=True)
    if '.ko' not in deps:
        raise RuntimeError('Missing '+name)
    (out/(name+'-dependencies.txt')).write_text(deps)
report = dict(status='built-not-installed', kcflags=isa_flags+' '+path_flags, kernel=release, kernel_config_sha256=sha(ko/'.config'),
              kernel_image_sha256=sha(ko/'arch/riscv/boot/Image'), source_hashes=source_hashes, modules=manifest,
              aes_candidate=json.loads((sources/'aes/prepare-manifest.json').read_text()),
              crypto_extension=json.loads((sources/'aes/crypto-prepare.json').read_text()),
              rtl8733bs=json.loads((rtl_out/'manifest.json').read_text()),
              small_core_module_included=False, hardware_qualification=False)
(out/'manifest.json').write_text(json.dumps(report, indent=2)+'\n')
print('Matched module bundle built:', out, 'modules:', len(manifest), flush=True)
