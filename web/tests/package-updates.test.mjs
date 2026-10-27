import assert from 'node:assert/strict';
import test from 'node:test';

import { parseUpgrades } from '../src/lib/update-components.ts';

test('package update lines become name, installed and available versions', () => {
  const listing =
    'nanokvm-rustdesk-0.5.5-r0               < 0.5.6-r0 \n' +
    'nanokvm-kernel-sg2002-3.6.0_rc1-r0     < 3.6.0-r0\n\n' +
    'odd-line';
  assert.deepEqual(parseUpgrades(listing), [
    { name: 'nanokvm-rustdesk', installed: '0.5.5-r0', available: '0.5.6-r0' },
    { name: 'nanokvm-kernel-sg2002', installed: '3.6.0_rc1-r0', available: '3.6.0-r0' },
    { name: 'odd-line', installed: '', available: '' }
  ]);
});
