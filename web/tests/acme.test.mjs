import assert from 'node:assert/strict';
import { test } from 'node:test';

import { hasWildcard, parseDomains } from '../src/lib/acme.ts';

test('domains are split, lowercased and deduplicated', () => {
  assert.deepEqual(
    parseDomains(' KVM.example.org, www.kvm.example.org\nkvm.example.org;*.example.org '),
    ['kvm.example.org', 'www.kvm.example.org', '*.example.org']
  );
  assert.deepEqual(parseDomains(''), []);
  assert.equal(hasWildcard(['a.example', '*.example']), true);
  assert.equal(hasWildcard(['a.example']), false);
});
