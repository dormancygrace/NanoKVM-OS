#!/usr/bin/env python3
"""Stage public downloads and immutable per-version package storage, then publish.
A technical release alone never changes Pages: the public OS release is the gate.
"""
import json
import os
import re
from pathlib import Path
import subprocess
import tempfile

from release_assets import marker, storage_tag, digest, STORAGE_MARKER, REQUIRED, TECHNICAL


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


def notes(tag, body):
    with tempfile.TemporaryDirectory() as directory:
        path = Path(directory) / 'notes.md'
        path.write_text(body)
        gh('release', 'edit', tag, '--notes-file', str(path))


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


def recover_storage(version, current, storage, verify_published=False):
    """Verify the original storage and finish an unpublished storage draft.

    Trust the already uploaded bytes, not a fresh (possibly different) build.
    Published OS/storage assets are never uploaded again on this path.
    """
    tag = storage_tag(version)
    associations = STORAGE_MARKER.findall(current.get('body') or '')
    if associations and associations != [tag]:
        raise ValueError(f'Unexpected storage association in {version}: expected {tag}')
    if storage is None:
        if associations:
            raise ValueError(f'Missing package storage: {tag}; restore the original uploaded assets')
        return  # Existing all-in-one release: only retry the Pages dispatch.
    names = [a['name'] for a in storage['assets']]
    if (len(names) != len(set(names)) or not REQUIRED | {'SHA256SUMS'} <= set(names)
            or any(Path(n).name != n or (n not in TECHNICAL | {'SHA256SUMS'}
                                        and not n.endswith('.apk')) for n in names)):
        raise ValueError(f'Incomplete or unexpected package storage assets: {tag}')
    if not storage['draft'] and not verify_published:
        return
    with tempfile.TemporaryDirectory() as temporary:
        gh('release', 'download', tag, '--pattern', '*', '--dir', temporary)
        try:
            files = validate(Path(temporary))
            if set(files) != set(names):
                raise ValueError('downloaded asset inventory differs from GitHub')
            manifest = json.loads(files['packages.json'].read_text())
            expected_version = version.removeprefix('v').replace('-beta.', '_beta')
            if manifest['release'] != expected_version or not manifest['packages']:
                raise ValueError('package manifest belongs to a different or empty release')
            changed = {item['apk'] for item in manifest['packages'].values() if item['changed']}
            if {n for n in files if n.endswith('.apk')} != changed:
                raise ValueError('changed APK set does not match packages.json')
            for item in manifest['packages'].values():
                if item['changed'] and digest(files[item['apk']]) != item['apk_sha256']:
                    raise ValueError(f'package fingerprint mismatch: {item["apk"]}')
        except (ValueError, KeyError, OSError, TypeError) as error:
            raise ValueError(f'Cannot publish package storage {tag}: {error}') from error
    if storage['draft']:
        gh('release', 'edit', tag, '--draft=false', '--prerelease', '--latest=false')


def verify_public_downloads(version, current):
    # The image is assembled AFTER signing too, not restored from release-build.
    # Once storage is published, retain its original public download set rather
    # than pairing it with a freshly rebuilt image containing different APKs.
    names = [a['name'] for a in current['assets']]
    required = {'SHA256SUMS', f'NanoKVM-OS-{version}-source.tar.xz'}
    images = [n for n in names if re.fullmatch(
        r'NanoKVM-OS-Image-.+-apps-' + re.escape(version) + r'\.img\.zip', n)]
    if (len(names) != len(set(names)) or any(Path(n).name != n for n in names)
            or len(images) > 1 or set(names) != required | set(images)):
        raise ValueError(f'{version}: original public download set is incomplete or unexpected')
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
    tag = storage_tag(version)
    current = release(repository, version)
    if current and not current['draft']:
        # A maintainer may have published the OS draft before storage was ready.
        recover_storage(version, current, release(repository, tag))
        gh('workflow', 'run', 'apk-repository.yml', '--ref', env['DEFAULT_BRANCH'])
        return
    storage = release(repository, tag)
    if storage and not storage['draft']:
        if current is None:
            raise ValueError(f'{tag} is already published but the OS draft is missing; '
                             'restore the original draft or follow the documented never-published re-cut procedure')
        # A rerun of the sign job creates different ECDSA signatures. Resume
        # from the original remote pair, never from the new local assembly.
        recover_storage(version, current, storage, verify_published=True)
        verify_public_downloads(version, current)
        if env['DRAFT'] != 'true':
            gh('release', 'edit', version, '--draft=false')
            gh('workflow', 'run', 'apk-repository.yml', '--ref', env['DEFAULT_BRANCH'])
        return
    directory = Path(env['OUT']) / 'assets'
    validate(directory / 'public')
    validate(directory / 'packages')
    title = f'NanoKVM OS {version}'
    if env['IMAGE'] == 'true':
        title += ' · Image ' + env['NANOKVM_IMAGE_VERSION']
    flags = ['--generate-notes']
    if '-beta.' in version:
        flags += ['--prerelease', '--latest=false']
    if env.get('PREVIOUS'):
        flags += ['--notes-start-tag', env['PREVIOUS']]
    stage(tag, storage, directory / 'packages', env['SHA'],
          f'Package storage for {version}', ['--prerelease', '--latest=false',
          '--notes', f'Technical package storage for {version}. Do not delete: updates reuse these files.'])
    stage(version, current, directory / 'public', env['SHA'], title, flags)
    # Preserve the maintainer's edited notes; only append the machine-readable
    # storage association, also used to reject an incomplete published release.
    current = release(repository, version)
    body = current.get('body') or ''
    associations = STORAGE_MARKER.findall(body)
    if associations and associations != [tag]:
        raise ValueError(f'Unexpected storage association in {version}')
    if marker(version) not in body:
        notes(version, body.rstrip() + '\n\n' + marker(version) + '\n')
    if storage is None or storage['draft']:
        gh('release', 'edit', tag, '--draft=false', '--prerelease', '--latest=false')
    # A human can publish the OS draft later without another storage step.
    if env['DRAFT'] != 'true':
        gh('release', 'edit', version, '--draft=false')
        gh('workflow', 'run', 'apk-repository.yml', '--ref', env['DEFAULT_BRANCH'])


if __name__ == '__main__':
    main()
