import assert from 'node:assert/strict';
import test from 'node:test';

import { atxFromHardware, isMissingEndpoint } from '../src/lib/atx.ts';

test('the power button follows the ATX switch', () => {
  assert.equal(atxFromHardware({ version: 'Pcie', atx: true }), true);
  assert.equal(atxFromHardware({ version: 'Alpha', atx: false }), false);
});

test('a server from before the switch keeps the power button', () => {
  // GET /api/vm/hardware of 3.6.0: only the version.
  assert.equal(atxFromHardware({ version: 'Pcie' }), true);
});

test('only a missing endpoint hides the ATX setting', () => {
  assert.equal(isMissingEndpoint({ response: { status: 404 } }), true);
  assert.equal(isMissingEndpoint({ response: { status: 500 } }), false);
  assert.equal(isMissingEndpoint(new Error('Network Error')), false);
  assert.equal(isMissingEndpoint(null), false);
});
