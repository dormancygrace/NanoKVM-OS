#!/usr/bin/env python3
"""Build a NanoKVM add-on APK from its recipe, addons/NAME/addon.json.

The add-on is either a static Go program in its own module (CGO off, so no
device toolchain), which may be a program of a module that module requires
(Tailscale), or TPU models: files of a directory that
tpu/models/convert.sh and stage.sh made, installed below MODELS_PREFIX. Its
files and lifecycle scripts go with it, packed by apk-tools 3 mkpkg. It has
the recipe's version and the given revision, never the OS release's.
RustDesk has a builder of its own (scripts/build-rustdesk-addon.py).
No device access, signing or publication.
"""
import argparse
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import addon_packages  # noqa: E402

ROOT = Path(__file__).resolve().parents[1]
ADDONS = ROOT / 'addons'
# The packages of golang.org/x/crypto the NanoKVM Go runtime carries C906 code
# for (firmware/cpu/sysmon-runtime/prepare.py puts it in the copy vendored
# into the standard library).
X_CRYPTO_C906 = ('chacha20', 'internal/poly1305')
SCRIPTS = ('pre-install', 'post-install', 'pre-upgrade', 'post-upgrade', 'pre-deinstall', 'post-deinstall')
# Where model add-ons install their models: the TPU programs of the core
# package nanokvm-tpu read them there.
MODELS_PREFIX = '/usr/lib/nanokvm-tpu/models'
# The TPU model converter (tpu/models/convert.sh), recorded with the models.
CONVERTER = ROOT / 'tpu/models/convert.sh'


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
    # A program or models, not both.
    if ('go' in r) == ('models' in r):
        raise ValueError(f'{name}: addon.json needs "go" or "models"')
    for path in r.get('models', []):
        parts = Path(path).parts
        if not path or path.startswith('/') or '..' in parts or '.' in parts:
            raise ValueError(f'{name}: bad model {path}')
    if 'models' in r and not r['models']:
        raise ValueError(f'{name}: no models')
    go = r.get('go', {})
    if not set(go) <= {'package', 'path', 'tags', 'experiment', 'stamps', 'x_crypto'}:
        raise ValueError(f'{name}: unknown "go" key')
    for key in ('tags', 'experiment'):
        if not re.fullmatch(r'[a-z0-9_]+(,[a-z0-9_]+)*', go.get(key, 'x')):
            raise ValueError(f'{name}: bad go.{key}')
    for symbol, value in go.get('stamps', {}).items():
        if not re.fullmatch(r'[A-Za-z0-9_./-]+', symbol) or not re.fullmatch(r'[A-Za-z0-9_.{}-]+', value):
            raise ValueError(f'{name}: bad go.stamps entry {symbol}')
    if not re.fullmatch(r'v[0-9]+[.][0-9]+[.][0-9]+', go.get('x_crypto', 'v0.0.0')):
        raise ValueError(f'{name}: bad go.x_crypto')
    for link, target in r.get('links', {}).items():
        if not link.startswith('/') or not target.startswith('/') or '..' in Path(link).parts + Path(target).parts:
            raise ValueError(f'{name}: bad link {link}')
    for key in ('provides', 'replaces'):
        if not re.fullmatch(r'[a-z0-9][a-z0-9+_.:=-]*( [a-z0-9][a-z0-9+_.:=-]*)*', r.get(key, 'x')):
            raise ValueError(f'{name}: bad {key}')
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


def x_crypto_c906(r, go_root, version, env, stage):
    """A go.mod for the recipe's module that replaces golang.org/x/crypto
    VERSION by a copy with the C906 ChaCha20 and Poly1305 of the NanoKVM Go
    runtime. A Go program is one static file: a program that takes
    golang.org/x/crypto as a module (not the standard library's copy) gets
    that code only this way. The module is the one go.sum pins; the runtime's
    two packages replace the module's whole directories and import nothing
    else of it but internal/alias, which both copies have."""
    info = json.loads(subprocess.check_output(
        [str(go_root / 'bin/go'), 'mod', 'download', '-json', f'golang.org/x/crypto@{version}'],
        cwd=r['directory'], env=dict(env, GOOS='', GOARCH='', GOFLAGS='-mod=mod'), text=True))
    copy = stage / 'x-crypto'
    shutil.copytree(info['Dir'], copy)
    for path in [copy, *copy.rglob('*')]:
        path.chmod(path.stat().st_mode | 0o200)
    for package in X_CRYPTO_C906:
        ours = go_root / 'src/vendor/golang.org/x/crypto' / package
        if not any(ours.glob('*_xtheadvector_riscv64.go')):
            raise SystemExit(f'{ours}: not the NanoKVM Go runtime (firmware/cpu/sysmon-runtime/prepare.py)')
        shutil.rmtree(copy / package)
        shutil.copytree(ours, copy / package)
    modfile = stage / 'go.mod'
    modfile.write_text((r['directory'] / 'go.mod').read_text()
                       + f'{chr(10)}replace golang.org/x/crypto {version} => {copy}{chr(10)}')
    shutil.copyfile(r['directory'] / 'go.sum', stage / 'go.sum')
    return modfile


def build_go(r, go, output, version):
    """Cross-build the recipe's Go program for the device."""
    # Resolve GOROOT, not bin/go: the prepared NanoKVM runtime symlinks bin/
    # to the official toolchain (as scripts/build-server-existing-libs.py).
    go_root = go.absolute().parent.parent.resolve()
    env = dict(os.environ, GOOS='linux', GOARCH='riscv64', GORISCV64='rva20u64', CGO_ENABLED='0',
               GOEXPERIMENT='', GOROOT=str(go_root), GOTOOLCHAIN='local')
    recipe = r['go']
    if 'experiment' in recipe:
        env['GOEXPERIMENT'] = recipe['experiment']
    binary = output / Path(recipe['path']).name
    ldflags = '-s -w -buildid=' + ''.join(f' -X {symbol}={value.format(version=version)}'
                                          for symbol, value in sorted(recipe.get('stamps', {}).items()))
    command = [str(go_root / 'bin/go'), 'build', '-trimpath', '-buildvcs=false', f'-ldflags={ldflags}']
    if 'tags' in recipe:
        command += ['-tags', recipe['tags']]
    if 'x_crypto' in recipe:
        command.append(f"-modfile={x_crypto_c906(r, go_root, recipe['x_crypto'], env, output)}")
    subprocess.run(command + ['-o', str(binary), recipe['package']], cwd=r['directory'], env=env, check=True)
    if 'x_crypto' in recipe:
        # The program's own record of its modules names the replacement.
        modules = subprocess.check_output([str(go_root / 'bin/go'), 'version', '-m', str(binary)], env=env, text=True)
        if not re.search(r'dep\tgolang[.]org/x/crypto\t\S+\t?\n\t=>\t', modules):
            raise SystemExit(f'{binary.name}: golang.org/x/crypto was not replaced')
    return binary, subprocess.check_output([str(go_root / 'bin/go'), 'version'], env=env, text=True).strip()


def converter():
    """The converter's pins, as convert.sh states them."""
    text = CONVERTER.read_text()
    image = re.search(r'sophgo/tpuc_dev:(\S+)', text)
    mlir = re.search(r'tpu_mlir==(\S+)', text)
    return {'image': f'sophgo/tpuc_dev:{image[1]}', 'tpu_mlir': mlir[1]}


def convert_parts(r):
    """The parts of tpu/models/convert.sh the recipe's models come from."""
    parts = set()
    for path in r.get('models', []):
        if path == 'ocr-det.cvimodel':
            parts.add('det')
        elif path == 'screen.cvimodel':
            parts.add('screen')
        else:
            parts.add(Path(path).parts[0])
    return sorted(parts)


def model_files(r, models):
    """The recipe's models in the directory models, with their SHA-256."""
    files = {}
    for path in r['models']:
        source = models / path
        if not source.is_file():
            raise SystemExit(f"{r['name']}: no model {path} in {models}")
        files[path] = hashlib.sha256(source.read_bytes()).hexdigest()
    return files


def payload(r, binary, root, source, models=None):
    """The package's files below root: its program (binary) or its models (the
    directory models), the recipe's files and licences, and source."""
    def install(src, dest, mode):
        path = root / dest.lstrip('/')
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(Path(src).read_bytes())
        path.chmod(mode)
    if 'go' in r:
        install(binary, r['go']['path'], 0o755)
    for link, target in r.get('links', {}).items():
        path = root / link.lstrip('/')
        path.parent.mkdir(parents=True, exist_ok=True)
        path.symlink_to(os.path.relpath(target, Path(link).parent))
    for path in r.get('models', []):
        install(models / path, f'{MODELS_PREFIX}/{path}', 0o644)
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
                  f"description:{r['description']}", f"depends:{depends(r)}", f"url:{r['url']}",
                  *(f'{key}:{r[key]}' for key in ('provides', 'replaces') if key in r)]:
        command += ['--info', value]
    for action, script in sorted(r['scripts'].items()):
        command += ['--script', f"{action}:{r['directory'] / script}"]
    return command


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--addon', required=True, help='the add-on, for example nanokvm-ipmi')
    parser.add_argument('--version-only', action='store_true', help='print the recipe version and stop')
    parser.add_argument('--parts-only', action='store_true',
                        help='print the convert.sh parts of a model add-on (nothing for a program) and stop')
    parser.add_argument('--go', type=Path, help='bin/go of the NanoKVM Go runtime (a program add-on)')
    parser.add_argument('--models', type=Path,
                        help='the staged models, OUTPUT/usr/lib/nanokvm-tpu/models of tpu/models/stage.sh (a model add-on)')
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
    if args.parts_only:
        print(' '.join(convert_parts(r)))
        return
    if None in (args.apk, args.output, args.commit, args.source_url):
        parser.error('--apk, --output, --commit and --source-url are required to build')
    if 'go' in r and args.go is None:
        parser.error('--go is required for a program add-on')
    if 'models' in r and args.models is None:
        parser.error('--models is required for a model add-on')
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
        source = {'commit': args.commit, 'url': args.source_url, 'package_version': version}
        binary = None
        if 'go' in r:
            binary, source['go'] = build_go(r, args.go, stage, r['version'])
            source.update({key: r['go'][key] for key in ('tags', 'experiment', 'x_crypto') if key in r['go']})
        else:
            source['converter'] = converter()
            source['models'] = model_files(r, args.models.resolve())
        files = stage / 'payload'
        payload(r, binary, files, source, args.models and args.models.resolve())
        # mkpkg records file owners: never ship a developer's UID. Run under
        # fakeroot, or as root.
        for path in [files, *files.rglob('*')]:
            if path.lstat().st_uid != 0 or path.lstat().st_gid != 0:
                try:
                    os.lchown(path, 0, 0)
                except PermissionError:
                    raise SystemExit('Run this script with fakeroot for root:root APK ownership.')
        subprocess.run(mkpkg(r, args.apk.resolve(), files, apkfile, version), check=True)
    print(apkfile)


if __name__ == '__main__':
    main()
