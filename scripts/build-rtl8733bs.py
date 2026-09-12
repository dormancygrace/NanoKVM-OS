#!/usr/bin/env python3
"""Build the pinned RTL8733BS port; never load modules or alter networking."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile

p = argparse.ArgumentParser(description=__doc__)
for name in ('sdk', 'kernel-source', 'kernel-output', 'buildroot-output', 'output'):
    p.add_argument('--' + name, type=Path, required=True)
p.add_argument('--kernel-release', default='7.2.5-nanokvm-os')
p.add_argument('--jobs', type=int, default=8)
a = p.parse_args()
repo = Path(__file__).resolve().parents[1]
policy = repo / 'firmware/wifi/rtl8733bs'
pin = json.loads((policy / 'source.json').read_text())
out = a.output.resolve()
if out.exists() or not 1 <= a.jobs <= 32:
    p.error('Use a fresh output directory and 1..32 jobs')
kernel, ko = a.kernel_source.resolve(), a.kernel_output.resolve()
if (ko / 'include/config/kernel.release').read_text().strip() != a.kernel_release:
    p.error('RTL8733BS port requires the explicitly selected matching kernel output')
cross = str(a.buildroot_output.resolve() / 'host/bin/riscv64-buildroot-linux-musl-')
if subprocess.check_output([cross + 'gcc', '-dumpfullversion'], text=True).strip() != '16.2.0':
    p.error('Expected GCC 16.2.0')
# Vendor diagnostics use __DATE__/__TIME__. Bind those to the source revision,
# not wall-clock time, so standalone and integrated builds have identical bytes.
epoch = subprocess.check_output(['git', '-C', str(a.sdk.resolve()), 'show',
                                 '-s', '--format=%ct', pin['commit']], text=True).strip()
if not epoch.isdecimal():
    raise RuntimeError('Invalid pinned SDK commit timestamp')
env = dict(os.environ, PATH='/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin',
           KBUILD_BUILD_USER='nanokvm', KBUILD_BUILD_HOST='builder',
           SOURCE_DATE_EPOCH=epoch, TZ='UTC')
archive = subprocess.check_output(['git', '-C', str(a.sdk.resolve()), 'archive', pin['commit'], pin['subdirectory']], env=env)
out.mkdir(parents=True)
with tarfile.open(fileobj=io.BytesIO(archive)) as tar:
    tar.extractall(out, filter='data')
src = out / pin['subdirectory']
with (policy / 'linux-7.2.patch').open('rb') as f:
    subprocess.run(['patch', '-d', str(src), '-p1', '--forward'], stdin=f, check=True, env=env)
flags = json.loads((repo / 'firmware/toolchain/thead/profile.json').read_text())['kernel']['kcflags']
for path, target in [(kernel, './linux'), (ko, './linux-build'), (src, './rtl8733bs')]:
    flags += f' -ffile-prefix-map={path}={target}'
args = ['make', '-C', str(kernel), 'O=' + str(ko), 'ARCH=riscv',
        'CROSS_COMPILE=' + cross, 'M=' + str(src), 'KCFLAGS=' + flags,
        'CONFIG_PLATFORM_I386_PC=n',
        'USER_EXTRA_CFLAGS=-DCONFIG_LITTLE_ENDIAN -DCONFIG_IOCTL_CFG80211 -DRTW_USE_CFG80211_STA_EVENT',
        '-j' + str(a.jobs), 'modules']
(out / 'build-command.json').write_text(json.dumps(args, indent=2) + '\n')
with (out / 'build.log').open('w') as log:
    subprocess.run(args, stdout=log, stderr=subprocess.STDOUT, check=True, env=env)
module = src / '8733bs.ko'
subprocess.run([cross + 'strip', '--strip-debug', str(module)], check=True)
sha = lambda f: hashlib.sha256(f.read_bytes()).hexdigest()
fields = {field: subprocess.check_output(['modinfo', '-F', field, str(module)], text=True).strip()
          for field in ('vermagic', 'alias', 'depends', 'version', 'firmware')}
if not fields['vermagic'].startswith(a.kernel_release + ' '):
    raise RuntimeError('Module vermagic mismatch')
for alias in ('sdio:c07v024CdB733*', 'sdio:c07v024CdB73A*'):
    if alias not in fields['alias'].splitlines():
        raise RuntimeError('Missing supported SDIO alias')
report = dict(source=pin, source_date_epoch=int(epoch), patch_sha256=sha(policy / 'linux-7.2.patch'),
              kernel_config_sha256=sha(ko / '.config'), module_sha256=sha(module),
              bytes=module.stat().st_size, fields=fields, kcflags=flags,
              hardware_qualified=False)
(out / 'manifest.json').write_text(json.dumps(report, indent=2) + '\n')
print(module)
