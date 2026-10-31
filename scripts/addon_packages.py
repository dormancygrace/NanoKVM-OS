#!/usr/bin/env python3
"""Validate independent add-ons while assembling the signed Pages repository.

Pages discovers add-on releases and verifies bytes against their published
checksums. Its generated addons.json records those checks for index signing;
OS builds neither consume nor produce an add-on version snapshot.
"""
import hashlib
import json
import re
import subprocess


# The add-ons released apart from NanoKVM OS, each as releases of its own,
# NAME-VERSION-rN, of this repository: RustDesk by the RustDesk add-on
# workflow, the others by the add-on workflow (addon.yml). Pages indexes
# them without an OS release. No published release pins one from the
# archived NanoKVM-OS-packages repository.
ADDONS = ('nanokvm-rustdesk', 'nanokvm-ipmi', 'nanokvm-model-ocr', 'nanokvm-model-ocr-latin',
          'nanokvm-model-ocr-cyrillic', 'nanokvm-model-ocr-chinese', 'nanokvm-model-screen')
RELEASES = 'https://github.com/dormancygrace/NanoKVM-OS/releases/download/'
SOURCES = tuple(RELEASES + name + '-' for name in ADDONS)
# A release tag, which is also the APK's name without .apk.
TAG = re.compile('(' + '|'.join(map(re.escape, ADDONS)) + r')-(\d+\.\d+\.\d+)-r(\d+)')


def addon(filename):
    """The add-on name of an add-on APK file name, or None."""
    match = TAG.fullmatch(filename.removesuffix('.apk'))
    return match[1] if match and filename.endswith('.apk') else None


def manifest(data):
    packages = json.loads(data)['packages']
    for name, item in packages.items():
        if not re.fullmatch(r'[a-z0-9][a-z0-9+_.-]*[.]apk', name):
            raise ValueError(f'Invalid add-on filename: {name}')
        if not item['url'].startswith(SOURCES):
            raise ValueError(f'Invalid add-on source: {name}')
        if not re.fullmatch(r'[a-f0-9]{64}', item['sha256']):
            raise ValueError(f'Invalid add-on SHA256: {name}')
        if not isinstance(item['size'], int) or item['size'] <= 0:
            raise ValueError(f'Invalid add-on size: {name}')
    return packages


def checked(name, item, fetch, size=None):
    if size is not None and size != item['size']:
        raise ValueError(f'{name}: add-on size differs from its index')
    data = fetch(item['url'])
    if len(data) != item['size'] or hashlib.sha256(data).hexdigest() != item['sha256']:
        raise ValueError(f'{name}: add-on differs from its pinned size or SHA256')
    return data


# What an add-on package may declare: its files, dependencies and lifecycle
# scripts. Everything else is refused. install-if would install it on devices
# that never chose it, provides or replaces would let it stand in for a core
# package, and triggers would run on changes of other packages.
SECTIONS = {'info', 'paths', 'scripts'}
INFO_FIELDS = {'name', 'version', 'hashes', 'description', 'arch', 'license', 'origin', 'maintainer',
               'url', 'repo-commit', 'build-time', 'installed-size', 'file-size', 'depends'}
SCRIPTS = {'pre-install', 'post-install', 'pre-upgrade', 'post-upgrade', 'pre-deinstall', 'post-deinstall'}


def metadata(dump, filename):
    """The info fields of the add-on APK filename, from its `apk adbdump`,
    after checking that its name, version and arch are those of its file name
    and that it declares nothing else (see SECTIONS)."""
    match = TAG.fullmatch(filename.removesuffix('.apk'))
    if not match or not filename.endswith('.apk'):
        raise ValueError(f'Not an add-on APK: {filename}')
    def refuse(what):
        raise ValueError(f'Add-on {filename} declares {what}')
    section = field = None
    info = {}
    for line in dump.splitlines():
        if not line.strip() or line.startswith('#'):
            continue
        top = re.fullmatch(r'([a-z-]+):.*', line)
        if top:
            section = top[1]
            if section not in SECTIONS:
                refuse(section)
            continue
        if section == 'info':
            entry = re.fullmatch(r'  ([a-z-]+):(?: (.*))?', line)
            if entry:
                field = entry[1]
                if field not in INFO_FIELDS:
                    refuse(field)
                info[field] = '' if field == 'depends' else entry[2] or ''
                continue
            item = re.fullmatch(r"    - '?([^']+)'?", line)
            if field != 'depends' or not item:
                refuse(f'an unexpected info line: {line.strip()}')
            # A conflict could make an upgrade remove a core package.
            if item[1].startswith('!'):
                refuse(f'a conflict with {item[1][1:]}')
            info['depends'] = (info['depends'] + ' ' + item[1]).strip()
        elif section == 'scripts':
            script = re.fullmatch(r'  ([a-z-]+):.*', line)
            if script and script[1] not in SCRIPTS:
                refuse(f'the script {script[1]}')
        elif section is None:
            refuse(f'an unexpected line: {line.strip()}')
    fields = {key: info[key] for key in ('name', 'version', 'arch') if key in info}
    if fields != {'name': match[1], 'version': match[2] + '-r' + match[3], 'arch': 'riscv64'}:
        raise ValueError(f'Add-on metadata mismatch: {filename}: {fields}')
    return info


def verify_repository(output, apk='apk', keys=None):
    """Verify signatures except for explicitly pinned, byte-verified add-ons."""
    entries = manifest((output / 'addons.json').read_bytes())
    for name, item in entries.items():
        checked(name, item, lambda url: (output / name).read_bytes())
    for package in sorted(output.glob('*.apk')):
        command = [apk]
        if keys is not None:
            command += ['--keys-dir', str(keys)]
        if package.name in entries:
            command += ['--allow-untrusted']
        subprocess.run(command + ['verify', str(package)], check=True)
