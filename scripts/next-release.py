#!/usr/bin/env python3
"""The version of the next release, for the Release workflow's button.

  patch  one more than the newest vX.Y.Z of the line: of all releases when
         releasing main, of vX.Y.* when releasing a release/X.Y branch
  minor  vX.(Y+1).0 after the newest release; only from main

Drafts reserve versions. Stable ignores beta releases. --channel beta uses
the next stable base with -beta.N tags and _betaN APK versions; final stable
supersedes its betas. Each channel follows its newest line, so a beta for an
older target is refused once a newer beta exists. SD images use the OS release
version; their presence and historical names do not affect version selection.

Reads the JSON of GitHub's release list (drafts included) on standard input and
prints key=value lines for $GITHUB_OUTPUT: version, apk_version,
and previous (the release the notes start from).
"""
import argparse
import json
import re
import sys

RELEASE = re.compile(r'v(\d+)\.(\d+)\.(\d+)')
BETA = re.compile(r'v(\d+)\.(\d+)\.(\d+)-beta\.([1-9][0-9]*)')
BRANCH = re.compile(r'release/(\d+)\.(\d+)')


def next_release(releases, kind, ref, channel='stable'):
    if channel not in ('stable', 'beta'):
        raise ValueError('unknown release channel')
    stable = [r for r in releases if not r.get('prerelease') and not BETA.fullmatch(r['tag_name'])]
    if channel == 'beta':
        result = next_release(stable, kind, ref)
        target = result['version']
        base = tuple(map(int, RELEASE.fullmatch(target).groups()))
        if any(tuple(map(int, m.groups()[:3])) > base for r in releases
               if (m := BETA.fullmatch(r['tag_name']))):
            raise ValueError('a newer beta target already exists; use its release line')
        candidates = [r for r in releases if (m := BETA.fullmatch(r['tag_name']))
                      and 'v' + '.'.join(m.groups()[:3]) == target]
        number = max((int(BETA.fullmatch(r['tag_name'])[4]) for r in candidates), default=0) + 1
        published = [r for r in candidates if not r['draft']]
        if published:
            result['previous'] = max(published, key=lambda r: int(BETA.fullmatch(r['tag_name'])[4]))['tag_name']
        result['version'] += f'-beta.{number}'
        result['apk_version'] += f'_beta{number}'
        return result
    releases = stable
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
    text = '.'.join(map(str, version))
    return {'version': 'v' + text, 'apk_version': text,
            'previous': 'v' + '.'.join(map(str, previous)) if previous else ''}


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--channel', choices=('stable', 'beta'), default='stable')
    parser.add_argument('--kind', choices=('patch', 'minor'), required=True)
    parser.add_argument('--ref', required=True)
    args = parser.parse_args()
    try:
        values = next_release(json.load(sys.stdin), args.kind, args.ref, args.channel)
    except ValueError as error:
        sys.exit(f'next-release: {error}')
    for key, value in values.items():
        print(f'{key}={value}')


if __name__ == '__main__':
    main()
