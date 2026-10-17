#!/usr/bin/env python3
"""The version of the next release, for the Release workflow's button.

  patch  one more than the newest vX.Y.Z of the line: of all releases when
         releasing main, of vX.Y.* when releasing a release/X.Y branch
  minor  vX.(Y+1).0 after the newest release; only from main

Drafts count, so two runs cannot pick one version. The image version is that
of the newest published image; with --image, its minor plus one.

Reads the JSON of GitHub's release list (drafts included) on standard input and
prints key=value lines for $GITHUB_OUTPUT: version, apk_version, image_version.
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
        line = [v for v in versions if v[:2] == tuple(map(int, m.groups()))]
    else:
        raise ValueError(f'release from main or a release/X.Y branch, not {ref}')
    if not line:
        raise ValueError(f'no vX.Y.Z release to follow on {ref}')
    major, minor, patch = max(line)
    version = (major, minor, patch + 1) if kind == 'patch' else (major, minor + 1, 0)
    if version in versions:
        raise ValueError(f'v{".".join(map(str, version))} exists already')
    images = [tuple(map(int, m.groups())) for r in releases if not r['draft']
              for a in r['assets'] if (m := IMAGE.match(a['name']))]
    if not images:
        raise ValueError('no published image')
    image_major, image_minor = max(images)
    if image:
        image_minor += 1
    text = '.'.join(map(str, version))
    return {'version': 'v' + text, 'apk_version': text, 'image_version': f'v{image_major}.{image_minor}'}


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
