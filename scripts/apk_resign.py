#!/usr/bin/env python3
"""Replace the signature of an APK v2 package.

An APK v2 package is three gzip streams: the signature (a tar of one
.SIGN.* file), the control data (.PKGINFO and scripts) and the files. The
signature signs the control stream, whose hash is the package identity in
the indexes and the installed database. Only the first stream is replaced,
so the identity stays; the file size changes.

  split APK DIR        write DIR/control.tar.gz (to be signed), a copy of it
                       and DIR/data.tar.gz
  join APK DIR PUBKEY  APK = DIR/control.tar.gz as signed by abuild-sign
                       (one .SIGN.RSA256.PUBKEY entry) + DIR/data.tar.gz
"""
import argparse
import io
from pathlib import Path
import tarfile
import zlib


def members(data):
    """The gzip streams of data, as bytes."""
    streams = []
    while data:
        stream = zlib.decompressobj(31)
        stream.decompress(data)
        if not stream.eof:
            raise ValueError('truncated gzip stream')
        used = len(data) - len(stream.unused_data)
        streams.append(data[:used])
        data = stream.unused_data
    return streams


def names(stream):
    # abuild cuts the end-of-archive blocks of the first two streams.
    with tarfile.open(fileobj=io.BytesIO(zlib.decompress(stream, 31)), mode='r:', ignore_zeros=True) as tar:
        return [member.name for member in tar]


def split(apk, directory):
    streams = members(Path(apk).read_bytes())
    if len(streams) != 3:
        raise ValueError(f'{apk}: {len(streams)} gzip streams, not 3')
    signature, control, data = streams
    if not names(signature) or not all(n.startswith('.SIGN.') for n in names(signature)):
        raise ValueError(f'{apk}: the first stream is not a signature')
    if '.PKGINFO' not in names(control):
        raise ValueError(f'{apk}: the second stream has no .PKGINFO')
    directory = Path(directory)
    for name, content in (('control.tar.gz', control), ('control.original', control), ('data.tar.gz', data)):
        (directory / name).write_bytes(content)


def join(apk, directory, pubkey):
    directory = Path(directory)
    streams = members((directory / 'control.tar.gz').read_bytes())
    if len(streams) != 2:
        raise ValueError(f'signed control data has {len(streams)} gzip streams, not 2')
    signature, control = streams
    if names(signature) != ['.SIGN.RSA256.' + pubkey]:
        raise ValueError(f'unexpected signature entries {names(signature)}')
    if control != (directory / 'control.original').read_bytes():
        raise ValueError('signing changed the control data')
    Path(apk).write_bytes(signature + control + (directory / 'data.tar.gz').read_bytes())


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command', required=True)
    s = sub.add_parser('split')
    s.add_argument('apk')
    s.add_argument('directory')
    j = sub.add_parser('join')
    j.add_argument('apk')
    j.add_argument('directory')
    j.add_argument('pubkey', help='file name of the public key')
    args = parser.parse_args()
    if args.command == 'split':
        split(args.apk, args.directory)
    else:
        join(args.apk, args.directory, args.pubkey)


if __name__ == '__main__':
    main()
