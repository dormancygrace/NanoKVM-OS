import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  hasWildcard,
  jobState,
  needsEab,
  needsEmail,
  parseDomains,
  providerFields,
  providerMatches
} from '../src/lib/acme.ts';

test('domains are split, lowercased and deduplicated', () => {
  assert.deepEqual(
    parseDomains(' KVM.example.org, www.kvm.example.org\nkvm.example.org;*.example.org '),
    ['kvm.example.org', 'www.kvm.example.org', '*.example.org']
  );
  assert.deepEqual(parseDomains(''), []);
  assert.equal(hasWildcard(['a.example', '*.example']), true);
  assert.equal(hasWildcard(['a.example']), false);
});

test('authority needs', () => {
  assert.equal(needsEab('google'), true);
  assert.equal(needsEab('google_test'), true);
  assert.equal(needsEab('letsencrypt'), false);
  assert.equal(needsEmail('zerossl'), true);
  assert.equal(needsEmail('buypass'), false);
});

test('provider fields and search', () => {
  const cf = {
    hook: 'dns_cf',
    name: 'CloudFlare',
    options: [{ name: 'CF_Key', description: 'API Key' }],
    optionsAlt: [{ name: 'CF_Token', description: 'API Token' }]
  };
  const duck = {
    hook: 'dns_duckdns',
    name: 'DuckDNS.org',
    options: [{ name: 'DuckDNS_Token', description: 'API Token' }]
  };
  assert.deepEqual(
    providerFields(cf, false).map((o) => o.name),
    ['CF_Key']
  );
  assert.deepEqual(
    providerFields(cf, true).map((o) => o.name),
    ['CF_Token']
  );
  // No alternative set: the main one is used either way.
  assert.deepEqual(
    providerFields(duck, true).map((o) => o.name),
    ['DuckDNS_Token']
  );
  assert.deepEqual(providerFields(undefined, false), []);
  assert.equal(providerMatches(cf, 'cloud'), true);
  assert.equal(providerMatches(cf, 'DNS_CF'), true);
  assert.equal(providerMatches(duck, 'cloud'), false);
  assert.equal(providerMatches(duck, '  '), true);
});

test('the last run state', () => {
  const base = { installed: true, updateAvailable: false, inUse: false, running: false };
  assert.equal(jobState({ ...base }), undefined);
  assert.equal(jobState({ ...base, running: true }), 'running');
  assert.equal(jobState({ ...base, lastRun: { action: 'issue', startedAt: 'x', ok: true } }), 'ok');
  assert.equal(
    jobState({ ...base, lastRun: { action: 'cron', startedAt: 'x', ok: true, notDue: true } }),
    'notDue'
  );
  assert.equal(
    jobState({ ...base, lastRun: { action: 'issue', startedAt: 'x', ok: false, error: 'e' } }),
    'failed'
  );
});
