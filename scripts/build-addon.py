#!/usr/bin/env python3
"""Build a NanoKVM add-on APK from its recipe, addons/NAME/addon.json.

The add-on is a static Go program in its own module (CGO off, so no device
toolchain), or TPU models: files of a directory that tpu/models/convert.sh
and stage.sh made, installed below MODELS_PREFIX, or a .NET program for the
managed computer (Windows), which the NanoKVM only serves. Its files and
lifecycle scripts go with it, packed by apk-tools 3 mkpkg. It has
the recipe's version and the given revision, never the OS release's.
RustDesk has a builder of its own (scripts/build-rustdesk-addon.py).
No device access, signing or publication.
"""
import argparse
import hashlib
import json
import os
import re
import subprocess
import sys
import tempfile
import urllib.request
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import addon_packages  # noqa: E402

ROOT = Path(__file__).resolve().parents[1]
ADDONS = ROOT / 'addons'
SCRIPTS = ('pre-install', 'post-install', 'pre-upgrade', 'post-upgrade', 'pre-deinstall', 'post-deinstall')
KINDS = ('go', 'models', 'dotnet')
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
    # One of: a program, models, a .NET program for the managed computer.
    if sum(kind in r for kind in KINDS) != 1:
        raise ValueError(f'{name}: addon.json needs one of "go", "models" or "dotnet"')
    if 'dotnet' in r:
        d = r['dotnet']
        if (set(d) != {'project', 'output', 'path', 'pawnio'} or not (directory / d['project']).is_file()
                or not re.fullmatch(r'[A-Za-z0-9._-]+', d['output']) or not d['path'].startswith('/usr/share/nanokvm/')
                or '..' in Path(d['path']).parts or not re.fullmatch(r'[A-Za-z0-9-]+/[A-Za-z0-9._-]+', d['pawnio'])):
            raise ValueError(f'{name}: bad "dotnet"')
    for path in r.get('models', []):
        parts = Path(path).parts
        if not path or path.startswith('/') or '..' in parts or '.' in parts:
            raise ValueError(f'{name}: bad model {path}')
    if 'models' in r and not r['models']:
        raise ValueError(f'{name}: no models')
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


def kind(r):
    return next(k for k in KINDS if k in r)


def pawnio_latest(repository, output):
    """Download PawnIO_setup.exe of the repository's latest release: the
    driver's installer the Windows agent carries. Latest, not a pin: an old
    driver is what an installer should not hand out, and the agent's library
    asks only for a minimum version. What was taken is recorded with the
    package (source.json)."""
    def get(url):
        request = urllib.request.Request(url, headers={'User-Agent': 'nanokvm-os-build'})
        token = os.environ.get('GH_TOKEN') or os.environ.get('GITHUB_TOKEN')
        if token and url.startswith('https://api.github.com/'):
            request.add_header('Authorization', 'Bearer ' + token)
        with urllib.request.urlopen(request, timeout=60) as response:
            return response.read()
    release = json.loads(get(f'https://api.github.com/repos/{repository}/releases/latest'))
    assets = [a for a in release['assets'] if a['name'] == 'PawnIO_setup.exe']
    if len(assets) != 1 or not assets[0]['browser_download_url'].startswith(f'https://github.com/{repository}/releases/download/'):
        raise SystemExit(f'{repository}: no PawnIO_setup.exe in the latest release')
    data = get(assets[0]['browser_download_url'])
    return pawnio_file(data, output, {'tag': release['tag_name'], 'url': assets[0]['browser_download_url']})


def pawnio_file(data, output, record):
    # A Windows program of a plausible size, not an error page.
    if data[:2] != b'MZ' or not 256 << 10 <= len(data) <= 32 << 20:
        raise SystemExit('PawnIO_setup.exe does not look like the installer')
    path = output / 'PawnIO_setup.exe'
    path.write_bytes(data)
    return path, dict(record, sha256=hashlib.sha256(data).hexdigest(), size=len(data))


def build_dotnet(r, dotnet, pawnio, output):
    """Build the recipe's .NET program (a single exe) with the PawnIO
    installer inside."""
    d = r['dotnet']
    env = dict(os.environ, DOTNET_CLI_TELEMETRY_OPTOUT='1', DOTNET_NOLOGO='1')
    out = output / 'dotnet'
    # bin/ and obj/ go to the stage, not into the source tree.
    subprocess.run([str(dotnet), 'build', str(r['directory'] / d['project']), '-c', 'Release', '-o', str(out),
                    f'-p:PawnIoSetup={pawnio}', f"-p:BaseIntermediateOutputPath={output / 'obj'}/",
                    '-p:ContinuousIntegrationBuild=true'], env=env, check=True)
    binary = out / d['output']
    if not binary.is_file() or binary.read_bytes()[:2] != b'MZ':
        raise SystemExit(f"{r['name']}: the build made no {d['output']}")
    version = subprocess.check_output([str(dotnet), '--version'], env=env, text=True).strip()
    return binary, version


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
    if 'dotnet' in r:
        # Served to the managed computer, never run on the NanoKVM.
        install(binary, r['dotnet']['path'], 0o644)
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
                  f"description:{r['description']}", f"depends:{depends(r)}", f"url:{r['url']}"]:
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
    parser.add_argument('--kind-only', action='store_true', help='print the recipe kind (go, models, dotnet) and stop')
    parser.add_argument('--dotnet', type=Path, help='the dotnet program of a .NET SDK (a dotnet add-on)')
    parser.add_argument('--pawnio', type=Path,
                        help='a PawnIO_setup.exe to use instead of downloading the latest release (a dotnet add-on)')
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
    if args.kind_only:
        print(kind(r))
        return
    if None in (args.apk, args.output, args.commit, args.source_url):
        parser.error('--apk, --output, --commit and --source-url are required to build')
    if 'go' in r and args.go is None:
        parser.error('--go is required for a program add-on')
    if 'models' in r and args.models is None:
        parser.error('--models is required for a model add-on')
    if 'dotnet' in r and args.dotnet is None:
        parser.error('--dotnet is required for a dotnet add-on')
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
            binary, source['go'] = build_go(r, args.go, stage)
        elif 'dotnet' in r:
            if args.pawnio:
                pawnio, source['pawnio'] = pawnio_file(args.pawnio.read_bytes(), stage, {'file': args.pawnio.name})
            else:
                pawnio, source['pawnio'] = pawnio_latest(r['dotnet']['pawnio'], stage)
            binary, source['dotnet'] = build_dotnet(r, args.dotnet.resolve(), pawnio, stage)
        else:
            source['converter'] = converter()
            source['models'] = model_files(r, args.models.resolve())
        files = stage / 'payload'
        payload(r, binary, files, source, args.models and args.models.resolve())
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
