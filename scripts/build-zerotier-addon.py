#!/usr/bin/env python3
"""Build the optional ZeroTier One APK (nanokvm-zerotier) from ZeroTier's release.

Cross-compiles the MPL-2.0 agent with the NanoKVM riscv64 musl toolchain and
the C906 profile, without ZeroTier's source-available network controller
(ZT_NONFREE) or SSO. The upstream archive is pinned by the checksum in
firmware/alpine/packages/nanokvm-zerotier/APKBUILD.
The APK must record root:root for every file: the script reads that back with
apk adbdump and discards a package that does not.
No device access, package installation, signing, publication or image changes.
"""
import argparse
import atexit
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile
import urllib.request

from nanokvm_cpu_profile import flags, record

ROOT = Path(__file__).resolve().parents[1]
PKG = ROOT / 'firmware/alpine/packages/nanokvm-zerotier'
NAME = 'nanokvm-zerotier'

# Code under ext/ that the agent may compile in or include, with its license
# file and SPDX identifier. An ext/ directory that a build uses and that is
# not listed here fails the build: a new dependency needs a reviewed entry.
EXT_LICENSES = {
    'miniupnpc': ('LICENSE', 'BSD-3-Clause'),
    'libnatpmp': ('LICENSE', 'BSD-3-Clause'),
    'http-parser': ('LICENSE-MIT', 'MIT'),
    'nlohmann': ('LICENSE.MIT', 'MIT'),
    'prometheus-cpp-lite-1.0': ('LICENSE', 'MIT'),
    'inja': ('LICENSE', 'MIT'),
    # Header-only, without its LICENSE: the header's attribution comment,
    # then upstream's license of that version, kept next to the recipe.
    'cpp-httplib': ('httplib.h', 'MIT'),
    # Header-only API; its LICENSE is in the full tree next to it.
    'opentelemetry-cpp-api-only': ('../opentelemetry-cpp-1.21.0/LICENSE', 'Apache-2.0'),
}
# Full license texts of header-only ext/ code that ZeroTier vendors without
# them, by the version the header declares. Another version fails the build.
EXT_LICENSE_TEXTS = {
    'cpp-httplib': (r'#define CPPHTTPLIB_VERSION "([^"]+)"', {'0.15.3': 'cpp-httplib-0.15.3.LICENSE'}),
}
# Markers of the source-available parts. The built binary must contain none.
NONFREE_MARKERS = (b'EmbeddedNetworkController', b'Source-Available License', b'nonfree/LICENSE')


def recipe():
    text = (PKG / 'APKBUILD').read_text()
    value = lambda key: re.search(r'^' + key + r'=(\S+)$', text, re.M).group(1)
    version = value('pkgver')
    ztver = value('_ztver').replace('$pkgver', version)
    archive = f'zerotier-one-{ztver}.tar.gz'
    sums = dict(reversed(line.split()) for line in
                re.search(r'^sha512sums="(.*?)"', text, re.M | re.S).group(1).split('\n') if line.strip())
    depends = re.search(r'^depends="([^"]*)"', text, re.M).group(1)
    return text, version, value('pkgrel'), ztver, archive, sums, depends


def fetch(url, digest, path):
    if not path.exists():
        part = path.with_suffix('.part')
        with urllib.request.urlopen(url, timeout=120) as response, part.open('wb') as out:
            shutil.copyfileobj(response, out)
        part.rename(path)
    actual = hashlib.sha512(path.read_bytes()).hexdigest()
    if actual != digest:
        raise SystemExit(f'{path}: sha512 {actual} does not match the recipe')


def static_elf(path):
    """Whether path is an ELF program without an interpreter (statically
    linked): fakeroot cannot change the owners such a program sees."""
    with open(path, 'rb') as f:
        head = f.read(64)
        if head[:4] != b'\x7fELF' or head[4] != 2 or head[5] != 1:
            return False  # A script (as the workflow's wrapper) or not ELF64 LE.
        phoff = int.from_bytes(head[32:40], 'little')
        phentsize = int.from_bytes(head[54:56], 'little')
        phnum = int.from_bytes(head[56:58], 'little')
        f.seek(phoff)
        table = f.read(phentsize * phnum)
    return not any(int.from_bytes(table[i:i + 4], 'little') == 3  # PT_INTERP
                   for i in range(0, len(table), phentsize))


def owners(dump):
    """The user and group names of every ACL in an `apk adbdump`."""
    return re.findall(r'^\s+(?:user|group): (.*)$', dump, re.M)


def used_ext(tree):
    """The ext/ directories behind the objects of this build, from gcc's -MD files.

    -MD, not -MMD: ext/ is an -isystem directory, so -MMD would omit its
    header-only libraries."""
    used = set()
    for depfile in tree.rglob('*.d'):
        for word in depfile.read_text().replace('\\\n', ' ').split():
            parts = Path(os.path.normpath(word)).parts
            if len(parts) > 1 and parts[0] == 'ext':
                used.add(parts[1])
    return used


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cc', type=Path, required=True, help='riscv64 musl GCC (its g++ and strip are used too)')
    parser.add_argument('--apk', type=Path, required=True,
                        help='host apk-tools 3 with mkpkg, run as root or under fakeroot (dynamically linked)')
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--archive', type=Path, help='the upstream archive, instead of downloading it')
    parser.add_argument('--package-revision', type=int, help='package revision; defaults to the recipe')
    parser.add_argument('--source-url', help='public URL of the Git tree this package revision is built from')
    parser.add_argument('--selftest', type=Path,
                        help="also build ZeroTier's selftest program (make selftest) and write it here, for the checks")
    parser.add_argument('-j', '--jobs', type=int, default=os.cpu_count() or 1)
    args = parser.parse_args()
    if args.source_url and not args.source_url.startswith('https://'):
        parser.error('--source-url must be an HTTPS URL')
    if args.package_revision is not None and args.package_revision < 0:
        parser.error('--package-revision must be nonnegative')

    text, version, revision, ztver, archive_name, sums, depends = recipe()
    digest = sums[archive_name]
    apk = args.apk.resolve()
    # Before the build: under fakeroot, a static apk-tools would record the
    # build account as the owner of every file.
    if os.environ.get('FAKEROOTKEY') and static_elf(apk):
        parser.error(f'{apk} is statically linked, so fakeroot cannot give it root:root files; '
                     'run apk as root (the workflow runs it in a container) or use a dynamically linked apk-tools')
    if args.package_revision is not None:
        revision = str(args.package_revision)
    package_version = f'{version}-r{revision}'
    url = f'https://github.com/zerotier/ZeroTierOne/archive/refs/tags/{ztver}.tar.gz'

    # Not resolved: a Buildroot compiler is a symlink to its wrapper.
    cc = args.cc.absolute()
    prefix = str(cc)[:-len('gcc')] if str(cc).endswith('gcc') else None
    if not prefix:
        parser.error('--cc must name a ...-gcc')
    cxx, strip = prefix + 'g++', prefix + 'strip'

    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    apkfile = output / f'{NAME}-{package_version}.apk'
    if apkfile.exists():
        raise SystemExit(f'refusing to overwrite artifact: {apkfile}')

    work = ROOT / 'work/zerotier-dist'
    work.mkdir(parents=True, exist_ok=True)
    archive = args.archive.resolve() if args.archive else work / archive_name
    fetch(url, digest, archive)

    # A fresh root isolates the build from earlier ones; removed at exit.
    staging = tempfile.TemporaryDirectory(prefix=f'build-{package_version}-', dir=work)
    atexit.register(staging.cleanup)
    root = Path(staging.name)
    # The archive has a symbolic link out of its tree, in the Rust code that
    # this build does not use (rustybits/smeeclient/rustfmt.toml). Skip what
    # the data filter refuses there; refuse it anywhere else.
    def member(info, path):
        try:
            return tarfile.data_filter(info, path)
        except tarfile.FilterError:
            if info.name.startswith(f'ZeroTierOne-{ztver}/rustybits/'):
                return None
            raise

    with tarfile.open(archive) as tar:
        tar.extractall(root, filter=member)
    tree = root / f'ZeroTierOne-{ztver}'
    if not (tree / 'make-linux.mk').exists():
        raise SystemExit(f'{archive}: not a ZeroTierOne {ztver} source tree')

    # The C906 profile without unconditional vector code, as for OpenSSL:
    # ZeroTier's crypto is portable C++.
    cflags = flags('openssl') + ['-g0', '-fstack-protector-strong', '-MD']
    ldflags = ['-pie', '-Wl,-z,relro,-z,now', '-Wl,-z,noexecstack', '-Wl,--as-needed']
    env = {k: v for k, v in os.environ.items() if k not in ('CFLAGS', 'CXXFLAGS', 'LDFLAGS', 'DEFS')}
    make = ['make', f'-j{args.jobs}', f'CC={cc}', f'CXX={cxx}', f'STRIP={strip}',
            'CFLAGS=' + ' '.join(cflags), 'CXXFLAGS=' + ' '.join(cflags), 'LDFLAGS=' + ' '.join(ldflags),
            'ZT_NONFREE=0', 'ZT_SSO_SUPPORTED=0', 'ZT_OFFICIAL=0']
    with (work / 'build.log').open('w') as log:
        subprocess.run(make + ['one'], cwd=tree, env=env, check=True, stdout=log, stderr=subprocess.STDOUT)
        if args.selftest:
            # The same objects as the agent, linked with selftest.o.
            subprocess.run(make + ['selftest'], cwd=tree, env=env, check=True, stdout=log, stderr=subprocess.STDOUT)
    binary = tree / 'zerotier-one'
    subprocess.run([strip, '--strip-unneeded', str(binary)], check=True)
    if args.selftest:
        selftest = args.selftest.resolve()
        selftest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(tree / 'zerotier-selftest', selftest)
        selftest.chmod(0o755)
        subprocess.run([strip, '--strip-unneeded', str(selftest)], check=True)
    image = binary.read_bytes()
    for marker in NONFREE_MARKERS:
        if marker in image:
            raise SystemExit(f'{binary}: contains {marker.decode()}: the non-free controller was built in')
    if b'Mozilla Public License' not in image:
        raise SystemExit(f'{binary}: no MPL-2.0 notice; not the free agent build')

    ext = used_ext(tree)
    unknown = sorted(ext - EXT_LICENSES.keys())
    if unknown:
        raise SystemExit('ext/ code without a reviewed license entry: ' + ', '.join(unknown))
    licenses = {'MPL-2.0'} | {EXT_LICENSES[name][1] for name in ext}

    payload = root / 'payload'

    def install(src, dest, mode=0o644):
        path = payload / dest
        path.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(src, path)
        path.chmod(mode)

    install(binary, 'usr/sbin/zerotier-one', 0o755)
    for link in ('zerotier-cli', 'zerotier-idtool'):
        (payload / 'usr/sbin' / link).symlink_to('zerotier-one')
    install(PKG / 'zerotier-one.initd', 'etc/init.d/zerotier-one', 0o755)
    share = f'usr/share/licenses/{NAME}'
    install(tree / 'LICENSE-MPL.txt', f'{share}/LICENSE-MPL.txt')
    for name in sorted(ext):
        license_file, spdx = EXT_LICENSES[name]
        src = tree / 'ext' / name / license_file
        if src.suffix == '.h':
            # The leading // comment block: copyright and license.
            header = src.read_text()
            notice = []
            for line in header.splitlines():
                if not line.startswith('//'):
                    break
                notice.append(line[2:].strip())
            if not any('MIT License' in line for line in notice):
                raise SystemExit(f'{src}: no MIT notice in its leading comment')
            pattern, texts = EXT_LICENSE_TEXTS[name]
            found = re.search(pattern, header)
            ext_version = found.group(1) if found else None
            if ext_version not in texts:
                raise SystemExit(f'{src}: version {ext_version}; add its upstream LICENSE to the recipe')
            full = PKG / texts[ext_version]
            if hashlib.sha512(full.read_bytes()).hexdigest() != sums.get(full.name):
                raise SystemExit(f'{full}: sha512 does not match the recipe')
            src = root / f'{name}.LICENSE'
            src.write_text('\n'.join(notice).strip() + '\n\n' + full.read_text())
        install(src, f'{share}/{name}.LICENSE')
    meta = f'usr/share/{NAME}'
    record(root / 'cpu-profile.json', cxx, 'openssl', cflags)
    install(root / 'cpu-profile.json', f'{meta}/cpu-profile.json')
    # MPL-2.0 section 3.2: where the Source Code Form is. The agent is
    # ZeroTier's unmodified release; the packaging is this repository.
    source = {
        'upstream': url, 'upstream_sha512': digest, 'upstream_version': ztver,
        'modifications': 'none', 'build': 'make one ZT_NONFREE=0 ZT_SSO_SUPPORTED=0',
        'ext': sorted(ext), 'packaging': args.source_url or 'local build',
        'package_version': package_version,
        'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
    }
    (root / 'source.json').write_text(json.dumps(source, indent=2) + '\n')
    install(root / 'source.json', f'{meta}/source.json')

    command = [str(apk), 'mkpkg', '--files', str(payload), '--output', str(apkfile)]
    for value in [f'name:{NAME}', f'version:{package_version}', 'arch:riscv64',
                  'license:' + ' AND '.join(sorted(licenses, key=lambda s: (s != 'MPL-2.0', s))),
                  'description:ZeroTier One virtual network agent for NanoKVM OS',
                  'depends:' + depends, 'url:https://github.com/zerotier/ZeroTierOne']:
        command += ['--info', value]
    for action in ('post-upgrade', 'pre-deinstall'):
        command += ['--script', f'{action}:{PKG / (NAME + "." + action)}']
    # mkpkg preserves filesystem ownership. Never ship a developer's UID:
    # run under fakeroot, or as root.
    for path in [payload, *payload.rglob('*')]:
        stat = path.lstat()
        if stat.st_uid != 0 or stat.st_gid != 0:
            try:
                os.lchown(path, 0, 0)
            except PermissionError:
                raise SystemExit('Payload is ready. Run this script with fakeroot for root:root APK ownership.')
    subprocess.run(command, check=True)
    # What apk recorded, not what this process sees: fakeroot's owners reach
    # only the programs it can intercept.
    try:
        dump = subprocess.run([str(apk), '--allow-untrusted', 'adbdump', str(apkfile)],
                              check=True, capture_output=True, text=True).stdout
        found = owners(dump)
        entries = 1 + sum(1 for _ in payload.rglob('*'))
        if len(found) != 2 * entries or set(found) != {'root'}:
            raise SystemExit(f'{apkfile}: {len(found)} owner records for {entries} entries, '
                             f'owners {sorted(set(found))}, not root:root; discarded')
    except BaseException:
        apkfile.unlink(missing_ok=True)
        raise
    print(apkfile)


if __name__ == '__main__':
    main()
