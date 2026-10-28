#!/usr/bin/env python3
"""Contracts for dependency PR coverage, source pins and review notifications."""
import importlib.util
import json
from pathlib import Path
import shutil
import sys
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / 'scripts'))
spec = importlib.util.spec_from_file_location('proposals', ROOT / 'scripts/propose-upstream-updates.py')
p = importlib.util.module_from_spec(spec)
spec.loader.exec_module(p)


class ProposalTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        for name in ('.github/upstream-watch.json', 'platform/sources.lock', 'firmware/sources.json',
                     'platform/build.sh', 'README.md', 'platform/README.md'):
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / name, path)
        self.sources = p.policies(self.root)

    def source(self, name):
        return next(s for s in self.sources if s['name'] == name)

    def result(self, source, latest, status='update available'):
        return dict(name=source['name'], current=p.upstream.current_pin(self.root, source),
                    latest=latest, status=status, path=source['path'],
                    link='https://github.com/example/project')

    def test_collection_only_checks_configured_sources_not_buildroot_master(self):
        def check(source, root):
            return dict(name=source['name'], status='current')
        with patch.object(p.upstream, 'check', side_effect=check) as checked, \
                patch.object(p.upstream, 'github', side_effect=AssertionError('Unexpected master scan')):
            collected = p.collect(self.root)
        self.assertEqual(len(collected), len(self.sources))
        self.assertEqual({s['name'] for s, _ in collected}, {s['name'] for s in self.sources})
        self.assertFalse(any(s['name'].startswith('Buildroot: ') for s, _ in collected))
        self.assertEqual(checked.call_count, len(self.sources))

    def test_all_requested_sources_have_an_explicit_policy(self):
        names = {s['name'] for s in self.sources}
        all_names = {s['name'] for s in json.loads((self.root / '.github/upstream-watch.json').read_text())['sources']}
        self.assertEqual(all_names - names, {'Linux', 'QEMU Ubuntu build helper'})
        self.assertEqual(len(names), 39)
        self.assertIn('OpenSSL 3.5 LTS', names)
        self.assertEqual(self.source('aic8800')['repo'], 'radxa-pkg/aic8800')
        for name in ('Go runtime', 'Patched Pion dtls/v3', 'Patched Pion ice/v4',
                     'Patched Pion srtp/v3', 'GCC compiler', 'Alpine stable branch', 'OpenSSL 3.5 LTS',
                     'OCR models: PaddleX model list', 'OCR models: PaddlePaddle on Hugging Face',
                     'OCR converter: tpu-mlir', 'TPU runtime (cviruntime dma-buf port)'):
            self.assertEqual(self.source(name)['pull_request']['mode'], 'review')

    def test_rustdesk_release_is_one_review_with_both_commits(self):
        source = self.source('RustDesk protocol reference')
        self.assertEqual(source['kind'], 'rustdesk-release-reference')
        self.assertFalse(any(s.get('repo') == 'rustdesk/hbb_common' for s in self.sources))
        reference = dict(rustdesk_version='1.6.0', rustdesk_commit='a' * 40, hbb_common_commit='b' * 40)
        result = dict(current='1.5.0', latest='1.6.0', status='update available', path=source['path'],
                      link='https://github.com/rustdesk/rustdesk/releases/tag/1.6.0',
                      reference=reference, candidate_key=':'.join(reference.values()))
        plan = p.proposal(source, result, self.root)
        path = '.github/upstream-candidates/rustdesk-protocol-reference.json'
        self.assertEqual(list(plan['files']), [path])
        record = json.loads(plan['files'][path])
        self.assertEqual(record['evidence']['reference'], reference)
        self.assertEqual(record['candidate_key'], result['candidate_key'])
        self.assertEqual(record['pin_files_updated'], [])
        saved = self.root / path
        saved.parent.mkdir(parents=True)
        saved.write_text(plan['files'][path])
        self.assertIsNone(p.proposal(source, result, self.root))
        # Retagging the same version must not be hidden by the old acknowledgement.
        result['reference'] = dict(reference, rustdesk_commit='c' * 40)
        result['candidate_key'] = ':'.join(result['reference'].values())
        updated = p.proposal(source, result, self.root)
        self.assertIsNotNone(updated)
        self.assertNotEqual(updated['branch'], plan['branch'])

    def test_ini_miniz_release_updates_and_divergence(self):
        for name, tag in (('inih', 'r63'), ('miniz', '3.1.3')):
            source = self.source(name)
            result = self.result(source, tag + ' (' + 'a' * 12 + ')')
            with self.subTest(name=name), patch.object(p.upstream, 'github', return_value={'sha': 'a' * 40}) as api, \
                    patch.object(p, 'git_paths', return_value={'.': 'b' * 40}):
                plan = p.proposal(source, result, self.root)
            api.assert_called_once_with('repos/' + source['repo'] + '/commits/' + tag)
            line = next(line for line in plan['files']['platform/sources.lock'].splitlines()
                        if line.startswith(name + ' '))
            self.assertIn('a' * 40 + ' .=' + 'b' * 40, line)
            manifest = json.loads(plan['files']['firmware/sources.json'])
            self.assertEqual(manifest[name]['commit'], 'a' * 40)
            result['status'] = 'review branch divergence'
            with patch.object(p, 'pin_changes', side_effect=AssertionError('Must preserve current pins')):
                plan = p.proposal(source, result, self.root)
            self.assertEqual(list(plan['files']), ['.github/upstream-candidates/' + name + '.json'])

    def test_ini_miniz_pinned_ahead_releases_do_not_open_prs(self):
        results = [(self.source(name), dict(self.result(self.source(name), tag), status='pinned ahead'))
                   for name, tag in (('inih', 'r62'), ('miniz', '3.1.2'))]
        with patch.object(p, 'collect', return_value=results), patch.object(p, 'proposal') as prepare, \
                patch.object(sys, 'argv', ['propose-upstream-updates.py']):
            p.main()
        prepare.assert_not_called()

    def test_archive_updates_hash_and_build_paths_together(self):
        source = self.source('U-Boot')
        result = self.result(source, '2027.01')
        before = (self.root / 'platform/sources.lock').read_text()
        with patch.object(p, 'archive_hash', return_value='a' * 64) as download:
            plan = p.proposal(source, result, self.root)
        download.assert_called_once_with('https://github.com/u-boot/u-boot/archive/refs/tags/v2027.01.tar.gz')
        self.assertIn('v2027.01.tar.gz ' + 'a' * 64, plan['files']['platform/sources.lock'])
        self.assertIn('$u/u-boot-2027.01', plan['files']['platform/build.sh'])
        self.assertIn('U-Boot 2027.01', plan['files']['README.md'])
        self.assertEqual(before, (self.root / 'platform/sources.lock').read_text())

    def test_archive_patch_release_does_not_replace_new_version_twice(self):
        for name, label in (('Buildroot', 'buildroot'), ('U-Boot', 'u-boot')):
            source = self.source(name)
            current = p.upstream.current_pin(self.root, source)
            latest = current + '.1'
            with self.subTest(name=name), patch.object(p, 'archive_hash', return_value='a' * 64):
                plan = p.proposal(source, self.result(source, latest), self.root)
            build = plan['files']['platform/build.sh']
            archive = f'buildroot-{latest}.tar.xz' if label == 'buildroot' else f'v{latest}.tar.gz'
            self.assertIn(archive, build)
            self.assertNotIn(latest + '.1', build)
            if label == 'u-boot':
                self.assertIn('$u/u-boot-' + latest, build)
            self.assertIn(name + ' ' + latest, build)

    def test_signer_archive_updates_only_x86_pin(self):
        source = self.source('Alpine signing minirootfs')
        before = (self.root / 'platform/sources.lock').read_text()
        with patch.object(p, 'archive_hash', return_value='a' * 64) as download:
            plan = p.proposal(source, self.result(source, '3.24.4'), self.root)
        url = 'https://dl-cdn.alpinelinux.org/alpine/v3.24/releases/x86_64/alpine-minirootfs-3.24.4-x86_64.tar.gz'
        download.assert_called_once_with(url)
        after = plan['files']['platform/sources.lock']
        self.assertIn('alpine-signer ' + url + ' ' + 'a' * 64, after)
        self.assertEqual(
            [line for line in before.splitlines() if not line.startswith('alpine-signer ')],
            [line for line in after.splitlines() if not line.startswith('alpine-signer ')])
        self.assertNotIn('platform/build.sh', plan['files'])
        self.assertEqual(before, (self.root / 'platform/sources.lock').read_text())

    def test_archive_failure_generates_review_without_partial_pin_changes(self):
        source = self.source('Buildroot')
        with patch.object(p, 'archive_hash', side_effect=OSError('download failed')):
            plan = p.proposal(source, self.result(source, '2027.02'), self.root)
        self.assertEqual(list(plan['files']), ['.github/upstream-candidates/buildroot.json'])
        self.assertIn('download failed', plan['body'])
        self.assertTrue(plan['preparation_error'])

    def test_patched_module_notification_does_not_rewrite_active_sources(self):
        source = self.source('Patched Pion dtls/v3')
        # Review-only proposals do not read/change active Go files.
        result = dict(name=source['name'], current='3.0.1', latest='3.1.0',
                      status='update available', path='server/go.mod', link='https://github.com/pion/dtls/tags')
        plan = p.proposal(source, result, self.root)
        self.assertEqual(list(plan['files']), ['.github/upstream-candidates/patched-pion-dtls-v3.json'])
        self.assertIn('does not change the active dependency', plan['body'])
        record = json.loads(next(iter(plan['files'].values())))
        self.assertEqual(record['candidate'], '3.1.0')
        self.assertEqual(record['pin_files_updated'], [])

    def test_acknowledged_candidate_is_not_repeated(self):
        source = self.source('GCC compiler')
        result = self.result(source, '17.1.0')
        plan = p.proposal(source, result, self.root)
        for name, text in plan['files'].items():
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text)
        self.assertIsNone(p.proposal(source, result, self.root))
        result['latest'] = '17.2.0'
        self.assertIsNotNone(p.proposal(source, result, self.root))

    def test_git_pins_include_selected_object_hashes(self):
        source = self.source('aic8800')
        result = self.result(source, 'a' * 40, 'upstream changes')
        original = (self.root / 'platform/sources.lock').read_text()
        with patch.object(p, 'git_paths', return_value={'src/driver': 'b' * 40}):
            plan = p.proposal(source, result, self.root)
        self.assertIn('a' * 40 + ' src/driver=' + 'b' * 40, plan['files']['platform/sources.lock'])
        self.assertEqual(original, (self.root / 'platform/sources.lock').read_text())

    def test_unchanged_selected_git_paths_do_not_create_noise(self):
        source = self.source('aic8800')
        line = next(line for line in (self.root / 'platform/sources.lock').read_text().splitlines()
                    if line.startswith('aic8800 '))
        objects = dict(f.split('=', 1) for f in line.split()[3:])
        with patch.object(p, 'git_paths', return_value=objects):
            self.assertIsNone(p.proposal(source, self.result(source, 'a' * 40, 'upstream changes'), self.root))

    def test_git_object_resolution_and_missing_path(self):
        sha = lambda c: c * 40
        responses = [{'tree': {'sha': sha('a')}},
                     {'tree': [{'path': 'src', 'sha': sha('b'), 'type': 'tree'}]},
                     {'tree': [{'path': 'driver', 'sha': sha('c'), 'type': 'tree'},
                               {'path': 'LICENSE', 'sha': sha('d'), 'type': 'blob'}]}]
        with patch.object(p.upstream, 'github', side_effect=responses):
            self.assertEqual(p.git_paths('radxa-pkg/aic8800', sha('e'), ['src/driver', 'src/LICENSE']),
                             {'src/driver': sha('c'), 'src/LICENSE': sha('d')})
        with patch.object(p.upstream, 'github', side_effect=[{'tree': {'sha': sha('a')}}, {'tree': []}]):
            with self.assertRaisesRegex(ValueError, 'Missing'):
                p.git_paths('example/repo', sha('e'), ['missing'])

    def test_job_keeps_processing_after_discovery_error_and_reports_failure(self):
        source = self.source('GCC compiler')
        good = self.result(source, '17.1.0')
        bad = dict(good, name='failed source', status='error', detail='network down')
        with patch.object(p, 'collect', return_value=[(source, bad), (source, good)]), \
                patch.object(p, 'proposal', return_value=dict(files={'candidate': '{}'})) as prepare, \
                patch.object(sys, 'argv', ['propose-upstream-updates.py']):
            with self.assertRaisesRegex(SystemExit, 'network down'):
                p.main()
        prepare.assert_called_once_with(source, good)

    def test_current_and_pinned_ahead_do_not_generate_proposals(self):
        source = self.source('GCC compiler')
        results = [(source, dict(self.result(source, '1.0'), status=status))
                   for status in ('current', 'pinned ahead')]
        with patch.object(p, 'collect', return_value=results), patch.object(p, 'proposal') as prepare, \
                patch.object(sys, 'argv', ['propose-upstream-updates.py']):
            p.main()
        prepare.assert_not_called()

    def test_picoclaw_monitoring_and_proposals_are_disabled(self):
        sources = json.loads((self.root / '.github/upstream-watch.json').read_text())['sources']
        self.assertFalse(any('picoclaw' in json.dumps(s).lower() for s in sources))

    def test_cadence_filters_before_proposal_discovery(self):
        for cadence in ('daily', 'quarterly', 'all'):
            with self.subTest(cadence=cadence), patch.object(p.upstream, 'check', return_value={}) as check:
                collected = p.collect(self.root, cadence)
            names = {s['name'] for s, _ in collected}
            if cadence == 'quarterly':
                self.assertEqual(names, {'tinyalsa'})
            elif cadence == 'daily':
                self.assertEqual(names, {s['name'] for s in self.sources} - {'tinyalsa'})
            else:
                self.assertEqual(names, {s['name'] for s in self.sources})
            self.assertEqual(check.call_count, len(names))

    def test_publisher_cli_passes_cadence_to_discovery(self):
        with patch.object(p, 'collect', return_value=[]) as collect, \
                patch.object(sys, 'argv', ['propose-upstream-updates.py', '--cadence', 'quarterly']):
            p.main()
        collect.assert_called_once_with(cadence='quarterly')

    def test_generic_publisher_accepts_only_candidate_new_files(self):
        plan = dict(latest='2.0', branch='deps/upstream-example-1234', title='review: example',
                    body='A candidate, not an active dependency update.',
                    files={'.github/upstream-candidates/example.json': '{}\n'})
        responses = [[], {'object': {'sha': 'base'}}, [], {'tree': {'sha': 'parent'}},
                     {'sha': 'tree'}, {'sha': 'commit'}, {}, {'html_url': 'https://example.test/pr/2'}]
        with patch.object(p.publisher, 'api', side_effect=responses) as api, \
                patch.object(p.publisher.subprocess, 'check_output', side_effect=['base\n', b'']):
            p.publisher.publish(plan, self.root)
        self.assertEqual(api.call_args.args[2]['title'], plan['title'])
        self.assertEqual(api.call_args.args[2]['body'], plan['body'])
        plan['files'] = {'unexpected.txt': 'no'}
        with patch.object(p.publisher, 'api', side_effect=responses), \
                patch.object(p.publisher.subprocess, 'check_output', side_effect=['base\n', b'']):
            with self.assertRaisesRegex(ValueError, 'Unexpected new'):
                p.publisher.publish(plan, self.root)


if __name__ == '__main__':
    unittest.main()
