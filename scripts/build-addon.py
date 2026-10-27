#!/usr/bin/env python3
"""Build a NanoKVM add-on APK from its recipe, addons/NAME/addon.json.

The add-on is a static Go program in its own module (CGO off, so no device toolchain)
with its files and lifecycle scripts, packed by apk-tools 3 mkpkg. It has the
recipe's version and the given revision, never the OS release's. RustDesk has
a builder of its own (scripts/build-rustdesk-addon.py).
No device access, signing or publication.
"""
import argparse
import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import addon_packages  # noqa: E402

ROOT = Path(__file__).resolve().parents[1]
ADDONS = ROOT / 'addons'
SCRIPTS = ('pre-install', 'post-install', 'pre-upgrade', 'post-upgrade', 'pre-deinstall', 'post-deinstall')


def recipe(name, addons=ADDONS):
    """The checked recipe of the add-on NAME."""
    if name not in addon_packages.ADDONS:
        raise ValueError(f'Not an add-on: {name}')
    directory = addons / name.removeprefix('nanokvm-')
    r = json.loads((directory / 'addon.json').read_text())
    if r['name'] != name or not addon_packages.TAG.fullmatch(f"{name}-{r['version']}-r0"):
        raise ValueError(f'{name}: bad name or version in addon.json')
    # The first NanoKVM OS release with what the add-on needs of it: a stable
    # APK version, or a beta's (3.5.0_beta1), which the stable release satisfies.
    if not re.fullmatch(r'\d+\.\d+\.\d+(_beta[1-9][0-9]*)?', r.get('os', '')) or 'nanokvm-os' in r['depends']:
        raise ValueError(f'{name}: addon.json needs "os", the minimum NanoKVM OS release')
    for path, item in r['files'].items():
        if not path.startswith('/') or '..' in Path(path).parts or not (directory / item['source']).is_file():
            raise ValueError(f'{name}: bad file {path}')
        int(item['mode'], 8)
    for action, script in r['scripts'].items():
        if action not in SCRIPTS or not (directory / script).is_file():
            raise ValueError(f'{name}: bad script {action}')
    for license in r['licenses']:
        if not (ROOT / license).is_file():
            raise ValueError(f'{name}: missing license {license}')
    r['directory'] = directory
    return r


def build_go(r, go, output):
    """Cross-build the recipe's Go program for the device."""
    # Resolve GOROOT, not bin/go: the prepared NanoKVM runtime symlinks bin/
    # to the official toolchain (as scripts/build-server-existing-libs.py).
    go_root = go.absolute().parent.parent.resolve()
    env = dict(os.environ, GOOS='linux', GOARCH='riscv64', GORISCV64='rva20u64', CGO_ENABLED='0',
               GOEXPERIMENT='', GOROOT=str(go_root), GOTOOLCHAIN='local')
    binary = output / Path(r['go']['path']).name
    subprocess.run([str(go_root / 'bin/go'), 'build', '-trimpath', '-buildvcs=false', '-ldflags=-s -w -buildid=',
                    '-o', str(binary), r['go']['package']], cwd=r['directory'], env=env, check=True)
    version = subprocess.check_output([str(go_root / 'bin/go'), 'version'], env=env, text=True).strip()
    return binary, version


def payload(r, binary, root, source):
    """The package's files below root; source is recorded for the program."""
    def install(src, dest, mode):
        path = root / dest.lstrip('/')
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(Path(src).read_bytes())
        path.chmod(mode)
    install(binary, r['go']['path'], 0o755)
    for dest, item in r['files'].items():
        install(r['directory'] / item['source'], dest, int(item['mode'], 8))
    for license in r['licenses']:
        install(ROOT / license, f"/usr/share/licenses/{r['name']}/{Path(license).name}", 0o644)
    record = root / 'usr/share' / r['name'] / 'source.json'
    record.parent.mkdir(parents=True, exist_ok=True)
    record.write_text(json.dumps(source, indent=2) + '\n')
    record.chmod(0o644)


def depends(r):
    """The APK dependencies: the recipe's and the minimum OS release, which
    nanokvm-release provides as nanokvm-os=VERSION."""
    return f"{r['depends']} nanokvm-os>={r['os']}".strip()


def mkpkg(r, apk, files, output, version):
    command = [str(apk), 'mkpkg', '--files', str(files), '--output', str(output)]
    for value in [f"name:{r['name']}", f'version:{version}', 'arch:riscv64', f"license:{r['license']}",
                  f"description:{r['description']}", f"depends:{depends(r)}", f"url:{r['url']}"]:
        command += ['--info', value]
    for action, script in sorted(r['scripts'].items()):
        command += ['--script', f"{action}:{r['directory'] / script}"]
    return command


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--addon', required=True, help='the add-on, for example nanokvm-ipmi')
    parser.add_argument('--version-only', action='store_true', help='print the recipe version and stop')
    parser.add_argument('--go', type=Path, help='bin/go of the NanoKVM Go runtime')
    parser.add_argument('--apk', type=Path, help='host apk-tools 3 with mkpkg')
    parser.add_argument('--output', type=Path)
    parser.add_argument('--revision', type=int, default=0)
    parser.add_argument('--commit', help='the source commit')
    parser.add_argument('--source-url', help='public URL of that source')
    args = parser.parse_args()
    r = recipe(args.addon)
    if args.version_only:
        print(r['version'])
        return
    if None in (args.go, args.apk, args.output, args.commit, args.source_url):
        parser.error('--go, --apk, --output, --commit and --source-url are required to build')
    if args.revision < 0 or not args.source_url.startswith('https://'):
        parser.error('bad --revision or --source-url')
    version = f"{r['version']}-r{args.revision}"
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    apkfile = output / f"{r['name']}-{version}.apk"
    if apkfile.exists():
        raise SystemExit(f'refusing to overwrite {apkfile}')
    with tempfile.TemporaryDirectory(prefix=f"build-{r['name']}-") as stage:
        stage = Path(stage)
        binary, go_version = build_go(r, args.go, stage)
        files = stage / 'payload'
        payload(r, binary, files, {'commit': args.commit, 'url': args.source_url, 'go': go_version,
                                   'package_version': version})
        # mkpkg records file owners: never ship a developer's UID. Run under
        # fakeroot, or as root.
        for path in [files, *files.rglob('*')]:
            if path.stat().st_uid != 0 or path.stat().st_gid != 0:
                try:
                    os.chown(path, 0, 0)
                except PermissionError:
                    raise SystemExit('Run this script with fakeroot for root:root APK ownership.')
        subprocess.run(mkpkg(r, args.apk.resolve(), files, apkfile, version), check=True)
    print(apkfile)


if __name__ == '__main__':
    main()
