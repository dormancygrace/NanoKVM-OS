"""Offline inventory and coverage contracts. Never execute package manifests."""
import json
from pathlib import Path
import re
import subprocess


def tracked_files(root):
    return subprocess.check_output(['git', '-C', str(root), 'ls-files'], text=True).splitlines()


def dependabot_directories(text):
    result = {}
    for block in re.split(r'^  - package-ecosystem: ', text, flags=re.M)[1:]:
        ecosystem = block.splitlines()[0].strip().strip('"\'')
        directories = re.findall(r'^    directory: (\S+)$', block, re.M)
        match = re.search(r'^    directories:\n((?:      - [^\n]+\n)+)', block, re.M)
        if match:
            directories += re.findall(r'^      - (\S+)$', match[1], re.M)
        if not directories:
            raise ValueError('Missing Dependabot directories: ' + ecosystem)
        result.setdefault(ecosystem, set()).update(d.strip('"\'') for d in directories)
    return result


# Manifests deliberately outside Dependabot, with the reason. An entry for a
# file that no longer exists fails, so the list cannot go stale.
UNMONITORED = {
    'server/third_party/pion-dtls/e2e/Dockerfile': 'upstream test image of the vendored fork; never built here',
    'server/third_party/pion-ice/examples/nat-rules/Dockerfile': 'upstream example of the vendored fork; never built here',
    'firmware/crypto/experimental/sg2002-crypto-all/ghash-go/go.mod': 'experimental benchmark module; not in the firmware',
}


def validate_coverage(root, sources):
    buildroot_recipes(root)
    files = tracked_files(root)
    stale = sorted(set(UNMONITORED) - set(files))
    if stale:
        raise ValueError('Stale unmonitored entries: ' + ', '.join(stale))
    configured = dependabot_directories((root / '.github/dependabot.yml').read_text())
    manifests = {'package.json': 'npm', 'go.mod': 'gomod', 'Cargo.toml': 'cargo', 'Dockerfile': 'docker',
                 'rust-toolchain.toml': 'rust-toolchain', 'rust-toolchain': 'rust-toolchain'}
    missing = []
    for path in files:
        ecosystem = manifests.get(Path(path).name)
        if ecosystem and path not in UNMONITORED and ('/' if str(Path(path).parent) == '.' else '/' + str(Path(path).parent)) not in configured.get(ecosystem, set()):
            missing.append(path)
        # Fail visibly when a new ecosystem needs configuration, not just the known ones.
        if Path(path).name in {'pyproject.toml', 'requirements.txt', 'Gemfile', 'composer.json', 'pom.xml'}:
            missing.append(path + ' (new ecosystem: add coverage)')
    if '/' not in configured.get('github-actions', set()):
        missing.append('.github/workflows')
    lock = {line.split()[0]: line.split() for line in (root / 'platform/sources.lock').read_text().splitlines()
            if line.strip() and not line.startswith('#')}
    pins = set(lock)
    owners = [s['lock_entry'] for s in sources if 'lock_entry' in s]
    missing += ['platform/sources.lock:' + n for n in sorted(pins - set(owners))]
    if len(owners) != len(set(owners)) or set(owners) - pins:
        raise ValueError('Duplicate or stale source-lock coverage')
    if missing:
        raise ValueError('Unmonitored dependency inputs: ' + ', '.join(missing))
    # These monitors use richer companion metadata; don't let it drift from
    # the inputs actually consumed by platform/build.sh.
    metadata = json.loads((root / 'firmware/sources.json').read_text())
    if metadata['json_c']['commit'] != lock['json-c'][2]:
        raise ValueError('json-c metadata disagrees with platform source pin')
    runtime = json.loads((root / 'firmware/cpu/sysmon-runtime/go-source-pin.json').read_text())
    if '/'+runtime['go_version']+'.linux-amd64.tar.gz' not in lock['go'][1]:
        raise ValueError('Go runtime metadata disagrees with platform source pin')


def npm_sources(root):
    result = []
    for path in tracked_files(root):
        if Path(path).name != 'package.json':
            continue
        manifest = json.loads((root / path).read_text())
        for name, value in {**manifest.get('dependencies', {}), **manifest.get('devDependencies', {})}.items():
            actual, pin = name, value
            if value.startswith('npm:'):
                actual, pin = value[4:].rsplit('@', 1)
            # Current manifests pin exact versions. A range needs a deliberate
            # lockfile-aware implementation instead of silently skipping it.
            if not re.fullmatch(r'\d+\.\d+\.\d+', pin):
                raise ValueError('Unsupported npm pin in ' + path + ': ' + name + '=' + value)
            pattern = r'"' + re.escape(name) + r'"\s*:\s*"' + re.escape(value[:-len(pin)]) + r'([0-9.]+)"'
            source = dict(name='npm: ' + name, path=path, pattern=pattern, kind='npm', package=actual)
            if name == '@types/node':
                source['policy'] = 'New majors are reported here, but Dependabot keeps types aligned with Node 26'
            result.append(source)
    return result


BUILDROOT_FEATURES = {
    'HOST_LINUX_HEADERS_CUSTOM_7_1', 'E2FSPROGS_RESIZE2FS', 'HOST_PYTHON3_XZ',
    'HOST_UBOOT_TOOLS_FIT_SUPPORT',
}
BUILDROOT_RECIPES = {
    'BUSYBOX': 'busybox', 'E2FSPROGS': 'e2fsprogs', 'F2FS_TOOLS': 'f2fs-tools',
    'LZ4': 'lz4', 'HOST_LZ4': 'lz4', 'HOST_DTC': 'dtc', 'HOST_KMOD': 'kmod', 'HOST_PATCHELF': 'patchelf',
    'HOST_PYTHON3': 'python3', 'HOST_UBOOT_TOOLS': 'uboot-tools', 'HOST_ZSTD': 'zstd',
    'HOST_CMAKE': 'cmake', 'HOST_DOSFSTOOLS': 'dosfstools', 'HOST_MTOOLS': 'mtools',
    'HOST_F2FS_TOOLS': 'f2fs-tools',
}


def buildroot_recipes(root):
    text = (root / 'firmware/buildroot/configs/nanokvm_platform_defconfig').read_text()
    selected = set(re.findall(r'^BR2_PACKAGE_([A-Z0-9_]+)=y$', text, re.M))
    missing = selected - BUILDROOT_FEATURES - BUILDROOT_RECIPES.keys()
    if missing:
        raise ValueError('Unmonitored Buildroot selections: ' + ', '.join(sorted(missing)))
    return sorted({BUILDROOT_RECIPES[n] for n in selected if n in BUILDROOT_RECIPES} | {'gcc', 'binutils', 'musl'})
