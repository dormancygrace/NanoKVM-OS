#!/usr/bin/env python3
"""Report upstream changes without rewriting qualified source pins or executing downloads."""
import argparse
from dependency_coverage import validate_coverage, npm_sources
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


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # In particular, never forward a GitHub token to a different host.
        return None


def request_bytes(url):
    if urllib.parse.urlparse(url).scheme != 'https':
        raise ValueError('Only HTTPS sources are supported')
    headers = {'User-Agent': 'NanoKVM-OS-upstream-watch'}
    if urllib.parse.urlparse(url).netloc == 'api.github.com':
        headers['Accept'] = 'application/vnd.github+json'
        if os.environ.get('GH_TOKEN'):
            headers['Authorization'] = 'Bearer ' + os.environ['GH_TOKEN']
    req = urllib.request.Request(url, headers=headers)
    with urllib.request.build_opener(NoRedirect).open(req, timeout=25) as response:
        raw = response.read(4 * 1024 * 1024 + 1)
    if len(raw) > 4 * 1024 * 1024:
        raise ValueError('Source response exceeds 4 MiB')
    return raw


def request(url):
    return request_bytes(url).decode('utf-8')


def github(path):
    return json.loads(request('https://api.github.com/' + path))


def github_tags(repo):
    tags = []
    for page in range(1, 21):
        batch = github('repos/' + repo + '/tags?per_page=100&page=' + str(page))
        tags.extend(batch)
        if len(batch) < 100:
            return tags
    raise ValueError('Tag pagination exceeded 2000 entries; refusing an incomplete latest-version check')


def version(value, separator='.'):
    value = value.replace(separator, '.')
    if not re.fullmatch(r'\d+(?:\.\d+)*', value):
        raise ValueError('Not a stable numeric version: ' + value)
    parts = tuple(int(x) for x in value.split('.'))
    return parts + (0,) * max(0, 4 - len(parts))


def rustdesk_release_reference():
    """Resolve the stable release first, then its gitlink at an immutable commit."""
    repo = 'rustdesk/rustdesk'
    release = github('repos/' + repo + '/releases/latest')
    tag = release['tag_name']
    if release.get('draft') or release.get('prerelease') or not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+', tag):
        raise ValueError('Expected a published stable RustDesk release')
    commit = github('repos/' + repo + '/commits/' + tag)['sha']
    if not re.fullmatch(r'[0-9a-f]{40}', commit):
        raise ValueError('Invalid RustDesk release commit')
    submodule = github('repos/' + repo + '/contents/libs/hbb_common?ref=' + commit)
    # GitHub Contents API also labels submodules as "file" on older API versions.
    if (submodule.get('type') not in ('submodule', 'file')
            or submodule.get('submodule_git_url') != 'https://github.com/rustdesk/hbb_common'
            or not re.fullmatch(r'[0-9a-f]{40}', submodule.get('sha', ''))):
        raise ValueError('Expected the hbb_common submodule pinned by the RustDesk release')
    return dict(rustdesk_version=tag, rustdesk_commit=commit, hbb_common_commit=submodule['sha'])


def select_sources(sources, cadence='all'):
    """Filter before network requests; manual checks default to all cadences."""
    if cadence not in ('all', 'daily', 'quarterly'):
        raise ValueError('Unknown check cadence: ' + cadence)
    for source in sources:
        if source.get('cadence', 'daily') not in ('daily', 'quarterly'):
            raise ValueError('Unknown source cadence: ' + source['name'])
    return [s for s in sources if cadence == 'all' or s.get('cadence', 'daily') == cadence]


def current_pin(root, source):
    path = (root / source['path']).resolve()
    if not path.is_relative_to(root.resolve()):
        raise ValueError('Source path escapes repository')
    matches = re.findall(source['pattern'], path.read_text(), re.MULTILINE)
    if len(matches) != 1 or not isinstance(matches[0], str):
        raise ValueError('Expected exactly one source pin in ' + source['path'])
    return matches[0]


def alpine_index(raw, arch):
    # Metadata only: no extraction and no execution of package code.
    with tarfile.open(fileobj=io.BytesIO(raw), mode='r:gz') as archive:
        members = [m for m in archive.getmembers() if m.name == 'APKINDEX']
        if len(members) != 1 or not members[0].isfile() or members[0].size > 32 * 1024 * 1024:
            raise ValueError('Invalid or oversized APKINDEX')
        text = archive.extractfile(members[0]).read().decode('utf-8')
    found = {}
    for record in text.strip().split('\n\n'):
        fields = dict(line.split(':', 1) for line in record.splitlines() if ':' in line)
        name = fields.get('P')
        if not name or name in found or fields.get('A') not in (arch, 'noarch'):
            raise ValueError('Duplicate package or unexpected architecture: ' + str(name))
        found[name] = fields['V']
    return found


def check_alpine_inventory(root=ROOT):
    path = 'firmware/alpine/packages'
    try:
        branch = current_pin(root, dict(path='scripts/build-alpine-packages.sh', pattern=r'ALPINE_VERSION="([0-9.]+)"'))
        packages = set()
        for recipe in (root / path).glob('*/APKBUILD'):
            for assignment in re.findall(r'^(?:make)?depends=(.*)$', recipe.read_text(), re.M):
                if not re.fullmatch(r'"[^"\n]*"', assignment):
                    raise ValueError('Unsupported APK dependency expression in ' + str(recipe.relative_to(root)))
                for item in assignment[1:-1].split():
                    if item.startswith(('!', 'nanokvm-')):
                        continue
                    name = re.split(r'[<>=~]', item)[0]
                    if name == 'nanokvm':
                        continue
                    if not re.fullmatch(r'[a-zA-Z0-9+_.-]+', name):
                        raise ValueError('Unrecognized APK dependency: ' + item)
                    packages.add(name)
        # Optional VPN packages use the same repository resolver as Software.
        tools = (root / 'server/service/extensions/vpn/versions.go').read_text()
        match = re.search(r'var tools = map\[string\]string\{([^}]+)\}', tools)
        if not match:
            raise ValueError('VPN package inventory could not be read')
        optional = set(re.findall(r'"([^"\n]+)":', match[1]))
        optional = {'wireguard-tools' if name == 'wireguard' else name for name in optional}
        packages |= optional | {'busybox', 'apk-tools'}
        available = {}
        for channel in ('v' + branch + '/main', 'v' + branch + '/community'):
            url = 'https://dl-cdn.alpinelinux.org/alpine/' + channel + '/riscv64/APKINDEX.tar.gz'
            for name, v in alpine_index(request_bytes(url), 'riscv64').items():
                available.setdefault(name, (v, url))
        return [dict(name='Alpine: ' + name, path=path, current='stock signed APK channel',
                     latest=available.get(name, ('?', ''))[0], link=available.get(name, ('?', ''))[1],
                     status='repository-managed' if name in available else 'error',
                     detail='Available package; installed versions are not inspected' if name in available else 'Required package absent from configured channels')
                for name in sorted(packages)]
    except Exception as error:
        return [dict(name='Alpine package inventory', path=path, current='?', latest='?', link='', status='error', detail=str(error))]


def check(source, root=ROOT):
    result = dict(name=source['name'], path=source['path'], current='?', latest='?', status='error', link='')
    try:
        current = current_pin(root, source)
        result['current'] = current
        kind = source['kind']
        if kind == 'npm':
            data = json.loads(request('https://registry.npmjs.org/' + urllib.parse.quote(source['package'], safe='') + '/latest'))
            latest = data['version']
            result.update(latest=latest, link='https://www.npmjs.com/package/' + source['package'])
            a, b = version(current), version(latest)
            result['status'] = 'update available' if b > a else ('current' if a == b else 'pinned ahead')
            if source.get('policy'):
                result['detail'] = source['policy']
        elif kind == 'go-proxy':
            module = source['module']
            escaped = re.sub('[A-Z]', lambda m: '!' + m[0].lower(), module)
            url = 'https://proxy.golang.org/' + escaped + '/@latest'
            latest = json.loads(request(url))['Version']
            normalized = current if current.startswith('v') else 'v' + current
            result.update(latest=latest, link=url,
                          status='current' if normalized == latest else 'review module release',
                          detail='Independent nested-module check; Dependabot cannot always resolve parent tags. Review versions before changing go.mod')
        elif kind == 'github-channel':
            data = github('repos/' + source['repo'] + '/releases/latest')
            if not any(a['name'] == source['asset'] for a in data['assets']):
                raise ValueError('Latest release is missing required asset: ' + source['asset'])
            result.update(current='latest release channel', latest=data['tag_name'], link=data['html_url'],
                          status='repository-managed', detail='Fetched on demand; installed device version is not inspected')
        elif kind == 'rustdesk-release-reference':
            reference = rustdesk_release_reference()
            pinned = json.loads((root / source['path']).read_text())
            latest = reference['rustdesk_version']
            a, b = version(current), version(latest)
            same = all(pinned.get(key) == value for key, value in reference.items())
            status = ('update available' if b > a else 'pinned ahead' if a > b else
                      'current' if same else 'review upstream replacement')
            result.update(latest=latest, status=status,
                          link='https://github.com/rustdesk/rustdesk/releases/tag/' + latest,
                          reference=reference, candidate_key=':'.join(reference.values()),
                          detail='Review RustDesk and its bundled hbb_common together. '
                                 'These are protocol references, not linked build dependencies; '
                                 'port and test protocol changes before updating upstream.json. '
                                 'RustDesk commit: ' + reference['rustdesk_commit'] +
                                 '; hbb_common commit: ' + reference['hbb_common_commit'])
        elif kind == 'github-release-commit':
            release = github('repos/' + source['repo'] + '/releases/latest')
            if release.get('draft') or release.get('prerelease'):
                raise ValueError('Expected a published stable release')
            head = github('repos/' + source['repo'] + '/commits/' + urllib.parse.quote(release['tag_name'], safe=''))['sha']
            data = github('repos/' + source['repo'] + '/compare/' + current + '...' + head)
            result.update(latest=release['tag_name'] + ' (' + head[:12] + ')', link=release['html_url'],
                          status={'ahead': 'update available', 'identical': 'current',
                                  'behind': 'pinned ahead', 'diverged': 'review branch divergence'}[data['status']])
        elif kind == 'launchpad-binary':
            url = ('https://api.launchpad.net/1.0/ubuntu/+archive/primary?'
                   + urllib.parse.urlencode({'ws.op': 'getPublishedBinaries', 'binary_name': source['package'],
                       'exact_match': 'true', 'status': 'Published',
                       'distro_arch_series': 'https://api.launchpad.net/1.0/ubuntu/' + source['series'] + '/amd64',
                       'order_by_date': 'true', 'ws.size': '100'}))
            data = json.loads(request(url))
            versions = sorted({row['binary_package_version'] for row in data['entries']})
            if not versions:
                raise ValueError('No published Ubuntu binary versions')
            # Debian version ordering is not numeric or lexical. Report candidates;
            # never present a possibly older pocket version as an automatic upgrade.
            result.update(latest=', '.join(versions), link='https://launchpad.net/ubuntu/' + source['series'] + '/+package/' + source['package'],
                          status='review package versions' if any(v != current for v in versions) else 'current',
                          detail='Published amd64 versions in the selected Ubuntu series; compare with dpkg before repinning')
        elif kind == 'alpine-rootfs':
            branch = '.'.join(current.split('.')[:2])
            arch = source.get('arch', 'riscv64')
            url = 'https://dl-cdn.alpinelinux.org/alpine/v' + branch + '/releases/' + arch + '/'
            values = re.findall(r'alpine-minirootfs-([0-9]+\.[0-9]+\.[0-9]+)-' + re.escape(arch), request(url))
            latest = max(values, key=version)
            result.update(latest=latest, link=url, status='update available' if version(latest) > version(current) else ('current' if latest == current else 'pinned ahead'))
        elif kind == 'github-reference':
            repo = source['repo']
            branch = github('repos/' + repo)['default_branch']
            base = github('repos/' + repo + '/commits/' + current)['sha']
            head = github('repos/' + repo + '/commits/' + urllib.parse.quote(branch, safe=''))['sha']
            result.update(latest=head, link='https://github.com/' + repo,
                          status='current' if base == head else 'review upstream replacement', detail=source['detail'])
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
                tags = github_tags(repo)
                text = '\n'.join(item['name'] for item in tags)
                result['link'] = 'https://github.com/' + repo + '/tags'
            else:
                text = request(source['url'])
                result['link'] = source['url']
            candidates = re.findall(source['upstream_pattern'], text, re.MULTILINE)
            if not candidates:
                raise ValueError('No stable upstream versions found')
            sep = source.get('separator', '.')
            if source.get('same_major'):
                candidates = [x for x in candidates if version(x, sep)[0] == version(current, sep)[0]]
                if not candidates:
                    raise ValueError('No stable versions in selected major')
                result['detail'] = 'Selected major only; changing the runtime major requires an explicit migration'
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
    lines = ['# Upstream dependency updates', '',
             'Coverage audit for the selected cadence (tinyalsa: quarterly; other sources: daily): Dependabot handles manifest PRs; this report independently checks direct npm versions, native pins, patched references and Alpine package availability. Latest releases are shown even during Dependabot cooldown.', '',
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
              '- Selected Buildroot packages and compiler/libc recipes follow the stable Buildroot release pin. Development-branch recipe changes do not produce reports or PRs; GCC stable releases are checked separately.',
              '- Production OpenSSL uses the stock signed Alpine riscv64 repository. Repository-managed rows show availability, not the installed device version.',
              '- This watcher does not run apk upgrade or change devices.',
              '- Retired Buildroot OpenVPN 3/Asio, Superfile, private apk-tools and vendor-libc loader recipes are excluded; they do not describe the current Alpine package set.',
              '- Alpine branch monitoring does not update installed APKs. APK package revisions and firmware release approval remain separate.',
              '- OpenSBI releases are monitored separately. Opaque base-FIP/vendor blobs follow the SDK source pin; local patches and historical experiments require manual qualification.',
              '- GitHub tags are paginated; exceeding the bounded scan fails visibly. Pinned-ahead and divergent results require review; they never trigger a downgrade.',
              '- Errors mean an upstream check was incomplete, not that the component is current.', '']
    return '\n'.join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--inventory', action='store_true', help='Validate and list local pins without network access')
    parser.add_argument('--output', type=Path, help='Write Markdown report')
    parser.add_argument('--cadence', choices=('all', 'daily', 'quarterly'), default='all')
    args = parser.parse_args()
    sources = json.loads((ROOT / '.github/upstream-watch.json').read_text())['sources']
    validate_coverage(ROOT, sources)
    sources += npm_sources(ROOT)
    if len({source['name'] for source in sources}) != len(sources):
        raise ValueError('Duplicate source name')
    if args.inventory:
        for source in sources:
            print(source['name'], current_pin(ROOT, source), source['path'])
        return
    sources = select_sources(sources, args.cadence)
    with ThreadPoolExecutor(max_workers=4) as workers:
        results = list(workers.map(check, sources))
    if args.cadence != 'quarterly':
        results += check_alpine_inventory()
    body = render(results)
    if args.output:
        args.output.write_text(body)
    if os.environ.get('GITHUB_STEP_SUMMARY'):
        Path(os.environ['GITHUB_STEP_SUMMARY']).write_text(body)
    print(body)
    if any(x['status'] == 'error' for x in results):
        raise SystemExit('One or more upstream checks failed; see the report')


if __name__ == '__main__':
    main()
