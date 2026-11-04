#!/usr/bin/env python3
"""Publish OS downloads only. Signed package candidates are transported to Pages."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

from release_assets import digest, image_versions


def gh(*args):
    return subprocess.check_output(['gh', *args], text=True)


def release(repository, tag):
    # The by-tag REST endpoint is for published releases. List releases with
    # the publisher's write access so interrupted uploads/private drafts are
    # found too; authentication or API failures must never mean "missing".
    pages = json.loads(gh('api', '--paginate', '--slurp',
                          f'repos/{repository}/releases?per_page=100'))
    return next((r for page in pages for r in page if r['tag_name'] == tag), None)


def validate(directory):
    files = {p.name: p for p in directory.iterdir()}
    sums = dict((name, sha) for sha, name in
                (line.split(None, 1) for line in files['SHA256SUMS'].read_text().splitlines()))
    if set(sums) != files.keys() - {'SHA256SUMS'}:
        raise ValueError(f'Incomplete checksums in {directory}')
    for name, sha in sums.items():
        if not files[name].is_file() or files[name].is_symlink() or digest(files[name]) != sha:
            raise ValueError(f'Invalid asset: {files[name]}')
    return files


def stage(tag, current, directory, sha, title, flags):
    if current and not current['draft']:
        raise ValueError(f'Refusing to stage published release: {tag}')
    files = validate(directory)
    if current is None:
        # Create without assets first; a failed upload remains a private draft.
        gh('release', 'create', tag, '--target', sha, '--draft', '--title', title, *flags)
    gh('release', 'upload', tag, '--clobber', *[str(files[n]) for n in sorted(files)])
    # Only drafts are mutable. Clear stale files from an interrupted/rebuilt
    # draft, including assets left by the old all-in-one publication layout.
    if current:
        for asset in current['assets']:
            if asset['name'] not in files:
                gh('release', 'delete-asset', tag, asset['name'], '--yes')


def validate_public_names(version, names):
    required = {'SHA256SUMS', f'NanoKVM-OS-{version}-source.tar.xz'}
    images = [n for n in names if (versions := image_versions(n)) and versions[1] == version]
    installer = {'nanokvm_' + version.removeprefix('v').split('-', 1)[0] + '.tar.gz'} & set(names)
    if (len(names) != len(set(names)) or any(Path(n).name != n for n in names)
            or len(images) > 1 or set(names) != required | set(images) | installer):
        raise ValueError(f'{version}: original public download set is incomplete or unexpected')


def verify_public_downloads(version, current):
    # Resume with the original signed assets, including historical image names.
    names = [a['name'] for a in current['assets']]
    validate_public_names(version, names)
    with tempfile.TemporaryDirectory() as temporary:
        gh('release', 'download', version, '--pattern', '*', '--dir', temporary)
        try:
            if set(validate(Path(temporary))) != set(names):
                raise ValueError('downloaded asset inventory differs from GitHub')
        except (ValueError, KeyError, OSError, TypeError) as error:
            raise ValueError(f'{version}: cannot verify original public downloads: {error}') from error


def main():
    env = os.environ
    version, repository = env['NANOKVM_VERSION'], env['GITHUB_REPOSITORY']
    current = release(repository, version)
    if current:
        if current.get('target_commitish') != env['SHA']:
            raise ValueError('Existing OS release targets a different commit; use a new version')
        verify_public_downloads(version, current)
        if current['draft'] and env['DRAFT'] != 'true':
            gh('release', 'edit', version, '--draft=false')
        return
    directory = Path(env['OUT']) / 'assets'
    validate_public_names(version, list(validate(directory / 'public')))
    flags = ['--generate-notes']
    if '-beta.' in version:
        flags += ['--prerelease', '--latest=false']
    if env.get('PREVIOUS'):
        flags += ['--notes-start-tag', env['PREVIOUS']]
    stage(version, current, directory / 'public', env['SHA'], f'NanoKVM OS {version}', flags)
    if env['DRAFT'] != 'true':
        gh('release', 'edit', version, '--draft=false')


if __name__ == '__main__':
    main()
