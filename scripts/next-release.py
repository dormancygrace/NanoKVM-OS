#!/usr/bin/env python3
"""The version of the next release, for the Release workflow's button.

  patch  one more than the newest vX.Y.Z of the line: of all releases when
         releasing main, of vX.Y.* when releasing a release/X.Y branch
  minor  vX.(Y+1).0 after the newest release; only from main

Drafts count, so two runs cannot pick one version. Devices follow one
repository, the newest release, so a patch of a line is refused once a newer
line has a release. The image version is that of the newest published image;
with --image, its minor plus one.

Reads the JSON of GitHub's release list (drafts included) on standard input and
prints key=value lines for $GITHUB_OUTPUT: version, apk_version, image_version,
and previous (the release the notes start from).
"""
import argparse
import json
import re
import sys

RELEASE = re.compile(r'v(\d+)\.(\d+)\.(\d+)')
IMAGE = re.compile(r'NanoKVM-OS-Image-v(\d+)\.(\d+)')
BRANCH = re.compile(r'release/(\d+)\.(\d+)')


def next_release(releases, kind, ref, image):
    versions = [tuple(map(int, m.groups())) for r in releases if (m := RELEASE.fullmatch(r['tag_name']))]
    if ref == 'main':
        line = versions
    elif m := BRANCH.fullmatch(ref):
        if kind != 'patch':
            raise ValueError('a release branch takes patch releases only')
        branch = tuple(map(int, m.groups()))
        line = [v for v in versions if v[:2] == branch]
        newer = [v for v in versions if v[:2] > branch]
        if newer:
            raise ValueError(f'v{".".join(map(str, max(newer)))} is released already; devices follow the '
                             'newest line, so they would never receive a patch of an older one')
    else:
        raise ValueError(f'release from main or a release/X.Y branch, not {ref}')
    if not line:
        raise ValueError(f'no vX.Y.Z release to follow on {ref}')
    major, minor, patch = max(line)
    version = (major, minor, patch + 1) if kind == 'patch' else (major, minor + 1, 0)
    if version in versions:
        raise ValueError(f'v{".".join(map(str, version))} exists already')
    published = [tuple(map(int, m.groups())) for r in releases if not r['draft']
                 and (m := RELEASE.fullmatch(r['tag_name']))]
    # A draft reserves its version, but may have no tag until publication.
    # For the first published release of a line, include changes since the
    # latest published earlier line.
    previous_line = [v for v in published if v[:2] == version[:2] and v < version]
    previous = max(previous_line or [v for v in published if v < version], default=None)
    images = [tuple(map(int, m.groups())) for r in releases if not r['draft'] or image
              for a in r['assets'] if (m := IMAGE.match(a['name']))]
    if not images:
        raise ValueError('no published image')
    image_major, image_minor = max(images)
    if image:
        image_minor += 1
    text = '.'.join(map(str, version))
    return {'version': 'v' + text, 'apk_version': text, 'image_version': f'v{image_major}.{image_minor}',
            'previous': 'v' + '.'.join(map(str, previous)) if previous else ''}


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--kind', choices=('patch', 'minor'), required=True)
    parser.add_argument('--ref', required=True)
    parser.add_argument('--image', action='store_true', help='the release publishes a new image')
    args = parser.parse_args()
    try:
        values = next_release(json.load(sys.stdin), args.kind, args.ref, args.image)
    except ValueError as error:
        sys.exit(f'next-release: {error}')
    for key, value in values.items():
        print(f'{key}={value}')


if __name__ == '__main__':
    main()
