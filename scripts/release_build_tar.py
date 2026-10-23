#!/usr/bin/env python3
"""Unpack the release-build tar in the signing job, after checking it.

The build job runs third-party build code (npm packages, Go modules,
Buildroot) and hands its outputs over as one tar. The signing job unpacks it
into its build tree, then runs that tree's toolchain with the release key on
disk. So the tar may hold only what the build job packs: the images and the
source archive, as directories and regular files. A member such as
buildroot-output/host/bin/python3, or a link, is refused before anything is
written. The same parser that checks the members unpacks them.
"""
import argparse
from pathlib import Path, PurePosixPath
import re
import tarfile

SOURCE = re.compile(r'NanoKVM-OS-[0-9A-Za-z.+~-]+-source\.tar\.xz')


def check(member):
    parts = PurePosixPath(member.name).parts
    if not parts or member.name.startswith('/') or '..' in parts:
        raise ValueError(f'Unsafe member name: {member.name!r}')
    if not (parts[0] == 'images' or (len(parts) == 1 and SOURCE.fullmatch(parts[0]))):
        raise ValueError(f'Unexpected member: {member.name}')
    if not (member.isreg() or member.isdir()):
        raise ValueError(f'Not a file or directory: {member.name}')


def unpack(archive, output):
    with tarfile.open(archive, 'r:') as tar:
        members = tar.getmembers()
        for member in members:
            check(member)
        tar.extractall(output, members=members, filter='tar')
    return [m.name for m in members]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('archive', type=Path)
    parser.add_argument('output', type=Path)
    args = parser.parse_args()
    print(f'{len(unpack(args.archive, args.output))} members unpacked')


if __name__ == '__main__':
    main()
