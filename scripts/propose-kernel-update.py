#!/usr/bin/env python3
"""Propose unqualified Linux point-release updates; publication is opt-in."""
import argparse
from datetime import date
import importlib.util
import json
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]
REPOSITORY = 'dormancygrace/NanoKVM-OS'
IDENTITY = {'name': 'Gleb Pesin', 'email': 'dormancygrace@gmail.com'}
# Proposal branches are BRANCH_PREFIX + 'upstream-' + candidate. Only the part
# after the first '/' names the candidate (see proposed()).
BRANCH_PREFIX = 'deps/'
spec = importlib.util.spec_from_file_location('upstream', ROOT / 'scripts/check-upstream.py')
upstream = importlib.util.module_from_spec(spec)
spec.loader.exec_module(upstream)


def replace_once(text, pattern, replacement):
    text, count = re.subn(pattern, lambda _: replacement, text, flags=re.MULTILINE)
    if count != 1:
        raise ValueError('Expected exactly one match: ' + pattern)
    return text


def prepare(root=ROOT):
    sources = json.loads((root / '.github/upstream-watch.json').read_text())['sources']
    source = next(s for s in sources if s['name'] == 'Linux')
    current = upstream.current_pin(root, source)
    raw = upstream.request(source['url'])
    candidates = re.findall(source['upstream_pattern'], raw)
    if not candidates:
        raise ValueError('No eligible Linux point releases in kernel.org feed')
    latest = max(candidates, key=upstream.version)
    if upstream.version(latest) <= upstream.version(current):
        return None
    # Also enforce the PR policy independently of the report's configuration.
    if not re.fullmatch(r'[0-9]+\.[0-9]+\.[1-9][0-9]*', latest):
        raise ValueError('Refusing an initial series release or prerelease')
    entries = [r for r in json.loads(raw)['releases'] if r['version'] == latest]
    if len(entries) != 1:
        raise ValueError('Expected exactly one release entry for ' + latest)
    released = date.fromisoformat(entries[0]['released']['date'])
    directory = 'https://cdn.kernel.org/pub/linux/kernel/v' + latest.split('.')[0] + '.x/'
    archive = 'linux-' + latest + '.tar.xz'
    checksum_url = directory + 'sha256sums.asc'
    sums = upstream.request(checksum_url)
    hashes = re.findall(r'^([0-9a-f]{64})[ \t]+\*?' + re.escape(archive) + r'$', sums, re.MULTILINE)
    if len(hashes) != 1:
        raise ValueError('Expected exactly one SHA-256 for ' + archive)
    checksum = hashes[0]
    url = directory + archive
    files = {}
    name = 'platform/sources.lock'
    lock = (root / name).read_text()
    old = re.findall(r'^linux[ \t]+(\S+)[ \t]+([0-9a-f]{64})[ \t]*$', lock, re.MULTILINE)
    if len(old) != 1:
        raise ValueError('Expected one Linux archive pin')
    files[name] = replace_once(lock, r'^linux[ \t]+\S+[ \t]+[0-9a-f]{64}[ \t]*$',
                              'linux      ' + url + '  ' + checksum)
    name = 'firmware/sources.json'
    manifest = json.loads((root / name).read_text())
    if manifest['linux'] != dict(version=current, url=old[0][0], sha256=old[0][1]):
        raise ValueError('Linux pins in source manifests disagree')
    manifest['linux'] = dict(version=latest, url=url, sha256=checksum)
    files[name] = json.dumps(manifest, indent=2) + '\n'
    name = 'platform/build.sh'
    build = (root / name).read_text()
    for required in ('release=' + current + '-nanokvm-os-',
                     'linux-' + current + '.tar.xz', 'linux-' + current + '"'):
        if required not in build:
            raise ValueError('Kernel build pin changed: ' + required)
    build = build.replace(current, latest)
    build = build.replace('release time.', 'release date (00:00 UTC).')
    timestamp = released.strftime('%a %b %d 00:00:00 UTC %Y')
    files[name] = replace_once(build, r"^kernel_timestamp='[^']*'$",
                              "kernel_timestamp='" + timestamp + "'")
    # The saved config and qualified output hashes still need a real rebuild.
    # Only update the generated config's version heading here.
    name = 'platform/kernel/config'
    config = (root / name).read_text()
    files[name] = replace_once(config, r'^# Linux/riscv ' + re.escape(current) + r' Kernel Configuration$',
                              '# Linux/riscv ' + latest + ' Kernel Configuration')
    for name in ('README.md', 'platform/README.md'):
        text = (root / name).read_text()
        if current not in text:
            raise ValueError('Missing documented kernel version in ' + name)
        files[name] = text.replace(current, latest)
    return dict(current=current, latest=latest, files=files, sha256=checksum,
                checksum_url=checksum_url, url=url, changelog=directory + 'ChangeLog-' + latest)


def api(path, method='GET', payload=None):
    command = ['gh', 'api', 'repos/' + REPOSITORY + '/' + path, '--method', method]
    data = None
    if payload is not None:
        command += ['--input', '-']
        data = json.dumps(payload)
    result = subprocess.run(command, input=data, text=True, capture_output=True)
    if result.returncode:
        raise RuntimeError('GitHub request failed: ' + result.stderr.strip())
    return json.loads(result.stdout) if result.stdout.strip() else None


def pull_body(plan):
    return f"""Update the Linux source candidate from {plan['current']} to {plan['latest']}.

- [Upstream changelog]({plan['changelog']})
- [Source archive]({plan['url']})
- SHA-256: {plan['sha256']} from [kernel.org checksums]({plan['checksum_url']}).
- Version pins, extraction paths, reproducible build timestamp and current documentation are updated together.

This is an automated draft, not a qualified firmware update. Checksums are read over HTTPS; the bot does not download/build the kernel or claim signature or device verification. Initial X.Y / X.Y.0 releases and release candidates are excluded.

Before marking ready and merging:
- [ ] Review/rebase NanoKVM kernel and out-of-tree driver patches.
- [ ] Regenerate and review the kernel config with olddefconfig.
- [ ] Build the kernel, modules and full image; test them on a device.
- [ ] Check the Release build comment: it lists the outputs this PR changes. Qualify them on a device before merging.

GitHub may show Approve workflows to run for this GITHUB_TOKEN-created PR. Approve those checks in the PR before review. No automatic merge or deployment is enabled.
"""


def pull_requests():
    """Every PR of the repository against main: open, merged and closed."""
    pulls, page = [], 1
    while True:
        batch = api('pulls?state=all&base=main&per_page=100&page=' + str(page))
        pulls += batch
        if len(batch) < 100:
            return pulls
        page += 1


def proposed(branch, pulls):
    """The PRs among pulls that propose the candidate of branch.

    A branch names its candidate after the first '/', so a PR opened under an
    earlier prefix still counts: a closed proposal keeps its candidate closed.
    Only branches of this repository count, not forks.
    """
    owner = REPOSITORY.split('/')[0]
    name = branch.split('/', 1)[1]
    return [p for p in pulls if p['head']['label'] == owner + ':' + p['head']['ref']
            and p['head']['ref'].partition('/')[2] == name]


def publish(plan, root=ROOT):
    branch = plan.get('branch', BRANCH_PREFIX + 'upstream-linux-' + plan['latest'])
    # Keep closed PRs closed and never overwrite a maintainer's work on a PR.
    existing = proposed(branch, pull_requests())
    if existing:
        print('Already proposed: ' + existing[0]['html_url'])
        return existing[0]['html_url']
    base = api('git/ref/heads/main')['object']['sha']
    head = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip()
    if base != head:
        raise ValueError('Checkout is not current main; rerun on main before publishing')
    # Exact match prevents touching another branch. A surviving branch is reused
    # after a previous run created it but failed before opening the PR.
    refs = api('git/matching-refs/heads/' + branch)
    exact = [ref for ref in refs if ref['ref'] == 'refs/heads/' + branch]
    if not exact:
        parent = api('git/commits/' + base)
        entries = subprocess.check_output(
            ['git', 'ls-files', '--stage', '-z', '--', *plan['files']], cwd=root)
        modes = {entry.split(b'\t', 1)[1].decode(): entry.split(b' ', 1)[0].decode()
                 for entry in entries.split(b'\0') if entry}
        for name in set(plan['files']) - modes.keys():
            if not re.fullmatch(r'\.github/upstream-candidates/[a-z0-9-]+\.json', name):
                raise ValueError('Unexpected new proposal file: ' + name)
            modes[name] = '100644'
        if any(m not in ('100644', '100755') for m in modes.values()):
            raise ValueError('Expected tracked regular files only')
        tree = api('git/trees', 'POST', {
            'base_tree': parent['tree']['sha'],
            'tree': [dict(path=name, mode=modes[name], type='blob', content=text)
                     for name, text in plan['files'].items()],
        })
        commit = api('git/commits', 'POST', {
            'message': plan.get('title', 'build: propose Linux ' + plan['latest']),
            'tree': tree['sha'], 'parents': [base],
            'author': IDENTITY, 'committer': IDENTITY,
        })
        api('git/refs', 'POST', {'ref': 'refs/heads/' + branch, 'sha': commit['sha']})
    pr = api('pulls', 'POST', {
        'title': plan.get('title', 'build: update Linux to ' + plan['latest']), 'head': branch,
        'base': 'main', 'body': plan['body'] if 'body' in plan else pull_body(plan), 'draft': True,
    })
    print('Created draft PR: ' + pr['html_url'])
    return pr['html_url']


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--publish', action='store_true', help='create a draft PR using GH_TOKEN')
    args = parser.parse_args()
    plan = prepare()
    if plan is None:
        print('No newer eligible Linux point release.')
    elif args.publish:
        publish(plan)
    else:
        print(json.dumps({key: value for key, value in plan.items() if key != 'files'}, indent=2))
        print('Files to update: ' + ', '.join(plan['files']))


if __name__ == '__main__':
    main()
