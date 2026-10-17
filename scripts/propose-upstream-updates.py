#!/usr/bin/env python3
"""Create opt-in draft PRs for configured upstream dependencies."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('kernel_proposals', ROOT / 'scripts/propose-kernel-update.py')
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)
upstream = publisher.upstream
MODES = {'archive-pin', 'git-pin', 'review'}
CHANGED = {'update available', 'upstream changes', 'review module release',
           'review branch divergence', 'review upstream replacement'}


def branch_for(identifier, target):
    digest = hashlib.sha256(target.encode()).hexdigest()[:16]
    return publisher.BRANCH_PREFIX + 'upstream-' + identifier + '-' + digest


def policies(root=ROOT):
    sources = json.loads((root / '.github/upstream-watch.json').read_text())['sources']
    selected = [s for s in sources if s.get('pull_request')]
    ids = []
    for source in selected:
        policy = source['pull_request']
        if policy['mode'] not in MODES or not re.fullmatch(r'[a-z0-9-]+', policy['id']):
            raise ValueError('Invalid PR policy: ' + source['name'])
        ids.append(policy['id'])
        if policy['mode'] != 'review' and 'lock_entry' not in source:
            raise ValueError('A pin update requires a source-lock entry')
    if len(ids) != len(set(ids)):
        raise ValueError('Duplicate upstream PR identifiers')
    aic = next(s for s in selected if s['name'] == 'aic8800')
    if aic['repo'] != 'radxa-pkg/aic8800':
        raise ValueError('AIC8800 must track radxa-pkg/aic8800')
    return selected


class HTTPSRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        if urllib.parse.urlparse(newurl).scheme != 'https':
            raise ValueError('Refusing a non-HTTPS archive redirect')
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def archive_hash(url):
    """Hash bytes only: no archive extraction, package code or credentials."""
    if urllib.parse.urlparse(url).scheme != 'https':
        raise ValueError('Expected an HTTPS source archive')
    digest = hashlib.sha256()
    size = 0
    with urllib.request.build_opener(HTTPSRedirect).open(url, timeout=40) as response:
        while chunk := response.read(1024 * 1024):
            size += len(chunk)
            if size > 256 * 1024 * 1024:
                raise ValueError('Source archive exceeds 256 MiB')
            digest.update(chunk)
    if not size:
        raise ValueError('Empty source archive')
    return digest.hexdigest()


def git_paths(repo, commit, paths):
    """Resolve exactly the subtrees/blobs selected by sources.lock."""
    if not re.fullmatch('[0-9a-f]{40}', commit):
        raise ValueError('Expected a full upstream commit ID')
    root = upstream.github('repos/' + repo + '/git/commits/' + commit)['tree']['sha']
    cache = {}
    output = {}
    for path in paths:
        sha, kind = root, 'tree'
        if path != '.':
            for part in path.split('/'):
                if part in ('', '.', '..') or kind != 'tree':
                    raise ValueError('Invalid locked Git path: ' + path)
                if sha not in cache:
                    data = upstream.github('repos/' + repo + '/git/trees/' + sha)
                    if data.get('truncated'):
                        raise ValueError('Truncated Git tree')
                    cache[sha] = data['tree']
                matches = [entry for entry in cache[sha] if entry['path'] == part]
                if len(matches) != 1 or matches[0]['type'] not in ('tree', 'blob'):
                    raise ValueError('Missing or unsupported Git object: ' + path)
                sha, kind = matches[0]['sha'], matches[0]['type']
        if not re.fullmatch('[0-9a-f]{40}', sha):
            raise ValueError('Invalid Git object ID')
        output[path] = sha
    return output


def git_target(source, result):
    if source['kind'] == 'github-commit':
        return result['latest']
    if source['kind'] == 'github-release-commit':
        ref = result['latest'].split(' (', 1)[0]
    else:
        tags = upstream.github_tags(source['repo'])
        matches = [tag['name'] for tag in tags
                   if re.fullmatch(source['upstream_pattern'], tag['name'])
                   and re.fullmatch(source['upstream_pattern'], tag['name'])[1] == result['latest']]
        if not matches:
            raise ValueError('No matching upstream release tag')
        ref = max(matches)
    return upstream.github('repos/' + source['repo'] + '/commits/' +
                           urllib.parse.quote(ref, safe=''))['sha']


def pin_changes(source, result, root=ROOT):
    lock_path = 'platform/sources.lock'
    original = (root / lock_path).read_text()
    name = source['lock_entry']
    matches = re.findall(r'^' + re.escape(name) + r'[ \t]+(.+)$', original, re.MULTILINE)
    if len(matches) != 1:
        raise ValueError('Expected a single source-lock entry: ' + name)
    fields = matches[0].split()
    old_url, old_pin = fields[:2]
    files = {}
    evidence = {}
    manifest_path = 'firmware/sources.json'
    manifest_text = (root / manifest_path).read_text()
    if source['pull_request']['mode'] == 'git-pin':
        commit = git_target(source, result)
        paths = dict(field.split('=', 1) for field in fields[2:])
        if not paths:
            raise ValueError('Missing locked Git paths')
        objects = git_paths(source['repo'], commit, paths)
        if objects == paths and source['kind'] == 'github-commit':
            return {}, {'unchanged_paths': True}
        replacement = ' '.join([name, old_url, commit, *[p + '=' + sha for p, sha in objects.items()]])
        manifest_text = manifest_text.replace(old_pin, commit)
        if source['name'] == 'json_c':
            manifest = json.loads(manifest_text)
            manifest['json_c']['version'] = result['latest']
            manifest_text = json.dumps(manifest, indent=2) + '\n'
        evidence = dict(commit=commit, objects=objects)
    else:
        current, latest = result['current'], result['latest']
        if current not in old_url:
            raise ValueError('Archive version is not present in its URL')
        url = old_url.replace(current, latest)
        checksum = archive_hash(url)
        replacement = ' '.join([name, url, checksum])
        manifest = json.loads(manifest_text)
        for entry in manifest.values():
            if isinstance(entry, dict) and entry.get('url') == old_url:
                entry.update(url=url, sha256=checksum)
                if 'version' in entry:
                    entry['version'] = latest
        manifest_text = json.dumps(manifest, indent=2) + '\n'
        # Update archive/extraction names, not arbitrary numeric text.
        old_archive = urllib.parse.urlparse(old_url).path.rsplit('/', 1)[1]
        new_archive = urllib.parse.urlparse(url).path.rsplit('/', 1)[1]
        build_path = 'platform/build.sh'
        build = (root / build_path).read_text().replace(old_archive, new_archive)
        for suffix in ('.tar.gz', '.tar.xz', '.tar.bz2', '.tgz'):
            if old_archive.endswith(suffix):
                build = build.replace(old_archive[:-len(suffix)], new_archive[:-len(suffix)])
                break
        if name == 'u-boot':
            build = build.replace('u-boot-' + current, 'u-boot-' + latest)
        label = {'u-boot': 'U-Boot', 'buildroot': 'Buildroot'}.get(name)
        if label:
            build = build.replace(label + ' ' + current, label + ' ' + latest)
            for path in ('README.md', 'platform/README.md'):
                text = (root / path).read_text()
                updated = text.replace(label + ' ' + current, label + ' ' + latest)
                if text != updated:
                    files[path] = updated
        if build != (root / build_path).read_text():
            files[build_path] = build
        evidence = dict(archive=url, sha256=checksum)
    files[lock_path] = re.sub(r'^' + re.escape(name) + r'[ \t]+.+$', lambda _: replacement,
                              original, flags=re.MULTILINE)
    if name == 'opensbi':
        files[lock_path] = re.sub(r'^# Official stable release .*$', '# OpenSBI candidate; qualification is tracked in the draft PR.',
                                  files[lock_path], flags=re.MULTILINE)
    if manifest_text != (root / manifest_path).read_text():
        files[manifest_path] = manifest_text
    return files, evidence


def proposal(source, result, root=ROOT):
    policy = source['pull_request']
    identifier = policy['id']
    key = result.get('candidate_key', result['latest'])
    files, evidence = {}, {}
    error = None
    if policy['mode'] != 'review' and result['status'] != 'review branch divergence':
        try:
            files, evidence = pin_changes(source, result, root)
            if evidence.get('unchanged_paths'):
                return None
        except Exception as exc:
            # Discovery still produces a visible review request if preparation
            # needs human integration. Never emit a half-rewritten lock file.
            files = {}
            error = str(exc)
    record = dict(component=source['name'], current=result['current'], candidate=result['latest'],
                  candidate_key=key, status='pending-review', upstream=result['link'],
                  source_path=result['path'], strategy=policy['mode'], evidence=evidence)
    if result.get('upstream_commit'):
        record['upstream_commit'] = result['upstream_commit']
    if error:
        record['preparation_error'] = error
    path = '.github/upstream-candidates/' + identifier + '.json'
    # A merged review record acknowledges this candidate without pretending the
    # runtime/source pin changed. Closed PRs are also checked by the publisher.
    if (root / path).exists():
        old = json.loads((root / path).read_text())
        if old.get('candidate_key') == key:
            return None
    record['pin_files_updated'] = sorted(files)
    files[path] = json.dumps(record, indent=2) + '\n'
    action = 'update' if record['pin_files_updated'] else 'review'
    title = action + ': ' + source['name'] + ' upstream ' + result['latest']
    body = f"""Upstream candidate for **{source['name']}**: {result['current']} → {result['latest']}.

[Upstream release / comparison]({result['link']})

"""
    if record['pin_files_updated']:
        body += 'This draft updates source pins and records verified archive bytes or selected Git object IDs. Related local patches and build outputs still require qualification.\n\n'
    else:
        body += 'This notification PR records a candidate for manual integration; it does not change the active dependency. Merging the record only acknowledges the notification. Rebase or port local changes before updating the active pin.\n\n'
    if error:
        body += '**Automatic pin preparation needs attention:** ' + error + '\n\n'
    if result.get('detail'):
        body += result['detail'] + '\n\n'
    body += """Before marking ready:
- Review the upstream changes and preserve NanoKVM patches, generated code and protocol compatibility.
- Update remaining version guards, companion metadata, source hashes and configuration as needed.
- Run relevant tests and rebuild affected outputs. Qualify hardware changes on a device.
- Check the Release build comment for the outputs this PR changes, and qualify them.

No automatic merge or device installation is enabled. For GITHUB_TOKEN-created PRs, select Approve workflows to run if GitHub requires it.
"""
    return dict(latest=result['latest'], branch=branch_for(identifier, key), title=title,
                body=body, files=files, preparation_error=error)


def collect(root=ROOT, cadence='all'):
    sources = upstream.select_sources(policies(root), cadence)
    with ThreadPoolExecutor(max_workers=4) as workers:
        results = list(workers.map(lambda source: upstream.check(source, root), sources))
    return list(zip(sources, results))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--publish', action='store_true')
    parser.add_argument('--cadence', choices=('all', 'daily', 'quarterly'), default='all')
    parser.add_argument('--output', type=Path, help='save candidate/error summary as JSON')
    args = parser.parse_args()
    summary = []
    failures = []
    pulls = publisher.pull_requests() if args.publish else []
    for source, result in collect(cadence=args.cadence):
        status = result['status']
        item = dict(name=source['name'], status=status, current=result['current'], latest=result['latest'])
        try:
            if status == 'error':
                raise ValueError(result.get('detail', 'Upstream check failed'))
            if status in CHANGED or (source.get('kind') == 'github-channel' and status == 'repository-managed'):
                key = result.get('candidate_key', result['latest'])
                branch = branch_for(source['pull_request']['id'], key)
                existing = publisher.proposed(branch, pulls)
                if existing:
                    item['pr'] = existing[0]['html_url']
                    item['status'] = 'already proposed'
                else:
                    plan = proposal(source, result)
                    if plan:
                        item['branch'] = branch
                        item['files'] = sorted(plan['files'])
                        item['status'] = 'proposal'
                        if args.publish:
                            item['pr'] = publisher.publish(plan)
                        if plan.get('preparation_error'):
                            raise ValueError(plan['preparation_error'])
                    else:
                        item['status'] = 'already acknowledged or selected paths unchanged'
        except Exception as exc:
            item['error'] = str(exc)
            failures.append(source['name'] + ': ' + str(exc))
        summary.append(item)
        print(json.dumps(item))
    if args.output:
        args.output.write_text(json.dumps(summary, indent=2) + '\n')
    if failures:
        raise SystemExit('Incomplete upstream checks/preparation:\n' + '\n'.join(failures))


if __name__ == '__main__':
    main()
