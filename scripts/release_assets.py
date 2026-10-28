"""Release storage contract shared by assembly, publication and Pages."""
import hashlib
import re
from pathlib import Path

STORAGE_PREFIX = 'os-packages-'
STORAGE_MARKER = re.compile(r'<!-- nanokvm-package-storage: ([^\s]+) -->')
TECHNICAL = {'APKINDEX.tar.gz', 'Packages.adb', 'packages.json', 'addons.json',
             'build-manifest.json', 'installed-packages.txt', 'installed-licenses.txt'}
# addons.json is accepted for old builds, not produced by current OS releases.
REQUIRED = TECHNICAL - {'build-manifest.json', 'addons.json'}
STOCK_INSTALLER = re.compile(r'nanokvm_\d+\.\d+\.\d+(?:-beta\.[1-9]\d*)?\.tar\.gz')


def storage_tag(version):
    if not re.fullmatch(r'v\d+\.\d+\.\d+(?:-beta\.[1-9]\d*)?', version):
        raise ValueError(f'Unsupported release version: {version}')
    return STORAGE_PREFIX + version


def marker(version):
    return f'<!-- nanokvm-package-storage: {storage_tag(version)} -->'


def digest(path):
    with path.open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def split(directory):
    """Move the flat build assets into two independently checksummed sets."""
    directory = Path(directory)
    files = list(directory.iterdir())
    names = {f.name for f in files}
    if not REQUIRED <= names:
        raise ValueError(f'Missing package metadata: {sorted(REQUIRED - names)}')
    public, packages = [], []
    for file in files:
        if file.is_symlink() or not file.is_file():
            raise ValueError(f'Not a regular release file: {file}')
        if re.fullmatch(r'NanoKVM-OS-.+-source\.tar\.xz', file.name) or re.fullmatch(
                r'NanoKVM-OS-Image-.+\.img\.zip', file.name) or STOCK_INSTALLER.fullmatch(file.name):
            public.append(file)
        elif file.name in TECHNICAL or file.name.endswith('.apk'):
            packages.append(file)
        else:
            raise ValueError(f'Unclassified release asset: {file.name}')
    if sum(f.name.endswith('-source.tar.xz') for f in public) != 1:
        raise ValueError('Expected exactly one corresponding-source archive')
    source = next(f for f in public if f.name.endswith('-source.tar.xz'))
    installer_name = 'nanokvm_' + source.name.removeprefix('NanoKVM-OS-v').removesuffix('-source.tar.xz') + '.tar.gz'
    installers = [f.name for f in public if STOCK_INSTALLER.fullmatch(f.name)]
    if installers and installers != [installer_name]:
        raise ValueError('Stock installer version differs from the release source')
    for name, group in [('public', public), ('packages', packages)]:
        target = directory / name
        target.mkdir()
        for file in group:
            file.rename(target / file.name)
        (target / 'SHA256SUMS').write_text(''.join(
            f'{digest(file)}  {file.name}\n' for file in sorted(target.iterdir())))


if __name__ == '__main__':
    import argparse
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('directory', type=Path)
    split(parser.parse_args().directory)
