#!/usr/bin/env python3
"""Download and run the NanoKVM OS stock installer over SSH (Python 3.8+).

Default: download, verify, and check compatibility only. --yes authorizes the
migration and reboot. An explicit --version vX.Y.Z selects a particular release.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import tarfile
import tempfile
import urllib.error
import urllib.parse
import urllib.request

REPOSITORY = 'dormancygrace/NanoKVM-OS'
BASE = 'https://github.com/' + REPOSITORY + '/releases/download/'
MAX_PACKAGE = 1024**3
MAX_EXPANDED = 2 * 1024**3
VERSION = re.compile(r'v([0-9]+\.[0-9]+\.[0-9]+(?:-beta\.[1-9][0-9]*)?)')


class HTTPSOnly(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        if urllib.parse.urlsplit(newurl).scheme != 'https':
            raise ValueError('refusing a non-HTTPS download redirect')
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def fetch(url, limit, target=None):
    if urllib.parse.urlsplit(url).scheme != 'https':
        raise ValueError('downloads require HTTPS')
    opener = urllib.request.build_opener(HTTPSOnly())
    request = urllib.request.Request(url, headers={'User-Agent': 'NanoKVM-OS-stock-installer'})
    with opener.open(request, timeout=60) as response:
        size = response.headers.get('Content-Length')
        if size is not None and (not size.isdigit() or int(size) > limit):
            raise ValueError('download exceeds size limit')
        if target is not None and size is not None:
            if shutil.disk_usage(target.parent).free < int(size) + 128 * 1024**2:
                raise ValueError('not enough space to download the installer')
        result = bytearray()
        stream = target.open('xb') if target is not None else None
        received = 0
        try:
            while True:
                block = response.read(1024 * 1024)
                if not block:
                    break
                received += len(block)
                if received > limit:
                    raise ValueError('download exceeds size limit')
                if stream is None:
                    result.extend(block)
                else:
                    if shutil.disk_usage(target.parent).free < len(block) + 128 * 1024**2:
                        raise ValueError('download would exhaust the stock root filesystem')
                    stream.write(block)
            if size is not None and received != int(size):
                raise ValueError('truncated download')
        finally:
            if stream is not None:
                stream.close()
        return bytes(result)


def expected_hash(text, name):
    matches = []
    for line in text.splitlines():
        fields = line.split()
        if len(fields) == 2 and fields[1] == name:
            matches.append(fields[0])
    if len(matches) != 1 or not re.fullmatch('[0-9a-f]{64}', matches[0]):
        raise ValueError('release has no unique checksum for ' + name)
    return matches[0]


def verify(path, expected):
    value = hashlib.sha256()
    with path.open('rb') as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b''):
            value.update(block)
    if value.hexdigest() != expected:
        raise ValueError('installer package SHA256 mismatch')


def unpack(archive, destination, version):
    stock_version = version.split('-', 1)[0]
    root_name = 'nanokvm_' + stock_version
    with tarfile.open(archive, 'r:gz') as tar:
        members = []
        names = set()
        expanded = 0
        for member in tar:
            parts = PurePosixPath(member.name).parts
            if (not parts or parts[0] != root_name or member.name.startswith('/') or '..' in parts
                    or not (member.isdir() or member.isreg()) or member.name in names
                    or len(members) >= 100000 or member.size < 0):
                raise ValueError('unsafe or unsupported installer archive member')
            names.add(member.name)
            expanded += member.size
            if expanded > MAX_EXPANDED:
                raise ValueError('expanded package exceeds size limit')
            members.append(member)
        if shutil.disk_usage(destination).free < expanded + 128 * 1024**2:
            raise ValueError('not enough space to unpack the installer')
        # No tar.extractall: stock Python may predate extraction filters. Only
        # regular files and directories under this new private directory exist.
        for member in members:
            target = destination.joinpath(*PurePosixPath(member.name).parts)
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
                continue
            target.parent.mkdir(parents=True, exist_ok=True)
            with tar.extractfile(member) as src, target.open('xb') as dst:
                shutil.copyfileobj(src, dst)
            target.chmod(0o755 if member.mode & 0o111 else 0o644)
    root = destination / root_name
    if (root / 'version').read_text().strip() != stock_version:
        raise ValueError('installer package version mismatch')
    for name in ('migration/install.sh', 'migration/SHA256SUMS', 'server/NanoKVM-Server',
                 'kvm_system/kvm_system', 'system/init.d/S95nanokvm'):
        if not (root / name).is_file():
            raise ValueError('installer package lacks ' + name)
    if (root / 'migration/target-version').read_text().strip() != version:
        raise ValueError('installer target version differs from the selected release')
    return root / 'migration'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', help='release tag, for example v3.7.0; default: latest stable')
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument('--check', action='store_true', help='compatibility check only (default)')
    mode.add_argument('--yes', action='store_true', help='replace stock OS and reboot after verification')
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error('run as root on the stock NanoKVM')
    if os.uname().machine != 'riscv64' or Path('/etc/alpine-release').exists():
        parser.error('this installer is for a stock SG2002 NanoKVM; use package updates on NanoKVM OS')
    tag = args.version
    if tag is None:
        metadata = json.loads(fetch('https://api.github.com/repos/' + REPOSITORY + '/releases/latest', 1024**2))
        tag = metadata['tag_name']
    match = VERSION.fullmatch(tag)
    if match is None:
        parser.error('unsupported release tag')
    version = match.group(1)
    filename = 'nanokvm_' + version.split('-', 1)[0] + '.tar.gz'
    cache = Path('/root/.cache')
    cache.mkdir(mode=0o700, exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix='nanokvm-stock-', dir=cache))
    detached = False
    try:
        sums = fetch(BASE + tag + '/SHA256SUMS', 1024**2).decode('ascii')
        expected = expected_hash(sums, filename)
        archive = work / filename
        print('Downloading ' + filename, flush=True)
        fetch(BASE + tag + '/' + filename, MAX_PACKAGE, archive)
        verify(archive, expected)
        payload = unpack(archive, work, version)
        archive.unlink()
        subprocess.run(['sh', str(payload / 'install.sh'), str(payload), '--check'], check=True)
        if not args.yes:
            print('Check passed. Run again with --yes to install and reboot.')
            return
        worker = work / 'run.sh'
        worker.write_text('#!/bin/sh\nsh "$2/install.sh" "$2" --yes\nstatus=$?\nrm -rf -- "$1"\nexit "$status"\n')
        log_path = '/var/log/nanokvm-stock-install.log'
        with open(log_path, 'wb') as log, open(os.devnull, 'rb') as stdin:
            subprocess.Popen(['sh', str(worker), str(work), str(payload)], stdin=stdin,
                             stdout=log, stderr=subprocess.STDOUT, start_new_session=True, close_fds=True)
        detached = True
        print('Installation started; it survives an SSH disconnect. Log: ' + log_path)
    finally:
        if not detached:
            shutil.rmtree(work)


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, KeyError, tarfile.TarError, subprocess.CalledProcessError) as error:
        raise SystemExit('NanoKVM OS installer: ' + str(error))
