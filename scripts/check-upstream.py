#!/usr/bin/env python3
"""Report upstream changes without rewriting qualified source pins or executing downloads."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import html
import io
import json
import os
from pathlib import Path
import re
import tarfile
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
MARKER = '<!-- nanokvm-upstream-watch -->'
TITLE = 'Upstream dependency update dashboard'


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # In particular, never forward a GitHub token to a different host.
        return None


def request_bytes(url, payload=None, method=None):
    if urllib.parse.urlparse(url).scheme != 'https':
        raise ValueError('Only HTTPS sources are supported')
    headers = {'User-Agent': 'NanoKVM-OS-upstream-watch'}
    if urllib.parse.urlparse(url).netloc == 'api.github.com':
        headers['Accept'] = 'application/vnd.github+json'
        if os.environ.get('GH_TOKEN'):
            headers['Authorization'] = 'Bearer ' + os.environ['GH_TOKEN']
    data = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    with urllib.request.build_opener(NoRedirect).open(req, timeout=25) as response:
        raw = response.read(4 * 1024 * 1024 + 1)
    if len(raw) > 4 * 1024 * 1024:
        raise ValueError('Source response exceeds 4 MiB')
    return raw


def request(url, payload=None, method=None):
    return request_bytes(url, payload, method).decode('utf-8')


def github(path, payload=None, method=None):
    return json.loads(request('https://api.github.com/' + path, payload, method))


def version(value, separator='.'):
    value = value.replace(separator, '.')
    if not re.fullmatch(r'\d+(?:\.\d+)*', value):
        raise ValueError('Not a stable numeric version: ' + value)
    parts = tuple(int(x) for x in value.split('.'))
    return parts + (0,) * max(0, 4 - len(parts))


def current_pin(root, source):
    path = (root / source['path']).resolve()
    if not path.is_relative_to(root.resolve()):
        raise ValueError('Source path escapes repository')
    matches = re.findall(source['pattern'], path.read_text(), re.MULTILINE)
    if len(matches) != 1 or not isinstance(matches[0], str):
        raise ValueError('Expected exactly one source pin in ' + source['path'])
    return matches[0]


def alpine_version(value):
    # OpenSSL stable APK versions. Fail visibly for a new syntax, never guess
    # APK ordering or compare revision strings lexicographically.
    match = re.fullmatch(r'(\d+(?:\.\d+)*)-r(\d+)', value)
    if not match:
        raise ValueError('Unsupported stable Alpine version: ' + value)
    return version(match[1]), int(match[2])


def alpine_packages(raw, names, arch):
    # Read metadata in memory only; never extract files or execute APKBUILD.
    with tarfile.open(fileobj=io.BytesIO(raw), mode='r:gz') as archive:
        members = [m for m in archive.getmembers() if m.name == 'APKINDEX']
        if len(members) != 1 or not members[0].isfile() or members[0].size > 16 * 1024 * 1024:
            raise ValueError('Invalid or oversized APKINDEX')
        text = archive.extractfile(members[0]).read().decode('utf-8')
    found = {}
    for record in text.strip().split('\n\n'):
        fields = dict(line.split(':', 1) for line in record.splitlines() if ':' in line)
        name = fields.get('P')
        if name not in names:
            continue
        if name in found or fields.get('A') != arch:
            raise ValueError('Duplicate package or unexpected architecture: ' + name)
        found[name] = fields['V']
    if set(found) != set(names):
        raise ValueError('Missing Alpine packages: ' + ', '.join(sorted(set(names) - set(found))))
    if len(set(found.values())) != 1:
        raise ValueError('Alpine OpenSSL subpackage versions disagree')
    return next(iter(found.values()))


def check_alpine(source, root, commit, result):
    branch = current_pin(root, source['branch_pin'])
    if not re.fullmatch(r'\d+\.\d+', branch):
        raise ValueError('Invalid Alpine stable branch')
    recipe_url = ('https://gitlab.alpinelinux.org/api/v4/projects/alpine%2Faports/repository/files/'
                  + urllib.parse.quote(source['aport'] + '/APKBUILD', safe='') + '/raw?ref=' + commit)
    recipe = request(recipe_url)
    versions = re.findall(r'^pkgver=(\d+(?:\.\d+)*)$', recipe, re.MULTILINE)
    revisions = re.findall(r'^pkgrel=(\d+)$', recipe, re.MULTILINE)
    if len(versions) != 1 or len(revisions) != 1:
        raise ValueError('Expected literal stable pkgver/pkgrel in pinned Alpine recipe')
    baseline = versions[0] + '-r' + revisions[0]
    # Compare the original Alpine recipe revision, not the local +1. Otherwise
    # an upstream r1 security fix would be hidden by our independently built r1.
    expected = versions[0] + '-r' + str(int(revisions[0]) + 1)
    url = ('https://dl-cdn.alpinelinux.org/alpine/v' + branch + '/'
           + source['aport'].split('/')[0] + '/' + source['arch'] + '/APKINDEX.tar.gz')
    latest = alpine_packages(request_bytes(url), source['packages'], source['arch'])
    a, b = alpine_version(baseline), alpine_version(latest)
    status = 'C906 rebuild required' if b > a else ('current' if a == b else 'pinned recipe ahead')
    result.update(current=baseline + ' (Alpine recipe; C906 build: ' + expected + ')',
                  latest=latest, link=url, status=status,
                  detail='Alpine v' + branch + ' ' + source['arch'] + ': ' + ', '.join(source['packages'])
                         + '; recipe pin ' + commit[:12] + '; installed/published C906 APKs are not inspected')


def check(source, root=ROOT):
    result = dict(name=source['name'], path=source['path'], current='?', latest='?', status='error', link='')
    try:
        current = current_pin(root, source)
        result['current'] = current
        kind = source['kind']
        if kind == 'alpine-package':
            check_alpine(source, root, current, result)
        elif kind == 'github-commit':
            repo = source['repo']
            branch = source.get('branch') or github('repos/' + repo)['default_branch']
            endpoint = 'repos/' + repo + '/compare/' + current + '...' + urllib.parse.quote(branch, safe='')
            head = github('repos/' + repo + '/commits/' + urllib.parse.quote(branch, safe=''))['sha']
            data = github(endpoint.rsplit('...', 1)[0] + '...' + head)
            result.update(latest=head,
                          link=data['html_url'])
            result['status'] = {'ahead': 'upstream changes', 'identical': 'current',
                                'behind': 'pinned ahead', 'diverged': 'review branch divergence'}[data['status']]
        elif kind in ('github-tags', 'index'):
            if kind == 'github-tags':
                repo = source['repo']
                tags = github('repos/' + repo + '/tags?per_page=100')
                text = '\n'.join(item['name'] for item in tags)
                result['link'] = 'https://github.com/' + repo + '/tags'
            else:
                text = request(source['url'])
                result['link'] = source['url']
            candidates = re.findall(source['upstream_pattern'], text, re.MULTILINE)
            if not candidates:
                raise ValueError('No stable upstream versions found')
            sep = source.get('separator', '.')
            latest = max(candidates, key=lambda x: version(x, sep))
            result['latest'] = latest
            a, b = version(current, sep), version(latest, sep)
            result['status'] = 'update available' if b > a else ('current' if a == b else 'pinned ahead')
        else:
            raise ValueError('Unknown source kind: ' + kind)
    except Exception as error:
        # Keep per-source failures visible without hiding successfully checked sources.
        result['detail'] = str(error)
    return result


def render(results):
    def cell(value):
        return html.escape(str(value)).replace('|', '&#124;').replace('\n', ' ')
    lines = [MARKER, '# Upstream dependency updates', '',
             'Dependabot handles dependency PRs. This report covers native source pins, patched forks and the Alpine baseline used by the C906 OpenSSL rebuild.', '',
             '| Component | Pinned | Upstream | Result | Pin file |',
             '|---|---|---|---|---|']
    for item in results:
        upstream = cell(item['latest'])
        if item['link']:
            upstream = '[' + upstream + '](' + item['link'] + ')'
        status = item['status']
        if item.get('detail'):
            status += ': ' + item['detail']
        lines.append('| ' + ' | '.join([cell(item['name']), cell(item['current']), upstream,
                                        cell(status), cell(item['path'])]) + ' |')
    lines += ['', '## Integration requirements', '',
              '- Review upstream changes and preserve NanoKVM patches; a newer commit on a vendor branch is a review candidate, not a qualified release.',
              '- Update related source/archive hashes, Go runtime patches, module ABI and platform output manifest together after rebuilding and testing.',
              '- Do not overwrite patched Pion sources by only bumping go.mod versions.',
              '- Buildroot package recipes inherit upstream changes when Buildroot is refreshed; this is not an individual version/CVE audit of every transitive native package.',
              '- Alpine OpenSSL is checked against the published stable riscv64 package index, using the pinned aports recipe as the C906 baseline. The local pkgrel +1 does not count as an upstream fix. Old Buildroot OpenSSL 4 is not the system OpenSSL.',
              '- C906 build versions are derived from the pinned recipe, not observed on a device or in the published overlay. Rebuild and publish the overlay after Alpine updates; this watcher does not run apk upgrade or change devices.',
              '- Retired Buildroot OpenVPN 3/Asio, Superfile, private apk-tools and vendor-libc loader recipes are excluded; they do not describe the current Alpine package set.',
              '- Alpine branch monitoring does not update installed APKs. APK package revisions and firmware release approval remain separate.',
              '- Opaque boot firmware (including the existing base FIP/OpenSBI), local patches and historical experiments have no general automatic updater.',
              '- GitHub tag checks inspect the latest 100 returned tags. Pinned-ahead and divergent results require review; they never trigger a downgrade.',
              '- Errors mean an upstream check was incomplete, not that the component is current.', '']
    return '\n'.join(lines)


def publish(repo, body, actionable):
    if not re.fullmatch(r'[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+', repo):
        raise ValueError('Invalid GitHub repository')
    # Only touch our own bot-owned dashboard, never an issue supplied by a third party.
    for page in range(1, 11):
        issues = github(f'repos/{repo}/issues?state=all&creator=github-actions%5Bbot%5D&per_page=100&page={page}')
        for issue in issues:
            if (not issue.get('pull_request') and issue['title'] == TITLE and
                    (issue.get('body') or '').startswith(MARKER)):
                update = {}
                if issue.get('body') != body:
                    update['body'] = body
                    if actionable:
                        update['state'] = 'open'
                if not actionable and issue['state'] == 'open':
                    update['state'] = 'closed'
                if update:
                    github(f'repos/{repo}/issues/{issue["number"]}', update, 'PATCH')
                print('Dashboard:', issue['html_url'])
                return
        if len(issues) < 100:
            if actionable:
                issue = github(f'repos/{repo}/issues', {'title': TITLE, 'body': body}, 'POST')
                print('Dashboard:', issue['html_url'])
            return
    raise RuntimeError('Dashboard search exceeded pagination limit; refusing to create a duplicate')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inventory', action='store_true', help='Validate and list local pins without network access')
    parser.add_argument('--publish', action='store_true', help='Maintain one issue dashboard in GITHUB_REPOSITORY')
    parser.add_argument('--output', type=Path, help='Write Markdown report')
    args = parser.parse_args()
    sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
    if len({source['name'] for source in sources}) != len(sources):
        raise ValueError('Duplicate source name')
    if args.inventory:
        for source in sources:
            print(source['name'], current_pin(ROOT, source), source['path'])
        return
    with ThreadPoolExecutor(max_workers=4) as workers:
        results = list(workers.map(check, sources))
    body = render(results)
    if args.output:
        args.output.write_text(body)
    if os.environ.get('GITHUB_STEP_SUMMARY'):
        Path(os.environ['GITHUB_STEP_SUMMARY']).write_text(body)
    print(body)
    if args.publish:
        publish(os.environ['GITHUB_REPOSITORY'], body,
                any(x['status'] != 'current' for x in results))
    if any(x['status'] == 'error' for x in results):
        raise SystemExit('One or more upstream checks failed; see the report')


if __name__ == '__main__':
    main()
